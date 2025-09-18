package main

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nareix/joy4/av/pubsub"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectDeviceCapabilities(t *testing.T) {
	tests := []struct {
		name          string
		userAgent     string
		expectedIOS   bool
		expectedHLS   bool
		expectedCodec string
	}{
		{
			name:          "iPhone user agent",
			userAgent:     "Mozilla/5.0 (iPhone; CPU iPhone OS 14_7_1 like Mac OS X) AppleWebKit/605.1.15",
			expectedIOS:   true,
			expectedHLS:   true,
			expectedCodec: "hls",
		},
		{
			name:          "iPad user agent",
			userAgent:     "Mozilla/5.0 (iPad; CPU OS 14_7_1 like Mac OS X) AppleWebKit/605.1.15",
			expectedIOS:   true,
			expectedHLS:   true,
			expectedCodec: "hls",
		},
		{
			name:          "Desktop Chrome",
			userAgent:     "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/93.0.4577.63 Safari/537.36",
			expectedIOS:   false,
			expectedHLS:   true,
			expectedCodec: "flv",
		},
		{
			name:          "Android device",
			userAgent:     "Mozilla/5.0 (Linux; Android 11; SM-G991B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/93.0.4577.62 Mobile Safari/537.36",
			expectedIOS:   false,
			expectedHLS:   true,
			expectedCodec: "hls",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("User-Agent", tt.userAgent)

			capabilities := DetectDeviceCapabilities(req)

			assert.Equal(t, tt.expectedIOS, capabilities.IsIOS)
			assert.Equal(t, tt.expectedHLS, capabilities.SupportsHLS)
			assert.Equal(t, tt.expectedCodec, capabilities.PreferredCodec)
			assert.Equal(t, tt.userAgent, capabilities.UserAgent)
		})
	}
}

func TestDetectDeviceCapabilities_NilRequest(t *testing.T) {
	capabilities := DetectDeviceCapabilities(nil)
	
	assert.False(t, capabilities.IsIOS)
	assert.False(t, capabilities.SupportsHLS)
	assert.True(t, capabilities.SupportsMPEGTS)
	assert.Equal(t, "flv", capabilities.PreferredCodec)
	assert.Equal(t, "", capabilities.UserAgent)
}

func TestShouldUseHLS(t *testing.T) {
	tests := []struct {
		name      string
		userAgent string
		queryParam string
		expected  bool
	}{
		{
			name:      "iOS device should use HLS",
			userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 14_7_1 like Mac OS X) AppleWebKit/605.1.15",
			expected:  true,
		},
		{
			name:      "Desktop Chrome should use FLV by default",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
			expected:  false,
		},
		{
			name:       "Force HLS via query parameter",
			userAgent:  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
			queryParam: "hls",
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/"
			if tt.queryParam != "" {
				url += "?format=" + tt.queryParam
			}
			
			req := httptest.NewRequest("GET", url, nil)
			req.Header.Set("User-Agent", tt.userAgent)

			result := ShouldUseHLS(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestShouldUseHLS_NilRequest(t *testing.T) {
	result := ShouldUseHLS(nil)
	assert.False(t, result)
}

func TestIsValidSegmentURI(t *testing.T) {
	tests := []struct {
		uri      string
		expected bool
	}{
		{"segment_0.ts", true},
		{"segment_123.ts", true},
		{"segment_999999.ts", true},
		{"invalid.ts", false},
		{"segment_0.mp4", false},
		{"", false},
		{"segment_.ts", false},
		{"_0.ts", false},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			result := IsValidSegmentURI(tt.uri)
			assert.Equal(t, tt.expected, result, "URI: %s", tt.uri)
		})
	}
}

func TestParseSequenceFromURI(t *testing.T) {
	tests := []struct {
		uri           string
		expectedSeq   uint64
		expectedError bool
	}{
		{"segment_0.ts", 0, false},
		{"segment_123.ts", 123, false},
		{"segment_999999.ts", 999999, false},
		{"invalid.ts", 0, true},
		{"segment_.ts", 0, true},
		{"", 0, true},
		{"segment_abc.ts", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			seq, err := ParseSequenceFromURI(tt.uri)
			
			if tt.expectedError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedSeq, seq)
			}
		})
	}
}

