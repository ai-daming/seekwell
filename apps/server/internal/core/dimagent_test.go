package core

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDimAgentOAuthChannel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DIMCODE_HOME", dir)
	data := fmt.Sprintf(`{"nextApiOauth":{"type":"oauth","access":"test-oauth-secret","expires":%d}}`, time.Now().Add(time.Hour).UnixMilli())
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.URL.Path == "/models" && r.URL.RawQuery != "type=dim") || r.Header.Get("User-Agent") != "DimAgent/0.5.27" || r.Header.Get("X-Title") != "DimCode" || r.Header.Get("HTTP-Referer") != "https://dimagent.com/" {
			t.Errorf("incorrect DimAgent OAuth request metadata")
		}
		if r.Header.Get("Authorization") != "Bearer test-oauth-secret" {
			t.Errorf("incorrect credential")
		}
		if r.URL.Path == "/chat/completions" {
			io.WriteString(w, `{"choices":[]}`)
		} else {
			io.WriteString(w, `{"data":[{"id":"deepseek-test"}]}`)
		}
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	c := Channel{Name: "dim", Kind: "dimagent", AuthMode: "cli_oauth", BaseURL: upstream.URL, Model: "deepseek-test", RemoteModel: "deepseek-test", Enabled: true}
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(h.URL + "/api/channels")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(b), "test-oauth-secret") {
		t.Fatal("credential leaked")
	}
	var list []Channel
	if json.Unmarshal(b, &list) != nil || len(list) != 1 || list[0].AuthState != "ok" || list[0].HasKey {
		t.Fatalf("bad channel list: %s", b)
	}
	resp, err = http.Post(fmt.Sprintf("%s/api/channels/%d/discover", h.URL, c.ID), "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ = io.ReadAll(resp.Body)
		t.Fatalf("discover: %d %s", resp.StatusCode, b)
	}

	resp2, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-test","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("proxy status %d", resp2.StatusCode)
	}
}

func TestDimAgentRefreshAndCooldown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	dir := t.TempDir()
	t.Setenv("DIMCODE_HOME", dir)
	t.Setenv("PATH", dir)
	auth := filepath.Join(dir, "auth.json")
	count := filepath.Join(dir, "count")
	expired := `{"nextApiOauth":{"type":"oauth","access":"old","expires":1}}`
	if err := os.WriteFile(auth, []byte(expired), 0600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\necho x >> %q\nprintf '%%s' %q > %q\n", count, fmt.Sprintf(`{"nextApiOauth":{"type":"oauth","access":"new","expires":%d}}`, time.Now().Add(time.Hour).UnixMilli()), auth)
	if err := os.WriteFile(filepath.Join(dir, "dim"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s, _ := newTestServer(t)
	c := Channel{AuthMode: "cli_oauth"}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := s.credential(c)
			if err != nil || token != "new" {
				t.Errorf("refresh: token=%q err=%v", token, err)
			}
		}()
	}
	wg.Wait()
	calls, _ := os.ReadFile(count)
	if strings.Count(string(calls), "x") != 1 {
		t.Fatalf("refresh calls: %s", calls)
	}
	if err := os.WriteFile(auth, []byte(expired), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dim"), []byte(fmt.Sprintf("#!/bin/sh\necho x >> %q\nexit 1\n", count)), 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.credential(c); err == nil {
			t.Fatal("expected refresh failure")
		}
	}
	calls, _ = os.ReadFile(count)
	if strings.Count(string(calls), "x") != 2 {
		t.Fatalf("cooldown failed: %s", calls)
	}
}

func TestDimAgentModeSwitchAndMissing(t *testing.T) {
	t.Setenv("DIMCODE_HOME", t.TempDir())
	s, h := newTestServer(t)
	c := Channel{Name: "dim", Kind: "dimagent", AuthMode: "cli_oauth", BaseURL: "https://example.com/v1", Model: "deepseek-test", RemoteModel: "deepseek-test"}
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(fmt.Sprintf("%s/api/channels/%d/discover", h.URL, c.ID), "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 400 || !strings.Contains(string(raw), "dim auth login") {
		t.Fatalf("missing auth: %d %s", resp.StatusCode, raw)
	}
	c.AuthMode = "api_key"
	if s.SaveChannel(&c) == nil {
		t.Fatal("switch without key accepted")
	}
	c.APIKey = "new-key"
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	c.AuthMode = "cli_oauth"
	c.APIKey = ""
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	cs, err := s.channels()
	if err != nil || len(cs) != 1 || cs[0].APIKey != "" {
		t.Fatal("old key retained")
	}
}

