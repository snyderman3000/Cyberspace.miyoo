package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// chatFake answers the C-Mail and cIRC endpoints and records what was sent.
type chatFake struct {
	mu      sync.Mutex
	base    *fakeCyberspace
	sent    []string // "dm:<id>:<text>" or "room:<slug>:<text>"
	typing  []string
	left    []string
	dmSent  int
	stream  http.HandlerFunc // serves the database stream when set
}

func now() int64 { return time.Now().UnixMilli() }

func (c *chatFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if strings.HasSuffix(p, ".json") {
		if c.stream != nil {
			c.stream(w, r)
			return
		}
		writeErr(w, 404, "NOT_FOUND")
		return
	}
	if !strings.HasPrefix(p, "/v1/cmail") && !strings.HasPrefix(p, "/v1/circ") {
		c.base.ServeHTTP(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer id-1" {
		writeErr(w, 401, "UNAUTHORIZED")
		return
	}
	body, _ := io.ReadAll(r.Body)
	var in map[string]any
	json.Unmarshal(body, &in)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case p == "/v1/cmail" && r.Method == "GET":
		writeEnv(w, 200, []map[string]any{
			{"conversationId": "c1", "otherUser": map[string]any{"userId": "u2", "username": "alice"}, "lastMessage": "hey there", "lastMessageAt": now() - 60000, "unreadCount": 2},
			{"conversationId": "c2", "otherUser": map[string]any{"userId": "u3", "username": "bob"}, "lastMessage": "old news", "lastMessageAt": now() - 9e6, "unreadCount": 0},
		}, nil)
	case p == "/v1/cmail" && r.Method == "POST":
		writeEnv(w, 201, map[string]any{"conversationId": "c3", "otherUser": map[string]any{"userId": "u4", "username": in["recipientUsername"]}}, nil)
	case p == "/v1/cmail/c1" && r.Method == "GET":
		writeEnv(w, 200, []map[string]any{
			{"id": "m1", "senderUsername": "alice", "content": "hey there", "timestamp": now() - 120000},
			{"id": "m2", "senderUsername": "me", "content": "*hi alice*, **bold** [site](https://x.y)", "timestamp": now() - 60000},
		}, nil)
	case p == "/v1/cmail/c1/read", p == "/v1/cmail/c1/typing" && r.Method == "DELETE":
		writeEnv(w, 200, map[string]any{"ok": true}, nil)
	case p == "/v1/cmail/c1/typing":
		writeEnv(w, 200, map[string]any{"ok": true, "heartbeatMs": 3000, "staleAfterMs": 9000}, nil)
	case strings.HasPrefix(p, "/v1/cmail/c1") && r.Method == "POST":
		c.dmSent++
		c.sent = append(c.sent, "dm:c1:"+fmt.Sprint(in["content"]))
		writeEnv(w, 201, map[string]any{"conversationId": "c1", "messageId": fmt.Sprintf("m%d", 2+c.dmSent)}, nil)
	case p == "/v1/circ" && r.Method == "GET":
		writeEnv(w, 200, []map[string]any{{"id": "r1", "slug": "general", "name": "General", "onlineCount": 3}}, nil)
	case p == "/v1/circ/general" && r.Method == "GET":
		writeEnv(w, 200, []map[string]any{
			{"id": "x1", "username": "lowbaud", "isChatAdmin": true, "content": "ahoy", "timestamp": now() - 30000},
		}, nil)
	case p == "/v1/circ/general" && r.Method == "POST":
		c.sent = append(c.sent, "room:general:"+fmt.Sprint(in["content"]))
		writeEnv(w, 201, map[string]any{"roomId": "general", "messageId": "x2"}, nil)
	case p == "/v1/circ/general/users":
		writeEnv(w, 200, []map[string]any{
			{"userId": "u1", "username": "lowbaud", "isChatAdmin": true, "lastSeen": now(), "lastActivity": nil},
			{"userId": "u9", "username": "zed", "isChatAdmin": false, "lastSeen": now(), "lastActivity": now() - 3600000},
		}, nil)
	case p == "/v1/circ/general/presence" && r.Method == "POST":
		writeEnv(w, 200, map[string]any{"ok": true, "heartbeatMs": 30000, "staleAfterMs": 180000, "idleAfterMs": 600000}, nil)
	case p == "/v1/circ/general/presence" && r.Method == "DELETE":
		c.left = append(c.left, "general")
		writeEnv(w, 200, map[string]any{"ok": true}, nil)
	default:
		writeErr(w, 404, "NOT_FOUND")
	}
}

