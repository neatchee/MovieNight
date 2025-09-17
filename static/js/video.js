/// <reference path='./both.js' />

let currentPlayer = null;

function initPlayer() {
    const videoElement = document.querySelector('#videoElement');
    const overlay = document.querySelector('#videoOverlay');
    
    // Set up overlay click handler
    overlay.onclick = () => {
        overlay.style.display = 'none';
        videoElement.muted = false;
    };
    
    // Try HLS first (for iOS compatibility)
    if (tryHLSPlayer(videoElement)) {
        console.log('Using HLS player');
        return;
    }
    
    // Fallback to MPEG-TS
    if (tryMpegTSPlayer(videoElement)) {
        console.log('Using MPEG-TS player');
        return;
    }
    
    console.warn('No supported video player available');
}

function tryHLSPlayer(videoElement) {
    // Check for native HLS support (iOS Safari)
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
            lowLatencyMode: true,
            backBufferLength: 90
        });
        
        hls.loadSource('/live.m3u8');
        hls.attachMedia(videoElement);
        
        hls.on(Hls.Events.MANIFEST_LOADED, () => {
            videoElement.play().catch(e => console.warn('HLS play failed:', e));
        });
        
        hls.on(Hls.Events.ERROR, (event, data) => {
            console.warn('HLS error:', data);
            if (data.fatal) {
                // Try fallback to MPEG-TS
                destroyCurrentPlayer();
                tryMpegTSPlayer(videoElement);
            }
        });
        
        currentPlayer = { type: 'hls.js', instance: hls };
        return true;
    }
    
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
        currentPlayer.instance.destroy();
    } else if (currentPlayer.type === 'mpegts' && currentPlayer.instance) {
        currentPlayer.instance.destroy();
    } else if (currentPlayer.type === 'native-hls') {
        videoElement.src = '';
        videoElement.load();
    }
    
    currentPlayer = null;
}

window.addEventListener('load', initPlayer);
