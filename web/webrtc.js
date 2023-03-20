const play_webrtc = async (id, dom) => {
  const pc = new RTCPeerConnection();
  pc.ontrack = e => {
    document.getElementById(dom).srcObject = e.streams[0];
  }
  pc.addTransceiver('video', {
    'direction': 'recvonly'
  })
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
}
