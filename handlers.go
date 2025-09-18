package main

import (
	"bytes"
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
	mediaSequence      uint64
	discontinuitySeq   uint64
	segments           []HLSSegment
	partialSegments    []HLSPartialSegment
	lastSegmentTime    time.Time
	lastPartialTime    time.Time
	segmentCache       map[uint64]*SegmentCache
	partialCache       map[string]*PartialCache
	encodingStartTime  time.Time
	lastDiscontinuity  bool
}

type HLSSegment struct {
	Index       uint64
	URL         string
	Duration    float64
	StartTime   time.Time
	EncodingEnd time.Time
}

type HLSPartialSegment struct {
	SeqID       uint64
	PartID      uint64
	URL         string
	Duration    float64
	StartTime   time.Time
	Independent bool
	EncodingEnd time.Time
}

type SegmentCache struct {
	Data     []byte
	Ready    bool
	Created  time.Time
}

type PartialCache struct {
	Data     []byte
	Ready    bool
	Created  time.Time
}

// HLS configuration constants
const (
	HLSSegmentDuration    = 6.0  // seconds per segment (as required by specification)
	HLSPartialDuration    = 2.0  // seconds per partial segment for low latency
	HLSWindowSize         = 6    // number of segments to keep in playlist (increased for better buffering)
	HLSTargetDuration     = 12.0 // target duration for HLS spec (adjusted for 6s segments)
	HLSPartTargetDuration = 2.0  // target duration for partial segments
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
	// Initialize HLS data with new structure
	ch.hlsData = &HLSData{
		mediaSequence:     0,
		discontinuitySeq:  0,
		segments:          make([]HLSSegment, 0),
		partialSegments:   make([]HLSPartialSegment, 0),
		lastSegmentTime:   time.Now(),
		lastPartialTime:   time.Now(),
		segmentCache:      make(map[uint64]*SegmentCache),
		partialCache:      make(map[string]*PartialCache),
		encodingStartTime: time.Now(),
		lastDiscontinuity: true, // Mark discontinuity for new stream
	}
	
	// Start background segment generator
	go generateHLSSegments(ch)
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
			mediaSequence:     0,
			discontinuitySeq:  0,
			segments:          make([]HLSSegment, 0),
			partialSegments:   make([]HLSPartialSegment, 0),
			lastSegmentTime:   time.Now(),
			lastPartialTime:   time.Now(),
			segmentCache:      make(map[uint64]*SegmentCache),
			partialCache:      make(map[string]*PartialCache),
			encodingStartTime: time.Now(),
			lastDiscontinuity: false,
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

	// Set playlist properties for low-latency HLS
	playlist.TargetDuration = HLSTargetDuration
	playlist.SeqNo = ch.hlsData.mediaSequence
	playlist.PartTargetDuration = HLSPartTargetDuration
	
	// Set discontinuity sequence if there have been discontinuities
	if ch.hlsData.discontinuitySeq > 0 {
		playlist.DiscontinuitySeq = ch.hlsData.discontinuitySeq
	}

	// Add segments to playlist
	for _, segment := range ch.hlsData.segments {
		err := playlist.Append(segment.URL, segment.Duration, "")
		if err != nil {
			common.LogErrorf("Error adding segment to playlist: %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	
	// Add partial segments for low-latency HLS
	for _, partial := range ch.hlsData.partialSegments {
		partialSeg := &m3u8.PartialSegment{
			SeqID:       partial.SeqID,
			URI:         partial.URL,
			Duration:    partial.Duration,
			Independent: partial.Independent,
		}
		err := playlist.AppendPartialSegment(partialSeg)
		if err != nil {
			common.LogErrorf("Error adding partial segment to playlist: %v\n", err)
			// Continue with other segments, don't fail the entire request
		}
	}

	// Set response headers for HLS manifest with strict cache control
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	// Strict no-cache headers to prevent segment reuse issues
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	
	// Add ETag based on media sequence and partial segments to help clients detect changes
	etag := fmt.Sprintf(`"%d-%d-%d"`, ch.hlsData.mediaSequence, len(ch.hlsData.segments), len(ch.hlsData.partialSegments))
	w.Header().Set("ETag", etag)
	
	// Check if client already has this version
	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	
	w.WriteHeader(http.StatusOK)

	// Write playlist
	_, err := w.Write(playlist.Encode().Bytes())
	if err != nil {
		common.LogErrorf("Error writing HLS manifest: %v\n", err)
	}

	if settings != nil && settings.HLSDebugLogging {
		common.LogInfof("Generated HLS playlist for live: %d segments, %d partials, seq: %d, discontinuity: %d\n",
			len(ch.hlsData.segments), len(ch.hlsData.partialSegments), ch.hlsData.mediaSequence, ch.hlsData.discontinuitySeq)
	}
}

func updateHLSSegments(hlsData *HLSData) {
	now := time.Now()
	timeSinceLastSegment := now.Sub(hlsData.lastSegmentTime).Seconds()
	timeSinceLastPartial := now.Sub(hlsData.lastPartialTime).Seconds()

	// Add new partial segment when we're past partial duration
	if timeSinceLastPartial >= HLSPartialDuration {
		// Calculate encoding time for performance monitoring
		encodingDuration := now.Sub(hlsData.encodingStartTime)
		
		// Create new partial segment
		partialSeqID := hlsData.mediaSequence + uint64(len(hlsData.segments))
		if len(hlsData.segments) == 0 {
			partialSeqID = hlsData.mediaSequence
		}
		
		newPartial := HLSPartialSegment{
			SeqID:       partialSeqID,
			PartID:      uint64(len(hlsData.partialSegments)) % 3, // 3 partials per segment
			URL:         fmt.Sprintf("/live_partial_%d_%d.ts", partialSeqID, uint64(len(hlsData.partialSegments))%3),
			Duration:    HLSPartialDuration,
			StartTime:   now,
			Independent: len(hlsData.partialSegments)%3 == 0, // First partial of each segment is independent
			EncodingEnd: now,
		}

		hlsData.partialSegments = append(hlsData.partialSegments, newPartial)
		hlsData.lastPartialTime = now
		hlsData.encodingStartTime = now // Reset encoding timer

		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("HLS: Added partial segment %d_%d (encoding: %.2fms), independent: %v\n",
				newPartial.SeqID, newPartial.PartID, encodingDuration.Seconds()*1000, newPartial.Independent)
		}
	}

	// Add new full segment when we're past segment duration OR have 3 partials
	shouldAddSegment := timeSinceLastSegment >= HLSSegmentDuration || len(hlsData.partialSegments) >= 3
	
	if shouldAddSegment {
		// Calculate encoding time for this segment
		encodingDuration := now.Sub(hlsData.encodingStartTime)
		
		// Add new segment with proper media sequence management
		newSegment := HLSSegment{
			Index:       hlsData.mediaSequence + uint64(len(hlsData.segments)),
			URL:         fmt.Sprintf("/live_segment_%d.ts", hlsData.mediaSequence+uint64(len(hlsData.segments))),
			Duration:    HLSSegmentDuration,
			StartTime:   now,
			EncodingEnd: now,
		}

		hlsData.segments = append(hlsData.segments, newSegment)
		hlsData.lastSegmentTime = now
		
		// Clear partial segments when we add a full segment
		hlsData.partialSegments = hlsData.partialSegments[:0]
		hlsData.lastPartialTime = now

		// Maintain sliding window - prevent segment reuse by proper sequence management
		if len(hlsData.segments) > HLSWindowSize {
			// Remove oldest segment and increment media sequence
			hlsData.segments = hlsData.segments[1:]
			hlsData.mediaSequence++
		}

		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("HLS: Added segment %d (after %.1fs, encoding: %.2fms), current seq: %d, window: %v\n",
				newSegment.Index, timeSinceLastSegment, encodingDuration.Seconds()*1000, 
				hlsData.mediaSequence, getSegmentIndexes(hlsData.segments))
		}
	}
	
	// Handle discontinuity detection for stream restarts
	if hlsData.lastDiscontinuity {
		hlsData.discontinuitySeq++
		hlsData.lastDiscontinuity = false
		
		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("HLS: Discontinuity detected, seq incremented to %d\n", hlsData.discontinuitySeq)
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

// markDiscontinuity marks that a discontinuity should be added to the next segment
func markDiscontinuity(hlsData *HLSData) {
	hlsData.Lock()
	defer hlsData.Unlock()
	hlsData.lastDiscontinuity = true
}

// generateHLSSegments runs in background to pre-generate complete segments and partials
func generateHLSSegments(ch *Channel) {
	ticker := time.NewTicker(500 * time.Millisecond) // Check every 500ms
	defer ticker.Stop()
	
	for range ticker.C {
		if ch.que == nil || ch.hlsData == nil {
			continue
		}
		
		ch.hlsData.RLock()
		needsGeneration := make([]uint64, 0)
		needsPartialGeneration := make([]string, 0)
		
		// Check which segments need to be generated
		for _, seg := range ch.hlsData.segments {
			if cache, exists := ch.hlsData.segmentCache[seg.Index]; !exists || !cache.Ready {
				needsGeneration = append(needsGeneration, seg.Index)
			}
		}
		
		// Check which partial segments need to be generated
		for _, partial := range ch.hlsData.partialSegments {
			partialKey := fmt.Sprintf("%d_%d", partial.SeqID, partial.PartID)
			if cache, exists := ch.hlsData.partialCache[partialKey]; !exists || !cache.Ready {
				needsPartialGeneration = append(needsPartialGeneration, partialKey)
			}
		}
		ch.hlsData.RUnlock()
		
		// Generate missing segments
		for _, segmentIndex := range needsGeneration {
			generateSingleSegment(ch, segmentIndex)
		}
		
		// Generate missing partial segments
		for _, partialKey := range needsPartialGeneration {
			generateSinglePartial(ch, partialKey)
		}
		
		// Clean up old segments and partials
		cleanupOldSegments(ch)
	}
}

// generateSingleSegment creates one complete segment in background
func generateSingleSegment(ch *Channel, segmentIndex uint64) {
	var segmentBuffer bytes.Buffer
	tsMuxer := ts.NewMuxer(&segmentBuffer)
	if tsMuxer == nil {
		return
	}
	
	cursor := ch.que.Latest()
	if cursor == nil {
		return
	}
	
	// Use timeout to prevent infinite blocking - monitor encoding time
	startTime := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	
	done := make(chan error, 1)
	go func() {
		done <- avutil.CopyFile(tsMuxer, cursor)
	}()
	
	select {
	case <-done:
		encodingTime := time.Since(startTime)
		// Segment completed
		if segmentBuffer.Len() > 0 {
			ch.hlsData.Lock()
			if ch.hlsData.segmentCache == nil {
				ch.hlsData.segmentCache = make(map[uint64]*SegmentCache)
			}
			ch.hlsData.segmentCache[segmentIndex] = &SegmentCache{
				Data:    segmentBuffer.Bytes(),
				Ready:   true,
				Created: time.Now(),
			}
			ch.hlsData.Unlock()
			
			if settings != nil && settings.HLSDebugLogging {
				common.LogInfof("HLS: Generated segment %d in %.2fms (%d bytes)\n", 
					segmentIndex, encodingTime.Seconds()*1000, segmentBuffer.Len())
			}
		}
	case <-ctx.Done():
		encodingTime := time.Since(startTime)
		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("HLS: Segment %d generation timeout after %.2fms\n", 
				segmentIndex, encodingTime.Seconds()*1000)
		}
		return
	}
}

// generateSinglePartial creates one partial segment in background
func generateSinglePartial(ch *Channel, partialKey string) {
	var partialBuffer bytes.Buffer
	tsMuxer := ts.NewMuxer(&partialBuffer)
	if tsMuxer == nil {
		return
	}
	
	cursor := ch.que.Latest()
	if cursor == nil {
		return
	}
	
	// Use shorter timeout for partial segments - monitor encoding time
	startTime := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	
	done := make(chan error, 1)
	go func() {
		done <- avutil.CopyFile(tsMuxer, cursor)
	}()
	
	select {
	case <-done:
		encodingTime := time.Since(startTime)
		// Partial segment completed
		if partialBuffer.Len() > 0 {
			ch.hlsData.Lock()
			if ch.hlsData.partialCache == nil {
				ch.hlsData.partialCache = make(map[string]*PartialCache)
			}
			ch.hlsData.partialCache[partialKey] = &PartialCache{
				Data:    partialBuffer.Bytes(),
				Ready:   true,
				Created: time.Now(),
			}
			ch.hlsData.Unlock()
			
			if settings != nil && settings.HLSDebugLogging {
				common.LogInfof("HLS: Generated partial %s in %.2fms (%d bytes)\n", 
					partialKey, encodingTime.Seconds()*1000, partialBuffer.Len())
			}
		}
	case <-ctx.Done():
		encodingTime := time.Since(startTime)
		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("HLS: Partial %s generation timeout after %.2fms\n", 
				partialKey, encodingTime.Seconds()*1000)
		}
		return
	}
}

// cleanupOldSegments removes segments and partials that are no longer in the manifest
func cleanupOldSegments(ch *Channel) {
	ch.hlsData.Lock()
	defer ch.hlsData.Unlock()
	
	if ch.hlsData.segmentCache == nil && ch.hlsData.partialCache == nil {
		return
	}
	
	// Clean up old full segments
	if ch.hlsData.segmentCache != nil {
		// Get current valid segment indexes
		validIndexes := make(map[uint64]bool)
		for _, seg := range ch.hlsData.segments {
			validIndexes[seg.Index] = true
		}
		
		// Remove segments not in current window
		for index := range ch.hlsData.segmentCache {
			if !validIndexes[index] {
				delete(ch.hlsData.segmentCache, index)
			}
		}
	}
	
	// Clean up old partial segments
	if ch.hlsData.partialCache != nil {
		// Get current valid partial keys
		validPartials := make(map[string]bool)
		for _, partial := range ch.hlsData.partialSegments {
			partialKey := fmt.Sprintf("%d_%d", partial.SeqID, partial.PartID)
			validPartials[partialKey] = true
		}
		
		// Remove partials not in current list
		for key := range ch.hlsData.partialCache {
			if !validPartials[key] {
				delete(ch.hlsData.partialCache, key)
			}
		}
	}
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

	// Comprehensive validation with nil checks to prevent segfaults
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
	
	// Check if we have a pre-generated segment ready
	var segmentData []byte
	if validSegment && ch.hlsData.segmentCache != nil {
		if cache, exists := ch.hlsData.segmentCache[segmentNum]; exists && cache.Ready && len(cache.Data) > 0 {
			segmentData = cache.Data
		}
	}
	ch.hlsData.RUnlock()

	if !validSegment {
		common.LogInfof("Requested segment %d not in current window\n", segmentNum)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// If we have pre-generated data, serve it immediately
	if len(segmentData) > 0 {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Content-Length", strconv.Itoa(len(segmentData)))
		w.WriteHeader(http.StatusOK)
		
		_, err := w.Write(segmentData)
		if err != nil {
			common.LogErrorf("Error writing pre-generated HLS segment %d: %v\n", segmentNum, err)
		}
		
		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("Served pre-generated HLS segment %d (%d bytes)\n", segmentNum, len(segmentData))
		}
		return
	}

	// Fallback: generate segment on-demand with short timeout to prevent hitching
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.WriteHeader(http.StatusOK)

	// Get HTTP flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		common.LogErrorln("ResponseWriter does not support flushing")
		return
	}

	// Very short timeout to prevent hitching
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	// Create write flusher
	wf := createProtectedWriteFlusher(w, flusher, ctx)

	// Use TS muxer
	tsMuxer := ts.NewMuxer(wf)
	if tsMuxer == nil {
		common.LogErrorln("Failed to create TS muxer")
		return
	}

	// Get cursor
	cursor := ch.que.Latest()
	if cursor == nil {
		common.LogErrorln("Failed to get latest cursor from queue")
		return
	}

	// Stream with short timeout to prevent blocking
	done := make(chan error, 1)
	go func() {
		done <- avutil.CopyFile(tsMuxer, cursor)
	}()

	select {
	case err := <-done:
		if err != nil && !isConnectionClosedError(err) {
			common.LogErrorf("Could not copy video to HLS segment: %v\n", err)
		}
		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("HLS segment %d completed (fallback)\n", segmentNum)
		}
	case <-ctx.Done():
		// Short timeout hit - this prevents hitching
		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("HLS segment %d timeout (preventing hitch)\n", segmentNum)
		}
	}
}

