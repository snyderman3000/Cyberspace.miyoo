package main

// A small client for the cyberspace.online API (v0.8). Shapes follow the
// published API reference and the open-source cyber-tui client.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultAPI = "https://api.cyberspace.online"

type APIError struct {
	Code    string
	Message string
	Status  int
}

func (e *APIError) Error() string { return fmt.Sprintf("%s (%d): %s", e.Code, e.Status, e.Message) }

var ErrUnauthorized = &APIError{Code: "UNAUTHORIZED", Status: 401, Message: "session expired - log in again"}

// Friendly turns an error into a short line for the screen.
func Friendly(err error) string {
	var ae *APIError
	if errors.As(err, &ae) {
		switch ae.Code {
		case "RATE_LIMITED":
			return "SLOW DOWN - THE SERVER'S RATE LIMIT WAS HIT. TRY AGAIN IN A MINUTE."
		case "EMAIL_NOT_VERIFIED":
			return "VERIFY YOUR EMAIL ON CYBERSPACE.ONLINE FIRST."
		case "UNAUTHORIZED":
			return "SESSION EXPIRED - LOG IN AGAIN."
		case "INVALID_CREDENTIALS":
			return "WRONG EMAIL OR PASSWORD."
		}
		if ae.Message != "" {
			return strings.ToUpper(ae.Message)
		}
		return strings.ToUpper(ae.Code)
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "no such host"), strings.Contains(s, "network is unreachable"), strings.Contains(s, "connection refused"):
		return "NO CARRIER - CHECK WI-FI."
	case strings.Contains(s, "Timeout"), strings.Contains(s, "timeout"):
		return "NO CARRIER - THE SERVER TIMED OUT."
	case strings.Contains(s, "x509"), strings.Contains(s, "certificate"):
		return "SECURE CONNECTION FAILED - CHECK THE DEVICE CLOCK."
	}
	return strings.ToUpper(s)
}

type Tokens struct {
	IDToken      string `json:"idToken"`
	RefreshToken string `json:"refreshToken"`
	RTDBURL      string `json:"rtdbUrl,omitempty"` // live-update database, from login/refresh
}

// flexTime accepts RFC3339, epoch-ms, or a Firestore {_seconds} object.
type flexTime struct{ time.Time }

func (t *flexTime) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	switch b[0] {
	case '"':
		var s string
		if json.Unmarshal(b, &s) == nil {
			if p, err := time.Parse(time.RFC3339Nano, s); err == nil {
				t.Time = p
			}
		}
	case '{':
		var fs struct {
			S  int64 `json:"_seconds"`
			S2 int64 `json:"seconds"`
		}
		if json.Unmarshal(b, &fs) == nil {
			if fs.S == 0 {
				fs.S = fs.S2
			}
			if fs.S != 0 {
				t.Time = time.Unix(fs.S, 0)
			}
		}
	default:
		var ms int64
		if json.Unmarshal(b, &ms) == nil && ms > 0 {
			t.Time = time.UnixMilli(ms)
		}
	}
	return nil
}

type Post struct {
	ID           string   `json:"postId"`
	Author       string   `json:"authorUsername"`
	Content      string   `json:"content"`
	Title        string   `json:"title"`
	Slug         string   `json:"slug"`
	GuildSlug    string   `json:"guildSlug"`
	Topics       []string `json:"topics"`
	RepliesCount int      `json:"repliesCount"`
	Bookmarks    int      `json:"bookmarksCount"`
	IsPublic     bool     `json:"isPublic"`
	IsNSFW       bool     `json:"isNSFW"`
	Deleted      bool     `json:"deleted"`
	CreatedAt    flexTime `json:"createdAt"`
	Attachments  []struct {
		Type   string `json:"type"`
		Src    string `json:"src"`
		Artist string `json:"artist"`
		Title  string `json:"title"`
	} `json:"attachments"`
}

type Reply struct {
	ID        string   `json:"replyId"`
	PostID    string   `json:"postId"`
	Author    string   `json:"authorUsername"`
	Content   string   `json:"content"`
	ParentID  string   `json:"parentReplyId"`
	CreatedAt flexTime `json:"createdAt"`
	Deleted   bool     `json:"deleted"`
	Depth     int      `json:"-"`
}

