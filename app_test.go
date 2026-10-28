package main

import (
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

// fakeCyberspace imitates the parts of api.cyberspace.online that Cyberspace.miyoo uses.
type fakeCyberspace struct {
	mu        sync.Mutex
	t         *testing.T
	idToken   string
	refreshes int
	posts     []map[string]any
	replies   map[string][]map[string]any
	created   []map[string]any
	replied   []map[string]any
	readAll   bool
	marked    []string
	expireNext bool
	offline    int // drop this many requests, like Wi-Fi that isn't up yet
}

func newFake(t *testing.T) (*fakeCyberspace, *httptest.Server) {
	f := &fakeCyberspace{t: t, idToken: "id-1", replies: map[string][]map[string]any{}}
	for i := 0; i < 25; i++ {
		f.posts = append(f.posts, map[string]any{
			"postId": fmt.Sprintf("p%02d", i), "authorUsername": fmt.Sprintf("user%d", i%4),
			"content": fmt.Sprintf("entry number %d", i), "topics": []string{"retro"},
			"repliesCount": i % 3, "createdAt": time.Now().Add(-time.Duration(i) * time.Hour).Format(time.RFC3339),
		})
	}
	f.replies["p00"] = []map[string]any{
		{"replyId": "r1", "postId": "p00", "authorUsername": "a", "content": "first", "createdAt": "2026-10-24T10:00:00Z"},
		{"replyId": "r3", "postId": "p00", "authorUsername": "c", "content": "child of first", "parentReplyId": "r1", "createdAt": "2026-10-24T10:20:00Z"},
		{"replyId": "r2", "postId": "p00", "authorUsername": "b", "content": "second", "createdAt": "2026-10-24T10:10:00Z"},
	}
	return f, httptest.NewServer(f)
}

func writeEnv(w http.ResponseWriter, status int, data any, cursor any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"data": data, "cursor": cursor})
}

func writeErr(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": strings.ToLower(code)}})
}

func (f *fakeCyberspace) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var in map[string]any
	json.Unmarshal(body, &in)
	p := r.URL.Path
	if f.offline > 0 {
		f.offline--
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
		return
	}
	switch {
	case p == "/v1/auth/login":
		if in["email"] == "me@example.com" && in["password"] == "hunter2" {
			writeEnv(w, 200, map[string]string{"idToken": f.idToken, "refreshToken": "rt-1"}, nil)
		} else {
			writeErr(w, 401, "UNAUTHORIZED")
		}
		return
	case p == "/v1/auth/refresh":
		if in["refreshToken"] != "rt-1" {
			writeErr(w, 401, "UNAUTHORIZED")
			return
		}
		f.refreshes++
		f.idToken = fmt.Sprintf("id-%d", f.refreshes+1)
		writeEnv(w, 200, map[string]string{"idToken": f.idToken}, nil)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.idToken || f.expireNext {
		f.expireNext = false
		writeErr(w, 401, "UNAUTHORIZED")
		return
	}
	q := r.URL.Query()
	switch {
	case p == "/v1/posts" && r.Method == "GET":
		start := 0
		if c := q.Get("cursor"); c != "" {
			for i, po := range f.posts {
				if po["postId"] == c {
					start = i + 1
				}
			}
		}
		end := min(start+20, len(f.posts))
		var cur any
		if end < len(f.posts) {
			cur = f.posts[end-1]["postId"]
		}
		writeEnv(w, 200, f.posts[start:end], cur)
	case p == "/v1/posts" && r.Method == "POST":
		f.created = append(f.created, in)
		writeEnv(w, 201, map[string]string{"postId": "new1", "slug": "x"}, nil)
	case strings.HasPrefix(p, "/v1/posts/") && strings.HasSuffix(p, "/replies"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/v1/posts/"), "/replies")
		writeEnv(w, 200, f.replies[id], nil)
	case strings.HasPrefix(p, "/v1/posts/"):
		id := strings.TrimPrefix(p, "/v1/posts/")
		for _, po := range f.posts {
			if po["postId"] == id {
				writeEnv(w, 200, po, nil)
				return
			}
		}
		writeErr(w, 404, "NOT_FOUND")
	case p == "/v1/replies" && r.Method == "POST":
		f.replied = append(f.replied, in)
		id := in["postId"].(string)
		f.replies[id] = append(f.replies[id], map[string]any{"replyId": "rnew", "postId": id, "authorUsername": "me",
			"content": in["content"], "parentReplyId": in["parentReplyId"], "createdAt": time.Now().Format(time.RFC3339)})
		writeEnv(w, 201, map[string]string{"replyId": "rnew"}, nil)
	case strings.HasPrefix(p, "/v1/replies/"):
		writeEnv(w, 200, map[string]any{"replyId": "r3", "postId": "p00", "authorUsername": "c", "content": "child of first"}, nil)
	case p == "/v1/users/me":
		writeEnv(w, 200, map[string]any{"username": "me"}, nil)
	case p == "/v1/notifications/unread-count":
		writeEnv(w, 200, map[string]any{"count": 2}, nil)
	case p == "/v1/notifications/read-all":
		f.readAll = true
		writeEnv(w, 200, map[string]any{"updated": 2}, nil)
	case p == "/v1/notifications" && r.Method == "GET":
		writeEnv(w, 200, []map[string]any{
			{"id": "n1", "type": "reply", "actorUsername": "c", "targetId": "r3", "targetType": "reply", "createdAt": "2026-10-24T10:20:00Z"},
			{"id": "n2", "type": "new_follower", "actorUsername": "d", "createdAt": "2026-10-24T09:00:00Z"},
		}, nil)
	case strings.HasPrefix(p, "/v1/notifications/") && r.Method == "PATCH":
		f.marked = append(f.marked, strings.TrimPrefix(p, "/v1/notifications/"))
		writeEnv(w, 200, map[string]any{}, nil)
	case p == "/v1/topics":
		writeEnv(w, 200, []map[string]any{{"topicId": "retro", "postsCount": 25}}, nil)
	case strings.HasPrefix(p, "/v1/topics/"):
		writeEnv(w, 200, f.posts[:3], nil)
	default:
		writeErr(w, 404, "NOT_FOUND")
	}
}

// testUI builds a UI against the fake server with background work run inline.
func testUI(t *testing.T, cfg string) (*UI, *fakeCyberspace) {
	t.Helper()
	f, srv := newFake(t)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	if cfg != "" {
		os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600)
	}
	env := newEnv(dir)
	env.API.Base = srv.URL
	env.API.HTTP = srv.Client()
	u := NewUI(env, 640, 480)
	u.sync = true
	return u, f
}

