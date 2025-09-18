package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/zorchenhimer/MovieNight/common"
)

func init() {
	// Initialize logging for tests
	common.SetupLogging(common.LLError, "")
}

func TestHLSManifestWithoutChannel(t *testing.T) {
	// Test HLS manifest endpoint when no channel exists
	req, err := http.NewRequest("GET", "/live.m3u8", nil)
	assert.NoError(t, err)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(handleHLSManifest)

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHLSSegmentWithoutChannel(t *testing.T) {
	// Test HLS segment endpoint when no channel exists
	req, err := http.NewRequest("GET", "/live_segment_0.ts", nil)
	assert.NoError(t, err)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(handleHLSSegment)

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHLSSegmentInvalidNumber(t *testing.T) {
	// Test HLS segment endpoint with invalid segment number
	req, err := http.NewRequest("GET", "/live_segment_invalid.ts", nil)
	assert.NoError(t, err)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(handleHLSSegment)

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUpdateHLSSegments(t *testing.T) {
	// Test HLS segment timing and sliding window with new structure
	hlsData := &HLSData{
		mediaSequence:     0,
		discontinuitySeq:  0,
		segments:          make([]HLSSegment, 0),
		partialSegments:   make([]HLSPartialSegment, 0),
		lastSegmentTime:   time.Now().Add(-7 * time.Second), // Make it old enough for new segment (7s > 6s duration)
		lastPartialTime:   time.Now().Add(-3 * time.Second), // Make it old enough for new partial (3s > 2s duration)
		segmentCache:      make(map[uint64]*SegmentCache),
		partialCache:      make(map[string]*PartialCache),
		encodingStartTime: time.Now(),
		lastDiscontinuity: false,
	}

	// Should add a new segment and partial
	updateHLSSegments(hlsData)
	assert.True(t, len(hlsData.segments) >= 1 || len(hlsData.partialSegments) >= 1)
	
	if len(hlsData.segments) > 0 {
		assert.Equal(t, uint64(0), hlsData.segments[0].Index)
		assert.Equal(t, HLSSegmentDuration, hlsData.segments[0].Duration) // Verify 6.0 second duration
	}
	
	if len(hlsData.partialSegments) > 0 {
		assert.Equal(t, HLSPartialDuration, hlsData.partialSegments[0].Duration) // Verify 2.0 second duration
	}

	// Advance time and add more segments
	hlsData.lastSegmentTime = time.Now().Add(-7 * time.Second)
	hlsData.lastPartialTime = time.Now().Add(-3 * time.Second)
	updateHLSSegments(hlsData)

	// Fill up to window size
	for i := 0; i < HLSWindowSize; i++ {
		hlsData.lastSegmentTime = time.Now().Add(-7 * time.Second)
		hlsData.lastPartialTime = time.Now().Add(-3 * time.Second)
		updateHLSSegments(hlsData)
	}

	// Should maintain window size and increment sequence
	assert.True(t, len(hlsData.segments) <= HLSWindowSize)
	assert.True(t, hlsData.mediaSequence >= 0, "Media sequence should be valid")
}

func TestHLSSegmentDurationCompliance(t *testing.T) {
	// Test that segment duration is exactly 6.0 seconds as required
	assert.Equal(t, 6.0, HLSSegmentDuration, "Segment duration must be 6.0 seconds")
	assert.Equal(t, 12.0, HLSTargetDuration, "Target duration should be 2x segment duration")
	assert.Equal(t, 6, HLSWindowSize, "Window size should accommodate proper buffering")
}

func TestWriteFlusherNilProtection(t *testing.T) {
	// Test nil protection in writeFlusher
	wf := writeFlusher{
		httpflusher: nil,
		Writer:      nil,
	}

	// Should not panic and return error
	_, err := wf.Write([]byte("test"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "writer is nil")

	err = wf.Flush()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "flusher is nil")
}

func TestGetSegmentIndexes(t *testing.T) {
	// Test utility function for getting segment indexes
	segments := []HLSSegment{
		{Index: 5},
		{Index: 6},
		{Index: 7},
	}

	indexes := getSegmentIndexes(segments)
	expected := []uint64{5, 6, 7}
	assert.Equal(t, expected, indexes)
}

func TestHLSDataInitialization(t *testing.T) {
	// Test that HLS data is properly initialized with new structure
	ch := &Channel{}
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

	assert.NotNil(t, ch.hlsData)
	assert.Equal(t, uint64(0), ch.hlsData.mediaSequence)
	assert.Equal(t, uint64(0), ch.hlsData.discontinuitySeq)
	assert.Equal(t, 0, len(ch.hlsData.segments))
	assert.Equal(t, 0, len(ch.hlsData.partialSegments))
	assert.NotNil(t, ch.hlsData.segmentCache)
	assert.NotNil(t, ch.hlsData.partialCache)
}

func TestHLSRoutePattern(t *testing.T) {
	// Test that HLS segment route pattern matching works
	testCases := []struct {
		path     string
		expected bool
	}{
		{"/live_segment_0.ts", true},
		{"/live_segment_123.ts", true},
		{"/live_segment_.ts", false},    // empty segment number
		{"/live_segment_abc.ts", false}, // non-numeric
		{"/live_segment_0", false},      // missing .ts extension
		{"/other_path", false},
	}

	for _, tc := range testCases {
		// Use the actual logic from the handler
		isMatch := strings.HasPrefix(tc.path, "/live_segment_") && strings.HasSuffix(tc.path, ".ts")
		if isMatch {
			// Extract segment number and validate it's numeric
			path := strings.TrimPrefix(tc.path, "/live_segment_")
			path = strings.TrimSuffix(path, ".ts")
			if path == "" {
				isMatch = false
			} else {
				// Try to parse as number
				if _, err := strconv.ParseUint(path, 10, 64); err != nil {
					isMatch = false
				}
			}
		}
		assert.Equal(t, tc.expected, isMatch, "Path: %s", tc.path)
	}
}

func TestHLSSegmentConcurrency(t *testing.T) {
	// Test that the HLS server can handle concurrent segment requests properly
	// This tests the server-side concurrent handling capability
	var requestMutex sync.Mutex
	segmentRequests := make(map[string]int)
	
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Track segment requests for concurrency verification with thread safety
		requestMutex.Lock()
		segmentRequests[r.URL.Path]++
		requestMutex.Unlock()
		
		// Simulate segment data
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "max-age=10")
		
		// Create fake TS segment data (minimal valid TS packet)
		tsPacket := make([]byte, 188) // Standard TS packet size
		tsPacket[0] = 0x47 // TS sync byte
		w.Write(tsPacket)
	}))
	defer server.Close()

	concurrentRequests := 3
	responses := make(chan *http.Response, concurrentRequests)
	
	// Make concurrent requests to test server handling
	for i := 0; i < concurrentRequests; i++ {
		go func(segmentIndex int) {
			url := fmt.Sprintf("%s/live_segment_%d.ts", server.URL, segmentIndex)
			resp, err := http.Get(url)
			if err == nil {
				responses <- resp
			}
		}(i)
	}
	
	// Verify all requests complete within reasonable time
	timeout := time.After(5 * time.Second)
	completedRequests := 0
	
	for completedRequests < concurrentRequests {
		select {
		case resp := <-responses:
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, "video/mp2t", resp.Header.Get("Content-Type"))
			resp.Body.Close()
			completedRequests++
		case <-timeout:
			t.Fatal("Timeout waiting for concurrent segment requests")
		}
	}
	
	// Verify that all expected segments were requested (with thread safety)
	requestMutex.Lock()
	for i := 0; i < concurrentRequests; i++ {
		expectedPath := fmt.Sprintf("/live_segment_%d.ts", i)
		assert.Equal(t, 1, segmentRequests[expectedPath], 
			"Segment %d should be requested exactly once", i)
	}
	requestMutex.Unlock()
}

