/// <reference path='./both.js' />

// Device detection utilities - prioritizing User Agent string
function isIOS() {
    const userAgent = navigator.userAgent;
    
    // Primary detection via User Agent string (prioritized as requested)
    if (/iPad|iPhone|iPod/.test(userAgent) && !window.MSStream) {
        return true;
    }
    
    // Additional iOS detection patterns
    if (/Macintosh.*Safari/.test(userAgent) && /Version\//.test(userAgent)) {
        // macOS Safari - also supports native HLS
        return true;
    }
    
    return false;
}

function isMobile() {
    const userAgent = navigator.userAgent;
    
    // Primary detection via User Agent string (prioritized as requested)
    return /Android|webOS|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini|Mobile|Tablet/i.test(userAgent);
}

function isAndroid() {
    const userAgent = navigator.userAgent;
    
    // Primary detection via User Agent string (prioritized as requested)
    return /Android/i.test(userAgent);
}

function supportsHLS() {
    // First check User Agent for known HLS support
    const userAgent = navigator.userAgent;
    if (/iPad|iPhone|iPod|Macintosh.*Safari/.test(userAgent)) {
        return true; // iOS and macOS Safari have native HLS support
    }
    
    // Fallback to feature detection only if User Agent doesn't give clear answer
    try {
        var video = document.createElement('video');
        return video.canPlayType('application/vnd.apple.mpegurl') !== '';
    } catch (e) {
        return false;
    }
}

function shouldUseHLS() {
    // Check URL parameters first (explicit override)
    const urlParams = new URLSearchParams(window.location.search);
    if (urlParams.get('format') === 'hls') {
        return true;
    }
    
    // Force HLS for iOS devices (prioritizing User Agent detection)
    if (isIOS()) {
        console.log('iOS detected via User Agent, using HLS');
        return true;
    }
    
    // For Android devices, also prefer HLS if supported
    if (isAndroid() && typeof Hls !== 'undefined' && Hls.isSupported()) {
        console.log('Android detected with HLS.js support, using HLS');
        return true;
    }
    
    // Default to FLV for desktop for better performance
    return false;
}

function initPlayer() {
    console.log('initPlayer: User Agent:', navigator.userAgent);
    console.log('initPlayer: isIOS():', isIOS());
    console.log('initPlayer: isAndroid():', isAndroid());
    console.log('initPlayer: isMobile():', isMobile());
    console.log('initPlayer: supportsHLS():', supportsHLS());
    
    const useHLS = shouldUseHLS();
    console.log('initPlayer: shouldUseHLS():', useHLS);
    
    if (useHLS) {
        initHLSPlayer();
    } else {
        initMPEGTSPlayer();
    }
}

function initHLSPlayer() {
    console.log('Initializing HLS player');
    
    let videoElement = document.querySelector('#videoElement');
    const hlsSource = '/live?format=hls';
    
    // Check for native HLS support (iOS Safari)
    if (supportsHLS()) {
        console.log('Using native HLS support');
        videoElement.src = hlsSource;
        videoElement.addEventListener('loadedmetadata', function() {
            videoElement.play().catch(e => {
                console.warn('Autoplay failed:', e);
            });
        });
        
        // Add error handling for native HLS
        videoElement.addEventListener('error', function(e) {
            console.error('Native HLS error:', e);
            // Fallback to hls.js or MPEG-TS
            if (typeof Hls !== 'undefined' && Hls.isSupported()) {
                console.log('Falling back to hls.js');
                initHLSWithLibrary(videoElement, hlsSource);
            } else {
                console.log('Falling back to MPEG-TS');
                initMPEGTSPlayer();
            }
        });
    } 
    // Use hls.js for browsers without native HLS support
    else if (typeof Hls !== 'undefined' && Hls.isSupported()) {
        console.log('Using hls.js');
        initHLSWithLibrary(videoElement, hlsSource);
    } else {
        console.warn('HLS not supported, falling back to MPEG-TS');
        initMPEGTSPlayer();
    }
    
    setupVideoOverlay();
}