type Notification struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`
	Read       bool     `json:"read"`
	Actor      string   `json:"actorUsername"`
	TargetID   string   `json:"targetId"`
	TargetType string   `json:"targetType"`
	CreatedAt  flexTime `json:"createdAt"`
	Metadata   struct {
		ReplyID      string `json:"replyId"`
		PostContent  string `json:"postContent"`
		ReplyContent string `json:"replyContent"`
		GuildName    string `json:"guildName"`
		RoomName     string `json:"roomName"`
	} `json:"metadata"`
}

type Topic struct {
	Name  string `json:"topicId"`
	Count int    `json:"postsCount"`
}

type User struct {
	UserID    string `json:"userId"`
	Username  string `json:"username"`
	Display   string `json:"displayName"`
	Bio       string `json:"bio"`
	Followers int    `json:"followersCount"`
	Following int    `json:"followingCount"`
}

type Client struct {
	Base string
	HTTP *http.Client
	UA   string

	Stream *http.Client // like HTTP but with no overall timeout, for live streams

	mu     sync.Mutex
	tok    Tokens
	OnSave func(Tokens) // called whenever the tokens change (persist the session)
}

func (c *Client) Tokens() Tokens {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tok
}

func (c *Client) SetTokens(t Tokens) {
	c.mu.Lock()
	c.tok = t
	save := c.OnSave
	c.mu.Unlock()
	if save != nil {
		save(t)
	}
}

type envelope struct {
	Data   json.RawMessage `json:"data"`
	Cursor *string         `json:"cursor"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) send(method, path string, body []byte) (*envelope, int, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.Base+path, rd)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.UA != "" {
		req.Header.Set("User-Agent", c.UA)
	}
	if t := c.Tokens().IDToken; t != "" && !strings.HasPrefix(path, "/v1/auth/") {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode == 429 {
		return nil, 429, &APIError{Code: "RATE_LIMITED", Status: 429}
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		if resp.StatusCode >= 400 {
			return nil, resp.StatusCode, &APIError{Code: http.StatusText(resp.StatusCode), Status: resp.StatusCode}
		}
		return nil, resp.StatusCode, fmt.Errorf("bad response from server: %v", err)
	}
	if env.Error != nil {
		return nil, resp.StatusCode, &APIError{Code: env.Error.Code, Message: env.Error.Message, Status: resp.StatusCode}
	}
	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, &APIError{Code: http.StatusText(resp.StatusCode), Status: resp.StatusCode}
	}
	return &env, resp.StatusCode, nil
}

// do sends a request; on a 401 it refreshes the session once and retries.
func (c *Client) do(method, path string, in any) (*envelope, error) {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return nil, err
		}
	}
	env, status, err := c.send(method, path, body)
	if status == 401 && !strings.HasPrefix(path, "/v1/auth/") && c.Tokens().RefreshToken != "" {
		if rerr := c.Refresh(); rerr != nil {
			return nil, rerr
		}
		env, _, err = c.send(method, path, body)
	}
	return env, err
}

func (c *Client) Login(email, password string) error {
	env, _, err := c.send("POST", "/v1/auth/login", mustJSON(map[string]string{"email": email, "password": password}))
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && (ae.Status == 400 || ae.Status == 401) && ae.Code != "EMAIL_NOT_VERIFIED" {
			return &APIError{Code: "INVALID_CREDENTIALS", Status: ae.Status, Message: ae.Message}
		}
		return err
	}
	var t Tokens
	if err := json.Unmarshal(env.Data, &t); err != nil {
		return err
	}
	if t.IDToken == "" {
		return errors.New("login answered without a token")
	}
	c.SetTokens(t)
	return nil
}

func (c *Client) Refresh() error {
	rt := c.Tokens().RefreshToken
	if rt == "" {
		return ErrUnauthorized
	}
	env, _, err := c.send("POST", "/v1/auth/refresh", mustJSON(map[string]string{"refreshToken": rt}))
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500 && ae.Status != 429 {
			c.SetTokens(Tokens{})
			return ErrUnauthorized
		}
		return err
	}
	var d struct {
		IDToken      string `json:"idToken"`
		RefreshToken string `json:"refreshToken"`
		RTDBURL      string `json:"rtdbUrl"`
	}
	if err := json.Unmarshal(env.Data, &d); err != nil || d.IDToken == "" {
		return ErrUnauthorized
	}
	if d.RefreshToken == "" {
		d.RefreshToken = rt
	}
	db := d.RTDBURL
	if db == "" {
		db = c.Tokens().RTDBURL
	}
	c.SetTokens(Tokens{IDToken: d.IDToken, RefreshToken: d.RefreshToken, RTDBURL: db})
	return nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func page[T any](c *Client, path string) ([]T, string, error) {
	env, err := c.do("GET", path, nil)
	if err != nil {
		return nil, "", err
	}
	var out []T
	if len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, &out); err != nil {
			return nil, "", fmt.Errorf("bad response from server: %v", err)
		}
	}
	cur := ""
	if env.Cursor != nil {
		cur = *env.Cursor
	}
	return out, cur, nil
}

func withCursor(path, cursor string) string {
	if cursor != "" {
		path += "&cursor=" + url.QueryEscape(cursor)
	}
	return path
}

func (c *Client) Feed(cursor string) ([]Post, string, error) {
	return page[Post](c, withCursor("/v1/posts?limit=20", cursor))
}

