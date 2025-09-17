/// <reference path='./both.js' />

let currentPlayer = null;

function initPlayer() {
    let videoElement = document.querySelector('#videoElement');
    let overlay = document.querySelector('#videoOverlay');
    
    // Clean up any existing player
    if (currentPlayer && currentPlayer.destroy) {
        currentPlayer.destroy();
        currentPlayer = null;
    }
    
    // Try HLS first (better for iOS and modern browsers)
    if (Hls && Hls.isSupported()) {
        console.log('Using HLS player');
        initHLSPlayer(videoElement);
    } else if (videoElement.canPlayType('application/vnd.apple.mpegurl')) {
        console.log('Using native HLS support');
        initNativeHLSPlayer(videoElement);
    } else if (mpegts && mpegts.isSupported()) {
        console.log('Using MPEG-TS player (fallback)');
        initMpegTSPlayer(videoElement);
    } else {
        console.error('No supported video player available');
        return;
    }
    
    // Set up overlay click handler
    overlay.onclick = () => {
        overlay.style.display = 'none';
        videoElement.muted = false;
    };
}

function initHLSPlayer(videoElement) {
    let hls = new Hls({
        enableWorker: false,
        liveBackBufferLength: 0,
        maxBufferLength: 10,
        maxMaxBufferLength: 60,
        startLevel: -1,
        autoStartLoad: true
    });
    
    hls.loadSource('/hls/live/playlist.m3u8');
    hls.attachMedia(videoElement);
    
    hls.on(Hls.Events.MANIFEST_LOADED, function() {
        console.log('HLS manifest loaded');
        videoElement.play();
    });
    
    hls.on(Hls.Events.ERROR, function(event, data) {
        console.error('HLS error:', data);
        if (data.fatal) {
            console.log('Fatal HLS error, falling back to MPEG-TS');
            hls.destroy();
            if (mpegts && mpegts.isSupported()) {
                initMpegTSPlayer(videoElement);
            }
        }
    });
    
    currentPlayer = hls;
}

function initNativeHLSPlayer(videoElement) {
    videoElement.src = '/hls/live/playlist.m3u8';
    videoElement.addEventListener('loadedmetadata', function() {
        console.log('Native HLS loaded');
        videoElement.play();
    });
    
    videoElement.addEventListener('error', function() {
        console.error('Native HLS error, falling back to MPEG-TS');
        if (mpegts && mpegts.isSupported()) {
            initMpegTSPlayer(videoElement);
        }
    });
    
    currentPlayer = { destroy: function() { videoElement.src = ''; } };
}

function initMpegTSPlayer(videoElement) {
    if (!mpegts.isSupported()) {
        console.warn('mpegts not supported');
        return;
    }

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
    
    currentPlayer = flvPlayer;
}

window.addEventListener('load', initPlayer);
