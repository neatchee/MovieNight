/// <reference path='./both.js' />

let currentPlayer = null;

/**
 * Detect if the browser is iOS Safari or desktop Safari
 */
function isIOSOrSafari() {
    const ua = navigator.userAgent;
    // iOS detection (iPhone, iPad, iPod)
    if (/iPad|iPhone|iPod/.test(ua)) {
        return true;
    }
    // Safari on macOS detection
    if (/Safari/.test(ua) && !/Chrome/.test(ua) && !/Chromium/.test(ua)) {
        return true;
    }
    return false;
}

/**
 * Initialize the video player with intelligent selection
 */
function initPlayer() {
    const videoElement = document.querySelector('#videoElement');
    const overlay = document.querySelector('#videoOverlay');
    
    if (!videoElement || !overlay) {
        console.error('Video element or overlay not found');
        return;
    }
    
    // Set up overlay click handler - CRITICAL functionality that must be preserved
    overlay.onclick = () => {
        console.log('Video unmuted via overlay click');
        overlay.style.display = 'none';
        videoElement.muted = false;
    };
    
    // Intelligent player selection based on browser capabilities and platform
    if (isIOSOrSafari()) {
        // On iOS/Safari, MPEG-TS often doesn't work well, so prefer HLS
        if (tryHLSPlayer(videoElement)) {
            console.log('Using HLS player (iOS/Safari preferred)');
            return;
        }
        // Fallback to MPEG-TS if HLS fails
        if (tryMpegTSPlayer(videoElement)) {
            console.log('Using MPEG-TS player (fallback on iOS/Safari)');
            return;
        }
    } else {
        // On other browsers, prefer MPEG-TS first as it's more reliable
        if (tryMpegTSPlayer(videoElement)) {
            console.log('Using MPEG-TS player (preferred on desktop)');
            return;
        }
        // Fallback to HLS if MPEG-TS fails
        if (tryHLSPlayer(videoElement)) {
            console.log('Using HLS player (fallback on desktop)');
            return;
        }
    }
    
    console.error('No supported video player available');
    setVideoError('No supported video player found for this browser.');
}

/**
 * Try to initialize HLS player (native or HLS.js)
 */