func TestDimAgentOAuthFailover(t *testing.T) {
	t.Setenv("DIMCODE_HOME", t.TempDir())
	s, h := newTestServer(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fallback" {
			t.Error("wrong fallback credential")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	for _, c := range []Channel{{Name: "dim", Kind: "dimagent", AuthMode: "cli_oauth", BaseURL: upstream.URL, Model: "deepseek-test", RemoteModel: "deepseek-test", Enabled: true}, {Name: "backup", APIKey: "fallback", BaseURL: upstream.URL, Model: "deepseek-test", RemoteModel: "deepseek-test", Enabled: true, Priority: 1}} {
		if err := s.SaveChannel(&c); err != nil {
			t.Fatal(err)
		}
	}
	resp, err := http.Post(h.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"deepseek-test","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("failover status %d", resp.StatusCode)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM requests WHERE channel='dim' AND status=0`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("missing failure record %d %v", count, err)
	}
}

func TestAuthModeMigrationDefaultsToAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE channels (id INTEGER PRIMARY KEY, name TEXT NOT NULL, base_url TEXT NOT NULL, api_key TEXT NOT NULL, model TEXT NOT NULL, remote_model TEXT NOT NULL, enabled INTEGER NOT NULL, paid INTEGER NOT NULL, auto_paid INTEGER NOT NULL, priority INTEGER NOT NULL, kind TEXT NOT NULL); INSERT INTO channels VALUES (1,'old','https://example.com','secret','deepseek-test','deepseek-test',1,0,0,0,'dimagent')`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cs, err := s.channels()
	if err != nil || len(cs) != 1 || cs[0].AuthMode != "api_key" || cs[0].APIKey != "secret" {
		t.Fatalf("migration: %+v %v", cs, err)
	}
}

func TestDimAgentAuthModeValidation(t *testing.T) {
	s, _ := newTestServer(t)
	base := Channel{Name: "test", Kind: "dimagent", BaseURL: "https://example.com", Model: "deepseek-test", RemoteModel: "deepseek-test"}
	for _, tc := range []struct{ kind, mode, key string }{{"compatible", "cli_oauth", ""}, {"dimagent", "cli_oauth", "key"}, {"dimagent", "bad", ""}, {"dimagent", "api_key", ""}} {
		c := base
		c.Kind = tc.kind
		c.AuthMode = tc.mode
		c.APIKey = tc.key
		if err := s.SaveChannel(&c); err == nil {
			t.Errorf("accepted invalid mode: %+v", tc)
		}
	}
}

func TestDimAgentRefreshWindowMatchesCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	dir := t.TempDir()
	t.Setenv("DIMCODE_HOME", dir)
	t.Setenv("PATH", dir)
	auth := filepath.Join(dir, "auth.json")
	count := filepath.Join(dir, "count")
	write := func(seconds int) {
		t.Helper()
		data := fmt.Sprintf(`{"nextApiOauth":{"type":"oauth","access":"old","expires":%d}}`, time.Now().Add(time.Duration(seconds)*time.Second).UnixMilli())
		if err := os.WriteFile(auth, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := fmt.Sprintf("#!/bin/sh\necho x >> %q\nprintf '%%s' %q > %q\n", count, fmt.Sprintf(`{"nextApiOauth":{"type":"oauth","access":"new","expires":%d}}`, time.Now().Add(time.Hour).UnixMilli()), auth)
	if err := os.WriteFile(filepath.Join(dir, "dim"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	s, _ := newTestServer(t)
	c := Channel{AuthMode: "cli_oauth"}
	write(45)
	tok, err := s.credential(c)
	if err != nil || tok != "old" {
		t.Fatalf("valid credential: %v", err)
	}
	if _, err = os.Stat(count); !os.IsNotExist(err) {
		t.Fatal("refreshed before CLI refresh window")
	}
	write(20)
	tok, err = s.credential(c)
	if err != nil || tok != "new" {
		t.Fatalf("expected refresh: %v", err)
	}
}
