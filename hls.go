package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Eyevinn/hls-m3u8/m3u8"
	"github.com/nareix/joy4/av/pubsub"
	"github.com/zorchenhimer/MovieNight/common"
)

// HLSConfig represents configuration for HLS streaming
type HLSConfig struct {
	SegmentDuration       time.Duration // Duration of each segment
	MaxSegments          int           // Maximum number of segments to keep in memory
	TargetDuration       time.Duration // Target duration for playlist
	BitrateReduction     float64       // Bitrate reduction factor for HLS (0.0-1.0)
	EnableLowLatency     bool          // Enable low latency optimizations
	MaxConcurrentSegments int          // Maximum number of segments to generate concurrently
	SegmentBufferSize    int           // Buffer size for segment data
	QualityAdaptation    bool          // Enable adaptive quality based on device capabilities
}

// DefaultHLSConfig returns the default HLS configuration
func DefaultHLSConfig() HLSConfig {
	return HLSConfig{
		SegmentDuration:       4 * time.Second, // Shorter segments for lower latency
		MaxSegments:          6,                 // Fewer segments for faster processing
		TargetDuration:       4 * time.Second,  // Match segment duration
		BitrateReduction:     0.7,              // 30% reduction for HLS efficiency
		EnableLowLatency:     true,
		MaxConcurrentSegments: 4,               // More concurrent processing
		SegmentBufferSize:    512 * 1024,       // Smaller buffer for faster processing
		QualityAdaptation:    true,
	}
}

// HLSQualitySettings represents quality settings for different device types
type HLSQualitySettings struct {
	BitrateMultiplier float64 // Multiplier for bitrate (1.0 = original, 0.7 = 30% reduction)
	Resolution        string  // Target resolution
	FrameRate         int     // Target frame rate
	KeyFrameInterval  int     // Key frame interval in seconds
}

// GetQualitySettings returns appropriate quality settings based on device capabilities
func GetQualitySettings(capabilities DeviceCapabilities) HLSQualitySettings {
	if capabilities.IsIOS {
		if capabilities.IsMobile {
			// iOS Mobile - optimize for battery and bandwidth
			return HLSQualitySettings{
				BitrateMultiplier: 0.7,  // 30% reduction
				Resolution:        "720p",
				FrameRate:         30,
				KeyFrameInterval:  2,
			}
		} else {
			// iOS Desktop (macOS) - higher quality
			return HLSQualitySettings{
				BitrateMultiplier: 0.85, // 15% reduction
				Resolution:        "1080p",
				FrameRate:         60,
				KeyFrameInterval:  2,
			}
		}
	} else if capabilities.IsAndroid {
		// Android devices - balance quality and performance
		return HLSQualitySettings{
			BitrateMultiplier: 0.75, // 25% reduction
			Resolution:        "720p",
			FrameRate:         30,
			KeyFrameInterval:  2,
		}
	}

	// Desktop/Other - use default HLS settings with moderate reduction
	return HLSQualitySettings{
		BitrateMultiplier: 0.8, // 20% reduction for HLS overhead
		Resolution:        "1080p",
		FrameRate:         60,
		KeyFrameInterval:  2,
	}
}

// HLSChannel represents an HLS stream with playlist and segments
type HLSChannel struct {
	que              *pubsub.Queue
	playlist         *m3u8.MediaPlaylist
	segments         []HLSSegment
	targetDuration   time.Duration
	sequenceNumber   uint64
	mutex            sync.RWMutex
	ctx              context.Context
	cancel           context.CancelFunc
	segmentDuration  time.Duration
	maxSegments      int
	viewers          map[string]int // Track HLS viewers
	viewersMutex     sync.RWMutex
	config           HLSConfig
}

// HLSSegment represents a single HLS segment
type HLSSegment struct {
	URI      string
	Duration float64
	Data     []byte
	Sequence uint64
}