func chatUI(t *testing.T) (*UI, *chatFake) {
	t.Helper()
	base, _ := newFake(t)
	cf := &chatFake{base: base}
	srv := httptest.NewServer(cf)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"email":"me@example.com","password":"hunter2"}`), 0o600)
	env := newEnv(dir)
	env.API.Base = srv.URL
	env.API.HTTP = srv.Client()
	env.API.Stream = srv.Client()
	u := NewUI(env, 640, 480)
	u.sync = true
	// the fake login answers with an id token of id-1 for /v1/cmail too
	u.Start()
	if u.scr != scrList {
		t.Fatalf("login failed: %s", u.loginErr)
	}
	return u, cf
}

func TestMailListUnreadFirst(t *testing.T) {
	u, _ := chatUI(t)
	press(u, BtnR1, BtnR1, BtnR1) // FEED → TOPICS → ALERTS → MAIL
	if u.tab != tabMail {
		t.Fatalf("tab %d", u.tab)
	}
	v := u.view()
	if len(v.mail) != 2 || v.mail[0].other != "alice" || v.mail[0].unread != 2 || v.mail[1].other != "bob" {
		t.Fatalf("mail order: %+v", v.mail)
	}
	if u.mailUnread() != 2 {
		t.Fatalf("mail unread %d", u.mailUnread())
	}
}

func TestOpenDMLoadsHistoryAndClearsUnread(t *testing.T) {
	u, _ := chatUI(t)
	press(u, BtnR1, BtnR1, BtnR1, BtnA) // open alice
	if u.scr != scrChat || u.chat == nil || u.chat.id != "c1" {
		t.Fatalf("want the chat open, scr=%v", u.scr)
	}
	if len(u.chat.msgs) != 2 || !u.chat.loaded {
		t.Fatalf("history not loaded: %d", len(u.chat.msgs))
	}
	if n := num(u.convs["c1"], "unreadCount"); n != 0 {
		t.Fatalf("unread should clear, got %d", n)
	}
	lines := u.chat.lines
	if len(lines) == 0 {
		t.Fatal("no lines built")
	}
	// "mine" lines are marked, and markdown is stripped
	var mine, joined string
	for _, l := range lines {
		joined += l.text + "|"
		if l.mine && l.kind == lnHead {
			mine = l.text
		}
	}
	if !strings.HasPrefix(mine, "me") || !strings.Contains(joined, "hi alice, bold site <https://x.y>") {
		t.Fatalf("lines: %q / %q", mine, joined)
	}
	press(u, BtnB) // back to the list
	if u.scr != scrList || u.chat != nil {
		t.Fatal("B should leave the chat")
	}
}

func TestSendDMShowsItAndTalksToServer(t *testing.T) {
	u, cf := chatUI(t)
	press(u, BtnR1, BtnR1, BtnR1, BtnA, BtnA) // open alice, then SAY
	if u.scr != scrEditor {
		t.Fatalf("A should open the editor, scr=%v", u.scr)
	}
	typeText(u, "  see you soon  ")
	press(u, BtnStart)
	cf.mu.Lock()
	sent := append([]string(nil), cf.sent...)
	cf.mu.Unlock()
	if len(sent) != 1 || sent[0] != "dm:c1:see you soon" {
		t.Fatalf("sent %v", sent)
	}
	if u.scr != scrChat {
		t.Fatalf("after sending, back in the chat, got %v", u.scr)
	}
	if _, ok := u.chat.msgs["m3"]; !ok {
		t.Fatalf("own message should show right away: %v", u.chat.msgs)
	}
	if len(u.drafts) != 0 {
		t.Fatalf("draft should clear after sending: %v", u.drafts)
	}
}

func TestStartNewConversation(t *testing.T) {
	u, _ := chatUI(t)
	press(u, BtnR1, BtnR1, BtnR1, BtnX) // X on MAIL → who to write to
	if u.scr != scrEditor {
		t.Fatal("X on MAIL should ask who to write to")
	}
	typeText(u, "@carol")
	press(u, BtnStart)
	if u.scr != scrChat || u.chat == nil || u.chat.id != "c3" || u.chat.title != "@carol" {
		t.Fatalf("want a chat with carol, got scr=%v chat=%+v", u.scr, u.chat)
	}
}

func TestRoomPresenceSendAndLeave(t *testing.T) {
	u, cf := chatUI(t)
	press(u, BtnR1, BtnR1, BtnR1, BtnR1, BtnA) // CIRC → general
	if u.scr != scrChat || !u.chat.room {
		t.Fatalf("want the room open, scr=%v", u.scr)
	}
	users := u.chatUsers(u.chat)
	if len(users) != 2 || users[0].name != "lowbaud" || !users[0].admin || users[1].name != "zed" || !users[1].idle {
		t.Fatalf("who's here: %+v", users)
	}
	press(u, BtnY) // who's here panel
	if !u.chat.showUsers {
		t.Fatal("Y should show who's here")
	}
	press(u, BtnB)
	if u.chat == nil || u.chat.showUsers {
		t.Fatal("B closes the panel, not the room")
	}
	press(u, BtnA)
	typeText(u, "hello general")
	press(u, BtnStart)
	cf.mu.Lock()
	sent := append([]string(nil), cf.sent...)
	cf.mu.Unlock()
	if len(sent) != 1 || sent[0] != "room:general:hello general" {
		t.Fatalf("sent %v", sent)
	}
	press(u, BtnB)
	cf.mu.Lock()
	left := len(cf.left)
	cf.mu.Unlock()
	time.Sleep(20 * time.Millisecond) // the leave goes out in the background
	cf.mu.Lock()
	left = len(cf.left)
	cf.mu.Unlock()
	if u.scr != scrList || left != 1 {
		t.Fatalf("leaving should tell the server once: scr=%v left=%d", u.scr, left)
	}
}

func TestRoomListOpens(t *testing.T) {
	u, _ := chatUI(t)
	press(u, BtnR1, BtnR1, BtnR1, BtnR1)
	if u.tab != tabRooms || len(u.view().rooms) != 1 {
		t.Fatalf("rooms %v", u.view().rooms)
	}
	press(u, BtnX) // rooms have nothing to write here
	if u.scr != scrList {
		t.Fatal("X on CIRC should do nothing")
	}
}

func TestDMTypingIndicator(t *testing.T) {
	u, cf := chatUI(t)
	press(u, BtnR1, BtnR1, BtnR1, BtnA)
	u.chat.presence = recMap{"u2": {"username": "alice", "typing": true, "timestamp": float64(now())}}
	if got := u.typingName(u.chat); got != "alice" {
		t.Fatalf("typing name %q", got)
	}
	u.chat.presence["u2"]["timestamp"] = float64(now() - 60000) // gone stale
	if got := u.typingName(u.chat); got != "" {
		t.Fatalf("stale typing should not show, got %q", got)
	}
	// typing in the editor announces it; going quiet clears it
	press(u, BtnA)
	typeText(u, "h")
	u.chatTypingTick(u.chat, "h")
	if !u.chat.typingOn {
		t.Fatal("typing should be announced")
	}
	u.chat.typingKeyAt = time.Now().Add(-time.Minute)
	u.Tick()
	if u.chat.typingOn {
		t.Fatal("typing should clear after a quiet spell")
	}
	_ = cf
}

// ---- live feed ----

func TestRecMapRules(t *testing.T) {
	m := recMap{}
	m.apply(sseEvent{Name: "put", Path: "/", Data: json.RawMessage(`{"a":{"content":"one","timestamp":1},"b":{"content":"two","timestamp":2}}`)})
	if len(m) != 2 {
		t.Fatalf("root put: %v", m)
	}
	m.apply(sseEvent{Name: "patch", Path: "/a", Data: json.RawMessage(`{"deleted":true,"content":"[DELETED]"}`)})
	if m["a"]["content"] != "[DELETED]" || num(m["a"], "timestamp") != 1 || !boolean(m["a"], "deleted") {
		t.Fatalf("patch should merge: %v", m["a"])
	}
	m.apply(sseEvent{Name: "put", Path: "/c", Data: json.RawMessage(`{"content":"three","timestamp":3}`)})
	m.apply(sseEvent{Name: "put", Path: "/b", Data: json.RawMessage(`null`)})
	if _, ok := m["b"]; ok || len(m) != 2 {
		t.Fatalf("null should delete: %v", m)
	}
	// one user leaving is a patch at the root that touches only that key
	m.apply(sseEvent{Name: "patch", Path: "/", Data: json.RawMessage(`{"c":null}`)})
	if _, ok := m["c"]; ok || len(m) != 1 {
		t.Fatalf("root patch should remove just that key: %v", m)
	}
	m.apply(sseEvent{Name: "put", Path: "/", Data: json.RawMessage(`null`)})
	if len(m) != 0 {
		t.Fatal("root put null empties the node")
	}
}

// streamServer sends the given SSE body for each connection, in order.
func streamServer(t *testing.T, bodies ...string) *httptest.Server {
	var mu sync.Mutex
	n := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		i := n
		n++
		mu.Unlock()
		if r.Header.Get("Accept") != "text/event-stream" || !strings.Contains(r.URL.RawQuery, "auth=") {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if i < len(bodies) {
			io.WriteString(w, bodies[i])
		}
		w.(http.Flusher).Flush()
		if i >= len(bodies)-1 { // the last connection stays open until the client leaves
			<-r.Context().Done()
		}
	}))
}

func TestOpenStreamParsesEvents(t *testing.T) {
	srv := streamServer(t, ": hello\n\n"+
		"event: put\ndata: {\"path\":\"/\",\"data\":{\"m1\":{\"senderUsername\":\"alice\",\"content\":\"hi\",\"timestamp\":5}}}\n\n"+
		"event: keep-alive\ndata: null\n\n"+
		"event: put\ndata: {\"path\":\"/m2\",\"data\":{\"content\":\"yo\",\"timestamp\":6}}\n\n")
	defer srv.Close()
	c := &Client{Base: srv.URL, Stream: srv.Client()}
	c.tok = Tokens{IDToken: "id-1", RTDBURL: srv.URL}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := c.openStream(ctx, "/dm_messages/c1", streamQueryJSON)
	if err != nil {
		t.Fatal(err)
	}
	m := recMap{}
	for i := 0; i < 2; i++ {
		select {
		case ev := <-events:
			m.apply(ev)
		case <-time.After(5 * time.Second):
			t.Fatal("no events")
		}
	}
	if m["m1"]["content"] != "hi" || m["m2"]["content"] != "yo" {
		t.Fatalf("events: %v", m)
	}
}

func TestOpenStreamEndsOnRevoke(t *testing.T) {
	srv := streamServer(t, "event: auth_revoked\ndata: {}\n\n")
	defer srv.Close()
	c := &Client{Base: srv.URL, Stream: srv.Client()}
	c.tok = Tokens{IDToken: "id-1", RTDBURL: srv.URL}
	events, err := c.openStream(context.Background(), "/x", "")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("revoked stream should close without an event")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not end")
	}
}

func TestFollowReconnectsAfterDrop(t *testing.T) {
	// first connection delivers one message and drops; the second delivers another
	srv := streamServer(t,
		"event: put\ndata: {\"path\":\"/m1\",\"data\":{\"content\":\"first\",\"timestamp\":1}}\n\n",
		"event: put\ndata: {\"path\":\"/m2\",\"data\":{\"content\":\"second\",\"timestamp\":2}}\n\n")
	defer srv.Close()
	u, _ := testUI(t, "")
	u.api.Stream = srv.Client()
	u.api.SetTokens(Tokens{IDToken: "id-1", RefreshToken: "rt-1", RTDBURL: srv.URL})
	u.sync = false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := recMap{}
	var statuses []string
	go u.follow(ctx, "/dm_messages/c1", "", func(ev sseEvent) { m.apply(ev) },
		func(s string) { statuses = append(statuses, s) })
	deadline := time.After(10 * time.Second)
	for len(m) < 2 {
		select {
		case f := <-u.post:
			f()
		case <-deadline:
			t.Fatalf("only got %v", m)
		}
	}
	cancel()
	if m["m1"]["content"] != "first" || m["m2"]["content"] != "second" {
		t.Fatalf("messages: %v", m)
	}
	if !strings.Contains(strings.Join(statuses, ","), "reconnecting 1/6") {
		t.Fatalf("should report reconnecting, got %v", statuses)
	}
}

func TestEveryChatSceneDraws(t *testing.T) {
	for _, th := range themes {
		for _, s := range []string{"mail", "dm", "rooms", "room", "users"} {
			env := newEnv(t.TempDir())
			env.Config.Theme = th.Name
			env.Now = func() time.Time { return demoNow }
			u := NewUI(env, 640, 480)
			if err := demoScene(u, s); err != nil {
				t.Fatal(err)
			}
			u.Draw(NewCanvas(640, 480))
			u2 := NewUI(env, 752, 560)
			demoScene(u2, s)
			u2.Draw(NewCanvas(752, 560))
		}
	}
}