function initHLSWithLibrary(videoElement, hlsSource) {
    console.log('Initializing HLS.js with source:', hlsSource);
    
    // Advanced hls.js configuration utilizing full feature set
    const hlsConfig = {
        // Core settings
        debug: false,
        enableWorker: true,
        lowLatencyMode: true,
        
        // Buffer management for optimal performance
        backBufferLength: 90,               // Keep 90s of back buffer
        liveBackBufferLength: 60,           // Keep 60s for live streams
        maxBufferLength: 30,                // Maximum forward buffer
        maxMaxBufferLength: 600,            // Absolute maximum buffer
        maxBufferSize: 60 * 1000 * 1000,    // 60MB max buffer size
        maxBufferHole: 0.5,                 // Max gap in buffer
        
        // Live streaming optimizations
        liveSyncDurationCount: 3,           // Segments to keep from live edge
        liveMaxLatencyDurationCount: 10,    // Max latency in segments
        maxLiveSyncPlaybackRate: 1.5,       // Max catchup playback rate
        liveDurationInfinity: true,         // Handle infinite live streams
        
        // Network and loading
        manifestLoadingTimeOut: 10000,      // 10s timeout for manifest
        manifestLoadingMaxRetry: 4,         // Retry manifest 4 times
        manifestLoadingRetryDelay: 500,     // Start with 500ms delay
        levelLoadingTimeOut: 10000,         // 10s timeout for segments
        levelLoadingMaxRetry: 4,            // Retry segments 4 times
        levelLoadingRetryDelay: 500,        // Start with 500ms delay
        fragLoadingTimeOut: 20000,          // 20s timeout for fragments
        fragLoadingMaxRetry: 6,             // Retry fragments 6 times
        fragLoadingRetryDelay: 1000,        // Start with 1s delay
        
        // Quality and adaptation
        startLevel: -1,                     // Auto start level
        capLevelToPlayerSize: true,         // Cap quality to player size
        capLevelOnFPSDrop: true,            // Drop quality on FPS drop
        
        // Advanced features
        enableSoftwareAES: true,            // Software AES decryption
        enableCEA708Captions: true,         // Support for captions
        enableWebVTT: true,                 // WebVTT subtitle support
        enableIMSC1: true,                  // IMSC1 subtitle support
        
        // Error recovery
        nudgeOffset: 0.1,                   // Nudge offset for recovery
        nudgeMaxRetry: 3,                   // Max nudge retries
        maxSeekHole: 2,                     // Max seek hole to jump
        
        // Watchdog timers
        highBufferWatchdogPeriod: 2,        // Buffer watchdog period
        stallReported: false,               // Track stall reporting
        
        // Advanced buffer management
        appendErrorMaxRetry: 3,             // Retry append errors
        loader: undefined,                  // Use default loader
        fLoader: undefined,                 // Use default fragment loader
        pLoader: undefined,                 // Use default playlist loader
        
        // Progressive enhancement
        progressive: false,                 // Disable progressive download
        lowLatencyMode: true,               // Enable low latency features
        
        // Bandwidth estimation
        abrEwmaFastLive: 3.0,              // Fast EWMA for live
        abrEwmaSlowLive: 9.0,              // Slow EWMA for live
        abrEwmaFastVoD: 3.0,               // Fast EWMA for VoD
        abrEwmaSlowVoD: 9.0,               // Slow EWMA for VoD
        abrEwmaDefaultEstimate: 500000,     // Default bandwidth estimate (500kbps)
        abrBandWidthFactor: 0.95,          // Bandwidth safety factor
        abrBandWidthUpFactor: 0.7,         // Factor for switching up
        abrMaxWithRealBitrate: false,       // Use measured bitrate
        maxStarvationDelay: 4,              // Max starvation delay
        maxLoadingDelay: 4,                 // Max loading delay
        
        // Additional iOS optimizations
        ...(isIOS() && {
            liveSyncDurationCount: 2,       // Shorter sync for iOS
            liveMaxLatencyDurationCount: 6, // Lower latency for iOS
            maxBufferLength: 20,            // Smaller buffer for mobile
            backBufferLength: 30,           // Smaller back buffer
        })
    };

    var hls = new Hls(hlsConfig);
    
    // Store reference for cleanup
    window.hlsPlayer = hls;
    
    // Load and attach media
    console.log('Loading HLS source:', hlsSource);
    hls.loadSource(hlsSource);
    console.log('Attaching HLS to video element');
    hls.attachMedia(videoElement);
    
    // Event listeners for comprehensive error handling and monitoring
    
    // Manifest events
    hls.on(Hls.Events.MANIFEST_LOADING, function(event, data) {
        console.log('Loading HLS manifest from:', data.url);
    });
    
    hls.on(Hls.Events.MANIFEST_LOADED, function(event, data) {
        console.log('HLS manifest loaded, levels:', data.levels.length);
        logAvailableQualities(data.levels);
    });
    
    hls.on(Hls.Events.MANIFEST_PARSED, function(event, data) {
        console.log('HLS manifest parsed, starting playback');
        console.log('Available levels:', data.levels.length);
        console.log('Audio tracks:', data.audioTracks?.length || 0);
        console.log('Subtitle tracks:', data.subtitleTracks?.length || 0);
        
        // Auto-play with better error handling
        videoElement.play().catch(e => {
            console.warn('Autoplay failed:', e);
            // Show click-to-play overlay if autoplay fails
            showPlayButton();
        });
    });
    
    // Level (quality) events
    hls.on(Hls.Events.LEVEL_SWITCHING, function(event, data) {
        console.log('Switching to level:', data.level);
    });
    
    hls.on(Hls.Events.LEVEL_SWITCHED, function(event, data) {
        console.log('Switched to level:', data.level);
        updateQualityIndicator(data.level);
    });
    
    hls.on(Hls.Events.LEVEL_LOADED, function(event, data) {
        console.debug('Level loaded:', data.level, 'live:', data.details.live);
    });
    
    // Fragment events for monitoring
    hls.on(Hls.Events.FRAG_LOADING, function(event, data) {
        console.debug('Loading fragment:', data.frag.sn);
    });
    
    hls.on(Hls.Events.FRAG_LOADED, function(event, data) {
        console.debug('Fragment loaded:', data.frag.sn, 'duration:', data.frag.duration);
    });
    
    hls.on(Hls.Events.FRAG_BUFFERED, function(event, data) {
        console.debug('Fragment buffered:', data.frag.sn);
        updateBufferIndicator();
    });
    
    // Error handling with recovery strategies
    hls.on(Hls.Events.ERROR, function(event, data) {
        console.error('HLS error:', data);
        
        if (data.fatal) {
            handleFatalError(hls, data, videoElement);
        } else {
            handleNonFatalError(hls, data);
        }
    });
    
    // Buffer events
    hls.on(Hls.Events.BUFFER_APPENDING, function(event, data) {
        console.debug('Appending buffer, type:', data.type);
    });
    
    hls.on(Hls.Events.BUFFER_APPENDED, function(event, data) {
        console.debug('Buffer appended, type:', data.type);
    });
    
    hls.on(Hls.Events.BUFFER_EOS, function(event, data) {
        console.log('End of stream reached');
    });
    
    hls.on(Hls.Events.BUFFER_FLUSHING, function(event, data) {
        console.debug('Flushing buffer');
    });
    
    // Stats and monitoring
    hls.on(Hls.Events.FPS_DROP, function(event, data) {
        console.warn('FPS drop detected:', data);
    });
    
    hls.on(Hls.Events.FPS_DROP_LEVEL_CAPPING, function(event, data) {
        console.warn('Quality capped due to FPS drop:', data);
    });
    
    // Subtitle events
    hls.on(Hls.Events.SUBTITLE_TRACKS_UPDATED, function(event, data) {
        console.log('Subtitle tracks updated:', data.subtitleTracks);
        setupSubtitleTracks(data.subtitleTracks);
    });
    
    hls.on(Hls.Events.SUBTITLE_TRACK_SWITCH, function(event, data) {
        console.log('Subtitle track switched:', data);
    });
    
    // Audio track events
    hls.on(Hls.Events.AUDIO_TRACKS_UPDATED, function(event, data) {
        console.log('Audio tracks updated:', data.audioTracks);
        setupAudioTracks(data.audioTracks);
    });
    
    hls.on(Hls.Events.AUDIO_TRACK_SWITCHING, function(event, data) {
        console.log('Audio track switching:', data);
    });
    
    // Performance monitoring
    if (window.performance && window.performance.mark) {
        hls.on(Hls.Events.MANIFEST_PARSED, () => {
            window.performance.mark('hls-manifest-parsed');
        });
        
        hls.on(Hls.Events.FRAG_LOADED, () => {
            window.performance.mark('hls-first-fragment');
        });
    }
    
    // Add quality control interface
    addQualityControls(hls);
    
    // Add buffer monitoring
    addBufferMonitoring(hls, videoElement);
}