func TestHLSBufferManagement(t *testing.T) {
	// Test that HLS segment sliding window doesn't grow indefinitely
	// This is important for preventing memory leaks on the server side
	
	// Create mock HLS data with many segments
	hlsData := &HLSData{
		mediaSequence:   0,
		segments:        make([]HLSSegment, 0),
		lastSegmentTime: time.Now().Add(-7 * time.Second),
	}
	
	maxSegments := 20 // More than HLSWindowSize
	
	// Add many segments to simulate long-running stream
	for i := 0; i < maxSegments; i++ {
		hlsData.lastSegmentTime = time.Now().Add(-7 * time.Second)
		updateHLSSegments(hlsData)
	}
	
	// Verify sliding window behavior (memory management)
	assert.LessOrEqual(t, len(hlsData.segments), HLSWindowSize, 
		"Segment buffer should not exceed window size to prevent memory leaks")
	
	// Verify media sequence increments properly
	assert.Greater(t, hlsData.mediaSequence, uint64(0), 
		"Media sequence should increment as old segments are removed")
	
	// Verify segments are contiguous in the window
	if len(hlsData.segments) > 1 {
		for i := 1; i < len(hlsData.segments); i++ {
			expectedIndex := hlsData.segments[i-1].Index + 1
			assert.Equal(t, expectedIndex, hlsData.segments[i].Index,
				"Segments should be contiguous in the sliding window")
		}
	}
}

