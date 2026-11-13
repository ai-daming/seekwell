package core

import (
	"bytes"
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
	c := Channel{Kind: "dimagent", AuthMode: "cli_oauth"}
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
	c.APIKey = "new-key"
	if err := s.SaveChannel(&c); err != nil {
		t.Fatal(err)
	}
	cs, err := s.channels()
	if err != nil || len(cs) != 1 || cs[0].AuthMode != "cli_oauth" || cs[0].APIKey != "" {
		t.Fatalf("dimagent api key was not normalized: %+v %v", cs, err)
	}
}

func TestDimAgentAPIKeyNormalizedToCLIOAuth(t *testing.T) {
	t.Setenv("DIMCODE_HOME", t.TempDir())
	s, h := newTestServer(t)
	status, body := postJSON(t, h.URL+"/api/channels", `{"name":"legacy-dim","base_url":"https://dimagent.cn/v1","api_key":"legacy-secret","auth_mode":"api_key","model":"deepseek-flash","remote_model":"deepseek-v4.1-flash","enabled":true,"kind":"dimagent"}`)
	if status != 201 || !bytesContainsAll(body, `"auth_mode":"cli_oauth"`, `"has_key":false`) || bytes.Contains(body, []byte("legacy-secret")) || bytes.Contains(body, []byte(`"api_key":`)) {
		t.Fatalf("POST normalization: %d %s", status, body)
	}
	var created Channel
	if err := json.Unmarshal(body, &created); err != nil || created.ID == 0 {
		t.Fatalf("invalid POST response %q: %v", body, err)
	}
	assertStored := func(label string) {
		t.Helper()
		var mode, key string
		if err := s.db.QueryRow(`SELECT auth_mode,api_key FROM channels WHERE id=?`, created.ID).Scan(&mode, &key); err != nil || mode != "cli_oauth" || key != "" {
			t.Fatalf("%s stored credential: mode=%q key=%q err=%v", label, mode, key, err)
		}
	}
	assertStored("POST")
	putReq, err := http.NewRequest(http.MethodPut, h.URL+"/api/channels/"+fmt.Sprint(created.ID), strings.NewReader(`{"name":"legacy-dim","base_url":"https://dimagent.cn/v1","api_key":"put-secret","auth_mode":"api_key","model":"deepseek-flash","remote_model":"deepseek-v4.1-flash","enabled":true,"kind":"dimagent"}`))
	if err != nil {
		t.Fatal(err)
	}
	putReq.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(putResp.Body)
	putResp.Body.Close()
	if putResp.StatusCode != 200 {
		t.Fatalf("PUT status: %d %s", putResp.StatusCode, body)
	}
	if !bytesContainsAll(body, `"auth_mode":"cli_oauth"`, `"has_key":false`) || bytes.Contains(body, []byte("put-secret")) || bytes.Contains(body, []byte(`"api_key":`)) {
		t.Fatalf("PUT normalization: %d %s", status, body)
	}
	assertStored("PUT")
	resp, err := http.Get(h.URL + "/api/channels")
	if err != nil {
		t.Fatal(err)
	}
	listBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var listed []Channel
	if err := json.Unmarshal(listBody, &listed); err != nil || len(listed) != 1 {
		t.Fatalf("invalid list %q: %v", listBody, err)
	}
	if listed[0].AuthMode != "cli_oauth" || listed[0].HasKey || listed[0].APIKey != "" || bytes.Contains(listBody, []byte("legacy-secret")) || bytes.Contains(listBody, []byte("put-secret")) || bytes.Contains(listBody, []byte(`"api_key":`)) {
		t.Fatalf("list leaked or kept credential: %s", listBody)
	}
}

func bytesContainsAll(body []byte, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(string(body), part) {
			return false
		}
	}
	return true
}

