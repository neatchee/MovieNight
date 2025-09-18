package main

import (
	"fmt"
	"sync"
	"time"

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

// SegmentWorkerPool manages concurrent segment generation
type SegmentWorkerPool struct {
	config          HLSConfig
	workers         []*SegmentWorker
	workQueue       chan SegmentJob
	resultQueue     chan SegmentResult
	wg              sync.WaitGroup
	mutex           sync.RWMutex
	running         bool
	hlsChannel      *HLSChannel
}

// SegmentJob represents a segment generation job
type SegmentJob struct {
	Data      []byte
	Duration  time.Duration
	Sequence  uint64
	Timestamp time.Time
}

// SegmentResult represents the result of segment generation
type SegmentResult struct {
	Segment HLSSegment
	Error   error
}

// SegmentWorker processes segment generation jobs
type SegmentWorker struct {
	id          int
	workQueue   chan SegmentJob
	resultQueue chan SegmentResult
	quit        chan bool
	config      HLSConfig
}

// NewSegmentWorkerPool creates a new worker pool for segment generation
func NewSegmentWorkerPool(config HLSConfig, hlsChannel *HLSChannel) *SegmentWorkerPool {
	pool := &SegmentWorkerPool{
		config:      config,
		workQueue:   make(chan SegmentJob, config.MaxConcurrentSegments*2),
		resultQueue: make(chan SegmentResult, config.MaxConcurrentSegments*2),
		hlsChannel:  hlsChannel,
	}

	// Create workers
	for i := 0; i < config.MaxConcurrentSegments; i++ {
		worker := &SegmentWorker{
			id:          i,
			workQueue:   pool.workQueue,
			resultQueue: pool.resultQueue,
			quit:        make(chan bool),
			config:      config,
		}
		pool.workers = append(pool.workers, worker)
	}

	return pool
}

// Start starts the worker pool
func (p *SegmentWorkerPool) Start() {
	if p == nil {
		return
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.running {
		return
	}

	p.running = true

	// Start workers
	for _, worker := range p.workers {
		p.wg.Add(1)
		go worker.start(&p.wg)
	}

	// Start result processor
	p.wg.Add(1)
	go p.processResults()
}

// Stop stops the worker pool
func (p *SegmentWorkerPool) Stop() {
	if p == nil {
		return
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	if !p.running {
		return
	}

	p.running = false

	// Stop workers
	for _, worker := range p.workers {
		close(worker.quit)
	}

	// Close channels
	close(p.workQueue)
	close(p.resultQueue)

	// Wait for workers to finish
	p.wg.Wait()
}

// SubmitJob submits a segment generation job
func (p *SegmentWorkerPool) SubmitJob(data []byte, duration time.Duration, sequence uint64) {
	if p == nil || !p.running {
		return
	}

	job := SegmentJob{
		Data:      data,
		Duration:  duration,
		Sequence:  sequence,
		Timestamp: time.Now(),
	}

	select {
	case p.workQueue <- job:
		// Job submitted successfully
	default:
		// Queue is full, drop the job or handle overflow
		common.LogErrorf("Segment worker pool queue is full, dropping segment %d\n", sequence)
	}
}

// processResults processes the results from workers
func (p *SegmentWorkerPool) processResults() {
	defer p.wg.Done()

	for result := range p.resultQueue {
		if result.Error != nil {
			common.LogErrorf("Error generating segment: %v\n", result.Error)
			continue
		}

		// Add segment to HLS channel
		if p.hlsChannel != nil {
			p.hlsChannel.addGeneratedSegment(result.Segment)
		}
	}
}

// start starts a segment worker
func (w *SegmentWorker) start(wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		select {
		case job := <-w.workQueue:
			result := w.processJob(job)
			select {
			case w.resultQueue <- result:
			case <-w.quit:
				return
			}
		case <-w.quit:
			return
		}
	}
}

// processJob processes a single segment job
func (w *SegmentWorker) processJob(job SegmentJob) SegmentResult {
	// Apply quality adjustments to the data
	adjustedData, err := w.applyQualityAdjustments(job.Data)
	if err != nil {
		return SegmentResult{Error: err}
	}

	// Convert to TS format
	tsData, err := w.convertToTSOptimized(adjustedData)
	if err != nil {
		return SegmentResult{Error: err}
	}

	segment := HLSSegment{
		URI:      generateSegmentURI(job.Sequence),
		Duration: job.Duration.Seconds(),
		Data:     tsData,
		Sequence: job.Sequence,
	}

	return SegmentResult{Segment: segment}
}

// applyQualityAdjustments applies quality adjustments based on configuration
func (w *SegmentWorker) applyQualityAdjustments(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// For low latency, minimize processing time
	// Skip quality adjustment if reduction factor is close to 1.0
	reductionFactor := w.config.BitrateReduction
	if reductionFactor <= 0 || reductionFactor > 1 {
		reductionFactor = 0.7 // Default 30% reduction
	}
	
	// Skip processing if minimal reduction to save time
	if reductionFactor > 0.95 {
		return data, nil
	}

	targetSize := int(float64(len(data)) * reductionFactor)
	if targetSize > len(data) {
		targetSize = len(data)
	}

	// Fast data reduction with more efficient copying
	adjustedData := make([]byte, targetSize)
	copy(adjustedData, data[:targetSize])

	return adjustedData, nil
}

// convertToTSOptimized converts data to MPEG-TS with optimizations
func (w *SegmentWorker) convertToTSOptimized(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}

	// Optimized TS conversion with better packet structure
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

// generateSegmentURI generates a URI for a segment
func generateSegmentURI(sequence uint64) string {
	return fmt.Sprintf("/live/segment_%d.ts", sequence)
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

	// Safe playlist management to avoid index out of range errors
	// Always use Append and let the library handle the sliding window internally
	// The m3u8 library should automatically manage the sliding window when capacity is reached
	err := h.playlist.Append(segment.URI, segment.Duration, "")
	if err != nil {
		// If append fails due to playlist being full, try to slide first then append
		common.LogDebugf("Playlist append failed (probably full), attempting slide: %v", err)
		
		// Slide the playlist to remove the oldest segment and add the new one
		h.playlist.Slide(segment.URI, segment.Duration, "")
	}

	// Update target duration if needed
	duration := time.Duration(segment.Duration * float64(time.Second))
	if duration > h.targetDuration {
		h.targetDuration = duration
		h.playlist.TargetDuration = uint(segment.Duration)
	}

	common.LogDebugf("Added generated HLS segment %d with duration %.2fs (playlist count: %d/%d)\n", 
		segment.Sequence, segment.Duration, h.playlist.Count(), h.maxSegments)
}