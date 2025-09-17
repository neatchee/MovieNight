package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zorchenhimer/MovieNight/common"

	"github.com/Eyevinn/hls-m3u8/m3u8"
	"github.com/gorilla/websocket"
	"github.com/nareix/joy4/av/avutil"
	"github.com/nareix/joy4/av/pubsub"
	"github.com/nareix/joy4/format/flv"
	"github.com/nareix/joy4/format/rtmp"
	"github.com/nareix/joy4/format/ts"
)

var (
	//global variable for handling all chat traffic
	chat *ChatRoom

	// Read/Write mutex for rtmp stream
	l = &sync.RWMutex{}

	// Map of active streams
	channels = map[string]*Channel{}
)

type Channel struct {
	que     *pubsub.Queue
	hlsData *HLSData
}

// HLS-related data structures
type HLSData struct {
	sync.RWMutex
	mediaSequence   uint64
	segments        []HLSSegment
	lastSegmentTime time.Time
}

type HLSSegment struct {
	Index     uint64
	URL       string
	Duration  float64
	StartTime time.Time
}

// HLS configuration constants
const (
	HLSSegmentDuration = 6.0  // seconds per segment (as required by specification)
	HLSWindowSize      = 6    // number of segments to keep in playlist (increased for better buffering)
	HLSTargetDuration  = 12.0 // target duration for HLS spec (adjusted for 6s segments)
)

type writeFlusher struct {
	httpflusher http.Flusher
	io.Writer
	ctx context.Context
}

// Write with comprehensive nil protection and connection state checking
func (w writeFlusher) Write(p []byte) (n int, err error) {
	if w.Writer == nil {
		return 0, errors.New("writer is nil")
	}

	// Check if the context is cancelled (connection closed)
	if w.ctx != nil {
		select {
		case <-w.ctx.Done():
			return 0, errors.New("connection closed by client")
		default:
			// Connection is still active
		}
	}

	defer func() {
		if r := recover(); r != nil {
			common.LogErrorf("Recovered from writer panic: %v\n", r)
			err = errors.New("writer panic recovered")
		}
	}()

	return w.Writer.Write(p)
}

// Flush with comprehensive nil protection to prevent segfaults
func (w writeFlusher) Flush() error {
	if w.httpflusher == nil {
		return errors.New("flusher is nil")
	}

	defer func() {
		if r := recover(); r != nil {
			common.LogErrorf("Recovered from flusher panic: %v\n", r)
		}
	}()

	w.httpflusher.Flush()
	return nil
}

func wsEmotes(w http.ResponseWriter, r *http.Request) {
	file := strings.TrimPrefix(r.URL.Path, "/")

	emoteDirSuffix := filepath.Base(emotesDir)
	if emoteDirSuffix == filepath.SplitList(file)[0] {
		file = strings.TrimPrefix(file, emoteDirSuffix+"/")
	}

	var body []byte
	err := filepath.WalkDir(emotesDir, func(path string, d fs.DirEntry, err error) error {
		if d.IsDir() || err != nil || len(body) > 0 {
			return nil
		}

		if filepath.Base(path) != filepath.Base(file) {
			return nil
		}

		body, err = os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	})
	if err != nil {
		common.LogErrorf("Emote could not be read %s: %v\n", file, err)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if len(body) == 0 {
		common.LogErrorf("Found emote file but pulled no data: %v\n", err)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	_, err = w.Write(body)
	if err != nil {
		common.LogErrorf("Could not write emote %s to response: %v\n", file, err)
		w.WriteHeader(http.StatusNotFound)
	}
}

// Handling the websocket
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, //not checking origin
}

// this is also the handler for joining to the chat
func wsHandler(w http.ResponseWriter, r *http.Request) {

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		common.LogErrorln("Error upgrading to websocket:", err)
		return
	}

	common.LogDebugln("Connection has been upgraded to websocket")

	chatConn := &chatConnection{
		Conn: conn,
		// If the server is behind a reverse proxy (eg, Nginx), look
		// for this header to get the real IP address of the client.
		forwardedFor: common.ExtractForwarded(r),
	}

	go func() {
		var client *Client

		// Get the client object
		for client == nil {
			var data common.ClientData
			err := chatConn.ReadData(&data)
			if err != nil {
				common.LogInfof("[handler] Client closed connection: %s: %v\n",
					conn.RemoteAddr().String(), err)
				conn.Close()
				return
			}

			if data.Type == common.CdPing {
				continue
			}

			var joinData common.JoinData
			err = json.Unmarshal([]byte(data.Message), &joinData)
			if err != nil {
				common.LogInfof("[handler] Could not unmarshal websocket %d data %#v: %v\n", data.Type, data.Message, err)
				continue
			}

			client, err = chat.Join(chatConn, joinData)
			if err != nil {
				switch err.(type) { //nolint:errorlint
				case UserFormatError, UserTakenError:
					common.LogInfof("[handler|%s] %v\n", errorName(err), err)
				case BannedUserError:
					common.LogInfof("[handler|%s] %v\n", errorName(err), err)
					// close connection since banned users shouldn't be connecting
					conn.Close()
				default:
					// for now all errors not caught need to be warned
					common.LogErrorf("[handler|uncaught] %v\n", err)
					conn.Close()
				}
			}
		}

		// Handle incomming messages
		for {
			var data common.ClientData
			err := conn.ReadJSON(&data)
			if err != nil { //if error then assuming that the connection is closed
				client.Exit()
				return
			}
			client.NewMsg(data)
		}

	}()
}