func TestDimAgentSaveWithoutAuthFile(t *testing.T) {
	t.Setenv("DIMCODE_HOME", t.TempDir())
	s, h := newTestServer(t)
	c := saveTestChannel(t, s, dimAgentDraft("no-auth-file", "https://dimagent.cn/v1", "dimagent"))
	resp, err := http.Get(h.URL + "/api/channels")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var listed []Channel
	if err := json.Unmarshal(body, &listed); err != nil || len(listed) != 1 {
		t.Fatalf("invalid list %q: %v", body, err)
	}
	got := listed[0]
	if got.ID != c.ID || got.AuthMode != "cli_oauth" || got.HasKey || got.AuthState != "missing" || !strings.Contains(got.AuthMessage, "dim auth login") {
		t.Fatalf("missing auth state: %+v", got)
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

func TestAuthModeMigrationNormalizesDimAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE channels (id INTEGER PRIMARY KEY, name TEXT NOT NULL, base_url TEXT NOT NULL, api_key TEXT NOT NULL, model TEXT NOT NULL, remote_model TEXT NOT NULL, enabled INTEGER NOT NULL, paid INTEGER NOT NULL, auto_paid INTEGER NOT NULL, priority INTEGER NOT NULL, kind TEXT NOT NULL); INSERT INTO channels VALUES (1,'old-dim','https://example.com','secret','deepseek-test','deepseek-test',1,0,0,0,'dimagent'); INSERT INTO channels VALUES (2,'old-deepseek','https://example.com','secret','deepseek-test','deepseek-test',1,0,0,0,'deepseek')`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := s.channels()
	if err != nil || len(cs) != 2 {
		t.Fatalf("migration: %+v %v", cs, err)
	}
	for _, c := range cs {
		switch c.Name {
		case "old-dim":
			if c.AuthMode != "cli_oauth" || c.APIKey != "" || c.HasKey {
				t.Fatalf("dimagent migration: %+v", c)
			}
		case "old-deepseek":
			if c.AuthMode != "api_key" || c.APIKey != "secret" || !c.HasKey {
				t.Fatalf("deepseek migration: %+v", c)
			}
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cs, err = s.channels()
	if err != nil || len(cs) != 2 {
		t.Fatalf("repeated migration: %+v %v", cs, err)
	}
	for _, c := range cs {
		if c.Name == "old-dim" && (c.AuthMode != "cli_oauth" || c.APIKey != "") {
			t.Fatalf("repeated migration changed dimagent: %+v", c)
		}
		if c.Name == "old-deepseek" && (c.AuthMode != "api_key" || c.APIKey != "secret") {
			t.Fatalf("repeated migration changed deepseek: %+v", c)
		}
	}
}

func TestDimAgentAuthModeValidation(t *testing.T) {
	s, _ := newTestServer(t)
	base := Channel{Name: "test", Kind: "dimagent", BaseURL: "https://example.com", Model: "deepseek-test", RemoteModel: "deepseek-test"}
	for _, tc := range []struct{ kind, mode, key, want string }{
		{"compatible", "cli_oauth", "", "只有 DimAgent"},
		{"dimagent", "bad", "", "不支持的凭据方式"},
		{"compatible", "bad", "key", "不支持的凭据方式"},
		{"compatible", "api_key", "", "请输入 API Key"},
	} {
		c := base
		c.Name = "invalid-" + tc.kind + "-" + tc.mode + "-" + tc.key
		c.Kind = tc.kind
		c.AuthMode = tc.mode
		c.APIKey = tc.key
		if err := s.SaveChannel(&c); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("invalid mode %+v: got %v", tc, err)
		}
	}
	for _, tc := range []struct{ mode, key string }{{"api_key", ""}, {"api_key", "key"}, {"cli_oauth", ""}, {"cli_oauth", "key"}} {
		c := base
		c.Name = "valid-" + tc.mode + "-" + tc.key
		c.AuthMode = tc.mode
		c.APIKey = tc.key
		if err := s.SaveChannel(&c); err != nil {
			t.Errorf("dimagent mode %+v rejected: %v", tc, err)
			continue
		}
		if c.AuthMode != "cli_oauth" || c.APIKey != "" {
			t.Errorf("dimagent mode %+v not normalized: %+v", tc, c)
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
	c := Channel{Kind: "dimagent", AuthMode: "cli_oauth"}
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
