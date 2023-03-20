package rtsp2web

import (
	"bytes"
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
	"github.com/deepch/vdk/format/flv"
	"github.com/deepch/vdk/format/mp4f"
	"github.com/deepch/vdk/format/rtspv2"
	webrtc "github.com/deepch/vdk/format/webrtcv3"
	"golang.org/x/net/websocket"
)

type Config struct {
	WebRtc struct {
		ICEServers    []string
		ICEUsername   string
		ICECredential string
		ICECandidates []string
		WebRTCPortMin uint16
		WebRTCPortMax uint16
	}
}

type Viewer struct {
	c chan av.Packet
}

type Stream struct {
	Url         string
	EnableAudio bool
	EnableDebug bool
	Running     bool
	ExitC       chan struct{}
	Codecs      []av.CodecData
	Viewers     map[string]Viewer
}

type Rtsp2Web struct {
	config  Config
	streams map[string]Stream
	lock    sync.RWMutex
}

var ErrRtspDisconnect = errors.New("rtsp disconnected")

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

func (g *Rtsp2Web) work(id string, s *Stream) error {
	RTSPClient, err := rtspv2.Dial(rtspv2.RTSPClientOptions{URL: s.Url, DisableAudio: !s.EnableAudio, DialTimeout: 3 * time.Second, ReadWriteTimeout: 3 * time.Second, Debug: s.EnableDebug})
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
		case <-s.ExitC:
			return fmt.Errorf("[%s] stream exit while delete stream", id)
		case <-viewerTimeout.C:
			if !g.hasViewer(id) {
				return fmt.Errorf("[%s] stream exit while no viewer", id)
			}
		case <-clientTimeout.C:
			return fmt.Errorf("[%s] stream exit while no keyframe", id)
		case signals := <-RTSPClient.Signals:
			switch signals {
			case rtspv2.SignalCodecUpdate:
				g.setCodec(id, RTSPClient.CodecData)
			case rtspv2.SignalStreamRTPStop:
				return ErrRtspDisconnect
			}
		case packetAV := <-RTSPClient.OutgoingPacketQueue:
			if packetAV.IsKeyFrame {
				clientTimeout.Reset(60 * time.Second)
			}
			g.castPkt(id, *packetAV)
		}
	}
}