// returns if it's OK to proceed
func checkRoomAccess(w http.ResponseWriter, r *http.Request) bool {
	session, err := sstore.Get(r, "moviesession")
	if err != nil {
		// Don't return as server error here, just make a new session.
		common.LogErrorf("Unable to get session for client %s: %v\n", r.RemoteAddr, err)
	}

	if settings.RoomAccess == AccessPin {
		pin := session.Values["pin"]
		// No pin found in session
		if pin == nil || len(pin.(string)) == 0 {
			if r.Method == "POST" {
				// Check for correct pin
				err = r.ParseForm()
				if err != nil {
					common.LogErrorf("Error parsing form")
					http.Error(w, "Unable to get session data", http.StatusInternalServerError)
				}

				postPin := strings.TrimSpace(r.Form.Get("txtInput"))
				common.LogDebugf("Received pin: %s\n", postPin)
				if postPin == settings.RoomAccessPin {
					// Pin is correct.  Save it to session and return true.
					session.Values["pin"] = settings.RoomAccessPin
					err = session.Save(r, w)
					if err != nil {
						common.LogErrorf("Could not save pin cookie: %v\n", err)
						return false
					}
					return true
				}
				// Pin is incorrect.
				handlePinTemplate(w, r, "Incorrect PIN")
				return false
			} else {
				qpin := r.URL.Query().Get("pin")
				if qpin != "" && qpin == settings.RoomAccessPin {
					// Pin is correct.  Save it to session and return true.
					session.Values["pin"] = settings.RoomAccessPin
					err = session.Save(r, w)
					if err != nil {
						common.LogErrorf("Could not save pin cookie: %v\n", err)
						return false
					}
					return true
				}
			}
			// nope.  display pin entry and return
			handlePinTemplate(w, r, "")
			return false
		}

		// Pin found in session, but it has changed since last time.
		if pin.(string) != settings.RoomAccessPin {
			// Clear out the old pin.
			session.Values["pin"] = nil
			err = session.Save(r, w)
			if err != nil {
				common.LogErrorf("Could not clear pin cookie: %v\n", err)
			}

			// Prompt for new one.
			handlePinTemplate(w, r, "Pin has changed.  Enter new PIN.")
			return false
		}

		// Correct pin found in session
		return true
	}

	// TODO: this.
	if settings.RoomAccess == AccessRequest {
		http.Error(w, "Requesting access not implemented yet", http.StatusNotImplemented)
		return false
	}

	// Room is open.
	return true
}

func handlePinTemplate(w http.ResponseWriter, r *http.Request, errorMessage string) {
	type Data struct {
		Title      string
		SubmitText string
		Notice     string
	}

	if errorMessage == "" {
		errorMessage = "Please enter the PIN"
	}

	data := Data{
		Title:      "Enter Pin",
		SubmitText: "Submit Pin",
		Notice:     errorMessage,
	}

	err := common.ExecuteServerTemplate(w, "pin", data)
	if err != nil {
		common.LogErrorf("Error executing file, %v", err)
	}
}