func (c *Client) TopicPosts(topic, cursor string) ([]Post, string, error) {
	return page[Post](c, withCursor("/v1/topics/"+url.PathEscape(topic)+"/posts?limit=20", cursor))
}

func (c *Client) UserPosts(user, cursor string) ([]Post, string, error) {
	return page[Post](c, withCursor("/v1/users/"+url.PathEscape(user)+"/posts?limit=20", cursor))
}

func (c *Client) Topics(cursor string) ([]Topic, string, error) {
	return page[Topic](c, withCursor("/v1/topics?limit=50", cursor))
}

func (c *Client) Notifications(cursor string) ([]Notification, string, error) {
	return page[Notification](c, withCursor("/v1/notifications?limit=20", cursor))
}

func (c *Client) Post(id string) (Post, error) {
	var p Post
	env, err := c.do("GET", "/v1/posts/"+url.PathEscape(id), nil)
	if err == nil {
		err = json.Unmarshal(env.Data, &p)
	}
	return p, err
}

func (c *Client) Reply(id string) (Reply, error) {
	var r Reply
	env, err := c.do("GET", "/v1/replies/"+url.PathEscape(id), nil)
	if err == nil {
		err = json.Unmarshal(env.Data, &r)
	}
	return r, err
}

func (c *Client) Me() (User, error) {
	var u User
	env, err := c.do("GET", "/v1/users/me", nil)
	if err == nil {
		err = json.Unmarshal(env.Data, &u)
	}
	return u, err
}

// Replies fetches every reply to a post (oldest first) and orders them as a
// thread: each reply follows its parent, children indented one level.
func (c *Client) Replies(postID string) ([]Reply, error) {
	var all []Reply
	cursor := ""
	for i := 0; i < 25; i++ { // 500 replies is plenty for a handheld
		rs, next, err := page[Reply](c, withCursor("/v1/posts/"+url.PathEscape(postID)+"/replies?limit=20", cursor))
		if err != nil {
			return nil, err
		}
		all = append(all, rs...)
		if next == "" || len(rs) == 0 {
			break
		}
		cursor = next
	}
	return Thread(all), nil
}

// Thread orders replies depth-first under their parents, keeping each
// level oldest-first. Orphans (parent missing) stay at the top level.
func Thread(rs []Reply) []Reply {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].CreatedAt.Before(rs[j].CreatedAt.Time) })
	ids := map[string]bool{}
	for _, r := range rs {
		ids[r.ID] = true
	}
	kids := map[string][]Reply{}
	var roots []Reply
	for _, r := range rs {
		if r.ParentID != "" && ids[r.ParentID] && r.ParentID != r.ID {
			kids[r.ParentID] = append(kids[r.ParentID], r)
		} else {
			roots = append(roots, r)
		}
	}
	out := make([]Reply, 0, len(rs))
	seen := map[string]bool{}
	var walk func(r Reply, d int)
	walk = func(r Reply, d int) {
		if seen[r.ID] {
			return
		}
		seen[r.ID] = true
		r.Depth = d
		out = append(out, r)
		for _, k := range kids[r.ID] {
			walk(k, d+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return out
}

type NewPost struct {
	Content  string   `json:"content"`
	Title    string   `json:"title,omitempty"`
	Topics   []string `json:"topics"`
	IsPublic bool     `json:"isPublic"`
	IsNSFW   bool     `json:"isNSFW"`
}

func (c *Client) CreatePost(p NewPost) (string, error) {
	if p.Topics == nil {
		p.Topics = []string{}
	}
	env, err := c.do("POST", "/v1/posts", p)
	if err != nil {
		return "", err
	}
	var d struct {
		PostID string `json:"postId"`
	}
	json.Unmarshal(env.Data, &d)
	return d.PostID, nil
}

func (c *Client) CreateReply(postID, parentID, content string) (string, error) {
	body := map[string]string{"postId": postID, "content": content}
	if parentID != "" {
		body["parentReplyId"] = parentID
	}
	env, err := c.do("POST", "/v1/replies", body)
	if err != nil {
		return "", err
	}
	var d struct {
		ReplyID string `json:"replyId"`
	}
	json.Unmarshal(env.Data, &d)
	return d.ReplyID, nil
}

func (c *Client) MarkRead(id string) error {
	_, err := c.do("PATCH", "/v1/notifications/"+url.PathEscape(id), nil)
	return err
}

func (c *Client) MarkAllRead() error {
	_, err := c.do("POST", "/v1/notifications/read-all", nil)
	return err
}

func (c *Client) UnreadCount() (int, error) {
	env, err := c.do("GET", "/v1/notifications/unread-count", nil)
	if err != nil {
		return 0, err
	}
	var d struct {
		Count int `json:"count"`
	}
	err = json.Unmarshal(env.Data, &d)
	return d.Count, err
}
