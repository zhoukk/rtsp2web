document.onreadystatechange = () => {
    if (document.readyState == 'complete') {
        play_webrtc('live', 'video1')
        play_wsmp4f('live', 'video2')
    }
}
