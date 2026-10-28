package main

// Screens, state and button handling.

import (
	"context"
	"sync/atomic"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type screen int

const (
	scrDial screen = iota // connecting / restoring the session
	scrLogin
	scrList
	scrPost
	scrCompose
	scrEditor
	scrChat
	scrBye
)

const (
	tabFeed = iota
	tabTopics
	tabAlerts
	tabMail
	tabRooms
	tabCount
)

var tabNames = [tabCount]string{"FEED", "TOPICS", "ALERTS", "MAIL", "CIRC"}

type listKind int

const (
	lvFeed listKind = iota
	lvTopicPosts
	lvTopics
	lvAlerts
	lvMail
	lvRooms
)

type listView struct {
	kind    listKind
	title   string
	arg     string
	posts   []Post
	topics  []Topic
	mail    []convRow
	rooms   []map[string]any
	notifs  []Notification
	cursor  string
	more    bool
	sel     int
	scroll  int
	loading bool
	loaded  bool
	err     string
	gen     int
}

func (v *listView) count() int {
	switch v.kind {
	case lvTopics:
		return len(v.topics)
	case lvAlerts:
		return len(v.notifs)
	case lvMail:
		return len(v.mail)
	case lvRooms:
		return len(v.rooms)
	}
	return len(v.posts)
}

type postView struct {
	post     Post
	replies  []Reply
	loading  bool
	err      string
	sel      int // 0 = the entry, 1.. = replies
	scroll   int // first visible line
	focus    string
	lines    []pvLine
	builtFor int
	gen      int
}

type pvLine struct {
	text  string
	kind  lineKind
	item  int
	indent int
	meta  bool
}

type compose struct {
	body, title, topics string
	public, nsfw         bool
	row                  int
}

type modal struct {
	text  string
	yes   string
	onYes func()
}

type UI struct {
	// activity must stay the first field: 64-bit atomics crash on 32-bit ARM
	// unless the word is 8-byte aligned, and the first field always is.
	activity int64 // ms of our last keypress, reported with presence
	env    *Env
	api    *Client
	W, H   int
	scale  float64
	th     Theme
	frame  int
	quit   bool
	post   chan func()
	sync   bool // run "background" work inline (tests and screenshots)
	now    func() time.Time

	fTitle, fBody, fSmall, fBig *Font

	scr, back screen
	byeFrame  int

	// login
	email, password string
	loginRow        int
	loginErr        string
	dialStart       int
	dialMsg         string

	me     User
	unread int
	lastPoll time.Time

	tab   int
	tabs  [tabCount][]*listView
	pv    *postView
	pvBack screen
	comp  compose
	ed    *Editor
	edBack screen
	confirm *modal
	busy    string
	drafts  map[string]string // unsent replies, by post and parent

	convs      recMap // C-Mail conversations, kept live from the account stream
	chat       *chatView
	sessCtx    context.Context // lives as long as the login
	sessCancel context.CancelFunc

	toast   string
	toastAt int

	inner  *Canvas // the screen inside the C64 theme's border
	iw, ih int
}

func NewUI(env *Env, w, h int) *UI {
	u := &UI{env: env, W: w, H: h, post: make(chan func(), 64), now: env.Now}
	u.scale = float64(h) / 480
	u.th = themeByName(env.Config.Theme)
	u.fBody = loadFont(vt323TTF, 20*u.scale)
	u.fSmall = loadFont(vt323TTF, 16*u.scale)
	u.fBig = loadFont(vt323TTF, 30*u.scale)
	u.fTitle = loadFont(vt323TTF, 24*u.scale)
	u.api = env.API
	u.dims()
	u.email = env.Config.Email
	u.tabs[tabFeed] = []*listView{{kind: lvFeed, title: "FEED"}}
	u.tabs[tabTopics] = []*listView{{kind: lvTopics, title: "TOPICS"}}
	u.tabs[tabAlerts] = []*listView{{kind: lvAlerts, title: "ALERTS"}}
	u.tabs[tabMail] = []*listView{{kind: lvMail, title: "MAIL"}}
	u.tabs[tabRooms] = []*listView{{kind: lvRooms, title: "CIRC"}}
	u.convs = recMap{}
	u.loadDraft()
	return u
}

// dims sets the drawable size (the C64 theme's border eats into it).
func (u *UI) dims() {
	fw := 0
	if u.th.FrameW > 0 {
		fw = u.px(u.th.FrameW)
	}
	u.iw, u.ih = u.W-2*fw, u.H-2*fw
}

func (u *UI) px(v int) int { return int(float64(v)*u.scale + 0.5) }

// async runs work off the UI goroutine; the closure it returns runs back on it.
func (u *UI) async(work func() func()) {
	if u.sync {
		if f := work(); f != nil {
			f()
		}
		return
	}
	go func() {
		f := work()
		if f != nil {
			u.post <- f
		}
	}()
}

func (u *UI) showToast(s string) {
	u.toast = s
	u.toastAt = u.frame
}

func (u *UI) Animating() bool {
	if c := u.chat; u.scr == scrChat && c != nil && (!c.loaded || c.loadingOld || u.typingName(c) != "") {
		return true
	}
	return u.scr == scrDial || u.scr == scrEditor || u.busy != "" || (u.toast != "" && u.frame-u.toastAt < 70) || u.loadingAny()
}

func (u *UI) loadingAny() bool {
	if u.pv != nil && u.pv.loading && u.scr == scrPost {
		return true
	}
	if v := u.view(); v != nil && v.loading {
		return true
	}
	return false
}

// restoreBackoff is how long Start waits between retries when the network
// isn't up yet (about 30 seconds in all).
var restoreBackoff = []time.Duration{2 * time.Second, 3 * time.Second, 5 * time.Second, 8 * time.Second, 12 * time.Second}

// Start restores the saved session or shows the login screen.
func (u *UI) Start() {
	cfg := u.env.Config
	switch {
	case cfg.Email != "" && cfg.Password != "":
		// credentials typed into config.json on a computer: log in once, then forget the password
		u.password = cfg.Password
		u.doLogin()
	case u.api.Tokens().RefreshToken != "":
		u.dial("RESTORING SESSION")
		u.async(func() func() {
			// Wi-Fi is often still joining when the app opens. A network error
			// isn't an expired session, so keep trying for about half a minute
			// before falling back to the login screen (the saved session is kept).
			err := u.api.Refresh()
			for i := 0; err != nil && err != ErrUnauthorized && i < len(restoreBackoff); i++ {
				log.Printf("restore session: %v (retrying in %s)", err, restoreBackoff[i])
				time.Sleep(restoreBackoff[i])
				err = u.api.Refresh()
			}
			return func() {
				if err != nil {
					if err == ErrUnauthorized {
						u.loginErr = "SESSION EXPIRED - LOG IN AGAIN."
					} else {
						u.loginErr = Friendly(err) + " YOU'RE STILL LOGGED IN: REOPEN THE APP ONCE WI-FI IS UP."
					}
					u.scr = scrLogin
					return
				}
				u.connected()
			}
		})
	default:
		u.scr = scrLogin
	}
}

func (u *UI) dial(msg string) {
	u.scr = scrDial
	u.dialStart = u.frame
	u.dialMsg = msg
}

func (u *UI) doLogin() {
	email, pw := strings.TrimSpace(u.email), u.password
	if email == "" || pw == "" {
		u.loginErr = "ENTER YOUR EMAIL AND PASSWORD."
		u.scr = scrLogin
		return
	}
	u.loginErr = ""
	u.dial("DIALING CYBERSPACE.ONLINE")
	u.async(func() func() {
		err := u.api.Login(email, pw)
		return func() {
			if u.env.Config.Password != "" {
				u.env.forgetPassword(email) // never leave it sitting on the SD card
			}
			if err != nil {
				u.loginErr = Friendly(err)
				u.scr = scrLogin
				return
			}
			u.password = ""
			u.email = email
			u.connected()
		}
	})
}

func (u *UI) connected() {
	u.scr = scrList
	u.tab = tabFeed
	u.reload(u.view())
	if u.sessCancel != nil {
		u.sessCancel()
	}
	u.sessCtx, u.sessCancel = context.WithCancel(context.Background())
	ctx := u.sessCtx
	u.refreshMail()
	u.async(func() func() {
		me, err := u.api.Me()
		n, nerr := u.api.UnreadCount()
		return func() {
			if err == nil {
				u.me = me
				if me.UserID != "" && !u.sync {
					go u.followConvs(ctx, me.UserID)
				}
			}
			if nerr == nil {
				u.unread = n
			}
			u.lastPoll = u.now()
		}
	})
}

func (u *UI) logout() {
	u.closeChat()
	if u.sessCancel != nil {
		u.sessCancel()
		u.sessCancel = nil
	}
	u.api.SetTokens(Tokens{})
	u.me = User{}
	u.unread = 0
	u.convs = recMap{}
	for t := range u.tabs {
		u.tabs[t] = u.tabs[t][:1]
		base := u.tabs[t][0]
		*base = listView{kind: base.kind, title: base.title}
	}
	u.loginErr = "LOGGED OUT."
	u.scr = scrLogin
}

// ---------- lists ----------

func (u *UI) view() *listView {
	st := u.tabs[u.tab]
	return st[len(st)-1]
}

func (u *UI) reload(v *listView) {
	switch v.kind {
	case lvMail:
		u.refreshMail()
		return
	case lvRooms:
		u.refreshRooms()
		return
	}
	v.posts, v.topics, v.notifs = nil, nil, nil
	v.cursor, v.more, v.sel, v.scroll, v.err = "", false, 0, 0, ""
	v.loaded = false
	u.fetch(v)
}

func (u *UI) fetch(v *listView) {
	if v.loading || v.kind == lvMail || v.kind == lvRooms {
		return
	}
	v.loading = true
	v.gen++
	gen, cur := v.gen, v.cursor
	u.async(func() func() {
		var posts []Post
		var topics []Topic
		var notifs []Notification
		var next string
		var err error
		switch v.kind {
		case lvFeed:
			posts, next, err = u.api.Feed(cur)
		case lvTopicPosts:
			posts, next, err = u.api.TopicPosts(v.arg, cur)
		case lvTopics:
			topics, next, err = u.api.Topics(cur)
		case lvAlerts:
			notifs, next, err = u.api.Notifications(cur)
		}
		return func() {
			if gen != v.gen {
				return
			}
			v.loading = false
			if err != nil {
				v.err = Friendly(err)
				if err == ErrUnauthorized {
					u.logout()
				}
				return
			}
			v.loaded = true
			v.err = ""
			for _, p := range posts {
				if !p.Deleted {
					v.posts = append(v.posts, p)
				}
			}
			v.topics = append(v.topics, topics...)
			v.notifs = append(v.notifs, notifs...)
			v.cursor = next
			v.more = next != ""
		}
	})
}

func (u *UI) keyList(k int) {
	v := u.view()
	n := v.count()
	switch k {
	case BtnUp:
		if v.sel > 0 {
			v.sel--
		}
	case BtnDown:
		if v.sel < n-1 {
			v.sel++
		}
	case BtnLeft:
		v.sel = max(0, v.sel-u.pageRows(v))
	case BtnRight:
		v.sel = max(0, min(n-1, v.sel+u.pageRows(v)))
	case BtnL1:
		u.tab = (u.tab + tabCount - 1) % tabCount
		u.enterTab()
		return
	case BtnR1:
		u.tab = (u.tab + 1) % tabCount
		u.enterTab()
		return
	case BtnA:
		u.openSelected(v)
	case BtnB:
		if len(u.tabs[u.tab]) > 1 {
			u.tabs[u.tab] = u.tabs[u.tab][:len(u.tabs[u.tab])-1]
		}
	case BtnX:
		switch v.kind {
		case lvAlerts:
			u.markAllRead()
		case lvMail:
			u.newDM()
		case lvRooms:
			// nothing to write here: pick a room first
		default:
			u.openCompose(v)
		}
	case BtnY:
		u.reload(v)
		u.pollUnread()
	case BtnSelect:
		u.cycleTheme()
	case BtnStart:
		u.confirm = &modal{text: "LOG OUT OF CYBERSPACE?", yes: "LOG OUT", onYes: u.logout}
	case BtnMenu:
		u.quit = true
	}
	// infinite scroll
	if v.more && !v.loading && v.sel >= v.count()-4 {
		u.fetch(v)
	}
}

func (u *UI) enterTab() {
	v := u.view()
	if !v.loaded && !v.loading {
		u.reload(v)
	}
	if v.kind == lvAlerts {
		u.reload(v)
	}
}

func (u *UI) openSelected(v *listView) {
	switch v.kind {
	case lvTopics:
		if v.sel < len(v.topics) {
			t := v.topics[v.sel]
			nv := &listView{kind: lvTopicPosts, title: "#" + t.Name, arg: t.Name}
			u.tabs[u.tab] = append(u.tabs[u.tab], nv)
			u.reload(nv)
		}
	case lvMail:
		if v.sel < len(v.mail) {
			r := v.mail[v.sel]
			u.openDM(r.id, r.other)
		}
	case lvRooms:
		if v.sel < len(v.rooms) {
			r := v.rooms[v.sel]
			u.openRoom(str(r, "slug"), str(r, "name"))
		}
	case lvAlerts:
		if v.sel < len(v.notifs) {
			n := &v.notifs[v.sel]
			if !n.Read {
				n.Read = true
				u.unread = max(0, u.unread-1)
				id := n.ID
				u.async(func() func() { u.api.MarkRead(id); return nil })
			}
			if opensPost(*n) {
				u.openNotification(*n)
			} else {
				u.showToast(strings.ToUpper(describe(*n)))
			}
		}
	default:
		if v.sel < len(v.posts) {
			u.openPost(v.posts[v.sel], "")
		}
	}
}

func (u *UI) markAllRead() {
	v := u.view()
	for i := range v.notifs {
		v.notifs[i].Read = true
	}
	u.unread = 0
	u.async(func() func() {
		err := u.api.MarkAllRead()
		return func() {
			if err != nil {
				u.showToast(Friendly(err))
			} else {
				u.showToast("ALL ALERTS MARKED READ")
			}
		}
	})
}

func (u *UI) pollUnread() {
	if u.api.Tokens().IDToken == "" {
		return
	}
	u.lastPoll = u.now()
	u.async(func() func() {
		n, err := u.api.UnreadCount()
		return func() {
			if err == nil {
				u.unread = n
			}
		}
	})
}

// Tick runs every frame (50ms).
func (u *UI) Tick() {
	if u.scr != scrDial && u.scr != scrLogin && u.api.Tokens().IDToken != "" && u.now().Sub(u.lastPoll) > 2*time.Minute {
		u.pollUnread()
	}
	if c := u.chat; c != nil && c.typingOn && u.now().Sub(c.typingKeyAt) > typingQuiet {
		u.typingOff(c)
	}
}

// Shutdown stops every live feed when the app quits.
func (u *UI) Shutdown() {
	if u.sessCancel != nil {
		u.sessCancel()
	}
}

// ---------- post view ----------

func (u *UI) openPost(p Post, focusReply string) {
	u.pv = &postView{post: p, loading: true, focus: focusReply, builtFor: -1}
	u.pvBack = u.scr
	if u.pvBack != scrList {
		u.pvBack = scrList
	}
	u.scr = scrPost
	u.loadReplies()
}

func (u *UI) loadReplies() {
	pv := u.pv
	pv.loading = true
	pv.gen++
	gen := pv.gen
	id := pv.post.ID
	full := pv.post.Content == "" // opened from an alert: fetch the entry too
	u.async(func() func() {
		var p Post
		var perr error
		if full {
			p, perr = u.api.Post(id)
		}
		rs, err := u.api.Replies(id)
		return func() {
			if u.pv != pv || gen != pv.gen {
				return
			}
			pv.loading = false
			if perr != nil {
				pv.err = Friendly(perr)
				return
			}
			if full {
				pv.post = p
			}
			if err != nil {
				pv.err = Friendly(err)
				return
			}
			pv.replies = rs
			pv.post.RepliesCount = max(pv.post.RepliesCount, len(rs))
			pv.builtFor = -1
			if pv.focus != "" {
				for i, r := range rs {
					if r.ID == pv.focus {
						pv.sel = i + 1
						u.buildPostLines()
						u.scrollToSel()
					}
				}
				pv.focus = ""
			}
		}
	})
}

func (u *UI) openNotification(n Notification) {
	if n.TargetType == "reply" {
		u.dialMsg = ""
		u.busy = "OPENING"
		u.async(func() func() {
			r, err := u.api.Reply(n.TargetID)
			return func() {
				u.busy = ""
				if err != nil {
					u.showToast(Friendly(err))
					return
				}
				u.openPost(Post{ID: r.PostID}, r.ID)
			}
		})
		return
	}
	u.openPost(Post{ID: n.TargetID}, n.Metadata.ReplyID)
}

func (u *UI) postArea() (top, bot int) { return u.contentTop(), u.footerY() - u.px(2) }

func (u *UI) visibleLines() int {
	t, b := u.postArea()
	return max(1, (b-t)/u.fBody.height)
}

// buildPostLines lays out the entry and its replies as one scrolling text.
func (u *UI) buildPostLines() {
	pv := u.pv
	w := u.iw - u.px(16)*2 - u.px(10)
	var out []pvLine
	add := func(item, indent int, ls []textLine, meta bool) {
		for _, l := range ls {
			out = append(out, pvLine{text: l.s, kind: l.kind, item: item, indent: indent, meta: meta})
		}
	}
	p := pv.post
	head := "@" + p.Author
	if t := ago(p.CreatedAt.Time, u.now()); t != "" {
		head += "  ·  " + t
	}
	if p.GuildSlug != "" {
		head += "  ·  guild/" + p.GuildSlug
	}
	add(0, 0, []textLine{{head, lnText}}, true)
	if p.Title != "" {
		add(0, 0, renderMarkdown("# "+p.Title, u.fBody, w), false)
	}
	if len(p.Topics) > 0 {
		add(0, 0, []textLine{{"#" + strings.Join(p.Topics, " #"), lnDim}}, false)
	}
	if p.IsNSFW {
		add(0, 0, []textLine{{"[CONTENT WARNING: NSFW]", lnHead}}, false)
	}
	add(0, 0, []textLine{{"", lnText}}, false)
	add(0, 0, renderMarkdown(p.Content, u.fBody, w), false)
	for _, a := range p.Attachments {
		if a.Type == "audio" {
			add(0, 0, []textLine{{"", lnText}, {"♪ " + a.Artist + " - " + a.Title, lnDim}}, false)
		}
	}
	add(0, 0, []textLine{{"", lnText}}, false)
	label := fmt.Sprintf(" REPLIES (%d) ", len(pv.replies))
	if pv.loading {
		label = " LOADING REPLIES "
	}
	out = append(out, pvLine{text: label, kind: lnDim, item: -1})
	for i, r := range pv.replies {
		ind := min(r.Depth, 4)
		rw := w - ind*u.fBody.cell*2
		out = append(out, pvLine{kind: lnText, item: -1})
		h := "@" + r.Author
		if t := ago(r.CreatedAt.Time, u.now()); t != "" {
			h += "  ·  " + t
		}
		add(i+1, ind, []textLine{{h, lnText}}, true)
		body := r.Content
		if r.Deleted {
			body = "(deleted)"
		}
		add(i+1, ind, renderMarkdown(body, u.fBody, rw), false)
	}
	if len(pv.replies) == 0 && !pv.loading {
		out = append(out, pvLine{kind: lnText, item: -1}, pvLine{text: "No replies yet. Press A to be the first.", kind: lnDim, item: -1})
	}
	pv.lines = out
	pv.builtFor = len(pv.replies)
}

func (u *UI) itemRange(item int) (first, last int) {
	first, last = -1, -1
	for i, l := range u.pv.lines {
		if l.item == item {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	return
}

func (u *UI) scrollToSel() {
	pv := u.pv
	first, last := u.itemRange(pv.sel)
	if first < 0 {
		return
	}
	vis := u.visibleLines()
	if first < pv.scroll || last-first+1 > vis {
		pv.scroll = max(0, first-1)
	} else if last >= pv.scroll+vis {
		pv.scroll = last - vis + 1
	}
	u.clampScroll()
}

func (u *UI) clampScroll() {
	pv := u.pv
	maxS := max(0, len(pv.lines)-u.visibleLines())
	pv.scroll = max(0, min(pv.scroll, maxS))
}

func (u *UI) keyPost(k int) {
	pv := u.pv
	if pv.builtFor != len(pv.replies) || pv.lines == nil {
		u.buildPostLines()
	}
	vis := u.visibleLines()
	items := len(pv.replies) + 1
	switch k {
	case BtnDown:
		_, last := u.itemRange(pv.sel)
		if last >= pv.scroll+vis { // the selected item continues below: read on
			pv.scroll += max(1, vis/2)
			u.clampScroll()
			return
		}
		if pv.sel < items-1 {
			pv.sel++
			u.scrollToSel()
		} else {
			pv.scroll += vis / 2
			u.clampScroll()
		}
	case BtnUp:
		first, _ := u.itemRange(pv.sel)
		if first < pv.scroll {
			pv.scroll = max(first-1, pv.scroll-max(1, vis/2))
			u.clampScroll()
			return
		}
		if pv.sel > 0 {
			pv.sel--
			first, last := u.itemRange(pv.sel)
			if last-first+1 > vis { // tall item: show its end first, keep reading upward
				pv.scroll = max(0, last-vis+1)
				u.clampScroll()
			} else {
				u.scrollToSel()
			}
		} else {
			pv.scroll = 0
		}
	case BtnL1, BtnLeft:
		pv.scroll -= vis - 2
		u.clampScroll()
		u.selFromScroll()
	case BtnR1, BtnRight:
		pv.scroll += vis - 2
		u.clampScroll()
		u.selFromScroll()
	case BtnA:
		u.openReply()
	case BtnX:
		u.openReplyTo(0)
	case BtnY:
		u.loadReplies()
	case BtnB, BtnMenu:
		u.scr = u.pvBack
	case BtnSelect:
		u.cycleTheme()
	}
}

// selFromScroll picks the first item starting in view after a page jump.
func (u *UI) selFromScroll() {
	pv := u.pv
	for i := pv.scroll; i < len(pv.lines) && i < pv.scroll+u.visibleLines(); i++ {
		if l := pv.lines[i]; l.item >= 0 && l.meta {
			pv.sel = l.item
			return
		}
	}
}

func (u *UI) openReply() { u.openReplyTo(u.pv.sel) }

func (u *UI) openReplyTo(item int) {
	pv := u.pv
	postID := pv.post.ID
	parent, who := "", pv.post.Author
	if item > 0 && item <= len(pv.replies) {
		parent, who = pv.replies[item-1].ID, pv.replies[item-1].Author
	}
	key := "reply:" + postID + ":" + parent
	e := newEditor("REPLY TO @"+strings.ToUpper(who), u.drafts[key], 32768, true)
	e.Hint = "write your reply"
	e.DoneLabel = "SEND"
	e.OnCancel = func(s string) { u.setDraft(key, s) }
	e.OnDone = func(s string) {
		u.setDraft(key, s)
		if strings.TrimSpace(s) == "" {
			u.showToast("NOTHING TO SEND")
			u.openEditor(e)
			return
		}
		u.confirm = &modal{text: "SEND THIS REPLY TO @" + strings.ToUpper(who) + "?", yes: "SEND", onYes: func() {
			u.busy = "TRANSMITTING"
			u.async(func() func() {
				_, err := u.api.CreateReply(postID, parent, strings.TrimSpace(s))
				return func() {
					u.busy = ""
					if err != nil {
						u.showToast(Friendly(err))
						u.openEditor(e)
						return
					}
					u.setDraft(key, "")
					u.showToast("REPLY TRANSMITTED")
					if u.pv != nil && u.pv.post.ID == postID {
						u.pv.sel = len(u.pv.replies) + 1
						u.pv.focus = ""
						u.loadRepliesFocusMine()
					}
				}
			})
		}}
	}
	u.openEditor(e)
}

// after replying, reload and jump to the newest reply by us
func (u *UI) loadRepliesFocusMine() {
	pv := u.pv
	pv.loading = true
	pv.gen++
	gen := pv.gen
	id := pv.post.ID
	me := u.me.Username
	u.async(func() func() {
		rs, err := u.api.Replies(id)
		return func() {
			if u.pv != pv || gen != pv.gen {
				return
			}
			pv.loading = false
			if err != nil {
				pv.err = Friendly(err)
				return
			}
			pv.replies = rs
			u.buildPostLines()
			best := -1
			for i, r := range rs {
				if me == "" || r.Author == me {
					if best < 0 || r.CreatedAt.After(rs[best].CreatedAt.Time) {
						best = i
					}
				}
			}
			if best >= 0 {
				pv.sel = best + 1
				u.scrollToSel()
			}
		}
	})
}

func (u *UI) openEditor(e *Editor) {
	if u.scr != scrEditor {
		u.edBack = u.scr
	}
	u.ed = e
	u.scr = scrEditor
}

func (u *UI) closeEditor() {
	if u.scr == scrEditor {
		u.scr = u.edBack
	}
}

// ---------- compose ----------

const (
	cmpBody = iota
	cmpTitle
	cmpTopics
	cmpPublic
	cmpNSFW
	cmpSend
	cmpRows
)

func (u *UI) openCompose(v *listView) {
	if v != nil && v.kind == lvTopicPosts && u.comp.topics == "" {
		u.comp.topics = v.arg
	}
	u.comp.row = cmpBody
	u.scr = scrCompose
}

func (u *UI) keyCompose(k int) {
	c := &u.comp
	switch k {
	case BtnUp:
		c.row = (c.row + cmpRows - 1) % cmpRows
	case BtnDown:
		c.row = (c.row + 1) % cmpRows
	case BtnA, BtnLeft, BtnRight:
		switch c.row {
		case cmpBody:
			if k == BtnA {
				e := newEditor("NEW ENTRY", c.body, 32768, true)
				e.Hint = "what's on your mind?"
				e.OnDone = func(s string) { c.body = s; u.saveDraft() }
				e.OnCancel = func(s string) { c.body = s; u.saveDraft() }
				u.openEditor(e)
			}
		case cmpTitle:
			if k == BtnA {
				e := newEditor("TITLE (OPTIONAL)", c.title, 100, false)
				e.Hint = "leave blank for none"
				e.OnDone = func(s string) { c.title = strings.TrimSpace(s); u.saveDraft() }
				e.OnCancel = e.OnDone
				u.openEditor(e)
			}
		case cmpTopics:
			if k == BtnA {
				e := newEditor("TOPICS (UP TO 3)", c.topics, 80, false)
				e.Hint = "e.g. retro, miyoo, linux"
				e.Lower = true
				e.OnDone = func(s string) { c.topics = strings.Join(cleanTopics(s), ", "); u.saveDraft() }
				e.OnCancel = e.OnDone
				u.openEditor(e)
			}
		case cmpPublic:
			c.public = !c.public
		case cmpNSFW:
			c.nsfw = !c.nsfw
		case cmpSend:
			if k == BtnA {
				u.sendEntry()
			}
		}
	case BtnStart:
		u.sendEntry()
	case BtnY:
		if c.body != "" || c.title != "" || c.topics != "" {
			u.confirm = &modal{text: "THROW AWAY THIS DRAFT?", yes: "DISCARD", onYes: func() {
				u.comp = compose{}
				u.saveDraft()
			}}
		}
	case BtnB, BtnMenu, BtnSelect:
		u.saveDraft()
		u.scr = scrList
	}
}

func (u *UI) sendEntry() {
	c := u.comp
	if strings.TrimSpace(c.body) == "" {
		u.showToast("WRITE SOMETHING FIRST")
		u.comp.row = cmpBody
		return
	}
	vis := "MEMBERS ONLY"
	if c.public {
		vis = "PUBLIC"
	}
	u.confirm = &modal{text: "TRANSMIT THIS ENTRY (" + vis + ")?", yes: "TRANSMIT", onYes: func() {
		u.busy = "TRANSMITTING"
		np := NewPost{Content: strings.TrimSpace(c.body), Title: c.title, Topics: cleanTopics(c.topics), IsPublic: c.public, IsNSFW: c.nsfw}
		u.async(func() func() {
			_, err := u.api.CreatePost(np)
			return func() {
				u.busy = ""
				if err != nil {
					u.showToast(Friendly(err))
					return
				}
				u.comp = compose{}
				u.saveDraft()
				u.showToast("ENTRY TRANSMITTED")
				u.tab = tabFeed
				u.tabs[tabFeed] = u.tabs[tabFeed][:1]
				u.scr = scrList
				u.reload(u.view())
			}
		})
	}}
}

// ---------- login ----------

const (
	lgEmail = iota
	lgPassword
	lgConnect
	lgRows
)

func (u *UI) keyLogin(k int) {
	switch k {
	case BtnUp:
		u.loginRow = (u.loginRow + lgRows - 1) % lgRows
	case BtnDown:
		u.loginRow = (u.loginRow + 1) % lgRows
	case BtnA:
		switch u.loginRow {
		case lgEmail:
			e := newEditor("EMAIL", u.email, 254, false)
			e.Hint = "you@example.com"
			e.DoneLabel = "NEXT"
			e.OnDone = func(s string) { u.email = strings.TrimSpace(s); u.loginRow = lgPassword }
			e.OnCancel = func(s string) { u.email = strings.TrimSpace(s) }
			u.openEditor(e)
		case lgPassword:
			e := newEditor("PASSWORD", u.password, 200, false)
			e.Mask = true
			e.DoneLabel = "LOG IN"
			e.OnDone = func(s string) { u.password = s; u.loginRow = lgConnect; u.doLogin() }
			e.OnCancel = func(s string) { u.password = s }
			u.openEditor(e)
		case lgConnect:
			u.doLogin()
		}
	case BtnStart:
		u.doLogin()
	case BtnSelect:
		u.cycleTheme()
	case BtnMenu, BtnB:
		u.quit = true
	}
}

// ---------- dispatch ----------

func (u *UI) Key(k int) {
	atomic.StoreInt64(&u.activity, u.nowMs())
	if u.confirm != nil {
		m := u.confirm
		switch k {
		case BtnA, BtnStart:
			u.confirm = nil
			m.onYes()
		case BtnB, BtnMenu, BtnSelect:
			u.confirm = nil
		}
		return
	}
	if u.busy != "" {
		return
	}
	switch u.scr {
	case scrLogin:
		u.keyLogin(k)
	case scrList:
		u.keyList(k)
	case scrPost:
		u.keyPost(k)
	case scrCompose:
		u.keyCompose(k)
	case scrChat:
		u.keyChat(k)
	case scrEditor:
		e := u.ed
		if e.Key(k) {
			if u.ed == e { // the callback didn't open another editor
				u.closeEditor()
			}
		} else if u.chat != nil && u.edBack == scrChat {
			u.chatTypingTick(u.chat, e.String())
		}
	case scrDial:
		if k == BtnMenu {
			u.quit = true
		}
	}
}

// ---------- theme, drafts ----------

func (u *UI) cycleTheme() {
	i := 0
	for j, t := range themes {
		if t.Name == u.th.Name {
			i = j
		}
	}
	u.th = themes[(i+1)%len(themes)]
	u.dims()
	if u.pv != nil {
		u.pv.builtFor = -1
	}
	u.env.Config.Theme = u.th.Name
	u.env.saveConfig()
	u.showToast("THEME: " + strings.ToUpper(u.th.Name))
}

type draftFile struct {
	Compose struct {
		Body, Title, Topics string
		Public, NSFW        bool
	}
	Replies map[string]string
}

func (u *UI) draftPath() string { return filepath.Join(u.env.DataDir, "drafts.json") }

func (u *UI) loadDraft() {
	u.drafts = map[string]string{}
	b, err := os.ReadFile(u.draftPath())
	if err != nil {
		return
	}
	var d draftFile
	if json.Unmarshal(b, &d) != nil {
		return
	}
	u.comp = compose{body: d.Compose.Body, title: d.Compose.Title, topics: d.Compose.Topics, public: d.Compose.Public, nsfw: d.Compose.NSFW}
	if d.Replies != nil {
		u.drafts = d.Replies
	}
}

func (u *UI) saveDraft() {
	if u.env.DataDir == "" {
		return
	}
	var d draftFile
	d.Compose.Body, d.Compose.Title, d.Compose.Topics = u.comp.body, u.comp.title, u.comp.topics
	d.Compose.Public, d.Compose.NSFW = u.comp.public, u.comp.nsfw
	d.Replies = u.drafts
	b, _ := json.MarshalIndent(d, "", "  ")
	if err := os.WriteFile(u.draftPath(), b, 0o600); err != nil {
		log.Printf("drafts: %v", err)
	}
}

func (u *UI) setDraft(key, s string) {
	if strings.TrimSpace(s) == "" {
		delete(u.drafts, key)
	} else {
		u.drafts[key] = s
	}
	u.saveDraft()
}