// NewHLSChannel creates a new HLS channel
func NewHLSChannel(que *pubsub.Queue) (*HLSChannel, error) {
	if que == nil {
		return nil, fmt.Errorf("queue cannot be nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	
	config := DefaultHLSConfig()
	
	// Create playlist with sliding window for live streaming
	// Important: Use the proper pattern for sliding window
	windowSize := uint(config.MaxSegments)
	playlist, err := m3u8.NewMediaPlaylist(windowSize, windowSize)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create playlist: %w", err)
	}

	// Set playlist properties for optimal HLS performance and sliding window
	playlist.SetVersion(6) // HLS version 6 for better live streaming support
	playlist.Closed = false // Keep playlist open for live streaming (sliding window)
	
	hls := &HLSChannel{
		que:             que,
		playlist:        playlist,
		segments:        make([]HLSSegment, 0),
		targetDuration:  config.TargetDuration,
		sequenceNumber:  0,
		ctx:             ctx,
		cancel:          cancel,
		segmentDuration: config.SegmentDuration,
		maxSegments:     config.MaxSegments,
		viewers:         make(map[string]int),
		config:          config,
	}

	return hls, nil
}

// NewHLSChannelWithDeviceOptimization creates a new HLS channel optimized for specific device capabilities
func NewHLSChannelWithDeviceOptimization(que *pubsub.Queue, r *http.Request) (*HLSChannel, error) {
	if que == nil {
		return nil, fmt.Errorf("queue cannot be nil")
	}

	// Detect device capabilities for optimization
	capabilities := DeviceCapabilities{}
	if r != nil {
		capabilities = DetectDeviceCapabilities(r)
	}

	qualitySettings := GetQualitySettings(capabilities)
	
	ctx, cancel := context.WithCancel(context.Background())
	
	config := DefaultHLSConfig()
	
	// Apply device-specific optimizations
	config.BitrateReduction = qualitySettings.BitrateMultiplier
	
	if capabilities.IsIOS && capabilities.IsMobile {
		// Optimize for iOS mobile devices - prioritize low latency
		config.SegmentDuration = 3 * time.Second  // Very short segments for low latency
		config.MaxSegments = 5                     // Minimal segments for fast processing
		config.EnableLowLatency = true
		config.MaxConcurrentSegments = 3          // Balanced concurrency for mobile
	} else if capabilities.IsAndroid {
		// Optimize for Android devices
		config.SegmentDuration = 4 * time.Second  // Short segments for good performance
		config.MaxSegments = 6
		config.MaxConcurrentSegments = 3
	} else {
		// Desktop optimization - can handle slightly longer segments
		config.SegmentDuration = 4 * time.Second  // Still short for low latency
		config.MaxSegments = 8
		config.MaxConcurrentSegments = 5
	}

	// Create playlist with device-optimized settings for proper sliding window
	windowSize := uint(config.MaxSegments)
	playlist, err := m3u8.NewMediaPlaylist(windowSize, windowSize)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create optimized playlist: %w", err)
	}
	playlist.SetVersion(6)
	playlist.Closed = false  // Keep playlist open for live streaming (sliding window)
	
	hls := &HLSChannel{
		que:             que,
		playlist:        playlist,
		segments:        make([]HLSSegment, 0),
		targetDuration:  config.TargetDuration,
		sequenceNumber:  0,
		ctx:             ctx,
		cancel:          cancel,
		segmentDuration: config.SegmentDuration,
		maxSegments:     config.MaxSegments,
		viewers:         make(map[string]int),
		config:          config,
	}

	common.LogDebugf("Created HLS channel optimized for device: iOS=%v, Mobile=%v, BitrateReduction=%.2f\n", 
		capabilities.IsIOS, capabilities.IsMobile, config.BitrateReduction)

	return hls, nil
}

// Start begins HLS segment generation
func (h *HLSChannel) Start() error {
	if h == nil {
		return fmt.Errorf("HLS channel is nil")
	}

	go h.generateSegments()
	return nil
}

// Stop stops HLS segment generation
func (h *HLSChannel) Stop() {
	if h != nil {
		if h.cancel != nil {
			h.cancel()
		}
	}
}

// generateSegments continuously generates HLS segments from the stream
func (h *HLSChannel) generateSegments() {
	if h == nil || h.que == nil {
		common.LogErrorln("Cannot generate segments: HLS channel or queue is nil")
		return
	}

	cursor := h.que.Latest()
	if cursor == nil {
		common.LogErrorln("Cannot get latest cursor from queue")
		return
	}

	segmentBuffer := make([]byte, 0, 1024*1024) // 1MB initial buffer
	segmentStartTime := time.Now()
	lastDataTime := time.Now()

	for {
		select {
		case <-h.ctx.Done():
			return
		default:
			// Read data from the stream and buffer it
			packet, err := cursor.ReadPacket()
			if err != nil {
				if err != io.EOF {
					common.LogErrorf("Error reading from stream cursor: %v\n", err)
				}
				
				// If we haven't seen data in a while but have some buffered, create a segment
				timeSinceData := time.Since(lastDataTime)
				if len(segmentBuffer) > 0 && timeSinceData > 5*time.Second {
					common.LogDebugf("Creating segment due to timeout with %d bytes\n", len(segmentBuffer))
					h.createSegment(segmentBuffer, time.Since(segmentStartTime))
					segmentBuffer = segmentBuffer[:0]
					segmentStartTime = time.Now()
				}
				
				time.Sleep(100 * time.Millisecond)
				continue
			}

			lastDataTime = time.Now()
			
			// Convert packet to bytes for buffering
			packetData := packet.Data
			if packetData != nil {
				segmentBuffer = append(segmentBuffer, packetData...)
			}

			// Check if we should create a new segment
			// Use smaller buffer sizes and prefer time-based segmentation for low latency
			elapsed := time.Since(segmentStartTime)
			bufferSize := len(segmentBuffer)
			
			// Create segment if we hit time limit OR if buffer gets reasonably sized
			// Prioritize time-based segmentation for lower latency
			shouldCreateSegment := elapsed >= h.segmentDuration || 
							  (bufferSize > 512*1024 && elapsed >= h.segmentDuration/2) ||
							  bufferSize > 1024*1024 // Fallback for large buffers
							  
			if shouldCreateSegment && bufferSize > 0 {
				h.createSegment(segmentBuffer, time.Since(segmentStartTime))
				segmentBuffer = segmentBuffer[:0] // Reset buffer
				segmentStartTime = time.Now()
			}
		}
	}
}