function tryHLSPlayer(videoElement) {
    try {
        // Check for native HLS support first (iOS Safari)
        if (videoElement.canPlayType('application/vnd.apple.mpegurl')) {
            videoElement.src = '/live.m3u8';
            videoElement.load();
            videoElement.play().catch(e => console.warn('HLS native play failed:', e));
            currentPlayer = { type: 'native-hls' };
            return true;
        }
        
        // Check for HLS.js support
        if (window.Hls && Hls.isSupported()) {
            const hls = new Hls({
                debug: false,
                enableWorker: false,
                
                // Live streaming configuration following HLS.js best practices
                // Remove progressive mode - it's for MP4, not live HLS
                
                // Buffer management optimized for smooth segment transitions
                maxBufferLength: 20,        // Reduced to 20s for faster transitions
                maxMaxBufferLength: 40,     // Reduced maximum buffer for tighter control
                maxBufferSize: 40 * 1000 * 1000, // 40MB buffer size limit (reduced)
                maxBufferHole: 0.3,         // Tighter buffer hole tolerance for smoother transitions
                backBufferLength: 10,       // Keep 10s of backward buffer for seamless seeking
                
                // Live streaming specific settings optimized for segment transitions
                liveSyncDurationCount: 2,   // Stay very close to live edge (2 segments = 12s)
                liveMaxLatencyDurationCount: 6, // Tighter maximum latency (6 segments = 36s)
                liveDurationInfinity: true, // Allow infinite duration for live streams
                
                // Segment transition optimization
                startOnSegmentBoundary: true, // Align playback with segment boundaries
                
                // Fragment loading optimized for seamless transitions
                fragLoadingTimeOut: 15000,  // Reduced timeout for faster recovery  
                fragLoadingMaxRetry: 2,     // Fewer retries for faster transition recovery
                fragLoadingRetryDelay: 500, // Faster retry for segment transitions
                
                // Manifest loading optimized for faster transitions
                manifestLoadingTimeOut: 8000, // Faster manifest timeout
                manifestLoadingMaxRetry: 2,     // Fewer manifest retries for faster recovery
                manifestLoadingRetryDelay: 500, // Faster retry delay
                
                // Error recovery optimized for segment transitions
                fragLoadingMaxRetryTimeout: 30000, // Reduced maximum retry timeout
                levelLoadingTimeOut: 8000,  // Faster level loading timeout
                levelLoadingMaxRetry: 3,     // Level loading retries
                levelLoadingRetryDelay: 500, // Faster level retry delay
                
                // Start configuration
                startLevel: -1,              // Auto-select start level
                autoStartLoad: true,         // Auto-start loading
                
                // Adaptive bitrate - more conservative for live
                abrEwmaFastLive: 3.0,        // Fast EWMA for live ABR
                abrEwmaSlowLive: 9.0,        // Slow EWMA for live ABR
                abrEwmaDefaultEstimate: 500000, // Default bandwidth estimate (500kbps)
                abrBandWidthFactor: 0.95,    // Conservative bandwidth factor
                abrBandWidthUpFactor: 0.7,   // Conservative upward switching
                
                // Stall recovery
                maxStarvationDelay: 4,       // Maximum starvation delay
                maxLoadingDelay: 4,          // Maximum loading delay
                nudgeOffset: 0.1,            // Nudge offset for stall recovery
                nudgeMaxRetry: 3,            // Maximum nudge retries
                
                // Cap level to player size for efficiency
                capLevelToPlayerSize: true
            });
            
            hls.loadSource('/live.m3u8');
            hls.attachMedia(videoElement);
            
            // Enhanced state tracking for smoother transitions
            let firstSegmentLoaded = false;
            let segmentCount = 0;
            let lastBufferUpdate = Date.now();
            let connectionState = 'connecting';
            let retryCount = 0;
            let stalledRecoveryTimeout = null;
            let lastSegmentBoundary = 0;
            let transitionMonitoringEnabled = true;
            
            hls.on(Hls.Events.MANIFEST_LOADED, () => {
                console.log('HLS manifest loaded successfully');
                connectionState = 'manifest-loaded';
                retryCount = 0; // Reset retry count on successful manifest load
            });
            
            // Enhanced fragment loading with better transition handling
            hls.on(Hls.Events.FRAG_LOADING, (event, data) => {
                if (connectionState === 'manifest-loaded') {
                    connectionState = 'loading-segments';
                }
                lastBufferUpdate = Date.now();
                
                // Clear any existing stall recovery timeout
                if (stalledRecoveryTimeout) {
                    clearTimeout(stalledRecoveryTimeout);
                    stalledRecoveryTimeout = null;
                }
            });
            
            hls.on(Hls.Events.FRAG_LOADED, (event, data) => {
                segmentCount++;
                lastBufferUpdate = Date.now();
                
                if (!firstSegmentLoaded) {
                    firstSegmentLoaded = true;
                    connectionState = 'playing';
                    console.log('HLS first segment loaded, starting playback');
                    // Reduced wait time for faster startup with 6s segments
                    setTimeout(() => {
                        videoElement.play().catch(e => console.warn('HLS play failed:', e));
                    }, 500);
                } else {
                    // Log segment transitions for debugging (but not too frequently)
                    if (segmentCount % 10 === 0) {
                        console.log(`HLS: ${segmentCount} segments loaded successfully`);
                    }
                }
            });
            
            // Connection state monitoring
            hls.on(Hls.Events.MEDIA_ATTACHED, () => {
                console.log('HLS media attached');
                connectionState = 'attached';
            });
            
            hls.on(Hls.Events.MEDIA_DETACHED, () => {
                console.log('HLS media detached');
                connectionState = 'detached';
                // Clean up when media is detached
                if (stalledRecoveryTimeout) {
                    clearTimeout(stalledRecoveryTimeout);
                    stalledRecoveryTimeout = null;
                }
            });
            
            // Enhanced buffer monitoring with stall detection
            hls.on(Hls.Events.BUFFER_CREATED, (event, data) => {
                console.log('HLS buffer created:', data.tracks);
                connectionState = 'buffer-created';
            });
            
            hls.on(Hls.Events.BUFFER_APPENDED, (event, data) => {
                lastBufferUpdate = Date.now();
                
                // Enhanced buffer transition monitoring
                if (data.frag && transitionMonitoringEnabled) {
                    const segmentIndex = data.frag.sn;
                    
                    // Monitor segment boundaries for smooth transitions
                    if (segmentIndex !== lastSegmentBoundary) {
                        lastSegmentBoundary = segmentIndex;
                        
                        // Log segment transitions for debugging (every 5th segment)
                        if (segmentIndex % 5 === 0) {
                            const bufferLength = videoElement.buffered.length > 0 ? 
                                Math.round(videoElement.buffered.end(videoElement.buffered.length - 1) - videoElement.currentTime) : 0;
                            console.log(`HLS segment transition: ${segmentIndex}, buffer: ${bufferLength}s`);
                        }
                        
                        // Check for buffer health during transitions
                        const bufferInfo = hls.bufferStall || {};
                        if (bufferInfo.audio !== undefined || bufferInfo.video !== undefined) {
                            console.log('Buffer stall detected during segment transition, implementing recovery');
                            // Trigger buffer flush for clean transition
                            try {
                                hls.trigger(Hls.Events.BUFFER_FLUSHING, {
                                    startOffset: 0,
                                    endOffset: Number.POSITIVE_INFINITY,
                                    type: 'video'
                                });
                            } catch (e) {
                                console.warn('Buffer flush during transition failed:', e);
                            }
                        }
                    }
                }
                
                // Monitor buffer health but don't spam logs
                if (data.frag && data.frag.sn % 10 === 0) {
                    console.log(`HLS buffer health: ${Math.round(videoElement.buffered.length > 0 ? videoElement.buffered.end(videoElement.buffered.length - 1) - videoElement.currentTime : 0)}s ahead`);
                }
            });
            
            hls.on(Hls.Events.BUFFER_EOS, () => {
                console.log('HLS end of stream reached');
                connectionState = 'ended';
            });
            
            // Enhanced buffer management for segment transitions
            hls.on(Hls.Events.BUFFER_FLUSHING, (event, data) => {
                console.log('HLS buffer flushing for clean transition');
            });
            
            hls.on(Hls.Events.BUFFER_FLUSHED, (event, data) => {
                console.log('HLS buffer flushed successfully');
                lastBufferUpdate = Date.now();
            });
            
            // Handle discontinuities in stream for seamless transitions
            hls.on(Hls.Events.LEVEL_PTS_UPDATED, (event, data) => {
                // PTS discontinuity detected - ensure smooth transition
                if (data.drift && Math.abs(data.drift) > 1000) {
                    console.log(`PTS discontinuity detected: ${data.drift}ms, adjusting for smooth transition`);
                }
            });
            
            // Enhanced audio/video track handling during transitions
            hls.on(Hls.Events.AUDIO_TRACK_SWITCHING, (event, data) => {
                console.log(`Audio track switching during transition: ${data.id}`);
            });
            
            hls.on(Hls.Events.AUDIO_TRACK_SWITCHED, (event, data) => {
                console.log(`Audio track switched: ${data.id}`);
                lastBufferUpdate = Date.now();
            });
            
            // Enhanced buffer stall detection and recovery for segment transitions
            hls.on(Hls.Events.BUFFER_STALLED, () => {
                console.warn('HLS buffer stalled during segment transition, implementing enhanced recovery');
                
                // Progressive recovery strategy optimized for segment transitions
                if (!stalledRecoveryTimeout) {
                    stalledRecoveryTimeout = setTimeout(() => {
                        if (connectionState === 'playing' && currentPlayer && currentPlayer.instance && currentPlayer.type === 'hls.js') {
                            console.log('Attempting segment transition stall recovery...');
                            try {
                                // First try buffer reset for clean transition
                                hls.trigger(Hls.Events.BUFFER_RESET);
                                
                                // Then restart loading from current position
                                const currentTime = videoElement.currentTime;
                                hls.startLoad(currentTime);
                                
                                // Monitor recovery progress
                                const recoveryMonitor = setTimeout(() => {
                                    const bufferLength = videoElement.buffered.length > 0 ? 
                                        videoElement.buffered.end(videoElement.buffered.length - 1) - videoElement.currentTime : 0;
                                    
                                    if (bufferLength < 2) {
                                        console.warn('Recovery progress slow, triggering emergency recovery');
                                        try {
                                            // Emergency recovery: seek to current position to force refresh
                                            videoElement.currentTime = videoElement.currentTime + 0.1;
                                        } catch (e) {
                                            console.warn('Emergency recovery failed:', e);
                                        }
                                    }
                                }, 3000);
                                
                                // Clear recovery monitor when buffer updates
                                const clearRecoveryMonitor = () => {
                                    clearTimeout(recoveryMonitor);
                                    hls.off(Hls.Events.BUFFER_APPENDED, clearRecoveryMonitor);
                                };
                                hls.on(Hls.Events.BUFFER_APPENDED, clearRecoveryMonitor);
                                
                            } catch (e) {
                                console.warn('Enhanced buffer stall recovery failed:', e);
                            }
                        }
                        stalledRecoveryTimeout = null;
                    }, 1500); // Faster recovery for segment transitions
                }
            });
            
            // Level switching for smooth transitions
            hls.on(Hls.Events.LEVEL_SWITCHING, (event, data) => {
                console.log(`HLS quality switching to level ${data.level}`);
            });
            
            hls.on(Hls.Events.LEVEL_SWITCHED, (event, data) => {
                console.log(`HLS quality switched to level ${data.level}`);
            });
            
            // Comprehensive error handling with enhanced recovery
            hls.on(Hls.Events.ERROR, (event, data) => {
                const currentTime = Date.now();
                const timeSinceLastBuffer = currentTime - lastBufferUpdate;
                
                // Enhanced non-fatal error handling
                if (!data.fatal) {
                    // Handle specific non-fatal errors according to HLS.js documentation
                    switch (data.details) {
                        case 'bufferStalledError':
                            if (!firstSegmentLoaded) {
                                console.log('Initial buffer stall, waiting for first segment...');
                                return;
                            }
                            console.log('Buffer stalled - normal during live streaming');
                            return;
                            
                        case 'bufferNudgedOnStall':
                            console.log('Buffer nudged on stall - recovery in progress');
                            return;
                            
                        case 'bufferSeekOverHole':
                            console.log('Buffer seek over hole - attempting recovery');
                            return;
                            
                        case 'bufferAppendingError':
                            console.log('Buffer appending error - will retry automatically');
                            return;
                            
                        case 'bufferAppendError':
                            console.log('Buffer append error during transition - implementing recovery');
                            // Enhanced recovery for segment transition append errors
                            try {
                                hls.trigger(Hls.Events.BUFFER_FLUSHING, {
                                    startOffset: 0,
                                    endOffset: Number.POSITIVE_INFINITY,
                                    type: null // Flush both audio and video
                                });
                            } catch (e) {
                                console.warn('Buffer flush recovery failed:', e);
                            }
                            return;
                            
                        case 'bufferFullError':
                            console.log('Buffer full during transition - implementing buffer management');
                            // Proactive buffer management during transitions
                            try {
                                const currentTime = videoElement.currentTime;
                                hls.trigger(Hls.Events.BUFFER_FLUSHING, {
                                    startOffset: 0,
                                    endOffset: currentTime - 5, // Keep 5s behind current position
                                    type: null
                                });
                            } catch (e) {
                                console.warn('Buffer management failed:', e);
                            }
                            return;
                            
                        case 'fragLoadError':
                            console.log('Fragment load error - will retry');
                            return;
                            
                        case 'fragParsingError':
                            console.log('Fragment parsing error - may indicate server-side TS issues');
                            return;
                            
                        default:
                            // Log other non-fatal errors with context
                            console.log(`Non-fatal HLS error: ${data.details} (connection: ${connectionState}, buffer age: ${timeSinceLastBuffer}ms)`);
                            return;
                    }
                }
                
                // Enhanced fatal error handling with connection state awareness
                console.warn('Fatal HLS error:', data);
                retryCount++;
                
                switch (data.type) {
                    case Hls.ErrorTypes.NETWORK_ERROR:
                        console.log(`Fatal network error (attempt ${retryCount}), implementing recovery...`);
                        if (retryCount <= 3) {
                            // Progressive retry with exponential backoff
                            setTimeout(() => {
                                try {
                                    // Check if HLS instance is still valid before recovery
                                    if (currentPlayer && currentPlayer.instance && currentPlayer.type === 'hls.js') {
                                        hls.startLoad();
                                        connectionState = 'recovering';
                                    }
                                } catch (e) {
                                    console.error('Network recovery failed:', e);
                                }
                            }, Math.min(1000 * Math.pow(2, retryCount - 1), 5000));
                        } else {
                            console.log('Maximum network recovery attempts reached, falling back');
                            destroyCurrentPlayer();
                            tryMpegTSPlayer(videoElement);
                        }
                        break;
                        
                    case Hls.ErrorTypes.MEDIA_ERROR:
                        console.log(`Fatal media error (attempt ${retryCount}), attempting recovery...`);
                        if (retryCount <= 2) {
                            try {
                                // Check if HLS instance is still valid before recovery
                                if (currentPlayer && currentPlayer.instance && currentPlayer.type === 'hls.js') {
                                    hls.recoverMediaError();
                                    connectionState = 'recovering';
                                } else {
                                    console.log('HLS instance no longer valid, falling back');
                                    destroyCurrentPlayer();
                                    tryMpegTSPlayer(videoElement);
                                }
                            } catch (e) {
                                console.error('Media recovery failed:', e);
                                destroyCurrentPlayer();
                                tryMpegTSPlayer(videoElement);
                            }
                        } else {
                            console.log('Maximum media recovery attempts reached, falling back');
                            destroyCurrentPlayer();
                            tryMpegTSPlayer(videoElement);
                        }
                        break;
                        
                    default:
                        console.log('Unrecoverable fatal error, falling back to MPEG-TS');
                        destroyCurrentPlayer();
                        tryMpegTSPlayer(videoElement);
                        break;
                }
            });
            
            // Monitor connection health and segment transition quality
            const connectionMonitor = setInterval(() => {
                if (!currentPlayer || currentPlayer.type !== 'hls.js' || !currentPlayer.instance) {
                    clearInterval(connectionMonitor);
                    return;
                }
                
                const timeSinceLastBuffer = Date.now() - lastBufferUpdate;
                const bufferLength = videoElement.buffered.length > 0 ? 
                    videoElement.buffered.end(videoElement.buffered.length - 1) - videoElement.currentTime : 0;
                
                // Enhanced connection health monitoring for segment transitions
                if (timeSinceLastBuffer > 10000 && connectionState === 'playing') {
                    console.warn(`Segment transition may be stalled (${timeSinceLastBuffer}ms since last buffer update)`);
                    connectionState = 'stalled';
                    
                    // Proactive recovery for segment transition issues
                    if (bufferLength < 3) {
                        console.log('Low buffer during stall, triggering transition recovery');
                        try {
                            hls.trigger(Hls.Events.BUFFER_RESET);
                            hls.startLoad(videoElement.currentTime);
                        } catch (e) {
                            console.warn('Transition recovery failed:', e);
                        }
                    }
                } else if (timeSinceLastBuffer < 10000 && connectionState === 'stalled') {
                    console.log('Segment transition recovered');
                    connectionState = 'playing';
                }
                
                // Monitor for segment transition artifacting (rapid buffer changes)
                if (bufferLength > 25) {
                    console.log('Large buffer detected, optimizing for segment transitions');
                    try {
                        // Trim excessive buffer to prevent transition issues
                        const currentTime = videoElement.currentTime;
                        hls.trigger(Hls.Events.BUFFER_FLUSHING, {
                            startOffset: currentTime + 15,
                            endOffset: Number.POSITIVE_INFINITY,
                            type: null
                        });
                    } catch (e) {
                        console.warn('Buffer optimization failed:', e);
                    }
                }
            }, 3000); // More frequent monitoring for segment transitions
            
            currentPlayer = { 
                type: 'hls.js', 
                instance: hls,
                connectionMonitor: connectionMonitor,
                getConnectionState: () => connectionState,
                getStats: () => ({
                    segmentCount: segmentCount,
                    retryCount: retryCount,
                    timeSinceLastBuffer: Date.now() - lastBufferUpdate,
                    lastSegmentBoundary: lastSegmentBoundary,
                    transitionMonitoring: transitionMonitoringEnabled,
                    bufferLength: videoElement.buffered.length > 0 ? 
                        Math.round(videoElement.buffered.end(videoElement.buffered.length - 1) - videoElement.currentTime) : 0
                })
            };
            return true;
        }
        
    } catch (error) {
        console.error('Error initializing HLS player:', error);
    }
    
    return false;
}

