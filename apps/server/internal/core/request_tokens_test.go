package core

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postModel(t *testing.T, h *httptest.Server, path, body string) {
	t.Helper()
	resp, err := http.Post(h.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
}

func lastHistory(t *testing.T, h *httptest.Server) historyEntry {
	t.Helper()
	got := historyEntries(t, h)
	if len(got) == 0 {
		t.Fatal("history is empty")
	}
	return got[0]
}

// S1 非流式 chat：四个 token 字段按上游 usage 原样记录。
func TestHistoryRecordsChatUsageTokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":30,"prompt_cache_hit_tokens":100,"prompt_cache_miss_tokens":20}}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "deepseek", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek"})
	postModel(t, h, "/v1/chat/completions", `{"model":"deepseek-flash","messages":[]}`)
	got := lastHistory(t, h)
	if got.Status != 200 {
		t.Fatalf("status = %d", got.Status)
	}
	assertToken(t, "input_tokens", got.InputTokens, 120)
	assertToken(t, "output_tokens", got.OutputTokens, 30)
	assertToken(t, "cache_hit_tokens", got.CacheHitTokens, 100)
	assertToken(t, "cache_miss_tokens", got.CacheMissTokens, 20)
}

// S2 流式 chat：usage 分散在多个 data chunk，重复的 usage 不累加。
func TestHistoryRecordsStreamingChatUsageTokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		io.WriteString(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":120}}\n\n")
		f.Flush()
		io.WriteString(w, "data: {\"usage\":{\"completion_tokens\":30,\"prompt_cache_hit_tokens\":100,\"prompt_cache_miss_tokens\":20}}\n\n")
		f.Flush()
		io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":120,\"completion_tokens\":30,\"prompt_cache_hit_tokens\":100,\"prompt_cache_miss_tokens\":20}}\n\n")
		f.Flush()
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "deepseek", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "deepseek"})
	postModel(t, h, "/v1/chat/completions", `{"model":"deepseek-flash","messages":[],"stream":true}`)
	got := lastHistory(t, h)
	if got.Status != 200 {
		t.Fatalf("status = %d", got.Status)
	}
	assertToken(t, "input_tokens", got.InputTokens, 120)
	assertToken(t, "output_tokens", got.OutputTokens, 30)
	assertToken(t, "cache_hit_tokens", got.CacheHitTokens, 100)
	assertToken(t, "cache_miss_tokens", got.CacheMissTokens, 20)
}

// S3 Responses：命中取 input_tokens_details.cached_tokens，未命中为 input - cached。
func TestHistoryRecordsResponsesUsageTokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"resp_1","usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":80}}}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "responses", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "compatible", SupportsResponses: true})
	postModel(t, h, "/v1/responses", `{"model":"deepseek-flash","input":"hi"}`)
	got := lastHistory(t, h)
	if got.Status != 200 {
		t.Fatalf("status = %d", got.Status)
	}
	assertToken(t, "input_tokens", got.InputTokens, 100)
	assertToken(t, "output_tokens", got.OutputTokens, 20)
	assertToken(t, "cache_hit_tokens", got.CacheHitTokens, 80)
	assertToken(t, "cache_miss_tokens", got.CacheMissTokens, 20)
}

// S4 Messages 流式：输入为三项之和，命中取 cache_read，未命中为 input + cache_creation。
func TestHistoryRecordsAnthropicStreamingUsageTokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":8,\"cache_read_input_tokens\":100,\"cache_creation_input_tokens\":12,\"output_tokens\":0}}}\n\n")
		f.Flush()
		io.WriteString(w, "data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":5}}\n\n")
		f.Flush()
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "messages", BaseURL: upstream.URL + "/v1", MessagesBaseURL: upstream.URL + "/anthropic/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "compatible", SupportsMessages: true})
	postModel(t, h, "/v1/messages", `{"model":"deepseek-flash","messages":[]}`)
	got := lastHistory(t, h)
	if got.Status != 200 {
		t.Fatalf("status = %d", got.Status)
	}
	assertToken(t, "input_tokens", got.InputTokens, 120)
	assertToken(t, "output_tokens", got.OutputTokens, 5)
	assertToken(t, "cache_hit_tokens", got.CacheHitTokens, 100)
	assertToken(t, "cache_miss_tokens", got.CacheMissTokens, 20)
}