// Enhanced error handling functions
function handleFatalError(hls, data, videoElement) {
    switch(data.type) {
        case Hls.ErrorTypes.NETWORK_ERROR:
            console.log('Fatal network error, attempting recovery...');
            hls.startLoad();
            break;
            
        case Hls.ErrorTypes.MEDIA_ERROR:
            console.log('Fatal media error, attempting recovery...');
            hls.recoverMediaError();
            break;
            
        case Hls.ErrorTypes.MUX_ERROR:
            console.log('Fatal mux error, attempting media recovery...');
            hls.recoverMediaError();
            break;
            
        case Hls.ErrorTypes.OTHER_ERROR:
            console.error('Fatal other error, cannot recover:', data);
            // Fallback to MPEG-TS
            setTimeout(() => {
                console.log('Falling back to MPEG-TS player');
                hls.destroy();
                initMPEGTSPlayer();
            }, 1000);
            break;
            
        default:
            console.error('Unknown fatal error:', data);
            hls.destroy();
            initMPEGTSPlayer();
            break;
    }
}

function handleNonFatalError(hls, data) {
    console.warn('Non-fatal HLS error:', data.type, data.details);
    
    // Log specific error details for debugging
    switch(data.details) {
        case Hls.ErrorDetails.MANIFEST_LOAD_ERROR:
        case Hls.ErrorDetails.MANIFEST_LOAD_TIMEOUT:
            console.warn('Manifest loading issue - possibly no active stream');
            // Check if it's a 503 Service Unavailable (no stream active)
            if (data.response && data.response.code === 503) {
                console.info('No active stream available. Waiting for stream to start...');
                showNoStreamMessage();
            }
            break;
            
        case Hls.ErrorDetails.LEVEL_LOAD_ERROR:
        case Hls.ErrorDetails.LEVEL_LOAD_TIMEOUT:
            console.warn('Level loading issue');
            break;
            
        case Hls.ErrorDetails.FRAG_LOAD_ERROR:
        case Hls.ErrorDetails.FRAG_LOAD_TIMEOUT:
            console.warn('Fragment loading issue');
            break;
            
        case Hls.ErrorDetails.BUFFER_APPEND_ERROR:
            console.warn('Buffer append issue');
            break;
            
        default:
            console.warn('Other non-fatal error:', data.details);
    }
}

