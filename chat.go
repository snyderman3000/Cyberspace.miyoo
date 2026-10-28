package main

// The chat screen shared by C-Mail (one private conversation) and CIRC (one
// room). Both keep the same shape: history from REST, new lines from the
// database stream, and a presence feed (who's in the room, or who's typing).

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	chatMaxLen      = 1000
	typingEvery     = 3 * time.Second
	typingQuiet     = 2500 * time.Millisecond
	streamQueryJSON = `orderBy=%22timestamp%22&limitToLast=100`
)

// chatLine is one wrapped row of the message area.
type chatLine struct {
	text string
	kind lineKind
	mine bool
	msg  string // message id, so a line can be traced back
}

type chatView struct {
	room   bool   // true for a cIRC room, false for a C-Mail conversation
	id     string // conversation id or room slug
	title  string
	name   string // the other person, or the room's name
	msgs   recMap // messages by id
	presence recMap // room: who's in it; C-Mail: typing flags

	loaded     bool
	loadingOld bool
	noMore     bool
	err        string
	status     string // "", "reconnecting 2/6" or "lost"

	lines    []chatLine
	dirty    bool
	scroll   int
	follow   bool // keep the newest line in view
	showUsers bool

	staleMs, idleMs int64
	typingStaleMs   int64 // a typing flag older than this is ignored

	ctx    context.Context
	cancel context.CancelFunc

	typingOn    bool
	typingSent  time.Time
	typingKeyAt time.Time
}

type chatMsg struct {
	id, who, body string
	ts            int64
	mine, action  bool
	deleted, art  bool
	media         []string
}

type presentUser struct {
	name         string
	admin, idle  bool
}

func newChat(room bool, id, title string) *chatView {
	return &chatView{room: room, id: id, title: title, msgs: recMap{}, presence: recMap{},
		dirty: true, follow: true, staleMs: 180000, idleMs: 600000, typingStaleMs: 9000}
}

// chatWidth is the width text can use in the message area.
func (u *UI) chatWidth() int { return u.iw - u.px(28) }

// chatArea is the rows of the message area: top and bottom pixel edges.
func (u *UI) chatArea() (top, bot int) { return u.contentTop(), u.footerY() - u.px(20) }

func (u *UI) chatVisible() int {
	top, bot := u.chatArea()
	return max(1, (bot-top)/u.fBody.height)
}

func (u *UI) nowMs() int64 { return u.now().UnixMilli() }

// chatMessages returns the messages oldest first.
func (u *UI) chatMessages(cv *chatView) []chatMsg {
	ids := make([]string, 0, len(cv.msgs))
	for id := range cv.msgs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := num(cv.msgs[ids[i]], "timestamp"), num(cv.msgs[ids[j]], "timestamp")
		if a != b {
			return a < b
		}
		return ids[i] < ids[j]
	})
	out := make([]chatMsg, 0, len(ids))
	for _, id := range ids {
		m := cv.msgs[id]
		cm := chatMsg{id: id, ts: num(m, "timestamp"), action: boolean(m, "isAction")}
		cm.who = str(m, "senderUsername")
		if cm.who == "" {
			cm.who = str(m, "username")
		}
		cm.mine = cm.who != "" && strings.EqualFold(cm.who, u.me.Username)
		body := str(m, "content")
		cm.deleted = boolean(m, "deleted") || body == "[DELETED]"
		for _, s := range strs(m, "style") {
			if s == "art" {
				cm.art = true
			}
		}
		if cm.art && !cm.deleted {
			if dec, err := base64.StdEncoding.DecodeString(body); err == nil {
				body = string(dec)
			}
		}
		// a picture or song posted with no caption repeats its link as the text
		if img := str(m, "imageUrl"); img != "" {
			cm.media = append(cm.media, "[image]")
			if body == img {
				body = ""
			}
		}
		if gif := str(m, "gifUrl"); gif != "" {
			cm.media = append(cm.media, "[gif]")
			if body == gif {
				body = ""
			}
		}
		if au, ok := m["audioAttachment"].(map[string]any); ok {
			cm.media = append(cm.media, "♪ "+str(au, "artist")+" - "+str(au, "title"))
		}
		if cm.deleted {
			body = ""
		}
		cm.body = strings.TrimRight(body, "\n")
		out = append(out, cm)
	}
	return out
}