/**
 * Try to initialize MPEG-TS player
 */
function tryMpegTSPlayer(videoElement) {
    try {
        if (!window.mpegts || !mpegts.isSupported()) {
            console.warn('mpegts not supported');
            return false;
        }

        // Detect Chrome browser for stricter MediaSource handling
        const isChrome = navigator.userAgent.includes('Chrome') && !navigator.userAgent.includes('Edg');
        
        const flvPlayer = mpegts.createPlayer({
            type: 'flv',
            url: '/live'
        }, {
            isLive: true,
            liveBufferLatencyChasing: !isChrome, // Disable for Chrome to prevent seek conflicts
            autoCleanupSourceBuffer: false,
            enableWorker: false,
            reuseRedirectedURL: true,
            deferLoadAfterSourceOpen: false,
            fixAudioTimestampGap: false,
            enableStashBuffer: !isChrome, // Disable stash buffer for Chrome
            stashInitialSize: isChrome ? 0 : undefined, // Minimize buffering for Chrome
        });
        
        flvPlayer.attachMediaElement(videoElement);
        flvPlayer.load();
        flvPlayer.play().catch(e => console.warn('MPEG-TS play failed:', e));
        
        currentPlayer = { type: 'mpegts', instance: flvPlayer };
        return true;
        
    } catch (error) {
        console.error('Error initializing MPEG-TS player:', error);
    }
    
    return false;
}

