/// <reference path='./both.js' />

// Device detection utilities
function isIOS() {
    return /iPad|iPhone|iPod/.test(navigator.userAgent) && !window.MSStream;
}

function isMobile() {
    return /Android|webOS|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini/i.test(navigator.userAgent);
}

function supportsHLS() {
    var video = document.createElement('video');
    return video.canPlayType('application/vnd.apple.mpegurl') !== '';
}

function shouldUseHLS() {
    // Force HLS for iOS devices
    if (isIOS()) {
        return true;
    }
    
    // Check URL parameters
    const urlParams = new URLSearchParams(window.location.search);
    if (urlParams.get('format') === 'hls') {
        return true;
    }
    
    // Default to FLV for desktop for better performance
    return false;
}

function initPlayer() {
    const useHLS = shouldUseHLS();
    
    if (useHLS) {
        initHLSPlayer();
    } else {
        initMPEGTSPlayer();
    }
}

function initHLSPlayer() {
    console.log('Initializing HLS player');
    
    let videoElement = document.querySelector('#videoElement');
    
    // Check for native HLS support (iOS Safari)
    if (supportsHLS()) {
        console.log('Using native HLS support');
        videoElement.src = '/hls/live/playlist.m3u8';
        videoElement.addEventListener('loadedmetadata', function() {
            videoElement.play();
        });
    } 
    // Use hls.js for browsers without native HLS support
    else if (typeof Hls !== 'undefined' && Hls.isSupported()) {
        console.log('Using hls.js');
        
        var hls = new Hls({
            debug: false,
            enableWorker: true,
            lowLatencyMode: true,
            backBufferLength: 90,
            liveBackBufferLength: 60,
            liveSyncDurationCount: 3,
            liveMaxLatencyDurationCount: 10,
            maxLiveSyncPlaybackRate: 1.5,
            liveDurationInfinity: true,
            highBufferWatchdogPeriod: 2
        });
        
        hls.loadSource('/hls/live/playlist.m3u8');
        hls.attachMedia(videoElement);
        
        hls.on(Hls.Events.MANIFEST_PARSED, function() {
            console.log('HLS manifest parsed, starting playback');
            videoElement.play().catch(e => {
                console.warn('Autoplay failed:', e);
            });
        });
        
        hls.on(Hls.Events.ERROR, function(event, data) {
            console.error('HLS error:', data);
            if (data.fatal) {
                switch(data.type) {
                    case Hls.ErrorTypes.NETWORK_ERROR:
                        console.log('Fatal network error encountered, trying to recover');
                        hls.startLoad();
                        break;
                    case Hls.ErrorTypes.MEDIA_ERROR:
                        console.log('Fatal media error encountered, trying to recover');
                        hls.recoverMediaError();
                        break;
                    default:
                        console.log('Fatal error, cannot recover');
                        hls.destroy();
                        // Fallback to MPEG-TS
                        initMPEGTSPlayer();
                        break;
                }
            }
        });
        
        // Store hls instance for cleanup
        window.hlsPlayer = hls;
    } else {
        console.warn('HLS not supported, falling back to MPEG-TS');
        initMPEGTSPlayer();
    }
    
    setupVideoOverlay();
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
            }
        };
    }
}

// Cleanup function for page unload
function cleanup() {
    if (window.hlsPlayer) {
        window.hlsPlayer.destroy();
        window.hlsPlayer = null;
    }
    if (window.flvPlayer) {
        window.flvPlayer.destroy();
        window.flvPlayer = null;
    }
}

window.addEventListener('load', initPlayer);
window.addEventListener('beforeunload', cleanup);
