/// <reference path='./both.js' />

let currentPlayer = null;

function initPlayer() {
    const videoElement = document.querySelector('#videoElement');
    const overlay = document.querySelector('#videoOverlay');
    
    // Set up overlay click handler - this is critical for unmuting the video player
    // The video starts muted and this is how users unmute it
    overlay.onclick = () => {
        overlay.style.display = 'none';
        videoElement.muted = false;
        console.log('Video unmuted via overlay click');
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
    
    console.warn('No supported video player available');
}

function isIOSOrSafari() {
    const userAgent = navigator.userAgent;
    
    // Check for iOS devices
    const isIOS = /iPad|iPhone|iPod/.test(userAgent);
    
    // Check for Safari (but not Chrome or other WebKit browsers)
    const isSafari = /^((?!chrome|android).)*safari/i.test(userAgent);
    
    // Check for macOS Safari specifically
    const isMacSafari = isSafari && /Macintosh/.test(userAgent);
    
    return isIOS || isMacSafari;
}

function tryHLSPlayer(videoElement) {
    // Check for native HLS support (iOS Safari)
    if (videoElement.canPlayType('application/vnd.apple.mpegurl')) {
        console.log('Using native HLS support');
        videoElement.src = '/live.m3u8';
        videoElement.load();
        videoElement.play().catch(e => console.warn('HLS native play failed:', e));
        currentPlayer = { type: 'native-hls' };
        return true;
    }
    
    // Check for HLS.js support (only if HLS.js is loaded and supported)
    if (window.Hls && Hls.isSupported()) {
        console.log('Using HLS.js library');
        const hls = new Hls({
            enableWorker: false,
            lowLatencyMode: true,
            backBufferLength: 90
        });
        
        hls.loadSource('/live.m3u8');
        hls.attachMedia(videoElement);
        
        hls.on(Hls.Events.MANIFEST_LOADED, () => {
            videoElement.play().catch(e => console.warn('HLS play failed:', e));
        });
        
        // Comprehensive error handling according to HLS.js documentation
        hls.on(Hls.Events.ERROR, (event, data) => {
            console.warn('HLS error:', data);
            
            if (data.fatal) {
                switch (data.type) {
                    case Hls.ErrorTypes.NETWORK_ERROR:
                        // Try to recover network error
                        console.log('Fatal network error encountered, trying to recover');
                        hls.startLoad();
                        break;
                    case Hls.ErrorTypes.MEDIA_ERROR:
                        // Try to recover media error
                        console.log('Fatal media error encountered, trying to recover');
                        hls.recoverMediaError();
                        break;
                    default:
                        // Cannot recover - fallback to MPEG-TS
                        console.log('Cannot recover from fatal error, falling back to MPEG-TS');
                        destroyCurrentPlayer();
                        tryMpegTSPlayer(videoElement);
                        break;
                }
            } else {
                // Non-fatal errors
                switch (data.type) {
                    case Hls.ErrorTypes.NETWORK_ERROR:
                        console.log('Network error - no action needed');
                        break;
                    case Hls.ErrorTypes.MEDIA_ERROR:
                        console.log('Media error - no action needed');
                        break;
                    default:
                        console.log('Other error - no action needed');
                        break;
                }
            }
        });
        
        // Additional event handlers for better monitoring
        hls.on(Hls.Events.MEDIA_ATTACHED, () => {
            console.log('HLS media attached');
        });
        
        hls.on(Hls.Events.MEDIA_DETACHED, () => {
            console.log('HLS media detached');
        });
        
        currentPlayer = { type: 'hls.js', instance: hls };
        return true;
    }
    
    console.log('HLS not supported or available');
    return false;
}

function tryMpegTSPlayer(videoElement) {
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
}

function destroyCurrentPlayer() {
    if (!currentPlayer) return;
    
    const videoElement = document.querySelector('#videoElement');
    
    if (currentPlayer.type === 'hls.js' && currentPlayer.instance) {
        // Properly destroy HLS.js instance
        currentPlayer.instance.destroy();
        console.log('HLS.js player destroyed');
    } else if (currentPlayer.type === 'mpegts' && currentPlayer.instance) {
        currentPlayer.instance.destroy();
        console.log('MPEG-TS player destroyed');
    } else if (currentPlayer.type === 'native-hls') {
        videoElement.src = '';
        videoElement.load();
        console.log('Native HLS player destroyed');
    }
    
    currentPlayer = null;
}

window.addEventListener('load', initPlayer);
