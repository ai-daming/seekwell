package core

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type createdKey struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Key  string `json:"key"`
}

func keyHash(k string) string { h := sha256.Sum256([]byte(k)); return hex.EncodeToString(h[:]) }
func (s *Server) newAccessKey(name string) (createdKey, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return createdKey{}, fmt.Errorf("请输入不超过 100 字节的名称")
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return createdKey{}, err
	}
	key := "sw-" + hex.EncodeToString(b[:])
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.Exec(`INSERT INTO access_keys(name,key_hash,prefix,enabled,created_at) VALUES(?,?,?,1,?)`, name, keyHash(key), key[:11], now)
	if err != nil {
		return createdKey{}, err
	}
	id, err := result.LastInsertId()
	return createdKey{id, name, key}, err
}
func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil {
		fail(w, 400, "无效请求")
		return
	}
	k, err := s.newAccessKey(input.Name)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	jsonOut(w, 201, k)
}
func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT k.id,k.name,k.prefix,k.enabled,k.created_at,COUNT(r.id),COALESCE(SUM(CASE WHEN r.status BETWEEN 200 AND 299 THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN r.input_tokens IS NOT NULL AND r.output_tokens IS NOT NULL THEN 1 ELSE 0 END),0),SUM(r.input_tokens),SUM(r.output_tokens) FROM access_keys k LEFT JOIN access_requests r ON r.key_id=k.id GROUP BY k.id ORDER BY k.id DESC`)
	if err != nil {
		fail(w, 500, "读取密钥失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, count, success, known int64
		var name, prefix, created string
		var enabled bool
		var in, outTokens sql.NullInt64
		if rows.Scan(&id, &name, &prefix, &enabled, &created, &count, &success, &known, &in, &outTokens) != nil {
			fail(w, 500, "读取统计失败")
			return
		}
		var input, output any
		if in.Valid {
			input = in.Int64
		}
		if outTokens.Valid {
			output = outTokens.Int64
		}
		out = append(out, map[string]any{"id": id, "name": name, "prefix": prefix, "enabled": enabled, "created_at": created, "request_count": count, "success_count": success, "failure_count": count - success, "usage_known_count": known, "input_tokens": input, "output_tokens": output})
	}
	if rows.Err() != nil {
		fail(w, 500, "读取统计失败")
		return
	}
	jsonOut(w, 200, out)
}
func (s *Server) updateKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err != nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil || input.Enabled == nil {
		fail(w, 400, "无效请求")
		return
	}
	res, err := s.db.Exec(`UPDATE access_keys SET enabled=? WHERE id=?`, *input.Enabled, id)
	if err != nil {
		fail(w, 500, "更新失败")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fail(w, 404, "密钥不存在")
		return
	}
	jsonOut(w, 200, map[string]bool{"enabled": *input.Enabled})
}

func (s *Server) authenticateAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		token := ""
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			token = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
		if token == "" {
			token = r.Header.Get("x-api-key")
		}
		var id int64
		err := s.db.QueryRow(`SELECT id FROM access_keys WHERE key_hash=? AND enabled=1`, keyHash(token)).Scan(&id)
		if token == "" || err == sql.ErrNoRows {
			fail(w, 401, "访问 Key 无效或已停用")
			return
		}
		if err != nil {
			fail(w, 500, "访问 Key 验证失败")
			return
		}
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		capture := &usageWriter{ResponseWriter: w}
		defer func() {
			status := capture.status
			if status == 0 {
				status = 200
			}
			if recoverValue := recover(); recoverValue != nil {
				status = 0
				s.recordAccess(id, r.URL.Path, status, started, capture)
				panic(recoverValue)
			}
			s.recordAccess(id, r.URL.Path, status, started, capture)
		}()
		next.ServeHTTP(capture, r)
	})
}
func (s *Server) recordAccess(id int64, path string, status int, start time.Time, c *usageWriter) {
	c.finish()
	var in, out any
	if c.hasInput {
		in = c.input
	}
	if c.hasOutput {
		out = c.output
	}
	_, _ = s.db.Exec(`INSERT INTO access_requests(key_id,created_at,path,status,duration_ms,input_tokens,output_tokens) VALUES(?,?,?,?,?,?,?)`, id, time.Now().UTC().Format(time.RFC3339), path, status, time.Since(start).Milliseconds(), in, out)
}

type usageWriter struct {
	http.ResponseWriter
	status              int
	buf                 []byte
	stream              bool
	input, output       int64
	hasInput, hasOutput bool
}

func (w *usageWriter) WriteHeader(code int) {
	if w.status != 0 {
		return
	}
	w.status = code
	w.stream = strings.Contains(w.Header().Get("Content-Type"), "text/event-stream")
	w.ResponseWriter.WriteHeader(code)
}
func (w *usageWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	if len(w.buf)+len(p) <= 2<<20 {
		w.buf = append(w.buf, p...)
	} else {
		w.buf = nil
	}
	if w.stream {
		for {
			i := bytes.IndexByte(w.buf, '\n')
			if i < 0 {
				break
			}
			line := bytes.TrimSpace(w.buf[:i])
			w.buf = w.buf[i+1:]
			if bytes.HasPrefix(line, []byte("data:")) {
				w.parse(bytes.TrimSpace(line[5:]))
			}
		}
	}
	return w.ResponseWriter.Write(p)
}
func (w *usageWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *usageWriter) finish() {
	if !w.stream {
		w.parse(w.buf)
	} else if bytes.HasPrefix(bytes.TrimSpace(w.buf), []byte("data:")) {
		w.parse(bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(w.buf), []byte("data:"))))
	}
}
func (w *usageWriter) parse(b []byte) {
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return
	}
	w.findUsage(v)
}
func (w *usageWriter) findUsage(v map[string]any) {
	if usage, ok := v["usage"].(map[string]any); ok {
		for _, name := range []string{"prompt_tokens", "input_tokens"} {
			if n, ok := usage[name].(float64); ok && n >= 0 {
				w.input = int64(n)
				w.hasInput = true
			}
		}
		for _, name := range []string{"completion_tokens", "output_tokens"} {
			if n, ok := usage[name].(float64); ok && n >= 0 {
				w.output = int64(n)
				w.hasOutput = true
			}
		}
	}
	if message, ok := v["message"].(map[string]any); ok {
		w.findUsage(message)
	}
	if response, ok := v["response"].(map[string]any); ok {
		w.findUsage(response)
	}
}
