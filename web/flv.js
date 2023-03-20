const play_httpflv =
    async (id, dom) => {
  var videoElement = document.getElementById(dom);
  var flvPlayer = flvjs.createPlayer(
      {type: 'flv', isLive: true, url: '/stream/httpflv?id=' + id});
  flvPlayer.attachMediaElement(videoElement);
  flvPlayer.load();
  flvPlayer.play();
}

const play_wsflv = async (id, dom) => {
  var videoElement = document.getElementById(dom);
  var flvPlayer = flvjs.createPlayer({
    type: 'flv',
    isLive: true,
    url: 'ws://' + window.location.host + '/stream/wsflv?id=' + id
  });
  flvPlayer.attachMediaElement(videoElement);
  flvPlayer.load();
  flvPlayer.play();
}
