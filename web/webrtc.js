const play_webrtc = (id, dom) => {
  const pc = new RTCPeerConnection({
    iceServers: [{
      urls: ["stun:stun.l.google.com:19302"]
    }]
  });

  pc.onnegotiationneeded = async () => {
    let offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    const ret = await fetch('/stream/webrtc?id=' + id, {
      method: 'POST',
      body: btoa(pc.localDescription.sdp)
    })
    let sdp = await ret.text()
    pc.setRemoteDescription(new RTCSessionDescription({
      type: 'answer',
      sdp: atob(sdp)
    }))
  };

  pc.ontrack = e => {
    document.getElementById(dom).srcObject = e.streams[0];
  }

  pc.oniceconnectionstatechange = () => console.log(pc.iceConnectionState)

  pc.addTransceiver('video', {
    'direction': 'sendrecv'
  })
}