func press(u *UI, keys ...int) {
	for _, k := range keys {
		u.Key(k)
		u.frame++
	}
	c := NewCanvas(u.W, u.H)
	u.Draw(c) // every screen must draw without panicking
}

func typeText(u *UI, s string) {
	for _, r := range s {
		u.ed.insert(string(r))
	}
}

func TestLoginTypedOnDevice(t *testing.T) {
	u, _ := testUI(t, "")
	u.Start()
	if u.scr != scrLogin {
		t.Fatalf("want login screen, got %v", u.scr)
	}
	press(u, BtnA) // email
	typeText(u, "me@example.com")
	press(u, BtnStart) // NEXT → password row
	if u.loginRow != lgPassword || u.email != "me@example.com" {
		t.Fatalf("email not taken: row=%d email=%q", u.loginRow, u.email)
	}
	press(u, BtnA)
	typeText(u, "wrong")
	press(u, BtnStart)
	if u.scr != scrLogin || !strings.Contains(u.loginErr, "WRONG") {
		t.Fatalf("bad password should stay on login with an error, got scr=%v err=%q", u.scr, u.loginErr)
	}
	u.password = "hunter2"
	press(u, BtnStart)
	if u.scr != scrList {
		t.Fatalf("want feed after login, got %v (%s)", u.scr, u.loginErr)
	}
	if u.me.Username != "me" || u.unread != 2 {
		t.Fatalf("me=%q unread=%d", u.me.Username, u.unread)
	}
	if n := len(u.view().posts); n != 20 {
		t.Fatalf("first page should have 20 entries, got %d", n)
	}
	b, err := os.ReadFile(filepath.Join(u.env.DataDir, "session.json"))
	if err != nil || !strings.Contains(string(b), "rt-1") {
		t.Fatalf("session not saved: %v %s", err, b)
	}
	if strings.Contains(string(b), "hunter2") {
		t.Fatal("password must never be stored")
	}
}

func TestPasswordInConfigIsErased(t *testing.T) {
	u, _ := testUI(t, `{"theme":"vt320","email":"me@example.com","password":"hunter2"}`)
	u.Start()
	if u.scr != scrList {
		t.Fatalf("auto-login failed: %v %s", u.scr, u.loginErr)
	}
	b, _ := os.ReadFile(filepath.Join(u.env.DataDir, "config.json"))
	if strings.Contains(string(b), "hunter2") || !strings.Contains(string(b), "me@example.com") || !strings.Contains(string(b), "vt320") {
		t.Fatalf("config after login: %s", b)
	}
}