func handleHLSPartialSegment(w http.ResponseWriter, r *http.Request) {
	// Extract partial segment info from URL: /live_partial_seqid_partid.ts
	path := strings.TrimPrefix(r.URL.Path, "/live_partial_")
	path = strings.TrimSuffix(path, ".ts")
	
	parts := strings.Split(path, "_")
	if len(parts) != 2 {
		common.LogErrorf("Invalid partial segment path: %s\n", path)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	
	seqID, err1 := strconv.ParseUint(parts[0], 10, 64)
	partID, err2 := strconv.ParseUint(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		common.LogErrorf("Invalid partial segment numbers: %s_%s\n", parts[0], parts[1])
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	
	l.RLock()
	ch := channels["live"]
	l.RUnlock()

	// Comprehensive validation with nil checks
	if ch == nil || ch.que == nil || ch.hlsData == nil {
		common.LogInfoln("HLS partial segment request for non-existent or incomplete channel")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	partialKey := fmt.Sprintf("%d_%d", seqID, partID)
	
	ch.hlsData.RLock()
	// Validate partial segment is in current list
	validPartial := false
	for _, partial := range ch.hlsData.partialSegments {
		if partial.SeqID == seqID && partial.PartID == partID {
			validPartial = true
			break
		}
	}
	
	// Check if we have a pre-generated partial segment ready
	var partialData []byte
	if validPartial && ch.hlsData.partialCache != nil {
		if cache, exists := ch.hlsData.partialCache[partialKey]; exists && cache.Ready && len(cache.Data) > 0 {
			partialData = cache.Data
		}
	}
	ch.hlsData.RUnlock()

	if !validPartial {
		common.LogInfof("Requested partial segment %s not in current list\n", partialKey)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Set headers with strict no-cache for partial segments
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	// If we have pre-generated data, serve it immediately
	if len(partialData) > 0 {
		w.Header().Set("Content-Length", strconv.Itoa(len(partialData)))
		w.WriteHeader(http.StatusOK)
		
		_, err := w.Write(partialData)
		if err != nil {
			common.LogErrorf("Error writing pre-generated HLS partial segment %s: %v\n", partialKey, err)
		}
		
		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("Served pre-generated HLS partial segment %s (%d bytes)\n", partialKey, len(partialData))
		}
		return
	}

	// Fallback: generate partial segment on-demand with very short timeout
	w.WriteHeader(http.StatusOK)
	
	flusher, ok := w.(http.Flusher)
	if !ok {
		common.LogErrorln("ResponseWriter does not support flushing")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(HLSPartialDuration/2)*time.Second)
	defer cancel()

	wf := createProtectedWriteFlusher(w, flusher, ctx)
	tsMuxer := ts.NewMuxer(wf)
	if tsMuxer == nil {
		common.LogErrorln("Failed to create TS muxer for partial segment")
		return
	}

	cursor := ch.que.Latest()
	if cursor == nil {
		common.LogErrorln("Failed to get latest cursor for partial segment")
		return
	}

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				common.LogErrorf("Recovered from panic in HLS partial segment handler: %v\n", r)
				done <- fmt.Errorf("panic recovered: %v", r)
			}
		}()
		done <- avutil.CopyFile(tsMuxer, cursor)
	}()

	select {
	case err := <-done:
		if err != nil && !isConnectionClosedError(err) {
			common.LogErrorf("Could not copy video to HLS partial segment: %v\n", err)
		}
		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("HLS partial segment %s completed\n", partialKey)
		}
	case <-ctx.Done():
		if settings != nil && settings.HLSDebugLogging {
			common.LogInfof("HLS partial segment %s timeout (preventing hitch)\n", partialKey)
		}
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
