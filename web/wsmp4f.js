const play_wsmp4f = (id, dom) => {
  let hidden;
  if (typeof document.hidden !== "undefined") {
    hidden = "hidden";
  } else if (typeof document.msHidden !== "undefined") {
    hidden = "msHidden";
  } else if (typeof document.webkitHidden !== "undefined") {
    hidden = "webkitHidden";
  }
  const videoDom = document.getElementById(dom)
  const ms = new MediaSource()
  ms.addEventListener('sourceopen', () => {
    let queue = []
    let sourceBuffer
    let streamingStarted = false

    let ws = new WebSocket("ws://" + window.location.host + "/stream/ws?id=" + id)
    ws.binaryType = "arraybuffer"
    ws.onopen = () => {
      console.log('connect')
    }
    ws.onmessage = e => {
      let data = new Uint8Array(e.data)
      if (data[0] == 9) {
        let codecs = new TextDecoder("utf-8").decode(data.slice(1))
        sourceBuffer = ms.addSourceBuffer('video/mp4; codecs="' + codecs + '"')
        sourceBuffer.mode = "segments"
        sourceBuffer.addEventListener("updateend", () => {
          if (queue.length > 0) {
            let data = queue.shift()
            sourceBuffer.appendBuffer(data)
          } else {
            streamingStarted = false
          }
        })
      } else {
        if (!streamingStarted) {
          sourceBuffer.appendBuffer(e.data)
          streamingStarted = true
        } else {
          queue.push(e.data)
        }
      }
      // fix stop when tab hidden
      if (document[hidden] && videoDom.buffered.length) {
        videoDom.currentTime = videoDom.buffered.end((videoDom.buffered.length - 1)) - 1;
      }
    }
  }, false)
  videoDom.src = URL.createObjectURL(ms)
}