func TestSessionRestoreAndTokenRefresh(t *testing.T) {
	u, f := testUI(t, "")
	u.api.SetTokens(Tokens{IDToken: "stale", RefreshToken: "rt-1"})
	u.Start()
	if u.scr != scrList || f.refreshes != 1 {
		t.Fatalf("restore: scr=%v refreshes=%d err=%s", u.scr, f.refreshes, u.loginErr)
	}
	// the server expires the token mid-session: the client refreshes and retries
	f.expireNext = true
	press(u, BtnY)
	if f.refreshes != 2 || len(u.view().posts) != 20 || u.view().err != "" {
		t.Fatalf("refresh-on-401 failed: refreshes=%d posts=%d err=%q", f.refreshes, len(u.view().posts), u.view().err)
	}
}

func TestRestoreWaitsForWiFi(t *testing.T) {
	old := restoreBackoff
	restoreBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { restoreBackoff = old })

	// Wi-Fi comes up after two failed tries: no login screen
	u, f := testUI(t, "")
	u.api.SetTokens(Tokens{IDToken: "stale", RefreshToken: "rt-1"})
	saveSession(u.env.sessionPath(), u.api.Tokens())
	f.offline = 2
	u.Start()
	if u.scr != scrList {
		t.Fatalf("should restore once the network is up, got scr=%v err=%q", u.scr, u.loginErr)
	}

	// never comes up: login screen, but the session is kept for next launch
	u, f = testUI(t, "")
	u.api.SetTokens(Tokens{IDToken: "stale", RefreshToken: "rt-1"})
	saveSession(u.env.sessionPath(), u.api.Tokens())
	f.offline = 100
	u.Start()
	if u.scr != scrLogin || !strings.Contains(u.loginErr, "STILL LOGGED IN") {
		t.Fatalf("got scr=%v err=%q", u.scr, u.loginErr)
	}
	if _, ok := loadSession(u.env.sessionPath()); !ok {
		t.Fatal("a network failure must not forget the session")
	}
}

func TestBadSessionGoesToLogin(t *testing.T) {
	u, _ := testUI(t, "")
	u.api.SetTokens(Tokens{IDToken: "x", RefreshToken: "revoked"})
	u.Start()
	if u.scr != scrLogin || !strings.Contains(u.loginErr, "EXPIRED") {
		t.Fatalf("got scr=%v err=%q", u.scr, u.loginErr)
	}
	if _, err := os.Stat(filepath.Join(u.env.DataDir, "session.json")); err == nil {
		t.Fatal("revoked session should be forgotten")
	}
}

func loggedIn(t *testing.T) (*UI, *fakeCyberspace) {
	u, f := testUI(t, `{"email":"me@example.com","password":"hunter2"}`)
	u.Start()
	if u.scr != scrList {
		t.Fatalf("login failed: %s", u.loginErr)
	}
	return u, f
}

func TestFeedPaginates(t *testing.T) {
	u, _ := loggedIn(t)
	for i := 0; i < 19; i++ {
		press(u, BtnDown)
	}
	v := u.view()
	if len(v.posts) != 25 || v.more {
		t.Fatalf("scrolling to the end should load page 2: %d posts, more=%v", len(v.posts), v.more)
	}
	press(u, BtnRight, BtnRight)
	if v.sel != 24 {
		t.Fatalf("page jump should stop at the last entry, sel=%d", v.sel)
	}
}

func TestThreadOrder(t *testing.T) {
	u, _ := loggedIn(t)
	press(u, BtnA)
	if u.scr != scrPost {
		t.Fatal("A should open the entry")
	}
	var got []string
	for _, r := range u.pv.replies {
		got = append(got, fmt.Sprintf("%s:%d", r.ID, r.Depth))
	}
	if strings.Join(got, ",") != "r1:0,r3:1,r2:0" {
		t.Fatalf("thread order %v", got)
	}
	// walk down through every reply and back up
	for i := 0; i < 10; i++ {
		press(u, BtnDown)
	}
	if u.pv.sel != 3 {
		t.Fatalf("down should reach the last reply, sel=%d", u.pv.sel)
	}
	for i := 0; i < 10; i++ {
		press(u, BtnUp)
	}
	if u.pv.sel != 0 || u.pv.scroll != 0 {
		t.Fatalf("up should return to the entry, sel=%d scroll=%d", u.pv.sel, u.pv.scroll)
	}
}