func (g *Rtsp2Web) Start(id string) {
	g.lock.Lock()
	defer g.lock.Unlock()
	if s, ok := g.streams[id]; ok && !s.Running {
		s.Running = true
		g.streams[id] = s
		go func(id string, s *Stream) {
			for {
				err := g.work(id, s)
				if err != ErrRtspDisconnect {
					log.Println(err)
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
		}(id, &s)
	}
}

func NewRtsp2Web(config Config) *Rtsp2Web {
	return &Rtsp2Web{
		config:  config,
		streams: make(map[string]Stream),
	}
}

func (g *Rtsp2Web) WebRtcHander() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.FormValue("id")
		if !g.exist(id) {
			http.Error(w, "Stream Not Exist", http.StatusNotFound)
			return
		}

		g.Start(id)

		codecs := g.getCodec(id)
		if codecs == nil {
			http.Error(w, "Stream No Codec", http.StatusInternalServerError)
			return
		}

		sdp, _ := ioutil.ReadAll(r.Body)

		muxerWebRTC := webrtc.NewMuxer(webrtc.Options{
			ICEServers:    g.config.WebRtc.ICEServers,
			ICEUsername:   g.config.WebRtc.ICEUsername,
			ICECredential: g.config.WebRtc.ICECredential,
			ICECandidates: g.config.WebRtc.ICECandidates,
			PortMin:       g.config.WebRtc.WebRTCPortMin,
			PortMax:       g.config.WebRtc.WebRTCPortMax,
		})
		answer, err := muxerWebRTC.WriteHeader(codecs, string(sdp))
		if err != nil {
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
					log.Printf("[%s] stream exit while no keyframe\n", id)
					return
				case pkt := <-ch:
					if pkt.IsKeyFrame {
						noVideoTimeout.Reset(60 * time.Second)
					}
					if err = muxerWebRTC.WritePacket(pkt); err != nil {
						log.Printf("[%s] stream exit while write packet:%s\n", id, err.Error())
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
			log.Printf("[%s] Stream Not Exist\n", id)
			return
		}

		g.Start(id)

		codecs := g.getCodec(id)
		if codecs == nil {
			log.Printf("[%s] Stream No Codec\n", id)
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

		noVideoTimeout := time.NewTimer(60 * time.Second)
		for {
			select {
			case <-noVideoTimeout.C:
				log.Printf("[%s] stream exit while no keyframe\n", id)
				return
			case pkt := <-ch:
				if pkt.IsKeyFrame {
					noVideoTimeout.Reset(60 * time.Second)
				}
				if ready, buf, _ := muxer.WritePacket(pkt, false); ready {
					err = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
					if err != nil {
						log.Println(err)
						return
					}
					err := websocket.Message.Send(ws, buf)
					if err != nil {
						log.Printf("[%s] stream exit while write packet:%s\n", id, err.Error())
						return
					}
				}
			}
		}
	})
}

func (g *Rtsp2Web) HttpFlvHander() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.FormValue("id")
		if !g.exist(id) {
			http.Error(w, "Stream Not Exist", http.StatusNotFound)
			return
		}

		g.Start(id)

		codecs := g.getCodec(id)
		if codecs == nil {
			http.Error(w, "Stream No Codec", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "video/x-flv")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(200)

		vid, ch := g.addViewer(id)
		defer g.delViewer(id, vid)

		fMux := flv.NewMuxer(w)
		err := fMux.WriteHeader(codecs)
		if err != nil {
			log.Println(err)
			return
		}

		noVideoTimeout := time.NewTimer(60 * time.Second)
		for {
			select {
			case <-noVideoTimeout.C:
				log.Printf("[%s] stream exit while no keyframe\n", id)
				return
			case pkt := <-ch:
				if pkt.IsKeyFrame {
					noVideoTimeout.Reset(60 * time.Second)
				}
				if err := fMux.WritePacket(pkt); err != nil {
					log.Printf("[%s] stream exit while write packet:%s\n", id, err.Error())
					return
				}
			}
		}
	})
}

func (g *Rtsp2Web) WsFlvHander() websocket.Handler {
	return websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()

		id := ws.Request().FormValue("id")
		if !g.exist(id) {
			log.Printf("[%s] Stream Not Exist\n", id)
			return
		}

		g.Start(id)

		codecs := g.getCodec(id)
		if codecs == nil {
			log.Printf("[%s] Stream No Codec\n", id)
			return
		}

		ws.SetWriteDeadline(time.Now().Add(5 * time.Second))

		vid, ch := g.addViewer(id)
		defer g.delViewer(id, vid)

		var b bytes.Buffer

		muxer := flv.NewMuxer(&b)
		err := muxer.WriteHeader(codecs)
		if err != nil {
			log.Println(err)
			return
		}
		err = websocket.Message.Send(ws, b.Bytes())
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

		noVideoTimeout := time.NewTimer(60 * time.Second)
		for {
			select {
			case <-noVideoTimeout.C:
				log.Printf("[%s] stream exit while no keyframe\n", id)
				return
			case pkt := <-ch:
				if pkt.IsKeyFrame {
					noVideoTimeout.Reset(60 * time.Second)
				}
				b.Reset()
				if err := muxer.WritePacket(pkt); err == nil {
					err = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
					if err != nil {
						log.Println(err)
						return
					}
					err := websocket.Message.Send(ws, b.Bytes())
					if err != nil {
						log.Printf("[%s] stream exit while write packet:%s\n", id, err.Error())
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
			ExitC:       make(chan struct{}),
			Codecs:      make([]av.CodecData, 0),
			Viewers:     make(map[string]Viewer),
		}
	}
	return nil
}

func (g *Rtsp2Web) RemoveStream(id string) {
	g.lock.Lock()
	defer g.lock.Unlock()
	if s, ok := g.streams[id]; ok {
		if s.Running {
			s.ExitC <- struct{}{}
		}
		close(s.ExitC)
		delete(g.streams, id)
	}
}
