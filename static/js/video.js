/// <reference path='./both.js' />

let currentPlayer = null;

function initPlayer() {
    const videoElement = document.querySelector('#videoElement');
    const overlay = document.querySelector('#videoOverlay');
    
    // Set up overlay click handler for unmuting
    overlay.onclick = () => {
        overlay.style.display = 'none';
        videoElement.muted = false;
        console.log('Video unmuted via overlay click');
    };
    
    // Simplified player selection: try HLS first on iOS/Safari, MPEG-TS elsewhere
    if (isIOSDevice() || isSafariBrowser()) {
        if (tryHLSPlayer(videoElement) || tryMpegTSPlayer(videoElement)) {
            return;
        }
    } else {
        if (tryMpegTSPlayer(videoElement) || tryHLSPlayer(videoElement)) {
            return;
        }
    }
    
    console.warn('No supported video player available');
}

function isIOSDevice() {
    return /iPad|iPhone|iPod/.test(navigator.userAgent);
}

function isSafariBrowser() {
    return /^((?!chrome|android).)*safari/i.test(navigator.userAgent);
}

function tryHLSPlayer(videoElement) {
    // Native HLS support (iOS Safari)
    if (videoElement.canPlayType('application/vnd.apple.mpegurl')) {
        console.log('Using native HLS support');
        videoElement.src = '/live.m3u8';
        videoElement.load();
        videoElement.play().catch(e => console.warn('Native HLS play failed:', e));
        currentPlayer = { type: 'native-hls' };
        return true;
    }
    
    // HLS.js support
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
        
        // Simplified error handling
        hls.on(Hls.Events.ERROR, (event, data) => {
            if (data.fatal) {
                console.log('Fatal HLS error, falling back to MPEG-TS:', data);
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
        console.warn('MPEG-TS not supported');
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
    
    if (currentPlayer.instance && currentPlayer.instance.destroy) {
        currentPlayer.instance.destroy();
        console.log(`${currentPlayer.type} player destroyed`);
    } else if (currentPlayer.type === 'native-hls') {
        videoElement.src = '';
        videoElement.load();
        console.log('Native HLS player destroyed');
    }
    
    currentPlayer = null;
}

window.addEventListener('load', initPlayer);