/**
 * Destroy the current player instance
 */
function destroyCurrentPlayer() {
    if (currentPlayer && currentPlayer.instance) {
        try {
            // Clean up connection monitor if it exists
            if (currentPlayer.connectionMonitor) {
                clearInterval(currentPlayer.connectionMonitor);
                currentPlayer.connectionMonitor = null;
            }
            
            if (currentPlayer.type === 'hls.js') {
                // Proper HLS.js cleanup sequence to prevent bufferAppendError
                // First detach media to stop buffer operations
                currentPlayer.instance.detachMedia();
                // Then destroy the instance
                currentPlayer.instance.destroy();
                console.log('HLS player destroyed and cleaned up');
            } else if (currentPlayer.type === 'mpegts') {
                // Detect Chrome for more aggressive cleanup handling
                const isChrome = navigator.userAgent.includes('Chrome') && !navigator.userAgent.includes('Edg');
                
                // Proper MPEG-TS cleanup sequence to prevent SourceBuffer abort issues
                try {
                    // First pause to stop any ongoing operations  
                    currentPlayer.instance.pause();
                    
                    // For Chrome, add extra delay and more cautious cleanup
                    if (isChrome) {
                        // Wait longer for Chrome to complete any pending operations
                        setTimeout(() => {
                            try {
                                currentPlayer.instance.unload();
                                // Longer delay for Chrome before destroy
                                setTimeout(() => {
                                    try {
                                        currentPlayer.instance.destroy();
                                        console.log('MPEG-TS player destroyed and cleaned up (Chrome)');
                                    } catch (e) {
                                        console.warn('Error during delayed Chrome MPEG-TS destroy:', e);
                                    }
                                }, 200);
                            } catch (e) {
                                console.warn('Error during Chrome MPEG-TS unload:', e);
                            }
                        }, 150);
                    } else {
                        // Standard cleanup for other browsers
                        currentPlayer.instance.unload();
                        setTimeout(() => {
                            try {
                                currentPlayer.instance.destroy();
                                console.log('MPEG-TS player destroyed and cleaned up');
                            } catch (e) {
                                console.warn('Error during delayed MPEG-TS destroy:', e);
                            }
                        }, 100);
                    }
                } catch (e) {
                    console.warn('Error during MPEG-TS cleanup:', e);
                    // Fallback - try to destroy immediately
                    try {
                        currentPlayer.instance.destroy();
                    } catch (e2) {
                        console.warn('Error during fallback MPEG-TS destroy:', e2);
                    }
                }
            }
        } catch (error) {
            console.error('Error destroying player:', error);
        }
    }
    currentPlayer = null;
}

/**
 * Display video error message to user
 */
function setVideoError(message) {
    const videoElement = document.querySelector('#videoElement');
    if (videoElement) {
        videoElement.style.display = 'none';
    }
    
    const overlay = document.querySelector('#videoOverlay');
    if (overlay) {
        overlay.innerHTML = `<div style="color: red; font-size: 16px; text-align: center; padding: 20px;">${message}</div>`;
        overlay.style.display = 'flex';
    }
}

window.addEventListener('load', initPlayer);
