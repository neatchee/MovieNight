# HLS Streaming Support for MovieNight

This document describes the HLS (HTTP Live Streaming) implementation added to MovieNight for iOS device compatibility.

## Overview

MovieNight now supports dual streaming protocols:
- **FLV/MPEG-TS**: Default for desktop browsers (better performance)
- **HLS**: Automatically used for iOS devices and available for all devices

## Features

### Device Detection and Optimization

- **Automatic iOS Detection**: iOS devices automatically use HLS for native compatibility
- **User Agent Analysis**: Comprehensive device capability detection
- **Quality Adaptation**: Different quality settings based on device type
- **Mobile Optimization**: Reduced bitrate and segment size for mobile devices

### HLS Implementation

- **Eyevinn/hls-m3u8 Library**: Robust playlist and segment management
- **MPEG-TS Muxing**: Proper container format for HLS segments
- **Concurrent Segment Generation**: Worker pool for performance optimization
- **Adaptive Bitrate**: 30% bitrate reduction for HLS efficiency
- **Low Latency Mode**: Optimized for live streaming

### Quality Settings by Device Type

| Device Type | Bitrate Reduction | Resolution | Frame Rate | Segments |
|-------------|------------------|------------|------------|----------|
| iOS Mobile  | 30%              | 720p       | 30fps      | 8        |
| iOS Desktop | 15%              | 1080p      | 60fps      | 10       |
| Android     | 25%              | 720p       | 30fps      | 10       |
| Desktop     | 20%              | 1080p      | 60fps      | 12       |

## API Endpoints

### HLS Streaming

- **Playlist**: `/hls/{stream}/playlist.m3u8`
- **Segments**: `/hls/{stream}/segment_N.ts`
- **Auto-detection**: `/live` (automatically serves HLS for iOS)

### Format Selection

- **Force HLS**: Add `?format=hls` to any video URL
- **Force FLV**: Default behavior for non-iOS devices

## Client-Side Implementation

### HLS.js Integration

The implementation uses the official hls.js library (version 1.6.12) from CDN:
```html
<script src="https://cdn.jsdelivr.net/npm/hls.js@1.6.12/dist/hls.min.js"></script>
```

### Advanced HLS.js Features Utilized

- **Comprehensive Error Recovery**: Network, media, and mux error handling with automatic fallback
- **Quality Level Management**: Automatic and manual quality selection with UI controls  
- **Buffer Optimization**: Advanced buffer management with configurable sizes and watchdog timers
- **Low Latency Mode**: Optimized for live streaming with minimal delay
- **Bandwidth Adaptation**: Intelligent bitrate selection based on network conditions
- **Subtitle Support**: WebVTT, CEA-708, and IMSC1 subtitle formats
- **Audio Track Selection**: Multi-language audio track support
- **Performance Monitoring**: FPS drop detection and quality capping
- **Statistics Collection**: Detailed playback metrics and debugging information

### Enhanced Configuration

```javascript
const hlsConfig = {
    // Core optimization
    lowLatencyMode: true,
    enableWorker: true,
    
    // Buffer management  
    backBufferLength: 90,
    liveBackBufferLength: 60,
    maxBufferLength: 30,
    
    // Network resilience
    manifestLoadingMaxRetry: 4,
    levelLoadingMaxRetry: 4,
    fragLoadingMaxRetry: 6,
    
    // Quality adaptation
    capLevelToPlayerSize: true,
    capLevelOnFPSDrop: true,
    abrBandWidthFactor: 0.95,
    
    // iOS-specific optimizations
    ...(isIOS() && {
        liveSyncDurationCount: 2,
        maxBufferLength: 20,
    })
};
```

### JavaScript Detection

```javascript
// Device detection
function isIOS() {
    return /iPad|iPhone|iPod/.test(navigator.userAgent);
}

// Native HLS support check
function supportsHLS() {
    var video = document.createElement('video');
    return video.canPlayType('application/vnd.apple.mpegurl') !== '';
}
```

### Player Selection

1. **iOS Safari**: Uses native HLS support
2. **Other browsers**: Falls back to hls.js
3. **No HLS support**: Falls back to MPEG-TS/FLV

## Configuration

### HLS Configuration Options

```go
type HLSConfig struct {
    SegmentDuration       time.Duration // Default: 10s
    MaxSegments          int           // Default: 10
    TargetDuration       time.Duration // Default: 10s
    BitrateReduction     float64       // Default: 0.7 (30% reduction)
    EnableLowLatency     bool          // Default: true
    MaxConcurrentSegments int          // Default: 3
    SegmentBufferSize    int           // Default: 1MB
    QualityAdaptation    bool          // Default: true
}
```

### Performance Optimizations

- **Worker Pool**: Concurrent segment generation for improved throughput
- **Memory Management**: Automatic cleanup of old segments
- **Buffer Optimization**: Configurable buffer sizes based on device capabilities
- **Quality Adaptation**: Dynamic bitrate adjustment based on device type

## Error Handling

### Comprehensive Error Recovery

- **Network Errors**: Automatic retry with exponential backoff
- **Media Errors**: Graceful degradation to MPEG-TS fallback
- **Nil Pointer Protection**: Extensive nil checks throughout the codebase
- **Resource Cleanup**: Proper cleanup of workers and resources on shutdown

### Logging

All HLS operations are logged with appropriate levels:
- `DEBUG`: Segment creation and playlist updates
- `INFO`: Stream start/stop and device detection
- `ERROR`: Failed operations and fallback scenarios

## Testing

### Unit Tests

Comprehensive test coverage includes:
- Device detection for various user agents
- Segment URI validation and parsing
- HLS channel lifecycle management
- Worker pool functionality
- Error handling scenarios

### Manual Testing

To test HLS functionality:

1. **iOS Device**: Open the stream URL on Safari - should automatically use HLS
2. **Desktop**: Add `?format=hls` to force HLS mode
3. **Developer Tools**: Check network tab for `.m3u8` and `.ts` requests

## Compatibility

### Supported Devices

- **iOS 9+**: Native HLS support
- **Android 4.4+**: Via hls.js
- **Desktop browsers**: Via hls.js or fallback to MPEG-TS
- **Smart TVs**: Many have native HLS support

### Browser Support

- **Safari**: Native HLS support
- **Chrome/Firefox/Edge**: Via hls.js
- **Legacy browsers**: Automatic fallback to MPEG-TS

## Performance Considerations

### HLS vs MPEG-TS

- **HLS Advantages**: Better mobile compatibility, adaptive bitrate, pause/seek support
- **MPEG-TS Advantages**: Lower latency, less CPU usage, smaller bandwidth overhead
- **Automatic Selection**: System chooses optimal format based on device capabilities

### Resource Usage

- **Memory**: HLS uses ~2-3x more memory due to segment buffering
- **CPU**: Concurrent segment generation may increase CPU usage
- **Bandwidth**: 20-30% reduction in effective bitrate for HLS streams

## Troubleshooting

### Common Issues

1. **No HLS playback**: Check browser console for hls.js errors
2. **High latency**: Reduce segment duration in configuration
3. **Buffering issues**: Adjust `MaxSegments` and `SegmentBufferSize`
4. **Quality issues**: Review `BitrateReduction` settings

### Debug Mode

Enable debug logging to troubleshoot HLS issues:
```bash
./MovieNight --log-level debug
```

## Future Enhancements

- **Adaptive Bitrate Streaming**: Multiple quality levels
- **WebRTC Integration**: Ultra-low latency streaming
- **CDN Support**: Edge caching for HLS segments
- **Analytics**: Detailed playback metrics and quality monitoring