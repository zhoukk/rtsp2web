package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/zhoukk/rtsp2web"
)

func main() {

	cfg := rtsp2web.Config{}
	// cfg.WebRtc.ICECandidates = []string{"192.168.1.2"}
	// cfg.WebRtc.WebRTCPortMin = 50010
	// cfg.WebRtc.WebRTCPortMax = 50020
	r2w := rtsp2web.NewRtsp2Web(cfg)

	r2w.AddStream("live", "rtsp://192.168.1.3:554/live", false)
	r2w.Start("live")

	http.Handle("/", http.FileServer(http.Dir("web")))
	http.Handle("/web/", http.FileServer(http.Dir("../")))

	http.HandleFunc("/api/stream", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			param := make(map[string]string)
			if err := json.NewDecoder(r.Body).Decode(&param); err != nil {
				log.Println(err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			ret := make(map[string]string)

			id := param["id"]
			url := param["url"]

			if err := r2w.AddStream(id, url, false); err != nil {
				ret["code"] = "500"
			} else {
				ret["code"] = "200"
			}

			w.Header().Add("Content-Type", "application/json;charset=utf-8")
			json.NewEncoder(w).Encode(ret)
		}
	})

	http.Handle("/stream/webrtc", r2w.WebRtcHander())

	http.Handle("/stream/ws", r2w.WsMp4fHander())

	http.Handle("/stream/httpflv", r2w.HttpFlvHander())

	http.Handle("/stream/wsflv", r2w.WsFlvHander())

	log.Println(http.ListenAndServe(":8010", nil))
}
