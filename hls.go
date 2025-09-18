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
	workerPool       *SegmentWorkerPool // For concurrent segment generation
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
	// For live playlists, winsize should be the actual window size we want to display
	// and capacity should be at least winsize (can be larger for buffering)
	windowSize := uint(config.MaxSegments)
	capacity := windowSize * 2  // Double capacity for smooth sliding window operation
	playlist, err := m3u8.NewMediaPlaylist(windowSize, capacity)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create playlist: %w", err)
	}

	// Set playlist properties for optimal HLS performance and sliding window
	playlist.SetVersion(6) // HLS version 6 for better live streaming support
	// For live streaming, we don't close the playlist - it stays open for continuous updates
	
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

	// Initialize worker pool for concurrent segment generation
	hls.workerPool = NewSegmentWorkerPool(config, hls)

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
	
	// Create playlist with sliding window for live streaming
	// For live playlists, winsize should be the actual window size we want to display
	// For proper sliding window behavior, set capacity equal to windowSize for live streaming
	windowSize := uint(config.MaxSegments)
	playlist, err := m3u8.NewMediaPlaylist(windowSize, windowSize)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create playlist: %w", err)
	}

	// Set playlist properties for optimal HLS performance and sliding window
	playlist.SetVersion(6) // HLS version 6 for better live streaming support
	playlist.Closed = false // Keep playlist open for live streaming (sliding window)
	
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

	// Recreate playlist with device-optimized settings for proper sliding window
	windowSize = uint(config.MaxSegments)
	playlist, err = m3u8.NewMediaPlaylist(windowSize, windowSize)
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

	// Initialize worker pool for concurrent segment generation
	hls.workerPool = NewSegmentWorkerPool(config, hls)

	common.LogDebugf("Created HLS channel optimized for device: iOS=%v, Mobile=%v, BitrateReduction=%.2f\n", 
		capabilities.IsIOS, capabilities.IsMobile, config.BitrateReduction)

	return hls, nil
}

// Start begins HLS segment generation
func (h *HLSChannel) Start() error {
	if h == nil {
		return fmt.Errorf("HLS channel is nil")
	}

	// Start the worker pool
	if h.workerPool != nil {
		h.workerPool.Start()
	}

	go h.generateSegments()
	return nil
}

// Stop stops HLS segment generation
func (h *HLSChannel) Stop() {
	if h != nil {
		if h.workerPool != nil {
			h.workerPool.Stop()
		}
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
			// Note: This is a simplified approach - in production you'd want proper stream parsing
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
				// Submit to worker pool for concurrent processing
				if h.workerPool != nil {
					h.workerPool.SubmitJob(
						append([]byte(nil), segmentBuffer...), // Copy the buffer
						elapsed,
						h.sequenceNumber,
					)
					h.sequenceNumber++
				} else {
					// Fallback to synchronous processing
					h.createSegment(segmentBuffer, time.Since(segmentStartTime))
				}
				segmentBuffer = segmentBuffer[:0] // Reset buffer
				segmentStartTime = time.Now()
			}
		}
	}
}

// createSegment creates a new HLS segment (for direct/synchronous processing)
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

	// Note: sequence number should be managed by the caller
	currentSeq := h.sequenceNumber
	h.sequenceNumber++ // Increment for next segment
	
	segmentURI := fmt.Sprintf("segment_%d.ts", currentSeq)
	durationSeconds := duration.Seconds()

	segment := HLSSegment{
		URI:      segmentURI,
		Duration: durationSeconds,
		Data:     tsData,
		Sequence: currentSeq,
	}

	// Use the centralized segment addition method
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
	// For now, we'll wrap the data in basic TS packets
	
	// Basic TS packet structure (simplified)
	// This is a placeholder implementation - in reality you'd need proper TS muxing
	tsPacketSize := 188
	numPackets := (len(data) + tsPacketSize - 1) / tsPacketSize
	tsData := make([]byte, 0, numPackets*tsPacketSize)

	for i := 0; i < numPackets; i++ {
		packet := make([]byte, tsPacketSize)
		packet[0] = 0x47 // TS sync byte
		
		// Copy data into packet payload (simplified)
		start := i * (tsPacketSize - 4)
		end := start + (tsPacketSize - 4)
		if end > len(data) {
			end = len(data)
		}
		
		if start < len(data) {
			copy(packet[4:], data[start:end])
		}
		
		tsData = append(tsData, packet...)
	}

	return tsData, nil
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
	if !strings.HasSuffix(uri, ".ts") || !strings.HasPrefix(uri, "segment_") {
		return false
	}

	// Extract sequence number and validate
	name := strings.TrimSuffix(uri, ".ts")
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

	// Extract sequence number from "segment_N.ts"
	name := strings.TrimSuffix(uri, ".ts")
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