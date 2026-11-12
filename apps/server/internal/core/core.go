package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Channel struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	BaseURL           string `json:"base_url"`
	APIKey            string `json:"api_key,omitempty"`
	HasKey            bool   `json:"has_key"`
	Model             string `json:"model"`
	RemoteModel       string `json:"remote_model"`
	Enabled           bool   `json:"enabled"`
	Paid              bool   `json:"paid"`
	AutoPaid          bool   `json:"auto_paid"`
	Priority          int    `json:"priority"`
	Kind              string `json:"kind"`
	SupportsTools     bool   `json:"supports_tools"`
	SupportsImages    bool   `json:"supports_images"`
	SupportsResponses bool   `json:"supports_responses"`
	SupportsMessages  bool   `json:"supports_messages"`
	MessagesBaseURL   string `json:"messages_base_url"`
}
type Server struct {
	db           *sql.DB
	client       *http.Client
	speedTimeout time.Duration
	testTimeout  time.Duration
	mu           sync.Mutex
	sessions     map[string]sessionBinding
}
type sessionBinding struct {
	channel int64
	expires time.Time
}

func New(path string) (*Server, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS channels (id INTEGER PRIMARY KEY, name TEXT NOT NULL, base_url TEXT NOT NULL, api_key TEXT NOT NULL, model TEXT NOT NULL, remote_model TEXT NOT NULL, enabled INTEGER NOT NULL, paid INTEGER NOT NULL, auto_paid INTEGER NOT NULL, priority INTEGER NOT NULL, kind TEXT NOT NULL); CREATE TABLE IF NOT EXISTS requests (id INTEGER PRIMARY KEY, created_at TEXT NOT NULL, model TEXT NOT NULL, channel TEXT NOT NULL, status INTEGER NOT NULL, duration_ms INTEGER NOT NULL, kind TEXT NOT NULL DEFAULT '');`)
	if err != nil {
		db.Close()
		return nil, err
	}
	for _, column := range []string{"supports_tools", "supports_images", "supports_responses", "supports_messages", "messages_base_url"} {
		var count int
		if err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('channels') WHERE name=?`, column).Scan(&count); err == nil && count == 0 {
			definition := ` INTEGER NOT NULL DEFAULT 0`
			if column == "messages_base_url" {
				definition = ` TEXT NOT NULL DEFAULT ''`
			}
			_, err = db.Exec(`ALTER TABLE channels ADD COLUMN ` + column + definition)
		}
		if err != nil {
			db.Close()
			return nil, err
		}
	}
	var kindColumns int
	if err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('requests') WHERE name='kind'`).Scan(&kindColumns); err == nil && kindColumns == 0 {
		_, err = db.Exec(`ALTER TABLE requests ADD COLUMN kind TEXT NOT NULL DEFAULT ''`)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0600)
	return &Server{db: db, client: &http.Client{Timeout: 5 * time.Minute}, speedTimeout: 30 * time.Second, testTimeout: 60 * time.Second, sessions: make(map[string]sessionBinding)}, nil
}
func (s *Server) Close() error { return s.db.Close() }
func validModel(v string) bool {
	return strings.Contains(strings.ToLower(v), "deepseek") && !strings.ContainsAny(v, " \n\r\t")
}
func (s *Server) SaveChannel(c *Channel) error {
	c.Name = strings.TrimSpace(c.Name)
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.Model = strings.TrimSpace(c.Model)
	c.RemoteModel = strings.TrimSpace(c.RemoteModel)
	u, e := url.Parse(c.BaseURL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("请输入有效的 HTTP(S) API 地址")
	}
	if c.Name == "" || !validModel(c.Model) || !validModel(c.RemoteModel) {
		return errors.New("名称及包含 deepseek 的统一模型和上游模型不能为空")
	}
	if c.Kind == "" {
		c.Kind = "compatible"
	}
	if c.Kind != "compatible" && c.Kind != "deepseek" && c.Kind != "opencode_go" && c.Kind != "dimagent" {
		return errors.New("不支持的渠道类型")
	}
	c.MessagesBaseURL = strings.TrimRight(strings.TrimSpace(c.MessagesBaseURL), "/")
	if c.SupportsMessages {
		mu, err := url.Parse(c.MessagesBaseURL)
		if err != nil || mu.Host == "" || (mu.Scheme != "http" && mu.Scheme != "https") || mu.User != nil || mu.RawQuery != "" || mu.Fragment != "" {
			return errors.New("支持 Messages 时请输入有效的 Messages API 地址")
		}
	}
	if c.ID == 0 {
		if c.APIKey == "" {
			return errors.New("请输入 API Key")
		}
		r, e := s.db.Exec(`INSERT INTO channels(name,base_url,api_key,model,remote_model,enabled,paid,auto_paid,priority,kind,supports_tools,supports_images,supports_responses,supports_messages,messages_base_url) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, c.Name, c.BaseURL, c.APIKey, c.Model, c.RemoteModel, c.Enabled, c.Paid, c.AutoPaid, c.Priority, c.Kind, c.SupportsTools, c.SupportsImages, c.SupportsResponses, c.SupportsMessages, c.MessagesBaseURL)
		if e != nil {
			return e
		}
		c.ID, e = r.LastInsertId()
		return e
	}
	result, e := s.db.Exec(`UPDATE channels SET name=?,base_url=?,api_key=CASE WHEN ?='' THEN api_key ELSE ? END,model=?,remote_model=?,enabled=?,paid=?,auto_paid=?,priority=?,kind=?,supports_tools=?,supports_images=?,supports_responses=?,supports_messages=?,messages_base_url=? WHERE id=?`, c.Name, c.BaseURL, c.APIKey, c.APIKey, c.Model, c.RemoteModel, c.Enabled, c.Paid, c.AutoPaid, c.Priority, c.Kind, c.SupportsTools, c.SupportsImages, c.SupportsResponses, c.SupportsMessages, c.MessagesBaseURL, c.ID)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e == nil && n == 0 {
		return errors.New("渠道不存在")
	}
	return e
}
func (s *Server) channels() ([]Channel, error) {
	rows, e := s.db.Query(`SELECT id,name,base_url,api_key,model,remote_model,enabled,paid,auto_paid,priority,kind,supports_tools,supports_images,supports_responses,supports_messages,messages_base_url FROM channels ORDER BY priority,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	cs := []Channel{}
	for rows.Next() {
		var c Channel
		if e = rows.Scan(&c.ID, &c.Name, &c.BaseURL, &c.APIKey, &c.Model, &c.RemoteModel, &c.Enabled, &c.Paid, &c.AutoPaid, &c.Priority, &c.Kind, &c.SupportsTools, &c.SupportsImages, &c.SupportsResponses, &c.SupportsMessages, &c.MessagesBaseURL); e != nil {
			return nil, e
		}
		c.HasKey = c.APIKey != ""
		cs = append(cs, c)
	}
	return cs, rows.Err()
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, msg string) {
	jsonOut(w, status, map[string]any{"error": map[string]string{"message": msg}})
}
func (s *Server) Handler(webDir string) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/channels", func(w http.ResponseWriter, r *http.Request) {
		cs, e := s.channels()
		if e != nil {
			fail(w, 500, "读取渠道失败")
			return
		}
		for i := range cs {
			cs[i].APIKey = ""
		}
		jsonOut(w, 200, cs)
	})
	m.HandleFunc("POST /api/channels", func(w http.ResponseWriter, r *http.Request) {
		var c Channel
		if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&c); e != nil {
			fail(w, 400, "无效的请求")
			return
		}
		if e := s.SaveChannel(&c); e != nil {
			fail(w, 400, e.Error())
			return
		}
		c.APIKey = ""
		c.HasKey = true
		jsonOut(w, 201, c)
	})
	m.HandleFunc("PUT /api/channels/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil {
			fail(w, 400, "无效 ID")
			return
		}
		var c Channel
		if e = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&c); e != nil {
			fail(w, 400, "无效的请求")
			return
		}
		c.ID = id
		if e = s.SaveChannel(&c); e != nil {
			fail(w, 400, e.Error())
			return
		}
		c.APIKey = ""
		c.HasKey = true
		jsonOut(w, 200, c)
	})
	m.HandleFunc("DELETE /api/channels/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil {
			fail(w, 400, "无效 ID")
			return
		}
		if _, e = s.db.Exec(`DELETE FROM channels WHERE id=?`, id); e != nil {
			fail(w, 500, "删除失败")
			return
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("GET /api/requests", s.history)
	m.HandleFunc("POST /api/channels/{id}/discover", s.discover)
	m.HandleFunc("POST /api/channels/{id}/speedtest", s.speedtest)
	m.HandleFunc("POST /api/channels/{id}/test", s.modelTest)
	m.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		cs, e := s.channels()
		if e != nil {
			fail(w, 500, "读取渠道失败")
			return
		}
		seen := map[string]bool{}
		items := []map[string]any{}
		for _, c := range cs {
			if c.Enabled && !seen[c.Model] {
				seen[c.Model] = true
				items = append(items, map[string]any{"id": c.Model, "object": "model", "owned_by": "seekwell"})
			}
		}
		jsonOut(w, 200, map[string]any{"object": "list", "data": items})
	})
	m.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { s.proxyModel(w, r, "/chat/completions") })
	m.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) { s.proxyModel(w, r, "/responses") })
	m.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) { s.proxyModel(w, r, "/messages") })
	m.HandleFunc("POST /v1/tools/search", func(w http.ResponseWriter, r *http.Request) { s.proxyTool(w, r, "/tools/search") })
	m.HandleFunc("POST /v1/tools/fetch", func(w http.ResponseWriter, r *http.Request) { s.proxyTool(w, r, "/tools/fetch") })
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]string{"status": "ok"}) })
	if webDir != "" {
		m.Handle("/", http.FileServer(http.Dir(webDir)))
	}
	return m
}
func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, 400, "无效 ID")
		return
	}
	channels, err := s.channels()
	if err != nil {
		fail(w, 500, "读取渠道失败")
		return
	}
	for _, c := range channels {
		if c.ID != id {
			continue
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, c.BaseURL+"/models", nil)
		if err != nil {
			fail(w, 400, "无效渠道地址")
			return
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		if c.Kind == "opencode_go" {
			req.Header.Set("User-Agent", "SeekWell/0.1")
		}
		resp, err := s.client.Do(req)
		if err != nil {
			fail(w, 502, "无法连接渠道模型目录")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			fail(w, 502, "渠道模型目录不可用，上游状态码 "+strconv.Itoa(resp.StatusCode))
			return
		}
		var catalog struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&catalog); err != nil {
			fail(w, 502, "渠道返回了无法解析的模型目录")
			return
		}
		models := []string{}
		for _, model := range catalog.Data {
			if validModel(model.ID) {
				models = append(models, model.ID)
			}
		}
		jsonOut(w, 200, map[string]any{"models": models})
		return
	}
	fail(w, 404, "渠道不存在")
}
func (s *Server) speedtest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, 400, "无效 ID")
		return
	}
	channels, err := s.channels()
	if err != nil {
		fail(w, 500, "读取渠道失败")
		return
	}
	for _, c := range channels {
		if c.ID != id {
			continue
		}
		payload, _ := json.Marshal(map[string]any{
			"model":      c.RemoteModel,
			"messages":   []map[string]string{{"role": "user", "content": "ping"}},
			"max_tokens": 1,
			"stream":     false,
		})
		ctx, cancel := context.WithTimeout(r.Context(), s.speedTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload))
		if err != nil {
			fail(w, 400, "无效渠道地址")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		if c.Kind == "opencode_go" {
			var session [16]byte
			if _, err := rand.Read(session[:]); err != nil {
				fail(w, 500, "无法生成会话标识")
				return
			}
			req.Header.Set("x-opencode-session", hex.EncodeToString(session[:]))
			req.Header.Set("User-Agent", "SeekWell/0.1")
		}
		start := time.Now()
		resp, err := s.client.Do(req)
		if err != nil {
			s.failSpeedtest(w, ctx, start)
			return
		}
		_, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if readErr != nil {
			s.failSpeedtest(w, ctx, start)
			return
		}
		jsonOut(w, 200, map[string]any{"duration_ms": time.Since(start).Milliseconds(), "status": resp.StatusCode, "model": c.RemoteModel})
		return
	}
	fail(w, 404, "渠道不存在")
}
func (s *Server) failSpeedtest(w http.ResponseWriter, ctx context.Context, start time.Time) {
	elapsed := time.Since(start).Milliseconds()
	if ctx.Err() == context.DeadlineExceeded {
		fail(w, 502, fmt.Sprintf("测速超时（>%dms，已耗时 %dms）", s.speedTimeout.Milliseconds(), elapsed))
		return
	}
	fail(w, 502, fmt.Sprintf("无法连接渠道（已耗时 %dms）", elapsed))
}
func (s *Server) modelTest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, 400, "无效 ID")
		return
	}
	var input struct {
		Protocol string `json:"protocol"`
		Model    string `json:"model"`
		Prompt   string `json:"prompt"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		fail(w, 400, "无效的测试请求")
		return
	}
	if strings.TrimSpace(input.Prompt) == "" {
		fail(w, 400, "请输入测试提示词")
		return
	}
	if len(input.Prompt) > 32<<10 {
		fail(w, 400, "测试提示词不能超过 32KB")
		return
	}
	if !validModel(input.Model) {
		fail(w, 400, "测试模型名必须包含 deepseek 且不能包含空白")
		return
	}
	if input.Protocol != "chat" && input.Protocol != "responses" && input.Protocol != "messages" {
		fail(w, 400, "不支持的测试协议")
		return
	}
	channels, err := s.channels()
	if err != nil {
		fail(w, 500, "读取渠道失败")
		return
	}
	for _, c := range channels {
		if c.ID != id {
			continue
		}
		path := "/chat/completions"
		baseURL := c.BaseURL
		var payload []byte
		switch input.Protocol {
		case "responses":
			path = "/responses"
			payload, _ = json.Marshal(map[string]any{
				"model":  input.Model,
				"input":  input.Prompt,
				"stream": false,
			})
		case "messages":
			if c.MessagesBaseURL == "" {
				fail(w, 400, "该渠道未配置 Messages API 地址，无法测试 messages 协议")
				return
			}
			path = "/messages"
			baseURL = c.MessagesBaseURL
			payload, _ = json.Marshal(map[string]any{
				"model":      input.Model,
				"max_tokens": 512,
				"messages":   []map[string]string{{"role": "user", "content": input.Prompt}},
			})
		default:
			payload, _ = json.Marshal(map[string]any{
				"model":    input.Model,
				"messages": []map[string]string{{"role": "user", "content": input.Prompt}},
				"stream":   false,
			})
		}
		ctx, cancel := context.WithTimeout(r.Context(), s.testTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(payload))
		if err != nil {
			fail(w, 400, "无效渠道地址")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		if c.Kind == "opencode_go" {
			var session [16]byte
			if _, err := rand.Read(session[:]); err != nil {
				fail(w, 500, "无法生成会话标识")
				return
			}
			req.Header.Set("x-opencode-session", hex.EncodeToString(session[:]))
			req.Header.Set("User-Agent", "SeekWell/0.1")
		}
		if input.Protocol == "messages" {
			req.Header.Set("x-api-key", c.APIKey)
			version := r.Header.Get("anthropic-version")
			if version == "" {
				version = "2023-06-01"
			}
			req.Header.Set("anthropic-version", version)
			if beta := r.Header.Get("anthropic-beta"); beta != "" {
				req.Header.Set("anthropic-beta", beta)
			}
		}
		start := time.Now()
		resp, err := s.client.Do(req)
		if err != nil {
			elapsed := time.Since(start).Milliseconds()
			if ctx.Err() == context.DeadlineExceeded {
				fail(w, 504, fmt.Sprintf("测试超时（>%dms，已耗时 %dms）", s.testTimeout.Milliseconds(), elapsed))
				return
			}
			fail(w, 502, fmt.Sprintf("无法连接渠道（已耗时 %dms）", elapsed))
			return
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		resp.Body.Close()
		elapsed := time.Since(start).Milliseconds()
		if readErr != nil {
			if ctx.Err() == context.DeadlineExceeded {
				fail(w, 504, fmt.Sprintf("测试超时（>%dms，已耗时 %dms）", s.testTimeout.Milliseconds(), elapsed))
				return
			}
			fail(w, 502, fmt.Sprintf("读取渠道响应失败（已耗时 %dms）", elapsed))
			return
		}
		truncated := len(raw) > 1<<20
		if truncated {
			raw = raw[:1<<20]
		}
		body := json.RawMessage(raw)
		if !json.Valid(raw) {
			body, _ = json.Marshal(string(raw))
		}
		jsonOut(w, 200, map[string]any{
			"channel":     c.Name,
			"model":       input.Model,
			"protocol":    input.Protocol,
			"status":      resp.StatusCode,
			"duration_ms": elapsed,
			"body":        body,
			"truncated":   truncated,
		})
		return
	}
	fail(w, 404, "渠道不存在")
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	rows, e := s.db.Query(`SELECT created_at,model,channel,kind,status,duration_ms FROM requests ORDER BY id DESC LIMIT 100`)
	if e != nil {
		fail(w, 500, "读取记录失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var at, model, ch, kind string
		var status, ms int
		if e = rows.Scan(&at, &model, &ch, &kind, &status, &ms); e != nil {
			fail(w, 500, "读取记录失败")
			return
		}
		out = append(out, map[string]any{"created_at": at, "model": model, "channel": ch, "kind": kind, "status": status, "duration_ms": ms})
	}
	jsonOut(w, 200, out)
}
func (s *Server) record(model, name, kind string, status int, start time.Time) {
	_, _ = s.db.Exec(`INSERT INTO requests(created_at,model,channel,kind,status,duration_ms) VALUES(?,?,?,?,?,?)`, time.Now().UTC().Format(time.RFC3339), model, name, kind, status, time.Since(start).Milliseconds())
}
func requestCapabilities(v any) (tools, images bool) {
	switch value := v.(type) {
	case []any:
		for _, child := range value {
			t, i := requestCapabilities(child)
			tools = tools || t
			images = images || i
		}
	case map[string]any:
		if t, ok := value["type"].(string); ok {
			if t == "image_url" || t == "input_image" || t == "image" {
				images = true
			}
			if t == "tool_use" || t == "tool_result" {
				tools = true
			}
		}
		if role, ok := value["role"].(string); ok && role == "tool" {
			tools = true
		}
		if calls, ok := value["tool_calls"].([]any); ok && len(calls) > 0 {
			tools = true
		}
		for _, child := range value {
			t, i := requestCapabilities(child)
			tools = tools || t
			images = images || i
		}
	}
	return
}
func (s *Server) proxyModel(w http.ResponseWriter, r *http.Request, path string) {
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 32<<20))
	if e != nil {
		fail(w, 400, "请求过大或无法读取")
		return
	}
	var body map[string]json.RawMessage
	if e = json.Unmarshal(b, &body); e != nil {
		fail(w, 400, "无效 JSON")
		return
	}
	var model string
	if e = json.Unmarshal(body["model"], &model); e != nil || !validModel(model) {
		fail(w, 400, "仅支持 DeepSeek 模型")
		return
	}
	var decoded map[string]any
	_ = json.Unmarshal(b, &decoded)
	needsTools, needsImages := requestCapabilities(decoded)
	if list, ok := decoded["tools"].([]any); ok && len(list) > 0 {
		needsTools = true
	}
	cs, e := s.channels()
	if e != nil {
		fail(w, 500, "读取渠道失败")
		return
	}
	session := r.Header.Get("X-OpenCode-Session")
	if session == "" {
		session = r.Header.Get("X-SeekWell-Session-Id")
	}
	if session == "" {
		session = r.Header.Get("X-Session-Id")
	}
	if session == "" {
		session = r.Header.Get("X-DeepSeek-Harness-Session-Id")
	}
	if len(session) > 256 {
		session = ""
	}
	key := model + "\x00" + session
	if session != "" {
		s.mu.Lock()
		binding := s.sessions[key]
		if time.Now().After(binding.expires) {
			delete(s.sessions, key)
			binding.channel = 0
		}
		s.mu.Unlock()
		for i, c := range cs {
			if c.ID == binding.channel && i > 0 {
				copy(cs[1:i+1], cs[:i])
				cs[0] = c
				break
			}
		}
	}
	start := time.Now()
	attempted := 0
	for _, c := range cs {
		if !c.Enabled || c.Model != model || (c.Paid && !c.AutoPaid) || (needsTools && !c.SupportsTools) || (needsImages && !c.SupportsImages) || (path == "/responses" && !c.SupportsResponses) || (path == "/messages" && !c.SupportsMessages) {
			continue
		}
		attempted++
		body["model"], _ = json.Marshal(c.RemoteModel)
		payload, _ := json.Marshal(body)
		baseURL := c.BaseURL
		if path == "/messages" {
			baseURL = c.MessagesBaseURL
		}
		req, e := http.NewRequestWithContext(r.Context(), "POST", baseURL+path, bytes.NewReader(payload))
		if e != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		if c.Kind == "opencode_go" {
			upstreamSession := session
			if upstreamSession == "" {
				var id [16]byte
				if _, err := rand.Read(id[:]); err != nil {
					fail(w, 500, "无法生成会话标识")
					return
				}
				upstreamSession = hex.EncodeToString(id[:])
			}
			req.Header.Set("x-opencode-session", upstreamSession)
			req.Header.Set("User-Agent", "SeekWell/0.1")
		}
		if path == "/messages" {
			req.Header.Set("x-api-key", c.APIKey)
			version := r.Header.Get("anthropic-version")
			if version == "" {
				version = "2023-06-01"
			}
			req.Header.Set("anthropic-version", version)
			if beta := r.Header.Get("anthropic-beta"); beta != "" {
				req.Header.Set("anthropic-beta", beta)
			}
		}
		resp, e := s.client.Do(req)
		if e != nil {
			s.record(model, c.Name, c.Kind, 0, start)
			if r.Context().Err() != nil {
				return
			}
			continue
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429 || resp.StatusCode >= 500 {
			s.record(model, c.Name, c.Kind, resp.StatusCode, start)
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			continue
		}
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.Header().Set("X-SeekWell-Channel", c.Name)
		w.WriteHeader(resp.StatusCode)
		copyErr := copyStream(w, resp.Body)
		resp.Body.Close()
		if copyErr != nil {
			s.record(model, c.Name, c.Kind, 0, start)
			panic(http.ErrAbortHandler)
		}
		if session != "" && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			s.mu.Lock()
			if len(s.sessions) >= 10000 {
				for k, v := range s.sessions {
					if time.Now().After(v.expires) {
						delete(s.sessions, k)
					}
				}
			}
			if len(s.sessions) < 10000 {
				s.sessions[key] = sessionBinding{channel: c.ID, expires: time.Now().Add(time.Hour)}
			}
			s.mu.Unlock()
		}
		s.record(model, c.Name, c.Kind, resp.StatusCode, start)
		return
	}
	if attempted == 0 {
		fail(w, 503, "没有启用且允许自动使用的匹配渠道")
	} else {
		fail(w, 502, "所有匹配渠道均不可用；请检查渠道记录")
	}
}
func copyStream(w http.ResponseWriter, r io.Reader) error {
	buf := make([]byte, 32*1024)
	for {
		n, e := r.Read(buf)
		if n > 0 {
			if _, we := w.Write(buf[:n]); we != nil {
				return we
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
	}
}
func (s *Server) proxyTool(w http.ResponseWriter, r *http.Request, path string) {
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if e != nil {
		fail(w, 400, "无效请求")
		return
	}
	cs, e := s.channels()
	if e != nil {
		fail(w, 500, "读取渠道失败")
		return
	}
	for _, c := range cs {
		if !c.Enabled || c.Kind != "deepseek" || (c.Paid && !c.AutoPaid) {
			continue
		}
		req, e := http.NewRequestWithContext(r.Context(), "POST", c.BaseURL+path, bytes.NewReader(b))
		if e != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		resp, e := s.client.Do(req)
		if e != nil {
			continue
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429 || resp.StatusCode >= 500 {
			resp.Body.Close()
			continue
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}
	fail(w, 503, "没有可用的官方 DeepSeek 搜索渠道")
}