// createSegment creates a new HLS segment
func (h *HLSChannel) createSegment(data []byte, duration time.Duration) {
	if h == nil {
		common.LogErrorln("Cannot create segment: HLS channel is nil")
		return
	}

	if len(data) == 0 {
		return // Skip empty segments
	}

	// Convert data to TS format
	tsData, err := h.convertToTS(data)
	if err != nil {
		common.LogErrorf("Failed to convert segment to TS: %v\n", err)
		return
	}

	currentSeq := h.sequenceNumber
	h.sequenceNumber++ // Increment for next segment
	
	segmentURI := fmt.Sprintf("/live/segment_%d.ts", currentSeq)
	durationSeconds := duration.Seconds()

	segment := HLSSegment{
		URI:      segmentURI,
		Duration: durationSeconds,
		Data:     tsData,
		Sequence: currentSeq,
	}

	// Add segment with proper sliding window management
	h.addGeneratedSegment(segment)
}

// convertToTS converts raw data to MPEG-TS format
func (h *HLSChannel) convertToTS(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("no data to convert")
	}

	// Create a simple TS muxer
	// Note: This is a simplified implementation
	// In a production environment, you'd want more sophisticated TS muxing
	
	// Basic TS packet structure (simplified)
	tsPacketSize := 188
	headerSize := 4
	payloadSize := tsPacketSize - headerSize

	numPackets := (len(data) + payloadSize - 1) / payloadSize
	tsData := make([]byte, 0, numPackets*tsPacketSize)

	for i := 0; i < numPackets; i++ {
		packet := make([]byte, tsPacketSize)
		
		// TS packet header
		packet[0] = 0x47 // Sync byte
		packet[1] = 0x00 // Transport Error Indicator, Payload Unit Start, Transport Priority, PID (high 5 bits)
		packet[2] = 0x01 // PID (low 8 bits) - use PID 1 for video
		packet[3] = 0x10 // Continuity counter and adaptation field control

		// Copy payload data
		start := i * payloadSize
		end := start + payloadSize
		if end > len(data) {
			end = len(data)
		}

		if start < len(data) {
			payloadLen := end - start
			copy(packet[headerSize:headerSize+payloadLen], data[start:end])
			
			// Pad remaining bytes with 0xFF (null packets)
			for j := headerSize + payloadLen; j < tsPacketSize; j++ {
				packet[j] = 0xFF
			}
		}

		tsData = append(tsData, packet...)
	}

	return tsData, nil
}

// addGeneratedSegment adds a generated segment to the HLS channel with proper sliding window
func (h *HLSChannel) addGeneratedSegment(segment HLSSegment) {
	if h == nil || h.playlist == nil {
		return
	}

	h.mutex.Lock()
	defer h.mutex.Unlock()

	// Add segment to our local list with sliding window management
	h.segments = append(h.segments, segment)
	
	// Remove old segments if we exceed max (manual sliding window for our data)
	if len(h.segments) > h.maxSegments {
		// Remove oldest segments to maintain window size
		excess := len(h.segments) - h.maxSegments
		h.segments = h.segments[excess:]
	}

	// For proper sliding window, we need to manually manage the playlist size
	// If the playlist is at max capacity, we need to remove the oldest segment first
	if int(h.playlist.Count()) >= h.maxSegments {
		// Create a new playlist and copy the recent segments
		newPlaylist, err := m3u8.NewMediaPlaylist(uint(h.maxSegments), uint(h.maxSegments))
		if err != nil {
			common.LogErrorf("Failed to create new playlist for sliding window: %v\n", err)
			return
		}
		newPlaylist.SetVersion(6)
		newPlaylist.Closed = false
		
		// Add only the segments that should remain (excluding the oldest one)
		segmentsToKeep := h.maxSegments - 1 // Leave room for the new segment
		startIdx := len(h.segments) - segmentsToKeep
		if startIdx < 0 {
			startIdx = 0
		}
		
		for i := startIdx; i < len(h.segments)-1; i++ { // -1 because we haven't added the new segment yet
			seg := h.segments[i]
			newPlaylist.Append(seg.URI, seg.Duration, "")
		}
		
		// Replace the old playlist
		h.playlist = newPlaylist
	}
	
	// Now add the new segment
	h.playlist.Append(segment.URI, segment.Duration, "")

	// Update target duration if needed
	duration := time.Duration(segment.Duration * float64(time.Second))
	if duration > h.targetDuration {
		h.targetDuration = duration
		h.playlist.TargetDuration = uint(segment.Duration)
	}

	common.LogDebugf("Added generated HLS segment %d with duration %.2fs (playlist count: %d/%d)\n", 
		segment.Sequence, segment.Duration, h.playlist.Count(), h.maxSegments)
}