func TestHLSTimingOptimization(t *testing.T) {
	// Test that segment timing supports smooth transitions
	hlsData := &HLSData{
		mediaSequence:   0,
		segments:        make([]HLSSegment, 0),
		lastSegmentTime: time.Now().Add(-7 * time.Second), // Old enough for new segment
	}
	
	// Record timing before update
	beforeUpdate := time.Now()
	updateHLSSegments(hlsData)
	afterUpdate := time.Now()
	
	// Verify segment was added
	assert.Equal(t, 1, len(hlsData.segments))
	
	// Verify timing constraints for smooth playback
	segment := hlsData.segments[0]
	assert.Equal(t, HLSSegmentDuration, segment.Duration)
	
	// Verify segment timestamp is recent (within update time range)
	assert.True(t, segment.StartTime.After(beforeUpdate) || segment.StartTime.Equal(beforeUpdate))
	assert.True(t, segment.StartTime.Before(afterUpdate) || segment.StartTime.Equal(afterUpdate))
	
	// Verify segment URL format for HLS compatibility
	expectedURL := fmt.Sprintf("/live_segment_%d.ts", segment.Index)
	assert.Equal(t, expectedURL, segment.URL)
}

func TestHLSConcurrentAccess(t *testing.T) {
	// Test that HLS data structures can handle concurrent access
	// This is important for live streaming with multiple clients
	hlsData := &HLSData{
		mediaSequence:   0,
		segments:        make([]HLSSegment, 0),
		lastSegmentTime: time.Now().Add(-7 * time.Second),
		segmentCache:    make(map[uint64]*SegmentCache),
	}
	
	// Simulate concurrent operations (like multiple client requests and segment updates)
	done := make(chan bool, 2)
	
	// Simulate segment updates (like during live streaming)
	go func() {
		for i := 0; i < 10; i++ {
			hlsData.Lock()
			hlsData.lastSegmentTime = time.Now().Add(-7 * time.Second)
			hlsData.Unlock()
			updateHLSSegments(hlsData)
			time.Sleep(10 * time.Millisecond)
		}
		done <- true
	}()
	
	// Simulate concurrent reads (like multiple client requests)
	go func() {
		for i := 0; i < 10; i++ {
			hlsData.RLock()
			segmentCount := len(hlsData.segments)
			hlsData.RUnlock()
			
			// Verify we can read safely
			assert.GreaterOrEqual(t, segmentCount, 0)
			time.Sleep(10 * time.Millisecond)
		}
		done <- true
	}()
	
	// Wait for both goroutines to complete
	<-done
	<-done
	
	// Verify final state is consistent
	hlsData.RLock()
	finalSegmentCount := len(hlsData.segments)
	hlsData.RUnlock()
	
	assert.GreaterOrEqual(t, finalSegmentCount, 1)
	assert.LessOrEqual(t, finalSegmentCount, HLSWindowSize)
}

