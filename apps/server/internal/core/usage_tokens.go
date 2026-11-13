package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

// usageTotals collects the usage fields that upstream protocols return. The
// access-key accounting and the per-attempt call history share this parser so
// both interpret usage the same way.
type usageTotals struct {
	input, output                int64
	hasInput, hasOutput          bool
	cacheHit, cacheMiss          int64
	hasCacheHit, hasCacheMiss    bool
	cacheCreate, cacheRead       int64
	hasCacheCreate, hasCacheRead bool
}

func usageNumber(value any) (int64, bool) {
	n, ok := value.(float64)
	if !ok || n < 0 {
		return 0, false
	}
	return int64(n), true
}

func nestedUsageNumber(usage map[string]any, parent, child string) (int64, bool) {
	details, ok := usage[parent].(map[string]any)
	if !ok {
		return 0, false
	}
	return usageNumber(details[child])
}

func (u *usageTotals) parse(b []byte) {
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return
	}
	u.findUsage(v)
}

func (u *usageTotals) findUsage(v map[string]any) {
	if usage, ok := v["usage"].(map[string]any); ok {
		u.readUsage(usage)
	}
	if message, ok := v["message"].(map[string]any); ok {
		u.findUsage(message)
	}
	if response, ok := v["response"].(map[string]any); ok {
		u.findUsage(response)
	}
}

func (u *usageTotals) readUsage(usage map[string]any) {
	for _, name := range []string{"prompt_tokens", "input_tokens"} {
		if n, ok := usageNumber(usage[name]); ok {
			u.input, u.hasInput = n, true
		}
	}
	for _, name := range []string{"completion_tokens", "output_tokens"} {
		if n, ok := usageNumber(usage[name]); ok {
			u.output, u.hasOutput = n, true
		}
	}
	// Cache hit priority follows the protocol variants: DeepSeek's explicit
	// hit tokens, then OpenAI-style cached details, then Anthropic cache reads.
	if !u.hasCacheHit {
		if n, ok := usageNumber(usage["prompt_cache_hit_tokens"]); ok {
			u.cacheHit, u.hasCacheHit = n, true
		}
	}
	if !u.hasCacheHit {
		if n, ok := nestedUsageNumber(usage, "prompt_tokens_details", "cached_tokens"); ok {
			u.cacheHit, u.hasCacheHit = n, true
		}
	}
	if !u.hasCacheHit {
		if n, ok := nestedUsageNumber(usage, "input_tokens_details", "cached_tokens"); ok {
			u.cacheHit, u.hasCacheHit = n, true
		}
	}
	if !u.hasCacheMiss {
		if n, ok := usageNumber(usage["prompt_cache_miss_tokens"]); ok {
			u.cacheMiss, u.hasCacheMiss = n, true
		}
	}
	if n, ok := usageNumber(usage["cache_creation_input_tokens"]); ok {
		u.cacheCreate, u.hasCacheCreate = n, true
	}
	if n, ok := usageNumber(usage["cache_read_input_tokens"]); ok {
		u.cacheRead, u.hasCacheRead = n, true
		if !u.hasCacheHit {
			u.cacheHit, u.hasCacheHit = n, true
		}
	}
}

// normalized returns the per-attempt values stored in the call history. Input
// tokens follow each protocol's usage definition; cache miss falls back to the
// derivable value only when the upstream did not report it. Unknown values stay
// nil so display layers can show them as unknown instead of zero.
func (u *usageTotals) normalized() (input, output, hit, miss *int64) {
	anthropic := u.hasCacheRead || u.hasCacheCreate
	if u.hasInput {
		value := u.input
		if anthropic {
			if u.hasCacheCreate {
				value += u.cacheCreate
			}
			if u.hasCacheRead {
				value += u.cacheRead
			}
		}
		input = &value
	}
	if u.hasOutput {
		value := u.output
		output = &value
	}
	if !u.hasCacheHit {
		return input, output, nil, nil
	}
	hitValue := u.cacheHit
	hit = &hitValue
	if u.hasCacheMiss {
		missValue := u.cacheMiss
		return input, output, hit, &missValue
	}
	switch {
	case anthropic && u.hasInput:
		missValue := u.input
		if u.hasCacheCreate {
			missValue += u.cacheCreate
		}
		return input, output, hit, &missValue
	case !anthropic && input != nil:
		missValue := *input - *hit
		if missValue < 0 {
			missValue = 0
		}
		return input, output, hit, &missValue
	}
	return input, output, hit, nil
}

// usageCapture buffers response bytes (up to 2MB) and extracts usage fields
// from JSON bodies or SSE data lines. Larger bodies leave usage unknown.
type usageCapture struct {
	buf    []byte
	stream bool
	usageTotals
}

func (c *usageCapture) feed(p []byte) {
	if len(c.buf)+len(p) <= 2<<20 {
		c.buf = append(c.buf, p...)
	} else {
		c.buf = nil
	}
	if c.stream {
		c.drain()
	}
}

func (c *usageCapture) drain() {
	for {
		i := bytes.IndexByte(c.buf, '\n')
		if i < 0 {
			return
		}
		line := bytes.TrimSpace(c.buf[:i])
		c.buf = c.buf[i+1:]
		if bytes.HasPrefix(line, []byte("data:")) {
			c.parse(bytes.TrimSpace(line[5:]))
		}
	}
}

func (c *usageCapture) finish() {
	if !c.stream {
		c.parse(c.buf)
		return
	}
	trimmed := bytes.TrimSpace(c.buf)
	if bytes.HasPrefix(trimmed, []byte("data:")) {
		c.parse(bytes.TrimSpace(trimmed[5:]))
	}
}

// usageTap forwards a proxied response to the client while capturing usage for
// the attempt row that actually delivered the body.
type usageTap struct {
	http.ResponseWriter
	usageCapture
}

func (t *usageTap) WriteHeader(code int) {
	t.stream = strings.Contains(t.Header().Get("Content-Type"), "text/event-stream")
	t.ResponseWriter.WriteHeader(code)
}

func (t *usageTap) Write(p []byte) (int, error) {
	t.feed(p)
	return t.ResponseWriter.Write(p)
}

func (t *usageTap) Flush() {
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