func handleHelpTemplate(w http.ResponseWriter, r *http.Request) {
	type Data struct {
		Title         string
		Commands      map[string]string
		ModCommands   map[string]string
		AdminCommands map[string]string
	}

	data := Data{
		Title:    "Help",
		Commands: getHelp(common.CmdlUser),
	}

	if len(r.URL.Query().Get("mod")) > 0 {
		data.ModCommands = getHelp(common.CmdlMod)
	}

	if len(r.URL.Query().Get("admin")) > 0 {
		data.AdminCommands = getHelp(common.CmdlAdmin)
	}

	err := common.ExecuteServerTemplate(w, "help", data)
	if err != nil {
		common.LogErrorf("Error executing file, %v", err)
	}
}

func handleEmoteTemplate(w http.ResponseWriter, r *http.Request) {
	type Data struct {
		Title  string
		Emotes map[string]string
	}

	data := Data{
		Title:  "Available Emotes",
		Emotes: common.Emotes,
	}

	common.LogDebugf("Emotes Data: %s", data)
	err := common.ExecuteServerTemplate(w, "emotes", data)
	if err != nil {
		common.LogErrorf("Error executing file, %v", err)
	}
}

func handleIndexTemplate(w http.ResponseWriter, r *http.Request) {
	type Data struct {
		Video, Chat         bool
		MessageHistoryCount int
		Title               string
	}

	data := Data{
		Video:               true,
		Chat:                true,
		MessageHistoryCount: settings.MaxMessageCount,
		Title:               settings.PageTitle,
	}

	path := strings.Split(strings.TrimLeft(r.URL.Path, "/"), "/")
	if path[0] == "chat" {
		data.Video = false
		data.Title += " - chat"
	} else if path[0] == "video" {
		data.Chat = false
		data.Title += " - video"
	}

	// Force browser to replace cache since file was not changed
	if settings.NoCache {
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	}

	err := common.ExecuteServerTemplate(w, "main", data)
	if err != nil {
		common.LogErrorf("Error executing file, %v", err)
	}
}

func handlePublish(conn *rtmp.Conn) {
	streams, _ := conn.Streams()

	l.Lock()
	common.LogDebugln("request string->", conn.URL.RequestURI())
	urlParts := strings.Split(strings.Trim(conn.URL.RequestURI(), "/"), "/")
	common.LogDebugln("urlParts->", urlParts)

	if len(urlParts) > 2 {
		common.LogErrorln("Extra garbage after stream key")
		l.Unlock()
		conn.Close()
		return
	}

	if len(urlParts) != 2 {
		common.LogErrorln("Missing stream key")
		l.Unlock()
		conn.Close()
		return
	}

	if urlParts[1] != settings.GetStreamKey() {
		common.LogErrorln("Stream key is incorrect.  Denying stream.")
		l.Unlock()
		conn.Close()
		return //If key not match, deny stream
	}

	streamPath := urlParts[0]
	_, exists := channels[streamPath]
	if exists {
		common.LogErrorln("Stream already running.  Denying publish.")
		conn.Close()
		l.Unlock()
		return
	}

	ch := &Channel{}
	ch.que = pubsub.NewQueue()
	// Initialize HLS data
	ch.hlsData = &HLSData{
		mediaSequence:   0,
		segments:        make([]HLSSegment, 0),
		lastSegmentTime: time.Now(),
	}
	err := ch.que.WriteHeader(streams)
	if err != nil {
		common.LogErrorf("Could not write header to streams: %v\n", err)
	}
	channels[streamPath] = ch
	l.Unlock()

	stats.startStream()

	common.LogInfoln("Stream started")
	err = avutil.CopyPackets(ch.que, conn)
	if err != nil {
		common.LogErrorf("Could not copy packets to connections: %v\n", err)
	}
	common.LogInfoln("Stream finished")

	stats.endStream()

	l.Lock()
	delete(channels, streamPath)
	l.Unlock()
	ch.que.Close()
}

func handlePlay(conn *rtmp.Conn) {
	l.RLock()
	ch := channels[conn.URL.Path]
	l.RUnlock()

	if ch != nil {
		cursor := ch.que.Latest()
		err := avutil.CopyFile(conn, cursor)
		if err != nil {
			common.LogErrorf("Could not copy video to connection: %v\n", err)
		}
	}
}