func TestReplyToReply(t *testing.T) {
	u, f := loggedIn(t)
	press(u, BtnA)       // open p00
	press(u, BtnDown)    // r1
	press(u, BtnA)       // reply to r1
	if u.scr != scrEditor || !strings.Contains(u.ed.Title, "@A") {
		t.Fatalf("want reply editor to @a, got %v %q", u.scr, u.ed.Title)
	}
	typeText(u, "hello from the miyoo")
	press(u, BtnStart) // SEND → confirm
	if u.confirm == nil {
		t.Fatal("sending should ask first")
	}
	press(u, BtnA)
	if len(f.replied) != 1 || f.replied[0]["parentReplyId"] != "r1" || f.replied[0]["content"] != "hello from the miyoo" || f.replied[0]["postId"] != "p00" {
		t.Fatalf("reply sent: %+v", f.replied)
	}
	if u.scr != scrPost || len(u.pv.replies) != 4 || u.pv.sel < 1 || u.pv.replies[u.pv.sel-1].ID != "rnew" || u.pv.replies[u.pv.sel-1].Depth != 1 {
		t.Fatalf("after sending, the new reply should be selected under its parent: scr=%v sel=%d n=%d", u.scr, u.pv.sel, len(u.pv.replies))
	}
	if len(u.drafts) != 0 {
		t.Fatal("draft should be cleared after sending")
	}
}

func TestCancelledReplyKeepsDraft(t *testing.T) {
	u, f := loggedIn(t)
	press(u, BtnA, BtnA)
	typeText(u, "half a thought")
	press(u, BtnSelect)
	if u.scr != scrPost || len(f.replied) != 0 {
		t.Fatal("select should back out without sending")
	}
	press(u, BtnA)
	if u.ed.String() != "half a thought" {
		t.Fatalf("draft lost: %q", u.ed.String())
	}
	b, _ := os.ReadFile(filepath.Join(u.env.DataDir, "drafts.json"))
	if !strings.Contains(string(b), "half a thought") {
		t.Fatal("draft not saved to disk")
	}
}

func TestNewEntry(t *testing.T) {
	u, f := loggedIn(t)
	press(u, BtnX)
	if u.scr != scrCompose {
		t.Fatal("X should open compose")
	}
	press(u, BtnStart)
	if u.confirm != nil || len(f.created) != 0 {
		t.Fatal("empty entry must not send")
	}
	press(u, BtnA) // body
	typeText(u, "Line one")
	press(u, BtnR2)
	typeText(u, "line two")
	press(u, BtnStart)
	press(u, BtnDown, BtnDown, BtnA) // topics
	typeText(u, "Retro, MIYOO #linux extra")
	press(u, BtnStart)
	press(u, BtnDown, BtnRight) // public on
	press(u, BtnStart, BtnA)
	if len(f.created) != 1 {
		t.Fatal("entry not sent")
	}
	got := f.created[0]
	if got["content"] != "Line one\nline two" || got["isPublic"] != true || fmt.Sprint(got["topics"]) != "[retro miyoo linux]" {
		t.Fatalf("sent %+v", got)
	}
	if u.scr != scrList || u.comp.body != "" {
		t.Fatal("after sending, back to a fresh feed with an empty draft")
	}
}

func TestAlerts(t *testing.T) {
	u, f := loggedIn(t)
	press(u, BtnR1, BtnR1) // FEED → TOPICS → ALERTS
	if u.tab != tabAlerts || len(u.view().notifs) != 2 {
		t.Fatalf("tab=%d notifs=%d", u.tab, len(u.view().notifs))
	}
	press(u, BtnA) // reply alert → opens the entry at that reply
	if u.scr != scrPost || u.pv.post.ID != "p00" || u.pv.sel != 2 {
		t.Fatalf("alert should open p00 at r3: scr=%v post=%q sel=%d", u.scr, u.pv.post.ID, u.pv.sel)
	}
	if len(f.marked) != 1 || f.marked[0] != "n1" || u.unread != 1 {
		t.Fatalf("marked=%v unread=%d", f.marked, u.unread)
	}
	press(u, BtnB, BtnX)
	if !f.readAll || u.unread != 0 {
		t.Fatal("X should mark everything read")
	}
}