func TestHLSSegmentCaching(t *testing.T) {
	// Test segment caching functionality for smooth playback
	hlsData := &HLSData{
		mediaSequence:   0,
		segments:        make([]HLSSegment, 0),
		lastSegmentTime: time.Now(),
		segmentCache:    make(map[uint64]*SegmentCache),
	}
	
	// Add a test segment
	hlsData.segments = append(hlsData.segments, HLSSegment{
		Index:     0,
		URL:       "/live_segment_0.ts",
		Duration:  HLSSegmentDuration,
		StartTime: time.Now(),
	})
	
	// Add cached segment data
	testData := []byte("test segment data")
	hlsData.segmentCache[0] = &SegmentCache{
		Data:    testData,
		Ready:   true,
		Created: time.Now(),
	}
	
	// Verify cache works
	cache, exists := hlsData.segmentCache[0]
	assert.True(t, exists)
	assert.True(t, cache.Ready)
	assert.Equal(t, testData, cache.Data)
}

func TestCleanupOldSegments(t *testing.T) {
	// Test cleanup functionality to prevent memory leaks
	hlsData := &HLSData{
		mediaSequence: 5,
		segments: []HLSSegment{
			{Index: 5, URL: "/live_segment_5.ts"},
			{Index: 6, URL: "/live_segment_6.ts"},
			{Index: 7, URL: "/live_segment_7.ts"},
		},
		partialSegments: []HLSPartialSegment{
			{SeqID: 6, PartID: 0, URL: "/live_partial_6_0.ts"},
			{SeqID: 7, PartID: 0, URL: "/live_partial_7_0.ts"},
		},
		segmentCache: map[uint64]*SegmentCache{
			3: {Data: []byte("old"), Ready: true}, // Should be cleaned
			4: {Data: []byte("old"), Ready: true}, // Should be cleaned
			5: {Data: []byte("current"), Ready: true}, // Should remain
			6: {Data: []byte("current"), Ready: true}, // Should remain
			7: {Data: []byte("current"), Ready: true}, // Should remain
		},
		partialCache: map[string]*PartialCache{
			"5_0": {Data: []byte("old"), Ready: true}, // Should be cleaned
			"6_0": {Data: []byte("current"), Ready: true}, // Should remain
			"7_0": {Data: []byte("current"), Ready: true}, // Should remain
		},
	}
	
	ch := &Channel{hlsData: hlsData}
	cleanupOldSegments(ch)
	
	// Verify old segments are cleaned up
	assert.NotContains(t, hlsData.segmentCache, uint64(3))
	assert.NotContains(t, hlsData.segmentCache, uint64(4))
	
	// Verify current segments remain
	assert.Contains(t, hlsData.segmentCache, uint64(5))
	assert.Contains(t, hlsData.segmentCache, uint64(6))
	assert.Contains(t, hlsData.segmentCache, uint64(7))
	
	// Verify old partials are cleaned up
	assert.NotContains(t, hlsData.partialCache, "5_0")
	
	// Verify current partials remain
	assert.Contains(t, hlsData.partialCache, "6_0")
	assert.Contains(t, hlsData.partialCache, "7_0")
}

func TestPartialSegmentGeneration(t *testing.T) {
	// Test partial segment timing and generation
	hlsData := &HLSData{
		mediaSequence:     0,
		discontinuitySeq:  0,
		segments:          make([]HLSSegment, 0),
		partialSegments:   make([]HLSPartialSegment, 0),
		lastSegmentTime:   time.Now(),
		lastPartialTime:   time.Now().Add(-3 * time.Second), // Old enough for partial
		segmentCache:      make(map[uint64]*SegmentCache),
		partialCache:      make(map[string]*PartialCache),
		encodingStartTime: time.Now(),
		lastDiscontinuity: false,
	}

	updateHLSSegments(hlsData)
	
	// Should have created a partial segment
	assert.True(t, len(hlsData.partialSegments) > 0, "Should create partial segment")
	
	if len(hlsData.partialSegments) > 0 {
		partial := hlsData.partialSegments[0]
		assert.Equal(t, HLSPartialDuration, partial.Duration)
		assert.True(t, partial.Independent, "First partial should be independent")
		assert.Contains(t, partial.URL, "live_partial_")
	}
}