// S5 多渠道切换：失败尝试无 usage，成功尝试记录 token；仍按 newest-first 返回。
func TestHistoryTokensOnFailover(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4}}`)
	}))
	defer good.Close()
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "busy", BaseURL: bad.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "compatible", Priority: 0})
	saveTestChannel(t, s, Channel{Name: "ok", BaseURL: good.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "compatible", Priority: 1})
	postModel(t, h, "/v1/chat/completions", `{"model":"deepseek-flash","messages":[]}`)
	got := historyEntries(t, h)
	if len(got) != 2 {
		t.Fatalf("expected 2 attempts, got %d: %+v", len(got), got)
	}
	if got[0].Channel != "ok" || got[0].Status != 200 {
		t.Fatalf("newest attempt is not the success: %+v", got[0])
	}
	assertToken(t, "success input_tokens", got[0].InputTokens, 9)
	assertToken(t, "success output_tokens", got[0].OutputTokens, 4)
	assertNilToken(t, "success cache_hit_tokens", got[0].CacheHitTokens)
	assertNilToken(t, "success cache_miss_tokens", got[0].CacheMissTokens)
	if got[1].Channel != "busy" || got[1].Status != 429 {
		t.Fatalf("oldest attempt is not the failure: %+v", got[1])
	}
	assertNilToken(t, "failure input_tokens", got[1].InputTokens)
	assertNilToken(t, "failure output_tokens", got[1].OutputTokens)
	assertNilToken(t, "failure cache_hit_tokens", got[1].CacheHitTokens)
	assertNilToken(t, "failure cache_miss_tokens", got[1].CacheMissTokens)
}

// S6 无 usage / 异常 JSON / 超大非流式响应 / 无缓存字段：不猜值。
func TestHistoryUsageUnknownCases(t *testing.T) {
	huge := `{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2},"pad":"` + strings.Repeat("x", 3<<20) + `"}`
	cases := []struct {
		name     string
		response string
	}{
		{"no usage", `{"choices":[]}`},
		{"invalid json", `not-json`},
		{"oversized body", huge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, tc.response)
			}))
			defer upstream.Close()
			s, h := newTestServer(t)
			saveTestChannel(t, s, Channel{Name: "compatible", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "compatible"})
			postModel(t, h, "/v1/chat/completions", `{"model":"deepseek-flash","messages":[]}`)
			raw := historyRaw(t, h)
			for _, key := range []string{`"input_tokens":null`, `"output_tokens":null`, `"cache_hit_tokens":null`, `"cache_miss_tokens":null`} {
				if !bytes.Contains(raw, []byte(key)) {
					t.Fatalf("history missing %s: %s", key, raw)
				}
			}
			got := lastHistory(t, h)
			assertNilToken(t, "input_tokens", got.InputTokens)
			assertNilToken(t, "output_tokens", got.OutputTokens)
			assertNilToken(t, "cache_hit_tokens", got.CacheHitTokens)
			assertNilToken(t, "cache_miss_tokens", got.CacheMissTokens)
		})
	}
}

// S6b 上游完全没返回缓存字段时，输入/输出仍记录，命中与未命中保持未知。
func TestHistoryUsageWithoutCacheFields(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
	}))
	defer upstream.Close()
	s, h := newTestServer(t)
	saveTestChannel(t, s, Channel{Name: "compatible", BaseURL: upstream.URL + "/v1", APIKey: "k", Model: "deepseek-flash", RemoteModel: "deepseek-flash", Enabled: true, Kind: "compatible"})
	postModel(t, h, "/v1/chat/completions", `{"model":"deepseek-flash","messages":[]}`)
	got := lastHistory(t, h)
	assertToken(t, "input_tokens", got.InputTokens, 10)
	assertToken(t, "output_tokens", got.OutputTokens, 2)
	assertNilToken(t, "cache_hit_tokens", got.CacheHitTokens)
	assertNilToken(t, "cache_miss_tokens", got.CacheMissTokens)
}

func TestUsageTotalsNormalized(t *testing.T) {
	cases := []struct {
		name                        string
		body                        string
		input, output, hit, miss    int64
		unknownInput, unknownOutput bool
		unknownHit, unknownMiss     bool
	}{
		{
			name:  "chat cached tokens details",
			body:  `{"usage":{"prompt_tokens":50,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":20}}}`,
			input: 50, output: 10, hit: 20, miss: 30,
		},
		{
			name:  "anthropic messages components",
			body:  `{"usage":{"input_tokens":8,"cache_read_input_tokens":100,"cache_creation_input_tokens":12,"output_tokens":5}}`,
			input: 120, output: 5, hit: 100, miss: 20,
		},
		{
			name:  "explicit miss wins",
			body:  `{"usage":{"prompt_tokens":30,"completion_tokens":1,"prompt_cache_hit_tokens":10,"prompt_cache_miss_tokens":7}}`,
			input: 30, output: 1, hit: 10, miss: 7,
		},
		{
			name:  "no cache fields",
			body:  `{"usage":{"prompt_tokens":10,"completion_tokens":2}}`,
			input: 10, output: 2, unknownHit: true, unknownMiss: true,
		},
		{
			name:         "usage absent",
			body:         `{"choices":[]}`,
			unknownInput: true, unknownOutput: true, unknownHit: true, unknownMiss: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var usage usageTotals
			usage.parse([]byte(tc.body))
			in, out, hit, miss := usage.normalized()
			if tc.unknownInput {
				assertNilToken(t, "input", in)
			} else {
				assertToken(t, "input", in, tc.input)
			}
			if tc.unknownOutput {
				assertNilToken(t, "output", out)
			} else {
				assertToken(t, "output", out, tc.output)
			}
			if tc.unknownHit {
				assertNilToken(t, "hit", hit)
			} else {
				assertToken(t, "hit", hit, tc.hit)
			}
			if tc.unknownMiss {
				assertNilToken(t, "miss", miss)
			} else {
				assertToken(t, "miss", miss, tc.miss)
			}
		})
	}
}

// 访问 Key 层的输入口径保持不变：Messages 仍只记录 input_tokens。
func TestUsageTotalsAccessInputUnchanged(t *testing.T) {
	var usage usageTotals
	usage.parse([]byte(`{"usage":{"input_tokens":8,"cache_read_input_tokens":100,"cache_creation_input_tokens":12,"output_tokens":5}}`))
	if !usage.hasInput || usage.input != 8 || !usage.hasOutput || usage.output != 5 {
		t.Fatalf("access usage fields changed: %+v", usage)
	}
}
