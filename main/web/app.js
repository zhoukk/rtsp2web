document.onreadystatechange = () => {
    if (document.readyState == 'complete') {
        play_webrtc('live', 'video1')
        play_wsmp4f('live', 'video2')
        play_httpflv('live', 'video3')
        play_wsflv('live', 'video4')
    }
}