// GetPlaylist returns the current m3u8 playlist
func (h *HLSChannel) GetPlaylist() string {
	if h == nil || h.playlist == nil {
		return ""
	}

	h.mutex.RLock()
	defer h.mutex.RUnlock()

	// Set final playlist properties
	h.playlist.TargetDuration = uint(h.targetDuration.Seconds())
	
	return h.playlist.String()
}

// HasSegments returns true if the playlist has any segments
func (h *HLSChannel) HasSegments() bool {
	if h == nil {
		return false
	}

	h.mutex.RLock()
	defer h.mutex.RUnlock()

	return len(h.segments) > 0
}

// GetSegment returns a specific segment by sequence number
func (h *HLSChannel) GetSegment(sequence uint64) ([]byte, error) {
	if h == nil {
		return nil, fmt.Errorf("HLS channel is nil")
	}

	h.mutex.RLock()
	defer h.mutex.RUnlock()

	for _, segment := range h.segments {
		if segment.Sequence == sequence {
			return segment.Data, nil
		}
	}

	return nil, fmt.Errorf("segment %d not found", sequence)
}

// GetSegmentByURI returns a segment by its URI
func (h *HLSChannel) GetSegmentByURI(uri string) ([]byte, error) {
	if h == nil {
		return nil, fmt.Errorf("HLS channel is nil")
	}

	h.mutex.RLock()
	defer h.mutex.RUnlock()

	for _, segment := range h.segments {
		if segment.URI == uri {
			return segment.Data, nil
		}
	}

	return nil, fmt.Errorf("segment with URI %s not found", uri)
}

// AddViewer adds an HLS viewer for tracking
func (h *HLSChannel) AddViewer(sessionID string) {
	if h == nil {
		return
	}

	h.viewersMutex.Lock()
	defer h.viewersMutex.Unlock()

	h.viewers[sessionID] = 1
}

// RemoveViewer removes an HLS viewer
func (h *HLSChannel) RemoveViewer(sessionID string) {
	if h == nil {
		return
	}

	h.viewersMutex.Lock()
	defer h.viewersMutex.Unlock()

	delete(h.viewers, sessionID)
}

// GetViewerCount returns the number of HLS viewers
func (h *HLSChannel) GetViewerCount() int {
	if h == nil {
		return 0
	}

	h.viewersMutex.RLock()
	defer h.viewersMutex.RUnlock()

	return len(h.viewers)
}

// IsValidSegmentURI checks if a segment URI is valid
func IsValidSegmentURI(uri string) bool {
	if uri == "" {
		return false
	}
	
	// Check if it's a .ts segment
	if !strings.HasSuffix(uri, ".ts") {
		return false
	}
	
	// Extract the filename from the URI (handle both relative and absolute paths)
	filename := uri
	if strings.Contains(uri, "/") {
		parts := strings.Split(uri, "/")
		filename = parts[len(parts)-1]
	}
	
	// Check if filename matches segment pattern
	if !strings.HasPrefix(filename, "segment_") {
		return false
	}

	// Extract sequence number and validate
	name := strings.TrimSuffix(filename, ".ts")
	parts := strings.Split(name, "_")
	if len(parts) != 2 {
		return false
	}

	// Check if the sequence number is valid
	_, err := strconv.ParseUint(parts[1], 10, 64)
	return err == nil
}

// ParseSequenceFromURI extracts sequence number from segment URI
func ParseSequenceFromURI(uri string) (uint64, error) {
	if !IsValidSegmentURI(uri) {
		return 0, fmt.Errorf("invalid segment URI: %s", uri)
	}

	// Extract the filename from the URI (handle both relative and absolute paths)
	filename := uri
	if strings.Contains(uri, "/") {
		parts := strings.Split(uri, "/")
		filename = parts[len(parts)-1]
	}

	// Extract sequence number from "segment_N.ts"
	name := strings.TrimSuffix(filename, ".ts")
	parts := strings.Split(name, "_")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid segment URI format: %s", uri)
	}

	sequence, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid sequence number in URI %s: %w", uri, err)
	}

	return sequence, nil
}