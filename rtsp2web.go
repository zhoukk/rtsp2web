package rtsp2web

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/deepch/vdk/av"
	"github.com/deepch/vdk/codec/h264parser"
	"github.com/deepch/vdk/format/mp4f"
	"github.com/deepch/vdk/format/rtspv2"
	webrtc "github.com/deepch/vdk/format/webrtcv3"
	"golang.org/x/net/websocket"
)

type Viewer struct {
	c chan av.Packet
}

type Stream struct {
	Url         string
	EnableAudio bool
	EnableDebug bool
	Running     bool
	Codecs      []av.CodecData
	Viewers     map[string]Viewer
}

type Rtsp2Web struct {
	streams map[string]Stream
	lock    sync.RWMutex
}

func (g *Rtsp2Web) hasViewer(id string) bool {
	g.lock.Lock()
	defer g.lock.Unlock()
	if s, ok := g.streams[id]; ok && len(s.Viewers) > 0 {
		return true
	}
	return false
}

func (g *Rtsp2Web) setCodec(id string, codecs []av.CodecData) {
	g.lock.Lock()
	defer g.lock.Unlock()
	if s, ok := g.streams[id]; ok {
		s.Codecs = codecs
		g.streams[id] = s
	}
}

func (g *Rtsp2Web) getCodec(id string) []av.CodecData {
	for i := 0; i < 1000; i++ {
		g.lock.RLock()
		s, ok := g.streams[id]
		g.lock.RUnlock()
		if !ok {
			break
		}
		if s.Codecs != nil && len(s.Codecs) > 0 {
			for _, codec := range s.Codecs {
				if codec.Type() == av.H264 {
					codecVideo := codec.(h264parser.CodecData)
					if codecVideo.SPS() == nil || codecVideo.PPS() == nil || len(codecVideo.SPS()) == 0 || len(codecVideo.PPS()) == 0 {
						time.Sleep(50 * time.Millisecond)
						continue
					}
				}
			}
			return s.Codecs
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func (g *Rtsp2Web) genViewerId() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	bytes := make([]byte, 16)
	for i := 0; i < 16; i++ {
		b := r.Intn(26) + 65
		bytes[i] = byte(b)
	}
	return string(bytes)
}

func (g *Rtsp2Web) addViewer(id string) (string, chan av.Packet) {
	g.lock.Lock()
	defer g.lock.Unlock()
	if s, ok := g.streams[id]; ok {
		vid := g.genViewerId()
		ch := make(chan av.Packet, 100)
		s.Viewers[vid] = Viewer{c: ch}
		g.streams[id] = s
		return vid, ch
	}
	return "", nil
}

func (g *Rtsp2Web) delViewer(id, vid string) {
	g.lock.Lock()
	defer g.lock.Unlock()
	if s, ok := g.streams[id]; ok {
		delete(s.Viewers, vid)
		g.streams[id] = s
	}
}

func (g *Rtsp2Web) castPkt(id string, pkt av.Packet) {
	g.lock.Lock()
	defer g.lock.Unlock()
	if s, ok := g.streams[id]; ok {
		for _, v := range s.Viewers {
			if len(v.c) < cap(v.c) {
				v.c <- pkt
			}
		}
	}
}

func (g *Rtsp2Web) exist(id string) bool {
	g.lock.Lock()
	defer g.lock.Unlock()
	_, ok := g.streams[id]
	return ok
}

func (g *Rtsp2Web) work(id, url string, enableAudio, enableDebug bool) error {
	RTSPClient, err := rtspv2.Dial(rtspv2.RTSPClientOptions{URL: url, DisableAudio: !enableAudio, DialTimeout: 3 * time.Second, ReadWriteTimeout: 3 * time.Second, Debug: enableDebug})
	if err != nil {
		return err
	}
	defer RTSPClient.Close()

	if !RTSPClient.WaitCodec {
		g.setCodec(id, RTSPClient.CodecData)
	}

	clientTimeout := time.NewTimer(60 * time.Second)
	viewerTimeout := time.NewTimer(20 * time.Second)

	for {
		select {
		case <-viewerTimeout.C:
			if !g.hasViewer(id) {
				return fmt.Errorf("stream %s exit while no viewer", id)
			}
		case <-clientTimeout.C:
			return fmt.Errorf("stream %s exit while no video", id)
		case signals := <-RTSPClient.Signals:
			switch signals {
			case rtspv2.SignalCodecUpdate:
				g.setCodec(id, RTSPClient.CodecData)
			case rtspv2.SignalStreamRTPStop:
				return fmt.Errorf("stream %s exit while rtsp disconnect", id)
			}
		case packetAV := <-RTSPClient.OutgoingPacketQueue:
			if packetAV.IsKeyFrame {
				clientTimeout.Reset(60 * time.Second)
			}
			g.castPkt(id, *packetAV)
		}
	}
}

func (g *Rtsp2Web) start(id string) {
	g.lock.Lock()
	defer g.lock.Unlock()
	if s, ok := g.streams[id]; ok && !s.Running {
		s.Running = true
		g.streams[id] = s
		go func(id, url string, enableAudio, enableDebug bool) {
			for {
				err := g.work(id, url, enableAudio, enableDebug)
				if err != nil {
					log.Println(err)
				}
				if !g.hasViewer(id) {
					log.Printf("stream %s exit while no viewer\n", id)
					break
				}
				time.Sleep(1 * time.Second)
			}
			g.lock.Lock()
			defer g.lock.Unlock()
			if s, ok := g.streams[id]; ok && s.Running {
				s.Running = false
				g.streams[id] = s
			}
		}(id, s.Url, s.EnableAudio, s.EnableDebug)
	}
}

func NewRtsp2Web() *Rtsp2Web {
	return &Rtsp2Web{
		streams: make(map[string]Stream),
	}
}

func (g *Rtsp2Web) WebRtcHander() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.FormValue("id")
		if !g.exist(id) {
			log.Printf("stream %s not exist\n", id)
			http.Error(w, "Stream Not Exist", http.StatusNotFound)
			return
		}

		g.start(id)

		codecs := g.getCodec(id)
		if codecs == nil {
			log.Printf("stream %s no codec\n", id)
			http.Error(w, "Stream No Codec", http.StatusInternalServerError)
			return
		}

		sdp, _ := ioutil.ReadAll(r.Body)

		muxerWebRTC := webrtc.NewMuxer(webrtc.Options{})
		answer, err := muxerWebRTC.WriteHeader(codecs, string(sdp))
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte(answer))

		go func() {
			vid, ch := g.addViewer(id)
			defer g.delViewer(id, vid)
			defer muxerWebRTC.Close()

			noVideoTimeout := time.NewTimer(60 * time.Second)
			for {
				select {
				case <-noVideoTimeout.C:
					log.Println("key frame timeout")
					return
				case pkt := <-ch:
					if pkt.IsKeyFrame {
						noVideoTimeout.Reset(60 * time.Second)
					}
					if err = muxerWebRTC.WritePacket(pkt); err != nil {
						log.Println(err)
						return
					}
				}
			}
		}()
	})
}

