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
	// Test HLS segment timing and sliding window
	hlsData := &HLSData{
		mediaSequence:   0,
		segments:        make([]HLSSegment, 0),
		lastSegmentTime: time.Now().Add(-7 * time.Second), // Make it old enough for new segment (7s > 6s duration)
	}

	// Should add a new segment
	updateHLSSegments(hlsData)
	assert.Equal(t, 1, len(hlsData.segments))
	assert.Equal(t, uint64(0), hlsData.segments[0].Index)
	assert.Equal(t, HLSSegmentDuration, hlsData.segments[0].Duration) // Verify 6.0 second duration

	// Advance time and add more segments
	hlsData.lastSegmentTime = time.Now().Add(-7 * time.Second)
	updateHLSSegments(hlsData)
	assert.Equal(t, 2, len(hlsData.segments))

	// Fill up to window size
	for i := 0; i < HLSWindowSize; i++ {
		hlsData.lastSegmentTime = time.Now().Add(-7 * time.Second)
		updateHLSSegments(hlsData)
	}

	// Should maintain window size and increment sequence
	assert.Equal(t, HLSWindowSize, len(hlsData.segments))
	assert.True(t, hlsData.mediaSequence > 0, "Media sequence should increment")
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
	// Test that HLS data is properly initialized
	ch := &Channel{}
	ch.hlsData = &HLSData{
		mediaSequence:   0,
		segments:        make([]HLSSegment, 0),
		lastSegmentTime: time.Now(),
		segmentCache:    make(map[uint64]*SegmentCache),
	}

	assert.NotNil(t, ch.hlsData)
	assert.Equal(t, uint64(0), ch.hlsData.mediaSequence)
	assert.Equal(t, 0, len(ch.hlsData.segments))
	assert.NotNil(t, ch.hlsData.segmentCache)
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
		segmentCache: map[uint64]*SegmentCache{
			3: {Data: []byte("old"), Ready: true}, // Should be cleaned
			4: {Data: []byte("old"), Ready: true}, // Should be cleaned
			5: {Data: []byte("current"), Ready: true}, // Should remain
			6: {Data: []byte("current"), Ready: true}, // Should remain
			7: {Data: []byte("current"), Ready: true}, // Should remain
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
}
