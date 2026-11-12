package main

import (
	"github.com/ai-daming/seekwell/apps/server/internal/core"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	data := os.Getenv("SEEKWELL_DATA")
	if data == "" {
		data = "./data"
	}
	s, e := core.New(filepath.Join(data, "seekwell.db"))
	if e != nil {
		log.Fatal(e)
	}
	defer s.Close()
	web := os.Getenv("SEEKWELL_WEB")
	if web == "" {
		web = "../web/dist"
	}
	log.Println("SeekWell: http://127.0.0.1:8350")
	log.Fatal(http.ListenAndServe("127.0.0.1:8350", s.Handler(web)))
}