// Utility functions for enhanced features
function logAvailableQualities(levels) {
    console.log('Available quality levels:');
    levels.forEach((level, index) => {
        console.log(`  ${index}: ${level.width}x${level.height} @ ${Math.round(level.bitrate/1000)}kbps`);
    });
}

function updateQualityIndicator(levelIndex) {
    // Update UI to show current quality
    const indicator = document.querySelector('#qualityIndicator');
    if (indicator && window.hlsPlayer) {
        const levels = window.hlsPlayer.levels;
        if (levels && levels[levelIndex]) {
            const level = levels[levelIndex];
            indicator.textContent = `${level.height}p`;
        }
    }
}

function updateBufferIndicator() {
    const videoElement = document.querySelector('#videoElement');
    if (videoElement && videoElement.buffered.length > 0) {
        const buffered = videoElement.buffered.end(videoElement.buffered.length - 1);
        const current = videoElement.currentTime;
        const bufferAhead = buffered - current;
        
        console.debug(`Buffer: ${bufferAhead.toFixed(1)}s ahead`);
        
        // Update buffer indicator in UI
        const indicator = document.querySelector('#bufferIndicator');
        if (indicator) {
            indicator.textContent = `Buffer: ${bufferAhead.toFixed(1)}s`;
        }
    }
}

function showPlayButton() {
    // Show a play button overlay for manual playback initiation
    const overlay = document.querySelector('#videoOverlay');
    if (overlay) {
        overlay.style.display = 'block';
        overlay.innerHTML = '<div class="play-button">▶ Click to Play</div>';
    }
}

function showNoStreamMessage() {
    // Show a message when no stream is active
    const overlay = document.querySelector('#videoOverlay');
    if (overlay) {
        overlay.style.display = 'block';
        overlay.innerHTML = '<div class="no-stream-message">📺 No active stream<br><small>Waiting for stream to start...</small></div>';
        
        // Add some basic styling
        const style = overlay.style;
        style.display = 'flex';
        style.alignItems = 'center';
        style.justifyContent = 'center';
        style.backgroundColor = 'rgba(0, 0, 0, 0.8)';
        style.color = 'white';
        style.fontSize = '18px';
        style.textAlign = 'center';
    }
}