// buildChat lays the messages out as wrapped lines.
func (u *UI) buildChat(cv *chatView) {
	w := u.chatWidth()
	indentW := u.fBody.Width("  ")
	var lines []chatLine
	for _, cm := range u.chatMessages(cv) {
		when := time.UnixMilli(cm.ts).Local().Format("15:04")
		if cm.ts == 0 {
			when = ""
		}
		if cm.action {
			txt := "* " + cm.who + " " + inline(cm.body)
			if cm.deleted {
				txt = "* " + cm.who + " [DELETED]"
			}
			for _, l := range u.fBody.Wrap(txt, w) {
				lines = append(lines, chatLine{text: l, kind: lnDim, mine: cm.mine, msg: cm.id})
			}
			continue
		}
		head := cm.who
		if when != "" {
			head += "  " + when
		}
		lines = append(lines, chatLine{text: head, kind: lnHead, mine: cm.mine, msg: cm.id})
		switch {
		case cm.deleted:
			lines = append(lines, chatLine{text: "  [DELETED]", kind: lnDim, msg: cm.id})
		case cm.art:
			for _, para := range strings.Split(cm.body, "\n") {
				for _, p := range u.fBody.Chop(para, w-indentW) {
					lines = append(lines, chatLine{text: "  " + p, kind: lnCode, msg: cm.id})
				}
			}
		default:
			for _, para := range strings.Split(cm.body, "\n") {
				for _, l := range u.fBody.Wrap(inline(para), w-indentW) {
					lines = append(lines, chatLine{text: "  " + l, kind: lnText, mine: cm.mine, msg: cm.id})
				}
			}
		}
		for _, m := range cm.media {
			lines = append(lines, chatLine{text: "  " + m, kind: lnDim, msg: cm.id})
		}
	}
	cv.lines = lines
	cv.dirty = false
}

func (u *UI) ensureChat(cv *chatView) {
	if cv.dirty || cv.lines == nil {
		u.buildChat(cv)
	}
}

func (u *UI) maxScroll(cv *chatView) int {
	return max(0, len(cv.lines)-u.chatVisible())
}

