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
                
                // Buffer management optimized for live streaming
                maxBufferLength: 30,        // 30 seconds of buffer for live streams
                maxMaxBufferLength: 60,     // Maximum buffer size
                maxBufferSize: 60 * 1000 * 1000, // 60MB buffer size limit
                maxBufferHole: 0.5,         // Allow larger buffer holes for live streams
                
                // Live streaming specific settings
                liveSyncDurationCount: 3,   // Stay close to live edge (3 segments = 18s)
                liveMaxLatencyDurationCount: 10, // Maximum acceptable latency (10 segments = 60s)
                liveDurationInfinity: true, // Allow infinite duration for live streams
                
                // Fragment loading optimized for live
                fragLoadingTimeOut: 20000,  // 20s timeout for 6s segments  
                fragLoadingMaxRetry: 3,     // Reduced retries for faster recovery
                fragLoadingRetryDelay: 1000, // 1s delay between retries
                
                // Manifest loading
                manifestLoadingTimeOut: 10000, // 10s manifest timeout
                manifestLoadingMaxRetry: 3,     // Fewer manifest retries
                manifestLoadingRetryDelay: 1000, // 1s delay
                
                // Error recovery optimized for live streams
                fragLoadingMaxRetryTimeout: 64000, // Maximum retry timeout
                levelLoadingTimeOut: 10000,  // Level loading timeout
                levelLoadingMaxRetry: 4,     // Level loading retries
                levelLoadingRetryDelay: 1000, // Level retry delay
                
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
                // Monitor buffer health but don't spam logs
                if (data.frag && data.frag.sn % 5 === 0) {
                    console.log(`HLS buffer health: ${Math.round(videoElement.buffered.length > 0 ? videoElement.buffered.end(videoElement.buffered.length - 1) - videoElement.currentTime : 0)}s ahead`);
                }
            });
            
            hls.on(Hls.Events.BUFFER_EOS, () => {
                console.log('HLS end of stream reached');
                connectionState = 'ended';
            });
            
            // Enhanced buffer stall detection and recovery
            hls.on(Hls.Events.BUFFER_STALLED, () => {
                console.warn('HLS buffer stalled, implementing recovery strategy');
                
                // Implement progressive recovery strategy
                if (!stalledRecoveryTimeout) {
                    stalledRecoveryTimeout = setTimeout(() => {
                        if (connectionState === 'playing' && currentPlayer && currentPlayer.instance && currentPlayer.type === 'hls.js') {
                            console.log('Attempting buffer stall recovery...');
                            try {
                                hls.trigger(Hls.Events.BUFFER_RESET);
                                hls.startLoad();
                            } catch (e) {
                                console.warn('Buffer stall recovery failed:', e);
                            }
                        }
                        stalledRecoveryTimeout = null;
                    }, 2000); // Wait 2s before recovery attempt
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
                            console.log('Buffer append error - likely caused by track changes, will recover');
                            // This is often non-fatal and HLS.js will recover automatically
                            return;
                            
                        case 'bufferFullError':
                            console.log('Buffer full - will manage automatically');
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
            
            // Monitor connection health periodically
            const connectionMonitor = setInterval(() => {
                if (!currentPlayer || currentPlayer.type !== 'hls.js' || !currentPlayer.instance) {
                    clearInterval(connectionMonitor);
                    return;
                }
                
                const timeSinceLastBuffer = Date.now() - lastBufferUpdate;
                
                // Check for connection issues
                if (timeSinceLastBuffer > 15000 && connectionState === 'playing') {
                    console.warn(`Connection may be stalled (${timeSinceLastBuffer}ms since last buffer update)`);
                    connectionState = 'stalled';
                } else if (timeSinceLastBuffer < 15000 && connectionState === 'stalled') {
                    console.log('Connection recovered');
                    connectionState = 'playing';
                }
            }, 5000);
            
            currentPlayer = { 
                type: 'hls.js', 
                instance: hls,
                connectionMonitor: connectionMonitor,
                getConnectionState: () => connectionState,
                getStats: () => ({
                    segmentCount: segmentCount,
                    retryCount: retryCount,
                    timeSinceLastBuffer: Date.now() - lastBufferUpdate
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

        const flvPlayer = mpegts.createPlayer({
            type: 'flv',
            url: '/live'
        }, {
            isLive: true,
            liveBufferLatencyChasing: true,
            autoCleanupSourceBuffer: false,
            enableWorker: false,
            reuseRedirectedURL: true,
            deferLoadAfterSourceOpen: false,
            fixAudioTimestampGap: false,
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
                // Proper MPEG-TS cleanup sequence to prevent SourceBuffer abort issues
                try {
                    // First pause to stop any ongoing operations  
                    currentPlayer.instance.pause();
                    // Then unload to clean up MediaSource properly
                    currentPlayer.instance.unload();
                    // Add a small delay to allow pending operations to complete
                    setTimeout(() => {
                        try {
                            // Finally destroy the instance
                            currentPlayer.instance.destroy();
                            console.log('MPEG-TS player destroyed and cleaned up');
                        } catch (e) {
                            console.warn('Error during delayed MPEG-TS destroy:', e);
                        }
                    }, 100);
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
