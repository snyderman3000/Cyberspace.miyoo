package main

// Live updates. Cyberspace delivers new C-Mail and cIRC messages, presence and
// typing through its Realtime Database over Server-Sent Events. We open one
// stream per thing we're watching and keep the latest state of each in a
// recMap, which the screens read.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const rtdbDefault = "https://cyberspace-cyberspace-default-rtdb.europe-west1.firebasedatabase.app"

// the server drops streams that go quiet this long
const streamIdle = 10 * time.Minute

type sseEvent struct {
	Name string          // put or patch
	Path string          // "/" for the whole node, "/<key>" for one child
	Data json.RawMessage // null means deleted
}

func (c *Client) rtdbURL() string {
	c.mu.Lock()
	db := c.tok.RTDBURL
	c.mu.Unlock()
	if db == "" {
		db = rtdbDefault
	}
	return strings.TrimRight(db, "/")
}

// openStream connects to an RTDB path. The returned channel closes when the
// stream ends for any reason; cancelling ctx ends it too.
func (c *Client) openStream(ctx context.Context, path, query string) (<-chan sseEvent, error) {
	tok := c.Tokens().IDToken
	if tok == "" {
		return nil, ErrUnauthorized
	}
	u := c.rtdbURL() + path + ".json?auth=" + url.QueryEscape(tok)
	if query != "" {
		u += "&" + query
	}
	sctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(sctx, "GET", u, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.UA != "" {
		req.Header.Set("User-Agent", c.UA)
	}
	hc := c.Stream
	if hc == nil {
		hc = &http.Client{}
	}
	resp, err := hc.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != 200 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		cancel()
		switch resp.StatusCode {
		case 401:
			return nil, ErrUnauthorized
		case 403:
			return nil, &APIError{Code: "FORBIDDEN", Status: 403, Message: "you can't open that right now"}
		}
		return nil, fmt.Errorf("live updates: HTTP %d", resp.StatusCode)
	}

	out := make(chan sseEvent, 32)
	idle := time.AfterFunc(streamIdle, cancel)
	go func() {
		defer close(out)
		defer cancel()
		defer resp.Body.Close()
		defer idle.Stop()
		r := bufio.NewReaderSize(resp.Body, 64<<10)
		var name string
		var data []string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			idle.Reset(streamIdle)
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				stop := name == "cancel" || name == "auth_revoked"
				if (name == "put" || name == "patch") && len(data) > 0 {
					var env struct {
						Path string          `json:"path"`
						Data json.RawMessage `json:"data"`
					}
					if json.Unmarshal([]byte(strings.Join(data, "\n")), &env) == nil {
						ev := sseEvent{Name: name, Path: env.Path, Data: env.Data}
						select {
						case out <- ev:
						case <-sctx.Done():
							return
						}
					}
				}
				if stop {
					return
				}
				name, data = "", nil
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(line[len("event:"):])
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimPrefix(line[len("data:"):], " "))
			}
		}
	}()
	return out, nil
}

// recMap is the latest state of a node: child key -> its fields.
type recMap map[string]map[string]any

// apply folds one stream event into the map.
func (m recMap) apply(ev sseEvent) {
	if ev.Path == "/" || ev.Path == "" {
		var obj map[string]json.RawMessage
		if len(ev.Data) == 0 || json.Unmarshal(ev.Data, &obj) != nil {
			if ev.Name == "put" { // the whole node was removed or replaced by nothing
				for k := range m {
					delete(m, k)
				}
			}
			return
		}
		if ev.Name == "put" {
			for k := range m {
				delete(m, k)
			}
		}
		for k, raw := range obj {
			m.set(k, raw, ev.Name == "patch")
		}
		return
	}
	key := strings.TrimPrefix(ev.Path, "/")
	if key == "" || strings.Contains(key, "/") {
		return // deeper paths are not used by these screens
	}
	m.set(key, ev.Data, ev.Name == "patch")
}

func (m recMap) set(key string, raw json.RawMessage, merge bool) {
	if len(raw) == 0 || string(raw) == "null" {
		delete(m, key)
		return
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil {
		return
	}
	if merge {
		if old, ok := m[key]; ok {
			for k, v := range fields {
				old[k] = v
			}
			return
		}
	}
	m[key] = fields
}

// load replaces the map from REST rows, using idKey as the child key.
func (m recMap) load(rows []map[string]any, idKey string) {
	for k := range m {
		delete(m, k)
	}
	for _, r := range rows {
		if id := str(r, idKey); id != "" {
			m[id] = r
		}
	}
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func num(m map[string]any, k string) int64 {
	f, _ := m[k].(float64)
	return int64(f)
}

func boolean(m map[string]any, k string) bool {
	b, _ := m[k].(bool)
	return b
}

// has reports whether the key is present at all (for fields that default to true).
func has(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

// strs reads a string or an array of strings (chained text styles).
func strs(m map[string]any, k string) []string {
	switch v := m[k].(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

