/// <reference path='./both.js' />

let currentPlayer = null;

/**
 * Asynchronous segment buffer for smooth HLS playback
 */
class SegmentBuffer {
    constructor(maxBufferSize = 3) {
        this.maxBufferSize = maxBufferSize;
        this.segments = new Map(); // segmentIndex -> ArrayBuffer
        this.fetchPromises = new Map(); // segmentIndex -> Promise
        this.currentSegmentIndex = 0;
        this.isEnabled = true;
        this.stats = {
            fetched: 0,
            cacheHits: 0,
            errors: 0,
            bytesBuffered: 0
        };
    }

    /**
     * Pre-fetch segments asynchronously
     */
    async prefetchSegments(startIndex, count = 2) {
        if (!this.isEnabled) return;

        const promises = [];
        for (let i = 0; i < count; i++) {
            const segmentIndex = startIndex + i;
            if (!this.segments.has(segmentIndex) && !this.fetchPromises.has(segmentIndex)) {
                promises.push(this.fetchSegment(segmentIndex));
            }
        }

        if (promises.length > 0) {
            // Fire and forget - don't wait for completion to avoid blocking
            Promise.allSettled(promises).then(results => {
                const successful = results.filter(r => r.status === 'fulfilled').length;
                if (successful > 0) {
                    console.log(`Pre-fetched ${successful}/${results.length} segments starting from ${startIndex}`);
                }
            });
        }
    }

    /**
     * Fetch a single segment asynchronously
     */
    async fetchSegment(segmentIndex) {
        if (!this.isEnabled) return null;

        // Check if already fetching
        if (this.fetchPromises.has(segmentIndex)) {
            return this.fetchPromises.get(segmentIndex);
        }

        const fetchPromise = this.doFetchSegment(segmentIndex);
        this.fetchPromises.set(segmentIndex, fetchPromise);

        try {
            const result = await fetchPromise;
            return result;
        } finally {
            this.fetchPromises.delete(segmentIndex);
        }
    }

    /**
     * Internal segment fetching implementation
     */
    async doFetchSegment(segmentIndex) {
        const url = `/live_segment_${segmentIndex}.ts`;
        
        try {
            const controller = new AbortController();
            const timeoutId = setTimeout(() => controller.abort(), 10000); // 10s timeout

            const response = await fetch(url, {
                signal: controller.signal,
                headers: {
                    'Cache-Control': 'no-cache',
                    'Pragma': 'no-cache'
                }
            });

            clearTimeout(timeoutId);

            if (!response.ok) {
                throw new Error(`HTTP ${response.status}`);
            }

            const arrayBuffer = await response.arrayBuffer();
            
            // Store in buffer with size management
            this.segments.set(segmentIndex, arrayBuffer);
            this.stats.fetched++;
            this.stats.bytesBuffered += arrayBuffer.byteLength;
            
            // Clean up old segments
            this.cleanupOldSegments(segmentIndex);
            
            return arrayBuffer;
        } catch (error) {
            this.stats.errors++;
            console.warn(`Failed to pre-fetch segment ${segmentIndex}:`, error.message);
            return null;
        }
    }

    /**
     * Check if segment is available in buffer
     */
    hasSegment(segmentIndex) {
        return this.segments.has(segmentIndex);
    }

    /**
     * Get segment from buffer
     */
    getSegment(segmentIndex) {
        if (this.segments.has(segmentIndex)) {
            this.stats.cacheHits++;
            return this.segments.get(segmentIndex);
        }
        return null;
    }

    /**
     * Clean up old segments to prevent memory leaks
     */
    cleanupOldSegments(currentIndex) {
        const segmentsToRemove = [];
        
        for (const [index, buffer] of this.segments) {
            // Remove segments that are too far behind current position
            if (index < currentIndex - this.maxBufferSize) {
                segmentsToRemove.push(index);
                this.stats.bytesBuffered -= buffer.byteLength;
            }
        }

        // Also ensure we don't exceed max buffer size ahead
        const sortedIndices = Array.from(this.segments.keys()).sort((a, b) => a - b);
        while (sortedIndices.length > this.maxBufferSize * 2) {
            const oldestIndex = sortedIndices.shift();
            if (oldestIndex < currentIndex + this.maxBufferSize) {
                segmentsToRemove.push(oldestIndex);
                this.stats.bytesBuffered -= this.segments.get(oldestIndex).byteLength;
            }
        }

        segmentsToRemove.forEach(index => {
            this.segments.delete(index);
        });

        if (segmentsToRemove.length > 0) {
            console.log(`Cleaned up ${segmentsToRemove.length} old segments, buffer size: ${this.segments.size}`);
        }
    }