func (g *Rtsp2Web) WsMp4fHander() websocket.Handler {
	return websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()

		id := ws.Request().FormValue("id")
		if !g.exist(id) {
			log.Printf("stream %s not exist\n", id)
			return
		}

		g.start(id)

		codecs := g.getCodec(id)
		if codecs == nil {
			log.Printf("stream %s no codec\n", id)
			return
		}

		ws.SetWriteDeadline(time.Now().Add(5 * time.Second))

		vid, ch := g.addViewer(id)
		defer g.delViewer(id, vid)

		muxer := mp4f.NewMuxer(nil)
		err := muxer.WriteHeader(codecs)
		if err != nil {
			log.Println(err)
			return
		}
		meta, init := muxer.GetInit(codecs)
		err = websocket.Message.Send(ws, append([]byte{9}, meta...))
		if err != nil {
			log.Println(err)
			return
		}
		err = websocket.Message.Send(ws, init)
		if err != nil {
			log.Println(err)
			return
		}

		go func() {
			for {
				var message string
				err := websocket.Message.Receive(ws, &message)
				if err != nil {
					ws.Close()
					return
				}
			}
		}()

		timeLine := make(map[int8]time.Duration)

		noVideoTimeout := time.NewTimer(60 * time.Second)
		for {
			select {
			case <-noVideoTimeout.C:
				log.Println("key frame timeout")
				return
			case pkt := <-ch:
				if pkt.IsKeyFrame {
					noVideoTimeout.Reset(60 * time.Second)
				}
				timeLine[pkt.Idx] += pkt.Duration
				pkt.Time = timeLine[pkt.Idx]
				if ready, buf, _ := muxer.WritePacket(pkt, false); ready {
					err = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
					if err != nil {
						log.Println(err)
						return
					}
					err := websocket.Message.Send(ws, buf)
					if err != nil {
						log.Println(err)
						return
					}
				}
			}
		}
	})
}

func (g *Rtsp2Web) AddStream(id, url string, enable_audio bool) error {
	g.lock.Lock()
	defer g.lock.Unlock()
	if s, ok := g.streams[id]; ok && s.Running {
		return errors.New("Stream already running")
	} else {
		g.streams[id] = Stream{
			Url:         url,
			EnableAudio: false,
			EnableDebug: false,
			Codecs:      make([]av.CodecData, 0),
			Viewers:     make(map[string]Viewer),
		}
	}
	return nil
}
