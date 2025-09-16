/// <reference path='./both.js' />


function initPlayer() {
    if (!mpegts.isSupported()) {
        console.warn('mpegts not supported');
        return;
    }

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

    let overlay = document.querySelector('#videoOverlay');
    overlay.onclick = () => {
        overlay.style.display = 'none';
        videoElement.muted = false;
    };
}

window.addEventListener('load', initPlayer);