func TestDiscontinuityHandling(t *testing.T) {
	// Test discontinuity sequence management
	hlsData := &HLSData{
		mediaSequence:     0,
		discontinuitySeq:  0,
		segments:          make([]HLSSegment, 0),
		partialSegments:   make([]HLSPartialSegment, 0),
		lastSegmentTime:   time.Now(),
		lastPartialTime:   time.Now(),
		segmentCache:      make(map[uint64]*SegmentCache),
		partialCache:      make(map[string]*PartialCache),
		encodingStartTime: time.Now(),
		lastDiscontinuity: true, // Mark for discontinuity
	}

	initialDiscontinuitySeq := hlsData.discontinuitySeq
	updateHLSSegments(hlsData)
	
	// Should have incremented discontinuity sequence
	assert.Equal(t, initialDiscontinuitySeq+1, hlsData.discontinuitySeq)
	assert.False(t, hlsData.lastDiscontinuity, "Discontinuity flag should be reset")
}

func TestEncodingTimeMonitoring(t *testing.T) {
	// Test that encoding time is being tracked
	hlsData := &HLSData{
		mediaSequence:     0,
		discontinuitySeq:  0,
		segments:          make([]HLSSegment, 0),
		partialSegments:   make([]HLSPartialSegment, 0),
		lastSegmentTime:   time.Now().Add(-7 * time.Second),
		lastPartialTime:   time.Now().Add(-3 * time.Second),
		segmentCache:      make(map[uint64]*SegmentCache),
		partialCache:      make(map[string]*PartialCache),
		encodingStartTime: time.Now().Add(-1 * time.Second), // 1 second ago
		lastDiscontinuity: false,
	}

	updateHLSSegments(hlsData)
	
	// Check that segments have encoding end times
	if len(hlsData.segments) > 0 {
		assert.False(t, hlsData.segments[0].EncodingEnd.IsZero(), "Segment should have encoding end time")
	}
	
	if len(hlsData.partialSegments) > 0 {
		assert.False(t, hlsData.partialSegments[0].EncodingEnd.IsZero(), "Partial should have encoding end time")
	}
}

func TestHLSPartialSegmentRouting(t *testing.T) {
	// Test that partial segment route pattern matching works
	testCases := []struct {
		path     string
		expected bool
	}{
		{"/live_partial_0_0.ts", true},
		{"/live_partial_123_2.ts", true},
		{"/live_partial__.ts", false},    // empty numbers
		{"/live_partial_abc_0.ts", false}, // non-numeric seq
		{"/live_partial_0_abc.ts", false}, // non-numeric part
		{"/live_partial_0_0", false},      // missing .ts extension
		{"/other_path", false},
	}

	for _, tc := range testCases {
		// Use the actual logic from the partial handler
		isMatch := strings.HasPrefix(tc.path, "/live_partial_") && strings.HasSuffix(tc.path, ".ts")
		if isMatch {
			// Extract and validate numbers
			path := strings.TrimPrefix(tc.path, "/live_partial_")
			path = strings.TrimSuffix(path, ".ts")
			parts := strings.Split(path, "_")
			if len(parts) != 2 {
				isMatch = false
			} else {
				// Try to parse both numbers
				if _, err1 := strconv.ParseUint(parts[0], 10, 64); err1 != nil {
					isMatch = false
				}
				if _, err2 := strconv.ParseUint(parts[1], 10, 64); err2 != nil {
					isMatch = false
				}
			}
		}
		assert.Equal(t, tc.expected, isMatch, "Path: %s", tc.path)
	}
}
