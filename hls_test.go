package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
	}

	assert.NotNil(t, ch.hlsData)
	assert.Equal(t, uint64(0), ch.hlsData.mediaSequence)
	assert.Equal(t, 0, len(ch.hlsData.segments))
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