func TestNewHLSChannel(t *testing.T) {
	// Test with nil queue
	_, err := NewHLSChannel(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "queue cannot be nil")

	// Test with valid queue
	queue := pubsub.NewQueue()
	hlsChan, err := NewHLSChannel(queue)
	require.NoError(t, err)
	require.NotNil(t, hlsChan)

	assert.Equal(t, queue, hlsChan.que)
	assert.NotNil(t, hlsChan.playlist)
	assert.Equal(t, 4*time.Second, hlsChan.targetDuration)
	assert.Equal(t, 4*time.Second, hlsChan.segmentDuration)
	assert.Equal(t, 6, hlsChan.maxSegments)
	assert.Equal(t, uint64(0), hlsChan.sequenceNumber)
	assert.NotNil(t, hlsChan.viewers)
	assert.NotNil(t, hlsChan.ctx)
	assert.NotNil(t, hlsChan.cancel)

	// Clean up
	hlsChan.Stop()
}

func TestHLSChannel_ViewerTracking(t *testing.T) {
	queue := pubsub.NewQueue()
	hlsChan, err := NewHLSChannel(queue)
	require.NoError(t, err)
	defer hlsChan.Stop()

	// Test adding viewers
	hlsChan.AddViewer("user1")
	hlsChan.AddViewer("user2")
	assert.Equal(t, 2, hlsChan.GetViewerCount())

	// Test removing viewers
	hlsChan.RemoveViewer("user1")
	assert.Equal(t, 1, hlsChan.GetViewerCount())

	hlsChan.RemoveViewer("user2")
	assert.Equal(t, 0, hlsChan.GetViewerCount())

	// Test removing non-existent viewer
	hlsChan.RemoveViewer("nonexistent")
	assert.Equal(t, 0, hlsChan.GetViewerCount())
}

func TestHLSChannel_GetPlaylist(t *testing.T) {
	queue := pubsub.NewQueue()
	hlsChan, err := NewHLSChannel(queue)
	require.NoError(t, err)
	defer hlsChan.Stop()

	playlist := hlsChan.GetPlaylist()
	assert.NotEmpty(t, playlist)
	assert.Contains(t, playlist, "#EXTM3U")
	assert.Contains(t, playlist, "#EXT-X-VERSION:6")
}

func TestHLSChannel_GetPlaylist_Nil(t *testing.T) {
	var hlsChan *HLSChannel
	playlist := hlsChan.GetPlaylist()
	assert.Empty(t, playlist)
}

func TestHLSChannel_NilChecks(t *testing.T) {
	var hlsChan *HLSChannel

	// Test nil checks
	hlsChan.AddViewer("test")    // Should not panic
	hlsChan.RemoveViewer("test") // Should not panic
	assert.Equal(t, 0, hlsChan.GetViewerCount())

	_, err := hlsChan.GetSegment(0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HLS channel is nil")

	_, err = hlsChan.GetSegmentByURI("segment_0.ts")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "HLS channel is nil")
}

func TestGetContentTypeForFormat(t *testing.T) {
	tests := []struct {
		format   string
		expected string
	}{
		{"hls", "application/vnd.apple.mpegurl"},
		{"ts", "video/mp2t"},
		{"flv", "video/x-flv"},
		{"unknown", "video/x-flv"},
		{"", "video/x-flv"},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			result := GetContentTypeForFormat(tt.format)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsHLSPlaylistRequest(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		accept   string
		expected bool
	}{
		{"m3u8 extension", "/live/playlist.m3u8", "", true},
		{"M3U8 extension uppercase", "/live/PLAYLIST.M3U8", "", true},
		{"playlist in path", "/live/playlist", "", true},
		{"Accept header", "/live", "application/vnd.apple.mpegurl", true},
		{"format=hls parameter", "/live?format=hls", "", true},
		{"regular path", "/live", "", false},
		{"ts segment", "/live/segment_0.ts", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}

			result := IsHLSPlaylistRequest(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsHLSSegmentRequest(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{"ts segment", "/live/segment_0.ts", true},
		{"TS segment uppercase", "/live/SEGMENT_0.TS", true},
		{"playlist", "/live/playlist.m3u8", false},
		{"regular path", "/live", false},
		{"other ts file", "/live/other.ts", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)

			result := IsHLSSegmentRequest(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateUserAgent(t *testing.T) {
	tests := []struct {
		userAgent string
		expected  bool
	}{
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 14_7_1 like Mac OS X)", true},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/93.0", true},
		{"", false},
		{"curl/7.68.0", false},
		{"wget/1.20.3", false},
		{"python-requests/2.25.1", false},
		{"Googlebot/2.1", false},
		{string(make([]byte, 1001)), false}, // Too long
	}

	for _, tt := range tests {
		t.Run(tt.userAgent, func(t *testing.T) {
			result := ValidateUserAgent(tt.userAgent)
			assert.Equal(t, tt.expected, result)
		})
	}
}