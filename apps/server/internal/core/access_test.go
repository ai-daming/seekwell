package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccessKeysAndRequestAccounting(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := httptest.NewServer(s.Handler(""))
	defer h.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}`)
	}))
	defer upstream.Close()
	c := Channel{Name: "test", BaseURL: upstream.URL + "/v1", APIKey: "upstream-secret", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true}
	if err = s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	post := func(path, body, key string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest("POST", h.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, b
	}
	res, _ := post("/v1/chat/completions", `{"model":"deepseek-flash","messages":[]}`, "")
	if res.StatusCode != 401 {
		t.Fatalf("missing key: %d", res.StatusCode)
	}
	res, b := post("/api/keys", `{"name":"Alice"}`, "")
	if res.StatusCode != 201 {
		t.Fatalf("create %d %s", res.StatusCode, b)
	}
	var created struct {
		ID  int64  `json:"id"`
		Key string `json:"key"`
	}
	if json.Unmarshal(b, &created) != nil || created.Key == "" {
		t.Fatalf("creation %s", b)
	}
	res, b = post("/v1/chat/completions", `{"model":"deepseek-flash","messages":[]}`, created.Key)
	if res.StatusCode != 200 {
		t.Fatalf("call %d %s", res.StatusCode, b)
	}
	res, e := http.Get(h.URL + "/api/keys")
	if e != nil {
		t.Fatal(e)
	}
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if bytes.Contains(b, []byte(created.Key)) {
		t.Fatal("key leaked in list")
	}
	var list []struct {
		ID           int64 `json:"id"`
		RequestCount int   `json:"request_count"`
		InputTokens  int   `json:"input_tokens"`
		OutputTokens int   `json:"output_tokens"`
	}
	if json.Unmarshal(b, &list) != nil || len(list) != 1 || list[0].RequestCount != 1 || list[0].InputTokens != 12 || list[0].OutputTokens != 7 {
		t.Fatalf("stats %s", b)
	}
	req, _ := http.NewRequest("PATCH", h.URL+"/api/keys/1", strings.NewReader(`{"enabled":false}`))
	res, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("disable %d", res.StatusCode)
	}
	res, _ = post("/v1/chat/completions", `{"model":"deepseek-flash","messages":[]}`, created.Key)
	if res.StatusCode != 401 {
		t.Fatalf("disabled key: %d", res.StatusCode)
	}
}

func TestAccessKeyStreamingFailoverCountsOneRequest(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		io.WriteString(w, "data: {\"choices\":[]}\n\n")
		f.Flush()
		io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":3,")
		f.Flush()
		io.WriteString(w, "\"completion_tokens\":4}}\n\ndata: [DONE]\n\n")
	}))
	defer good.Close()
	s, e := New(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for i, u := range []string{bad.URL, good.URL} {
		c := Channel{Name: u, BaseURL: u, APIKey: "upstream", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Priority: i}
		if e = s.SaveChannel(&c); e != nil {
			t.Fatal(e)
		}
	}
	k, e := s.newAccessKey("stream")
	if e != nil {
		t.Fatal(e)
	}
	h := httptest.NewServer(s.Handler(""))
	defer h.Close()
	req, _ := http.NewRequest("POST", h.URL+"/v1/chat/completions", strings.NewReader(`{"model":"deepseek-flash","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+k.Key)
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Contains(b, []byte("[DONE]")) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var count, in, out int
	if e = s.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0) FROM access_requests WHERE key_id=?`, k.ID).Scan(&count, &in, &out); e != nil || count != 1 || in != 3 || out != 4 {
		t.Fatalf("count=%d in=%d out=%d err=%v", count, in, out, e)
	}
}

func TestAnthropicStreamUsageEvents(t *testing.T) {
	w := &usageWriter{ResponseWriter: httptest.NewRecorder()}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Write([]byte("data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":8,\"output_tokens\":0}}}\n\n"))
	w.Write([]byte("data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":5}}\n\n"))
	w.finish()
	if !w.hasInput || !w.hasOutput || w.input != 8 || w.output != 5 {
		t.Fatalf("usage: %+v", w)
	}
}