    /**
     * Update current playback position
     */
    updatePosition(segmentIndex) {
        this.currentSegmentIndex = segmentIndex;
        
        // Trigger pre-fetching of upcoming segments
        this.prefetchSegments(segmentIndex + 1, 2);
        
        // Clean up old segments
        this.cleanupOldSegments(segmentIndex);
    }

    /**
     * Get buffer statistics
     */
    getStats() {
        return {
            ...this.stats,
            bufferSize: this.segments.size,
            bufferedIndices: Array.from(this.segments.keys()).sort((a, b) => a - b),
            memoryUsageMB: Math.round(this.stats.bytesBuffered / 1024 / 1024 * 100) / 100
        };
    }

    /**
     * Clear all buffers and reset
     */
    clear() {
        this.segments.clear();
        this.fetchPromises.clear();
        this.stats = {
            fetched: 0,
            cacheHits: 0,
            errors: 0,
            bytesBuffered: 0
        };
    }

    /**
     * Enable/disable segment pre-fetching
     */
    setEnabled(enabled) {
        this.isEnabled = enabled;
        if (!enabled) {
            // Cancel all pending fetches
            this.fetchPromises.clear();
        }
    }
}

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
        // Initialize segment buffer for async pre-fetching
        const segmentBuffer = new SegmentBuffer(3); // Buffer 3 segments max
        
        // Check for native HLS support first (iOS Safari)
        if (videoElement.canPlayType('application/vnd.apple.mpegurl')) {
            videoElement.src = '/live.m3u8';
            videoElement.load();
            videoElement.play().catch(e => console.warn('HLS native play failed:', e));
            
            // Even with native HLS, we can use our buffer for smoother transitions
            currentPlayer = { 
                type: 'native-hls',
                segmentBuffer: segmentBuffer,
                getStats: () => segmentBuffer.getStats()
            };
            
            // Start pre-fetching after a short delay
            setTimeout(() => {
                segmentBuffer.prefetchSegments(0, 2);
            }, 2000);
            
            return true;
        }
        
        // Check for HLS.js support
        if (window.Hls && Hls.isSupported()) {
            const hls = new Hls({
                debug: false,
                enableWorker: false,
                
                // Live streaming configuration following HLS.js best practices
                // Buffer management optimized for live streaming with async pre-fetching
                maxBufferLength: 18,        // Reduced to 18s since we have async pre-fetching
                maxMaxBufferLength: 30,     // Reduced max buffer size  
                maxBufferSize: 40 * 1000 * 1000, // 40MB buffer size limit
                maxBufferHole: 0.3,         // Smaller buffer holes for smoother playback
                
                // Live streaming specific settings
                liveSyncDurationCount: 2,   // Stay closer to live edge (2 segments = 12s)
                liveMaxLatencyDurationCount: 8, // Reduced latency (8 segments = 48s)
                liveDurationInfinity: true, // Allow infinite duration for live streams
                
                // Fragment loading optimized for live with faster timeouts
                fragLoadingTimeOut: 15000,  // 15s timeout for 6s segments  
                fragLoadingMaxRetry: 2,     // Fewer retries since we have pre-fetching
                fragLoadingRetryDelay: 500, // Faster retry delay
                
                // Manifest loading
                manifestLoadingTimeOut: 8000, // Faster manifest timeout
                manifestLoadingMaxRetry: 3,     
                manifestLoadingRetryDelay: 500, // Faster retry
                
                // Error recovery optimized for live streams
                fragLoadingMaxRetryTimeout: 30000, // Reduced retry timeout
                levelLoadingTimeOut: 8000,  // Faster level loading
                levelLoadingMaxRetry: 3,     
                levelLoadingRetryDelay: 500, 
                
                // Start configuration
                startLevel: -1,              // Auto-select start level
                autoStartLoad: true,         // Auto-start loading
                
                // Adaptive bitrate - more aggressive for better quality
                abrEwmaFastLive: 2.0,        // Faster EWMA for live ABR
                abrEwmaSlowLive: 6.0,        // Faster slow EWMA 
                abrEwmaDefaultEstimate: 800000, // Higher default bandwidth estimate
                abrBandWidthFactor: 0.9,     // Less conservative bandwidth factor
                abrBandWidthUpFactor: 0.8,   // Less conservative upward switching
                
                // Stall recovery
                maxStarvationDelay: 2,       // Faster starvation recovery
                maxLoadingDelay: 2,          // Faster loading recovery
                nudgeOffset: 0.05,           // Smaller nudge offset
                nudgeMaxRetry: 5,            // More nudge retries
                
                // Cap level to player size for efficiency
                capLevelToPlayerSize: true
            });
            
            hls.loadSource('/live.m3u8');
            hls.attachMedia(videoElement);
            
            // Enhanced state tracking for smoother transitions with async pre-fetching
            let firstSegmentLoaded = false;
            let segmentCount = 0;
            let lastBufferUpdate = Date.now();
            let connectionState = 'connecting';
            let retryCount = 0;
            let stalledRecoveryTimeout = null;
            let currentSegmentIndex = 0;
            
            hls.on(Hls.Events.MANIFEST_LOADED, () => {
                console.log('HLS manifest loaded successfully');
                connectionState = 'manifest-loaded';
                retryCount = 0; // Reset retry count on successful manifest load
                
                // Start pre-fetching segments after manifest is loaded
                setTimeout(() => {
                    segmentBuffer.prefetchSegments(0, 2);
                }, 500);
            });
            
            // Enhanced fragment loading with async pre-fetching integration
            hls.on(Hls.Events.FRAG_LOADING, (event, data) => {
                if (connectionState === 'manifest-loaded') {
                    connectionState = 'loading-segments';
                }
                lastBufferUpdate = Date.now();
                
                // Track current segment and trigger pre-fetching
                if (data.frag && data.frag.sn !== undefined) {
                    currentSegmentIndex = data.frag.sn;
                    segmentBuffer.updatePosition(currentSegmentIndex);
                }
                
                // Clear any existing stall recovery timeout
                if (stalledRecoveryTimeout) {
                    clearTimeout(stalledRecoveryTimeout);
                    stalledRecoveryTimeout = null;
                }
            });
            
            hls.on(Hls.Events.FRAG_LOADED, (event, data) => {
                segmentCount++;
                lastBufferUpdate = Date.now();
                
                // Update segment buffer position for pre-fetching
                if (data.frag && data.frag.sn !== undefined) {
                    currentSegmentIndex = data.frag.sn;
                    segmentBuffer.updatePosition(currentSegmentIndex);
                }
                
                if (!firstSegmentLoaded) {
                    firstSegmentLoaded = true;
                    connectionState = 'playing';
                    console.log('HLS first segment loaded, starting playback with async pre-fetching');
                    // Reduced wait time for faster startup with 6s segments
                    setTimeout(() => {
                        videoElement.play().catch(e => console.warn('HLS play failed:', e));
                    }, 300); // Even faster startup with pre-fetching
                } else {
                    // Log segment transitions and buffer stats
                    if (segmentCount % 5 === 0) {
                        const bufferStats = segmentBuffer.getStats();
                        console.log(`HLS: ${segmentCount} segments loaded, buffer: ${bufferStats.bufferSize} segments (${bufferStats.memoryUsageMB}MB), cache hits: ${bufferStats.cacheHits}`);
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
                // Clean up segment buffer
                segmentBuffer.clear();
            });
            
            // Enhanced buffer monitoring with async pre-fetching integration
            hls.on(Hls.Events.BUFFER_CREATED, (event, data) => {
                console.log('HLS buffer created:', data.tracks);
                connectionState = 'buffer-created';
            });
            
            hls.on(Hls.Events.BUFFER_APPENDED, (event, data) => {
                lastBufferUpdate = Date.now();
                
                // Trigger additional pre-fetching when buffer is healthy
                if (data.frag && data.frag.sn !== undefined) {
                    currentSegmentIndex = Math.max(currentSegmentIndex, data.frag.sn);
                    segmentBuffer.updatePosition(currentSegmentIndex);
                }
                
                // Monitor buffer health with segment buffer stats
                if (data.frag && data.frag.sn % 3 === 0) {
                    const bufferStats = segmentBuffer.getStats();
                    const videoBufferAhead = videoElement.buffered.length > 0 ? 
                        Math.round(videoElement.buffered.end(videoElement.buffered.length - 1) - videoElement.currentTime) : 0;
                    console.log(`HLS buffer health: ${videoBufferAhead}s video, ${bufferStats.bufferSize} pre-fetched segments`);
                }
            });
            
            hls.on(Hls.Events.BUFFER_EOS, () => {
                console.log('HLS end of stream reached');
                connectionState = 'ended';
                // Clean up segment buffer when stream ends
                segmentBuffer.clear();
            });
            
            // Enhanced buffer stall detection with pre-fetching recovery
            hls.on(Hls.Events.BUFFER_STALLED, () => {
                console.warn('HLS buffer stalled, implementing recovery with pre-fetching assist');
                
                // Use pre-fetched segments if available
                const bufferStats = segmentBuffer.getStats();
                if (bufferStats.bufferSize > 0) {
                    console.log(`${bufferStats.bufferSize} pre-fetched segments available for stall recovery`);
                }
                
                // Implement progressive recovery strategy
                if (!stalledRecoveryTimeout) {
                    stalledRecoveryTimeout = setTimeout(() => {
                        if (connectionState === 'playing' && currentPlayer && currentPlayer.instance && currentPlayer.type === 'hls.js') {
                            console.log('Attempting buffer stall recovery with enhanced pre-fetching...');
                            try {
                                hls.trigger(Hls.Events.BUFFER_RESET);
                                hls.startLoad();
                                // Trigger aggressive pre-fetching after recovery
                                setTimeout(() => {
                                    segmentBuffer.prefetchSegments(currentSegmentIndex + 1, 3);
                                }, 1000);
                            } catch (e) {
                                console.warn('Buffer stall recovery failed:', e);
                            }
                        }
                        stalledRecoveryTimeout = null;
                    }, 1500); // Faster recovery with pre-fetching
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
            
            // Monitor connection health periodically with enhanced pre-fetching
            const connectionMonitor = setInterval(() => {
                if (!currentPlayer || currentPlayer.type !== 'hls.js' || !currentPlayer.instance) {
                    clearInterval(connectionMonitor);
                    return;
                }
                
                const timeSinceLastBuffer = Date.now() - lastBufferUpdate;
                const bufferStats = segmentBuffer.getStats();
                
                // Enhanced connection monitoring with buffer awareness
                if (timeSinceLastBuffer > 12000 && connectionState === 'playing') {
                    console.warn(`Connection may be stalled (${timeSinceLastBuffer}ms since last buffer update), buffer has ${bufferStats.bufferSize} segments`);
                    connectionState = 'stalled';
                    // Trigger more aggressive pre-fetching when stalled
                    segmentBuffer.prefetchSegments(currentSegmentIndex + 1, 3);
                } else if (timeSinceLastBuffer < 12000 && connectionState === 'stalled') {
                    console.log('Connection recovered with segment buffer assist');
                    connectionState = 'playing';
                }
                
                // Periodic buffer stats logging (every 30 seconds)
                if (segmentCount > 0 && segmentCount % 30 === 0) {
                    console.log('Segment buffer stats:', bufferStats);
                }
            }, 3000); // More frequent monitoring
            
            currentPlayer = { 
                type: 'hls.js', 
                instance: hls,
                segmentBuffer: segmentBuffer,
                connectionMonitor: connectionMonitor,
                getConnectionState: () => connectionState,
                getStats: () => ({
                    segmentCount: segmentCount,
                    retryCount: retryCount,
                    timeSinceLastBuffer: Date.now() - lastBufferUpdate,
                    bufferStats: segmentBuffer.getStats(),
                    currentSegmentIndex: currentSegmentIndex
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
            
            // Clean up segment buffer if it exists
            if (currentPlayer.segmentBuffer) {
                currentPlayer.segmentBuffer.clear();
                currentPlayer.segmentBuffer.setEnabled(false);
                console.log('Segment buffer cleared and disabled');
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