function addQualityControls(hls) {
    // Add quality selection controls
    const videoWrapper = document.querySelector('#videoWrapper');
    if (videoWrapper && hls.levels.length > 1) {
        const qualitySelect = document.createElement('select');
        qualitySelect.id = 'qualitySelect';
        qualitySelect.style.position = 'absolute';
        qualitySelect.style.top = '10px';
        qualitySelect.style.right = '10px';
        qualitySelect.style.zIndex = '1000';
        
        // Add auto option
        const autoOption = document.createElement('option');
        autoOption.value = '-1';
        autoOption.textContent = 'Auto';
        qualitySelect.appendChild(autoOption);
        
        // Add quality options
        hls.levels.forEach((level, index) => {
            const option = document.createElement('option');
            option.value = index;
            option.textContent = `${level.height}p (${Math.round(level.bitrate/1000)}k)`;
            qualitySelect.appendChild(option);
        });
        
        qualitySelect.addEventListener('change', (e) => {
            const selectedLevel = parseInt(e.target.value);
            hls.currentLevel = selectedLevel;
            console.log('Manual quality change to:', selectedLevel === -1 ? 'auto' : selectedLevel);
        });
        
        videoWrapper.appendChild(qualitySelect);
    }
}

function addBufferMonitoring(hls, videoElement) {
    // Add buffer level monitoring
    setInterval(() => {
        if (videoElement && window.hlsPlayer) {
            const bufferInfo = hls.bufferLength;
            if (bufferInfo < 5) {
                console.warn('Low buffer warning:', bufferInfo);
            }
        }
    }, 5000);
}

function setupSubtitleTracks(subtitleTracks) {
    // Setup subtitle track selection
    if (subtitleTracks && subtitleTracks.length > 0) {
        console.log('Setting up subtitle tracks:', subtitleTracks);
        // Implementation for subtitle UI would go here
    }
}

function setupAudioTracks(audioTracks) {
    // Setup audio track selection
    if (audioTracks && audioTracks.length > 1) {
        console.log('Setting up audio tracks:', audioTracks);
        // Implementation for audio track UI would go here
    }
}

function initMPEGTSPlayer() {
    if (!mpegts.isSupported()) {
        console.warn('mpegts not supported');
        return;
    }
    
    console.log('Initializing MPEG-TS player');

    let videoElement = document.querySelector('#videoElement');
    let flvPlayer = mpegts.createPlayer({
        type: 'flv',
        url: '/live'
    }, {
        isLive: true,
        liveBufferLatencyChasing: true,
        autoCleanupSourceBuffer: true,
    });
    
    flvPlayer.attachMediaElement(videoElement);
    flvPlayer.load();
    flvPlayer.play();
    
    // Store player instance for cleanup
    window.flvPlayer = flvPlayer;
    
    setupVideoOverlay();
}

function setupVideoOverlay() {
    let overlay = document.querySelector('#videoOverlay');
    if (overlay) {
        overlay.onclick = () => {
            overlay.style.display = 'none';
            let videoElement = document.querySelector('#videoElement');
            if (videoElement) {
                videoElement.muted = false;
                videoElement.play().catch(e => {
                    console.warn('Manual play failed:', e);
                });
            }
        };
    }
}

// Cleanup function for page unload
function cleanup() {
    if (window.hlsPlayer) {
        console.log('Cleaning up HLS player');
        window.hlsPlayer.destroy();
        window.hlsPlayer = null;
    }
    if (window.flvPlayer) {
        console.log('Cleaning up FLV player');
        window.flvPlayer.destroy();
        window.flvPlayer = null;
    }
}

// Enhanced statistics collection
function collectPlaybackStats() {
    if (window.hlsPlayer) {
        const stats = {
            currentLevel: window.hlsPlayer.currentLevel,
            autoLevelEnabled: window.hlsPlayer.autoLevelEnabled,
            levels: window.hlsPlayer.levels?.length || 0,
            loadLevel: window.hlsPlayer.loadLevel,
            nextLevel: window.hlsPlayer.nextLevel,
            bufferLength: window.hlsPlayer.bufferLength
        };
        console.log('HLS Stats:', stats);
        return stats;
    }
    return null;
}

// Expose stats collection for debugging
window.getHLSStats = collectPlaybackStats;

window.addEventListener('load', initPlayer);
window.addEventListener('beforeunload', cleanup);

// Add periodic stats logging in debug mode
if (window.location.search.includes('debug=1')) {
    setInterval(() => {
        collectPlaybackStats();
    }, 10000);
}