func handleLive(w http.ResponseWriter, r *http.Request) {
	l.RLock()
	ch := channels[strings.Trim(r.URL.Path, "/")]
	l.RUnlock()

	if ch != nil {
		w.Header().Set("Content-Type", "video/x-flv")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(200)
		flusher := w.(http.Flusher)
		flusher.Flush()

		muxer := flv.NewMuxerWriteFlusher(writeFlusher{httpflusher: flusher, Writer: w})
		cursor := ch.que.Latest()

		session, _ := sstore.Get(r, "moviesession")
		stats.addViewer(session.ID)
		err := avutil.CopyFile(muxer, cursor)
		if err != nil {
			common.LogErrorf("Could not copy video to connection: %v\n", err)
		}
		stats.removeViewer(session.ID)
	} else {
		// Maybe HTTP_204 is better than HTTP_404
		w.WriteHeader(http.StatusNoContent)
		stats.resetViewers()
	}
}

func handleHLSManifest(w http.ResponseWriter, r *http.Request) {
	l.RLock()
	ch := channels["live"]
	l.RUnlock()

	// Comprehensive validation with nil checks to prevent segfaults
	if ch == nil {
		common.LogInfoln("HLS manifest request for non-existent channel")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if ch.que == nil {
		common.LogInfoln("HLS manifest request for channel with nil queue")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Initialize HLS data if needed
	if ch.hlsData == nil {
		ch.hlsData = &HLSData{
			mediaSequence:   0,
			segments:        make([]HLSSegment, 0),
			lastSegmentTime: time.Now(),
		}
	}

	ch.hlsData.Lock()
	defer ch.hlsData.Unlock()

	// Update HLS segments based on timing
	updateHLSSegments(ch.hlsData)

	// Generate M3U8 playlist using Eyevinn/hls-m3u8 library
	playlist, _ := m3u8.NewMediaPlaylist(HLSWindowSize, HLSWindowSize)
	if playlist == nil {
		common.LogErrorln("Failed to create media playlist")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Set playlist properties
	playlist.TargetDuration = HLSTargetDuration
	playlist.SeqNo = ch.hlsData.mediaSequence

	// Add segments to playlist
	for _, segment := range ch.hlsData.segments {
		err := playlist.Append(segment.URL, segment.Duration, "")
		if err != nil {
			common.LogErrorf("Error adding segment to playlist: %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	// Set response headers
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)

	// Write playlist
	_, err := w.Write(playlist.Encode().Bytes())
	if err != nil {
		common.LogErrorf("Error writing HLS manifest: %v\n", err)
	}

	if settings != nil && settings.HLSDebugLogging {
		common.LogInfof("Generated HLS playlist for live: %d segments, seq: %d\n",
			len(ch.hlsData.segments), ch.hlsData.mediaSequence)
	}
}

func updateHLSSegments(hlsData *HLSData) {
	now := time.Now()

	// Check if it's time for a new segment
	if now.Sub(hlsData.lastSegmentTime).Seconds() >= HLSSegmentDuration {
		// Add new segment
		newSegment := HLSSegment{
			Index:     hlsData.mediaSequence + uint64(len(hlsData.segments)),
			URL:       fmt.Sprintf("/live_segment_%d.ts", hlsData.mediaSequence+uint64(len(hlsData.segments))),
			Duration:  HLSSegmentDuration,
			StartTime: now,
		}

		hlsData.segments = append(hlsData.segments, newSegment)
		hlsData.lastSegmentTime = now

		// Maintain sliding window
		if len(hlsData.segments) > HLSWindowSize {
			// Remove oldest segment and increment media sequence
			hlsData.segments = hlsData.segments[1:]
			hlsData.mediaSequence++
		}

		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("HLS: Added segment %d, current seq: %d, window: %v\n",
				newSegment.Index, hlsData.mediaSequence, getSegmentIndexes(hlsData.segments))
		}
	}
}

func getSegmentIndexes(segments []HLSSegment) []uint64 {
	indexes := make([]uint64, len(segments))
	for i, seg := range segments {
		indexes[i] = seg.Index
	}
	return indexes
}

func handleHLSSegment(w http.ResponseWriter, r *http.Request) {
	// Extract segment number from URL
	path := strings.TrimPrefix(r.URL.Path, "/live_segment_")
	path = strings.TrimSuffix(path, ".ts")

	segmentNum, err := strconv.ParseUint(path, 10, 64)
	if err != nil {
		common.LogErrorf("Invalid segment number: %s\n", path)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	l.RLock()
	ch := channels["live"]
	l.RUnlock()

	// Comprehensive validation with nil checks
	if ch == nil {
		common.LogInfoln("HLS segment request for non-existent channel")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if ch.que == nil {
		common.LogInfoln("HLS segment request for channel with nil queue")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if ch.hlsData == nil {
		common.LogInfoln("HLS segment request for channel with nil HLS data")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	ch.hlsData.RLock()
	// Validate segment is in current window
	validSegment := false
	for _, seg := range ch.hlsData.segments {
		if seg.Index == segmentNum {
			validSegment = true
			break
		}
	}
	ch.hlsData.RUnlock()

	if !validSegment {
		common.LogInfof("Requested segment %d not in current window\n", segmentNum)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Set headers for FAST streaming - no blocking
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache") // Don't cache live segments
	w.Header().Set("Transfer-Encoding", "chunked") // Enable chunked transfer
	w.WriteHeader(http.StatusOK)

	// Get flusher for immediate response
	flusher, ok := w.(http.Flusher)
	if !ok {
		common.LogErrorln("ResponseWriter does not support flushing")
		return
	}

	// Use a VERY short timeout to prevent blocking - this is key to eliminating hitches
	ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second) // Just 1 second!
	defer cancel()

	// Create write flusher with immediate flushing
	wf := createProtectedWriteFlusher(w, flusher, ctx)

	// Use TS muxer for proper HLS segment format
	tsMuxer := ts.NewMuxer(wf)
	if tsMuxer == nil {
		common.LogErrorln("Failed to create TS muxer")
		return
	}

	// Get cursor positioned to the latest stream data
	cursor := ch.que.Latest()
	if cursor == nil {
		common.LogErrorln("Failed to get latest cursor from queue")
		return
	}

	// Copy stream data with aggressive timeout to prevent hitching
	done := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				common.LogErrorf("Recovered from panic in HLS segment handler: %v\n", r)
				done <- fmt.Errorf("panic recovered: %v", r)
			}
		}()

		// Copy with immediate timeout
		done <- avutil.CopyFile(tsMuxer, cursor)
	}()

	// Wait with AGGRESSIVE timeout to prevent any blocking/hitching
	select {
	case err := <-done:
		if err != nil && !isConnectionClosedError(err) {
			common.LogErrorf("Could not copy video to HLS segment: %v\n", err)
		}
	case <-ctx.Done():
		// This is EXPECTED for live streaming - we timeout quickly to prevent hitching
		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("HLS segment %d fast timeout (prevents hitching)\n", segmentNum)
		}
	case <-time.After(500 * time.Millisecond): // Emergency timeout even shorter
		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("HLS segment %d emergency timeout\n", segmentNum)
		}
	}

	// Always flush the response to send partial data immediately
	if flusher != nil {
		flusher.Flush()
	}

	if settings != nil && settings.HLSDebugLogging {
		common.LogInfof("HLS segment %d served (non-blocking)\n", segmentNum)
	}
}

// isConnectionClosedError checks if an error is due to a closed connection
func isConnectionClosedError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection closed") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "writer panic recovered") ||
		strings.Contains(errStr, "use of closed network connection")
}

// createProtectedWriteFlusher creates a write flusher with comprehensive nil protection
func createProtectedWriteFlusher(w http.ResponseWriter, flusher http.Flusher, ctx context.Context) writeFlusher {
	return writeFlusher{
		httpflusher: flusher,
		Writer:      w,
		ctx:         ctx,
	}
}

func handleDefault(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		// not really an error for the server, but for the client.
		common.LogInfoln("[http 404] ", r.URL.Path)
		http.NotFound(w, r)
	} else {
		handleIndexTemplate(w, r)
	}
}

func wrapAuth(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if settings.RoomAccess != AccessOpen {
			if !checkRoomAccess(w, r) {
				common.LogDebugln("Denied access")
				return
			}
			common.LogDebugln("Granted access")
		}
		next.ServeHTTP(w, r)
	})
}
