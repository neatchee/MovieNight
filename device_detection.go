package main

import (
	"net/http"
	"regexp"
	"strings"
)

// DeviceCapabilities represents the streaming capabilities of a device
type DeviceCapabilities struct {
	SupportsHLS    bool
	SupportsMPEGTS bool
	IsMobile       bool
	IsIOS          bool
	IsAndroid      bool
	UserAgent      string
	PreferredCodec string
}

// iOS user agent patterns for detection
var iosPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)iphone`),
	regexp.MustCompile(`(?i)ipad`),
	regexp.MustCompile(`(?i)ipod`),
	regexp.MustCompile(`(?i)mac os.*safari`), // macOS Safari also supports HLS natively
}

// Android user agent patterns
var androidPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)android`),
}

// Mobile device patterns
var mobilePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)mobile`),
	regexp.MustCompile(`(?i)tablet`),
	regexp.MustCompile(`(?i)iphone`),
	regexp.MustCompile(`(?i)ipad`),
	regexp.MustCompile(`(?i)ipod`),
	regexp.MustCompile(`(?i)android`),
	regexp.MustCompile(`(?i)blackberry`),
	regexp.MustCompile(`(?i)windows phone`),
}

// DetectDeviceCapabilities analyzes the HTTP request to determine device capabilities
func DetectDeviceCapabilities(r *http.Request) DeviceCapabilities {
	if r == nil {
		return DeviceCapabilities{
			SupportsHLS:    false,
			SupportsMPEGTS: true,
			IsMobile:       false,
			IsIOS:          false,
			IsAndroid:      false,
			UserAgent:      "",
			PreferredCodec: "flv",
		}
	}

	userAgent := r.Header.Get("User-Agent")
	
	capabilities := DeviceCapabilities{
		UserAgent: userAgent,
	}

	// Detect iOS devices
	for _, pattern := range iosPatterns {
		if pattern.MatchString(userAgent) {
			capabilities.IsIOS = true
			break
		}
	}

	// Detect Android devices
	for _, pattern := range androidPatterns {
		if pattern.MatchString(userAgent) {
			capabilities.IsAndroid = true
			break
		}
	}

	// Detect mobile devices
	for _, pattern := range mobilePatterns {
		if pattern.MatchString(userAgent) {
			capabilities.IsMobile = true
			break
		}
	}

	// Determine streaming capabilities
	if capabilities.IsIOS {
		// iOS devices have native HLS support
		capabilities.SupportsHLS = true
		capabilities.SupportsMPEGTS = false // iOS Safari doesn't support MPEG-TS well
		capabilities.PreferredCodec = "hls"
	} else if capabilities.IsAndroid {
		// Android devices may support HLS via hls.js
		capabilities.SupportsHLS = true
		capabilities.SupportsMPEGTS = true
		capabilities.PreferredCodec = "hls" // Prefer HLS for mobile
	} else {
		// Desktop browsers - prefer MPEG-TS for better performance
		capabilities.SupportsHLS = true    // via hls.js
		capabilities.SupportsMPEGTS = true // via mpegts.js
		capabilities.PreferredCodec = "flv"
	}

	return capabilities
}

// ShouldUseHLS determines if HLS should be used for this request
func ShouldUseHLS(r *http.Request) bool {
	if r == nil {
		return false
	}

	capabilities := DetectDeviceCapabilities(r)
	
	// Use HLS for iOS devices as they have native support and better performance
	if capabilities.IsIOS {
		return true
	}

	// Check if explicitly requested via query parameter
	if r.URL.Query().Get("format") == "hls" {
		return true
	}

	// For other devices, use MPEG-TS by default for better performance
	return false
}

// GetStreamingFormat returns the preferred streaming format for the device
func GetStreamingFormat(r *http.Request) string {
	if ShouldUseHLS(r) {
		return "hls"
	}
	return "flv"
}

// GetAcceptHeader returns the appropriate Accept header for the streaming format
func GetAcceptHeader(format string) string {
	switch format {
	case "hls":
		return "application/vnd.apple.mpegurl"
	case "flv":
		return "video/x-flv"
	default:
		return "video/x-flv"
	}
}

// IsHLSPlaylistRequest checks if the request is for an HLS playlist
func IsHLSPlaylistRequest(r *http.Request) bool {
	if r == nil {
		return false
	}

	path := strings.ToLower(r.URL.Path)
	return strings.HasSuffix(path, ".m3u8") || 
		   strings.Contains(path, "playlist") ||
		   r.Header.Get("Accept") == "application/vnd.apple.mpegurl"
}

// IsHLSSegmentRequest checks if the request is for an HLS segment
func IsHLSSegmentRequest(r *http.Request) bool {
	if r == nil {
		return false
	}

	path := strings.ToLower(r.URL.Path)
	return strings.HasSuffix(path, ".ts") && strings.Contains(path, "segment")
}

// GetContentTypeForFormat returns the appropriate Content-Type header for the format
func GetContentTypeForFormat(format string) string {
	switch format {
	case "hls":
		return "application/vnd.apple.mpegurl"
	case "ts":
		return "video/mp2t"
	case "flv":
		return "video/x-flv"
	default:
		return "video/x-flv"
	}
}

// ValidateUserAgent performs basic validation on the User-Agent string
func ValidateUserAgent(userAgent string) bool {
	if userAgent == "" {
		return false
	}

	// Basic validation - check for reasonable length and common patterns
	if len(userAgent) > 1000 {
		return false
	}

	// Check for suspicious patterns that might indicate bot/scraper
	suspiciousPatterns := []string{
		"curl",
		"wget",
		"python",
		"bot",
		"crawler",
		"spider",
	}

	lowerUA := strings.ToLower(userAgent)
	for _, pattern := range suspiciousPatterns {
		if strings.Contains(lowerUA, pattern) {
			return false
		}
	}

	return true
}