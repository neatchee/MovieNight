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
                enableWorker: false,
                lowLatencyMode: false,  // Disable low latency for better buffering
                backBufferLength: 60,   // Reduce back buffer to save memory
                maxBufferLength: 40,    // Increase max buffer for smoother playback (adjusted for 4s segments)
                maxMaxBufferLength: 80, // Increase maximum buffer size (adjusted for 4s segments)
                maxBufferSize: 40 * 1000 * 1000, // 40MB buffer size
                maxBufferHole: 0.1,     // Allow small buffer holes
                liveSyncDurationCount: 4, // Number of segments for live sync (adjusted for 4s segments)
                liveMaxLatencyDurationCount: 8, // Maximum latency segments (adjusted for 4s segments)
                manifestLoadingTimeOut: 10000, // 10 second manifest timeout
                manifestLoadingMaxRetry: 6, // Retry manifest loading
                manifestLoadingRetryDelay: 500, // Delay between retries
                fragLoadingTimeOut: 15000, // 15 second fragment timeout (reduced for 4s segments)
                fragLoadingMaxRetry: 6, // Retry fragment loading
                fragLoadingRetryDelay: 1000, // Delay between retries
                startLevel: -1, // Auto select start level
                abrEwmaFastLive: 3, // Fast switching for live streams
                abrEwmaSlowLive: 9, // Slow switching for live streams
                debug: false
            });
            
            hls.loadSource('/live.m3u8');
            hls.attachMedia(videoElement);
            
            // Wait for first segment to be loaded before attempting to play
            let firstSegmentLoaded = false;
            
            hls.on(Hls.Events.MANIFEST_LOADED, () => {
                console.log('HLS manifest loaded successfully');
                // Don't auto-play yet, wait for first fragment
            });
            
            hls.on(Hls.Events.FRAG_LOADED, () => {
                if (!firstSegmentLoaded) {
                    firstSegmentLoaded = true;
                    console.log('HLS first segment loaded, starting playback');
                    // Wait a bit more to ensure buffer has some data
                    setTimeout(() => {
                        videoElement.play().catch(e => console.warn('HLS play failed:', e));
                    }, 1000);
                }
            });
            
            hls.on(Hls.Events.MEDIA_ATTACHED, () => {
                console.log('HLS media attached');
            });
            
            hls.on(Hls.Events.MEDIA_DETACHED, () => {
                console.log('HLS media detached');
            });
            
            // Add buffer monitoring for better performance
            hls.on(Hls.Events.BUFFER_CREATED, () => {
                console.log('HLS buffer created');
            });
            
            hls.on(Hls.Events.BUFFER_APPENDED, () => {
                // Buffer is growing, good for performance
            });
            
            hls.on(Hls.Events.BUFFER_EOS, () => {
                console.log('HLS end of stream reached');
            });
            
            // Comprehensive error handling following HLS.js documentation
            hls.on(Hls.Events.ERROR, (event, data) => {
                // Handle specific non-fatal errors more gracefully
                if (!data.fatal) {
                    // Common initial buffering issues that can be ignored
                    if (data.details === 'bufferStalledError' && !firstSegmentLoaded) {
                        console.log('Initial buffer stall, waiting for first segment...');
                        return; // Don't log this as an error
                    }
                    if (data.details === 'bufferNudgedOnStall') {
                        console.log('Buffer nudged on stall - normal behavior');
                        return; // Don't log this as an error
                    }
                    // Log other non-fatal errors for debugging
                    console.log('Non-fatal HLS error:', data.details);
                    return;
                }
                
                // Handle fatal errors
                console.warn('Fatal HLS error:', data);
                
                switch (data.type) {
                    case Hls.ErrorTypes.NETWORK_ERROR:
                        console.log('Fatal network error, attempting to recover...');
                        hls.startLoad();
                        break;
                    case Hls.ErrorTypes.MEDIA_ERROR:
                        console.log('Fatal media error, attempting to recover...');
                        hls.recoverMediaError();
                        break;
                    default:
                        console.log('Fatal error, cannot recover - falling back to MPEG-TS');
                        destroyCurrentPlayer();
                        tryMpegTSPlayer(videoElement);
                        break;
                }
            });
            
            currentPlayer = { type: 'hls.js', instance: hls };
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
            autoCleanupSourceBuffer: true,
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
            if (currentPlayer.type === 'hls.js') {
                currentPlayer.instance.destroy();
            } else if (currentPlayer.type === 'mpegts') {
                currentPlayer.instance.destroy();
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