// chatUsers lists who's in a room now: admins first, then everyone else, A to Z.
func (u *UI) chatUsers(cv *chatView) []presentUser {
	now := u.nowMs()
	var out []presentUser
	for _, m := range cv.presence {
		if has(m, "online") && !boolean(m, "online") {
			continue
		}
		if ls := num(m, "lastSeen"); ls > 0 && now-ls > cv.staleMs {
			continue
		}
		la := num(m, "lastActivity")
		out = append(out, presentUser{
			name:  str(m, "username"),
			admin: boolean(m, "isChatAdmin"),
			idle:  la > 0 && now-la > cv.idleMs,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].admin != out[j].admin {
			return out[i].admin
		}
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	return out
}

// typingName is who in the conversation is typing right now ("" if nobody).
func (u *UI) typingName(cv *chatView) string {
	if cv.room {
		return ""
	}
	now := u.nowMs()
	for _, m := range cv.presence {
		if !boolean(m, "typing") {
			continue
		}
		if ts := num(m, "timestamp"); ts > 0 && now-ts > cv.typingStaleMs {
			continue
		}
		if name := str(m, "username"); name != "" && !strings.EqualFold(name, u.me.Username) {
			return name
		}
	}
	return ""
}

// ---------- opening, following, closing ----------

func (u *UI) openDM(id, other string) {
	u.closeChat()
	u.startChat(newChat(false, id, "@"+other), other)
}

func (u *UI) openRoom(slug, name string) {
	u.closeChat()
	cv := newChat(true, slug, "#"+slug)
	cv.name = name
	u.startChat(cv, name)
}

func (u *UI) startChat(cv *chatView, name string) {
	base := u.sessCtx
	if base == nil {
		base = context.Background()
	}
	cv.ctx, cv.cancel = context.WithCancel(base)
	cv.name = name
	u.chat = cv
	u.scr = scrChat
	atomic.StoreInt64(&u.activity, u.nowMs())
	u.loadChat(cv)
	if u.sync {
		return
	}
	msgPath, presPath := "/dm_messages/"+cv.id, "/dm_presence/"+cv.id
	if cv.room {
		msgPath, presPath = "/chat_messages/"+cv.id, "/chat_presence/"+cv.id
	}
	ctx := cv.ctx
	go u.follow(ctx, msgPath, streamQueryJSON, func(ev sseEvent) {
		if u.chat == cv {
			cv.msgs.apply(ev)
			cv.dirty = true
		}
	}, func(s string) {
		if u.chat == cv {
			cv.status = s
		}
	})
	go u.follow(ctx, presPath, "", func(ev sseEvent) {
		if u.chat == cv {
			cv.presence.apply(ev)
		}
	}, nil)
	if cv.room {
		go u.heartbeat(ctx, cv)
	}
}

// closeChat stops the live feeds for the open conversation or room.
func (u *UI) closeChat() {
	cv := u.chat
	if cv == nil {
		return
	}
	u.typingOff(cv)
	if cv.cancel != nil {
		cv.cancel()
	}
	if cv.room {
		id, api := cv.id, u.api
		go api.PresenceLeave(id)
	}
	u.chat = nil
}

// follow keeps one live stream open. When it drops (the login token expires
// about hourly, or the network blips) it refreshes the session and reconnects,
// backing off up to six times before it gives up.
func (u *UI) follow(ctx context.Context, path, query string, apply func(sseEvent), status func(string)) {
	backoff := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second}
	const maxAttempts = 6
	attempt := 0
	for {
		events, err := u.api.openStream(ctx, path, query)
		if err == nil {
			attempt = 0
			if status != nil {
				u.deliver(ctx, func() { status("") })
			}
			for ev := range events {
				ev := ev
				if !u.deliver(ctx, func() { apply(ev) }) {
					return
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		if rerr := u.api.Refresh(); errors.Is(rerr, ErrUnauthorized) {
			u.deliver(ctx, u.logout)
			return
		}
		attempt++
		if attempt > maxAttempts {
			if status != nil {
				u.deliver(ctx, func() { status("lost") })
			}
			return
		}
		if status != nil {
			msg := fmt.Sprintf("reconnecting %d/%d", attempt, maxAttempts)
			u.deliver(ctx, func() { status(msg) })
		}
		select {
		case <-time.After(backoff[min(attempt-1, len(backoff)-1)]):
		case <-ctx.Done():
			return
		}
	}
}

// deliver hands work to the UI goroutine, or gives up once the feed is cancelled.
func (u *UI) deliver(ctx context.Context, f func()) bool {
	select {
	case u.post <- f:
		return true
	case <-ctx.Done():
		return false
	}
}

// heartbeat keeps us listed in a room while we stay in it.
func (u *UI) heartbeat(ctx context.Context, cv *chatView) {
	interval := 30 * time.Second
	for {
		hb, st, idle, err := u.api.Presence(cv.id, atomic.LoadInt64(&u.activity))
		if err == nil {
			interval = time.Duration(hb) * time.Millisecond
			u.deliver(ctx, func() {
				if u.chat == cv {
					cv.staleMs, cv.idleMs = st, idle
				}
			})
		} else {
			if errors.Is(err, ErrUnauthorized) {
				u.api.Refresh()
			}
			interval = 10 * time.Second
		}
		select {
		case <-time.After(interval):
		case <-ctx.Done():
			return
		}
	}
}

// loadChat fetches the history (and, for a room, who's in it).
func (u *UI) loadChat(cv *chatView) {
	cv.status = ""
	u.async(func() func() {
		var rows []map[string]any
		var users []map[string]any
		var err error
		if cv.room {
			rows, err = u.api.RoomHistory(cv.id, 0)
			if err == nil {
				users, _ = u.api.RoomUsers(cv.id)
			}
		} else {
			rows, err = u.api.MailHistory(cv.id, 0)
			if err == nil {
				u.api.MailRead(cv.id)
			}
		}
		return func() {
			if u.chat != cv {
				return
			}
			if err != nil {
				cv.err = Friendly(err)
				if errors.Is(err, ErrUnauthorized) {
					u.logout()
				}
				return
			}
			cv.err = ""
			for _, r := range rows {
				if id := str(r, "id"); id != "" {
					cv.msgs[id] = r
				}
			}
			if cv.room {
				cv.presence.load(users, "userId")
			} else {
				u.markConvRead(cv.id)
			}
			cv.loaded = true
			cv.follow = true
			cv.dirty = true
		}
	})
}

// loadOlder pages back through history when the reader reaches the top.
func (u *UI) loadOlder(cv *chatView) {
	if cv.loadingOld || cv.noMore || !cv.loaded {
		return
	}
	var oldest int64
	for _, m := range cv.msgs {
		if ts := num(m, "timestamp"); ts > 0 && (oldest == 0 || ts < oldest) {
			oldest = ts
		}
	}
	if oldest == 0 {
		cv.noMore = true
		return
	}
	cv.loadingOld = true
	u.async(func() func() {
		var rows []map[string]any
		var err error
		if cv.room {
			rows, err = u.api.RoomHistory(cv.id, oldest)
		} else {
			rows, err = u.api.MailHistory(cv.id, oldest)
		}
		return func() {
			cv.loadingOld = false
			if u.chat != cv {
				return
			}
			if err != nil {
				cv.err = Friendly(err)
				return
			}
			if len(rows) == 0 {
				cv.noMore = true
				return
			}
			u.ensureChat(cv)
			before := len(cv.lines)
			for _, r := range rows {
				if id := str(r, "id"); id != "" {
					if _, ok := cv.msgs[id]; !ok {
						cv.msgs[id] = r
					}
				}
			}
			cv.dirty = true
			u.ensureChat(cv)
			// keep the same text on screen: the new lines went in above it
			cv.scroll += len(cv.lines) - before
			cv.follow = false
		}
	})
}

// ---------- sending ----------

func (u *UI) openChatEditor(cv *chatView) {
	key := "chat:" + cv.id
	e := newEditor("SAY", u.drafts[key], chatMaxLen, false)
	e.Hint = "say something"
	e.DoneLabel = "SEND"
	e.OnDone = func(s string) { u.sendChat(cv, s) }
	e.OnCancel = func(s string) {
		u.setDraft(key, s)
		u.typingOff(cv)
	}
	u.openEditor(e)
}

func (u *UI) sendChat(cv *chatView, text string) {
	key := "chat:" + cv.id
	text = strings.TrimSpace(text)
	u.typingOff(cv)
	if text == "" {
		u.setDraft(key, "")
		return
	}
	u.setDraft(key, "")
	u.busy = "TRANSMITTING"
	u.async(func() func() {
		var id, reply string
		var err error
		if cv.room {
			id, reply, err = u.api.RoomSend(cv.id, text)
		} else {
			id, err = u.api.MailSend(cv.id, text)
		}
		return func() {
			u.busy = ""
			if err != nil {
				u.setDraft(key, text)
				u.showToast(Friendly(err))
				return
			}
			if reply != "" {
				u.showToast(reply)
			}
			if id != "" && u.chat == cv {
				// show it now; the live feed replaces this copy with the server's
				if _, ok := cv.msgs[id]; !ok {
					me := u.me.Username
					rec := map[string]any{"id": id, "content": text, "timestamp": float64(u.nowMs())}
					if cv.room {
						rec["username"] = me
					} else {
						rec["senderUsername"] = me
					}
					cv.msgs[id] = rec
				}
				cv.dirty = true
				cv.follow = true
			}
		}
	})
}

// chatTypingTick runs on each key typed into a C-Mail message: it tells the
// other person we're typing, repeating while we keep typing.
func (u *UI) chatTypingTick(cv *chatView, text string) {
	if cv.room {
		return
	}
	now := u.now()
	cv.typingKeyAt = now
	if strings.TrimSpace(text) == "" {
		u.typingOff(cv)
		return
	}
	if cv.typingOn && now.Sub(cv.typingSent) < typingEvery {
		return
	}
	cv.typingOn = true
	cv.typingSent = now
	id, api := cv.id, u.api
	go func() {
		api.TypingOn(id) // if it fails, the flag just won't show
	}()
}

func (u *UI) typingOff(cv *chatView) {
	if cv == nil || !cv.typingOn {
		return
	}
	cv.typingOn = false
	id, api := cv.id, u.api
	go api.TypingOff(id)
}

// ---------- keys ----------

func (u *UI) keyChat(k int) {
	cv := u.chat
	if cv == nil {
		u.scr = scrList
		return
	}
	u.ensureChat(cv)
	if cv.showUsers {
		switch k {
		case BtnY, BtnB, BtnSelect:
			cv.showUsers = false
		case BtnMenu:
			u.quit = true
		}
		return
	}
	vis := u.chatVisible()
	setScroll := func(s int) {
		cv.scroll = max(0, min(s, u.maxScroll(cv)))
		cv.follow = cv.scroll >= u.maxScroll(cv)
	}
	switch k {
	case BtnUp:
		if cv.scroll > 0 {
			setScroll(cv.scroll - 1)
		} else {
			u.loadOlder(cv)
		}
	case BtnDown:
		setScroll(cv.scroll + 1)
	case BtnL1, BtnLeft:
		setScroll(cv.scroll - (vis - 1))
		if cv.scroll == 0 {
			u.loadOlder(cv)
		}
	case BtnR1, BtnRight:
		setScroll(cv.scroll + (vis - 1))
	case BtnA, BtnX:
		u.openChatEditor(cv)
	case BtnY:
		if cv.room {
			cv.showUsers = true
		}
	case BtnStart:
		u.loadChat(cv)
	case BtnB, BtnSelect:
		u.closeChat()
		u.scr = scrList
	case BtnMenu:
		u.quit = true
	}
}

// ---------- drawing ----------

func (u *UI) drawChat(c *Canvas) {
	t := u.th
	cv := u.chat
	if cv == nil {
		u.scr = scrList
		return
	}
	u.ensureChat(cv)
	top := u.headerBar(c, cv.title+"  "+cv.name)
	_ = top
	// the right of the title strip: who's here, or who's typing
	ty := u.headH() + (u.tabH()-u.fBody.height)/2
	right := ""
	if cv.room {
		n := len(u.chatUsers(cv))
		right = fmt.Sprintf("%d ONLINE", n)
	} else if who := u.typingName(cv); who != "" {
		right = fmt.Sprintf("@%s is typing%s", who, strings.Repeat(".", (u.frame/25)%4))
	}
	if right != "" {
		c.TextRight(u.fBody, c.W-u.px(14), ty, right, t.Warn)
	}

	atop, abot := u.chatArea()
	if cv.showUsers {
		u.drawChatUsers(c, atop, abot)
		u.footer(c, [][2]string{{"Y", "BACK"}, {"B", "BACK"}, {"MENU", "QUIT"}})
		return
	}
	lh := u.fBody.height
	vis := u.chatVisible()
	if cv.follow {
		cv.scroll = u.maxScroll(cv)
	}
	cv.scroll = max(0, min(cv.scroll, u.maxScroll(cv)))
	pad := u.px(14)
	if len(cv.lines) == 0 {
		msg := "NO MESSAGES YET. PRESS A TO SAY HELLO."
		if !cv.loaded {
			msg = "RECEIVING " + u.spinner()
		}
		if cv.err != "" {
			msg = cv.err
		}
		u.centerMsg(c, u.fBody.Wrap(msg, c.W-u.px(80)), t.Dim)
	}
	for i := cv.scroll; i < len(cv.lines) && i < cv.scroll+vis; i++ {
		l := cv.lines[i]
		y := atop + (i-cv.scroll)*lh
		col := t.FG
		switch l.kind {
		case lnHead:
			col = t.Dim
			if l.mine {
				col = t.Hi
			}
		case lnDim:
			col = t.Dim
		case lnCode:
			col = t.Hi
		}
		c.Text(u.fBody, pad, y, l.text, col)
	}
	u.scrollbar(c, atop, abot, cv.scroll, vis, len(cv.lines))

	// status line above the footer
	sy := abot + u.px(2)
	switch {
	case cv.err != "" && len(cv.lines) > 0:
		c.Text(u.fSmall, pad, sy, u.fSmall.Ellipsize(cv.err, c.W-pad*2), t.Warn)
	case cv.loadingOld:
		c.Text(u.fSmall, pad, sy, "LOADING OLDER "+u.spinner(), t.Dim)
	case cv.status == "lost":
		c.Text(u.fSmall, pad, sy, "LIVE UPDATES LOST - PRESS START TO RELOAD", t.Warn)
	case cv.status != "":
		c.Text(u.fSmall, pad, sy, "LIVE UPDATES DROPPED - "+strings.ToUpper(cv.status), t.Warn)
	}
	keys := [][2]string{{"A", "SAY"}, {"▲▼", "SCROLL"}, {"L1/R1", "PAGE"}}
	if cv.room {
		keys = append(keys, [2]string{"Y", "WHO'S HERE"})
	}
	keys = append(keys, [2]string{"START", "RELOAD"}, [2]string{"B", "BACK"})
	u.footer(c, keys)
}

func (u *UI) drawChatUsers(c *Canvas, top, bot int) {
	t := u.th
	users := u.chatUsers(u.chat)
	pad := u.px(14)
	c.Text(u.fBody, pad, top, fmt.Sprintf("IN %s (%d)", u.chat.title, len(users)), t.Hi)
	y := top + u.fBody.height + u.px(4)
	for _, p := range users {
		if y+u.fBody.height > bot {
			break
		}
		mark := "  "
		if p.admin {
			mark = "* "
		}
		name := mark + "@" + p.name
		if p.idle {
			name += "  (idle)"
		}
		col := t.FG
		if p.idle {
			col = t.Dim
		}
		if strings.EqualFold(p.name, u.me.Username) {
			col = t.Hi
		}
		c.Text(u.fBody, pad+u.px(6), y, u.fBody.Ellipsize(name, c.W-pad*2), col)
		y += u.fBody.height
	}
	if len(users) == 0 {
		c.Text(u.fBody, pad+u.px(6), y, "nobody else is here right now.", t.Dim)
	}
}