func TestTopicDrillDown(t *testing.T) {
	u, _ := loggedIn(t)
	press(u, BtnR1, BtnA)
	v := u.view()
	if v.kind != lvTopicPosts || v.arg != "retro" || len(v.posts) != 3 {
		t.Fatalf("topic view %+v", v)
	}
	press(u, BtnX)
	if u.comp.topics != "retro" {
		t.Fatal("new entry from a topic should pre-fill it")
	}
	press(u, BtnB, BtnB)
	if u.view().kind != lvTopics {
		t.Fatal("B should return to the topic list")
	}
}

func TestThemeCycleSaves(t *testing.T) {
	u, _ := loggedIn(t)
	press(u, BtnSelect)
	if u.th.Name != "c64" {
		t.Fatalf("theme %s", u.th.Name)
	}
	press(u, BtnA) // post view with the C64 border
	press(u, BtnSelect)
	b, _ := os.ReadFile(filepath.Join(u.env.DataDir, "config.json"))
	if !strings.Contains(string(b), "vt320") {
		t.Fatalf("theme not saved: %s", b)
	}
}

func TestEditorCursor(t *testing.T) {
	e := newEditor("t", "helo", 10, false)
	e.Key(BtnL1)
	e.insert("l")
	if e.String() != "hello" {
		t.Fatal(e.String())
	}
	e.Key(BtnR1)
	e.Key(BtnR2) // no newlines in single-line fields
	e.insert("123456789")
	if e.String() != "hello12345" {
		t.Fatalf("max length: %q", e.String())
	}
	e = newEditor("t", "", 100, true)
	e.Key(BtnY) // shift once
	e.row, e.col = 1, 0
	e.Key(BtnA)
	e.Key(BtnA)
	if e.String() != "Qq" {
		t.Fatalf("one-shot shift: %q", e.String())
	}
	long := strings.Repeat("word ", 40)
	e = newEditor("t", long, 0, true)
	starts, line, _ := e.layout(30)
	if len(starts) < 6 || line != len(starts)-1 {
		t.Fatalf("wrap: %d lines, cursor on %d", len(starts), line)
	}
}

func TestMarkdown(t *testing.T) {
	f := loadFont(vt323TTF, 20)
	ls := renderMarkdown("# Hi\n**bold** and [a link](https://x.y)\n\n```\n  /\\_/\\\n ( o.o )\n```\n> quoted", f, 400)
	var got []string
	for _, l := range ls {
		got = append(got, l.s)
	}
	j := strings.Join(got, "|")
	for _, want := range []string{"HI", "bold and a link <https://x.y>", "  /\\_/\\", " ( o.o )", "│ quoted"} {
		if !strings.Contains(j, want) {
			t.Errorf("missing %q in %q", want, j)
		}
	}
	if p := preview("```\nart\n```\nhello **there**"); p != "[art] hello there" {
		t.Errorf("preview %q", p)
	}
	if fmt.Sprint(cleanTopics("Retro, retro  #Linux,a,b")) != "[retro linux a]" {
		t.Error(cleanTopics("Retro, retro  #Linux,a,b"))
	}
}

func TestEveryDemoSceneDraws(t *testing.T) {
	for _, th := range themes {
		for _, s := range []string{"login", "dial", "feed", "feed2", "topics", "alerts", "post", "post2", "art", "compose", "editor", "confirm"} {
			env := newEnv(t.TempDir())
			env.Config.Theme = th.Name
			env.Now = func() time.Time { return demoNow }
			u := NewUI(env, 640, 480)
			if err := demoScene(u, s); err != nil {
				t.Fatal(err)
			}
			u.Draw(NewCanvas(640, 480))
			u2 := NewUI(env, 752, 560) // Miyoo Mini v4 resolution
			demoScene(u2, s)
			u2.Draw(NewCanvas(752, 560))
		}
	}
}

// The device runs background work on goroutines; drive it the same way.
func TestAsyncLoop(t *testing.T) {
	u, _ := testUI(t, `{"email":"me@example.com","password":"hunter2"}`)
	u.sync = false
	u.Start()
	deadline := time.After(5 * time.Second)
	c := NewCanvas(640, 480)
	for u.scr != scrList || u.view().loading || u.me.Username == "" {
		select {
		case f := <-u.post:
			f()
		case <-deadline:
			t.Fatalf("stuck: scr=%v", u.scr)
		}
		u.frame++
		u.Tick()
		u.Draw(c)
	}
	u.Key(BtnA)
	for u.pv.loading {
		(<-u.post)()
		u.Draw(c)
	}
	if len(u.pv.replies) != 3 {
		t.Fatalf("replies %d", len(u.pv.replies))
	}
}
