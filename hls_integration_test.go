package main

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestHLSIntegration demonstrates the full HLS workflow
func TestHLSIntegration(t *testing.T) {
	t.Run("iOS Device HLS Flow", func(t *testing.T) {
		// Simulate iOS Safari request
		req := httptest.NewRequest("GET", "/live", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 14_7_1 like Mac OS X) AppleWebKit/605.1.15")

		// Test device detection
		shouldUseHLS := ShouldUseHLS(req)
		assert.True(t, shouldUseHLS, "iOS devices should use HLS")

		format := GetStreamingFormat(req)
		assert.Equal(t, "hls", format, "iOS should get HLS format")

		capabilities := DetectDeviceCapabilities(req)
		assert.True(t, capabilities.IsIOS, "Should detect iOS")
		assert.True(t, capabilities.SupportsHLS, "iOS should support HLS")
		assert.Equal(t, "hls", capabilities.PreferredCodec, "iOS should prefer HLS")

		// Test quality settings
		qualitySettings := GetQualitySettings(capabilities)
		assert.Equal(t, 0.7, qualitySettings.BitrateMultiplier, "iOS mobile should have 30% bitrate reduction")
		assert.Equal(t, "720p", qualitySettings.Resolution, "iOS mobile should use 720p")
	})

	t.Run("Desktop Chrome FLV Flow", func(t *testing.T) {
		// Simulate Desktop Chrome request
		req := httptest.NewRequest("GET", "/live", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/93.0.4577.63 Safari/537.36")

		// Test device detection
		shouldUseHLS := ShouldUseHLS(req)
		assert.False(t, shouldUseHLS, "Desktop should use FLV by default")

		format := GetStreamingFormat(req)
		assert.Equal(t, "flv", format, "Desktop should get FLV format")

		capabilities := DetectDeviceCapabilities(req)
		assert.False(t, capabilities.IsIOS, "Should not detect iOS")
		assert.True(t, capabilities.SupportsHLS, "Desktop should support HLS via hls.js")
		assert.Equal(t, "flv", capabilities.PreferredCodec, "Desktop should prefer FLV")
	})

	t.Run("Force HLS via Query Parameter", func(t *testing.T) {
		// Simulate desktop request with HLS forced
		req := httptest.NewRequest("GET", "/live?format=hls", nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

		shouldUseHLS := ShouldUseHLS(req)
		assert.True(t, shouldUseHLS, "Should use HLS when forced via query parameter")

		format := GetStreamingFormat(req)
		assert.Equal(t, "hls", format, "Should get HLS format when requested")
	})

	t.Run("HLS Request Types", func(t *testing.T) {
		// Test playlist request
		playlistReq := httptest.NewRequest("GET", "/hls/live/playlist.m3u8", nil)
		assert.True(t, IsHLSPlaylistRequest(playlistReq), "Should detect playlist request")
		assert.False(t, IsHLSSegmentRequest(playlistReq), "Should not detect as segment request")

		// Test segment request
		segmentReq := httptest.NewRequest("GET", "/hls/live/segment_0.ts", nil)
		assert.False(t, IsHLSPlaylistRequest(segmentReq), "Should not detect as playlist request")
		assert.True(t, IsHLSSegmentRequest(segmentReq), "Should detect segment request")
	})

	t.Run("Content Type Headers", func(t *testing.T) {
		hlsContentType := GetContentTypeForFormat("hls")
		assert.Equal(t, "application/vnd.apple.mpegurl", hlsContentType, "HLS should have correct content type")

		tsContentType := GetContentTypeForFormat("ts")
		assert.Equal(t, "video/mp2t", tsContentType, "TS should have correct content type")

		flvContentType := GetContentTypeForFormat("flv")
		assert.Equal(t, "video/x-flv", flvContentType, "FLV should have correct content type")
	})
}

// TestHLSConfigurationDemo demonstrates configuration options
func TestHLSConfigurationDemo(t *testing.T) {
	config := DefaultHLSConfig()
	
	fmt.Printf("Default HLS Configuration:\n")
	fmt.Printf("  Segment Duration: %v\n", config.SegmentDuration)
	fmt.Printf("  Max Segments: %d\n", config.MaxSegments)
	fmt.Printf("  Bitrate Reduction: %.1f%% (%.2f multiplier)\n", 
		(1.0-config.BitrateReduction)*100, config.BitrateReduction)
	fmt.Printf("  Low Latency: %v\n", config.EnableLowLatency)
	fmt.Printf("  Concurrent Segments: %d\n", config.MaxConcurrentSegments)
	fmt.Printf("  Quality Adaptation: %v\n", config.QualityAdaptation)

	// Test device-specific configurations
	iosReq := httptest.NewRequest("GET", "/", nil)
	iosReq.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 14_7_1 like Mac OS X)")
	iosCapabilities := DetectDeviceCapabilities(iosReq)
	iosQuality := GetQualitySettings(iosCapabilities)

	fmt.Printf("\niOS Mobile Quality Settings:\n")
	fmt.Printf("  Bitrate Reduction: %.1f%% (%.2f multiplier)\n", 
		(1.0-iosQuality.BitrateMultiplier)*100, iosQuality.BitrateMultiplier)
	fmt.Printf("  Resolution: %s\n", iosQuality.Resolution)
	fmt.Printf("  Frame Rate: %d fps\n", iosQuality.FrameRate)
	fmt.Printf("  Key Frame Interval: %d seconds\n", iosQuality.KeyFrameInterval)

	assert.Equal(t, 10*time.Second, config.SegmentDuration)
	assert.Equal(t, 0.7, config.BitrateReduction)
	assert.True(t, config.EnableLowLatency)
	assert.Equal(t, 0.7, iosQuality.BitrateMultiplier)
}