package main

import (
	"fmt"
	"time"
	"image/color"
	"strings"
)

// Draw renders the current screen.
func (u *UI) Draw(full *Canvas) {
	fw := 0
	if u.th.FrameW > 0 {
		fw = u.px(u.th.FrameW)
	}
	iw, ih := full.W-2*fw, full.H-2*fw
	if u.inner == nil || u.inner.W != iw || u.inner.H != ih {
		u.inner = NewCanvas(iw, ih)
	}
	u.iw, u.ih = iw, ih
	c := u.inner
	c.Fill(0, 0, c.W, c.H, u.th.BG)

	switch u.scr {
	case scrDial:
		u.drawDial(c)
	case scrLogin:
		u.drawLogin(c)
	case scrList:
		u.drawList(c)
	case scrPost:
		u.drawPost(c)
	case scrCompose:
		u.drawCompose(c)
	case scrEditor:
		u.drawEditor(c, u.ed)
	case scrChat:
		u.drawChat(c)
	case scrBye:
		u.drawBye(c)
	}
	if u.scr != scrBye {
		if u.toast != "" && u.frame-u.toastAt < 70 {
			u.drawToast(c)
		}
		if u.busy != "" {
			u.drawBusy(c)
		}
		if u.confirm != nil {
			u.drawModal(c)
		}
	}
	if u.th.Scan > 0 {
		c.Scanlines(u.th.Scan)
	}
	if fw > 0 {
		full.Fill(0, 0, full.W, full.H, u.th.Frame)
	}
	for y := 0; y < ih; y++ {
		copy(full.Pix[(y+fw)*full.Stride+fw*4:], c.Pix[y*c.Stride:y*c.Stride+iw*4])
	}
}

// ---------- chrome ----------

func (u *UI) headH() int { return u.px(34) }
func (u *UI) tabH() int  { return u.px(26) }

func (u *UI) contentTop() int { return u.headH() + u.tabH() + u.px(6) }
func (u *UI) footerY() int    { return u.ih - u.px(24) }

func (u *UI) text(c *Canvas, f *Font, x, y int, s string, col color.RGBA) int {
	if u.th.Glow {
		return c.TextGlow(f, x, y, s, col, u.th.BG)
	}
	return c.Text(f, x, y, s, col)
}

// header draws the title bar: the network's name, who's logged in, unread alerts, the clock.
func (u *UI) header(c *Canvas) {
	th := u.th
	h := u.headH()
	c.Fill(0, 0, c.W, h, th.Panel)
	c.Fill(0, h-1, c.W, 1, th.Dim)
	x := u.px(12)
	x = c.Text(u.fTitle, x, (h-u.fTitle.height)/2, "▓▒░", th.Dim)
	u.text(c, u.fTitle, x+u.px(6), (h-u.fTitle.height)/2, "CYBERSPACE", th.Hi)
	right := c.W - u.px(12)
	y := (h - u.fBody.height) / 2
	right -= u.fBody.Width("00:00")
	c.Text(u.fBody, right, y, u.now().Format("15:04"), th.FG)
	right -= u.px(16)
	if u.unread > 0 {
		s := fmt.Sprintf(" %d NEW ", u.unread)
		w := u.fBody.Width(s)
		right -= w
		if u.frame/10%2 == 0 || u.scr != scrList {
			c.Fill(right, y+u.px(2), w, u.fBody.height-u.px(2), th.Warn)
			c.Text(u.fBody, right, y, s, th.BG)
		} else {
			c.Text(u.fBody, right, y, s, th.Warn)
		}
		right -= u.px(12)
	}
	if u.me.Username != "" {
		s := "@" + u.me.Username
		c.TextRight(u.fBody, right, y, u.fBody.Ellipsize(s, u.px(200)), th.Dim)
	}
}

// headerBar is the header plus a single title strip; returns where content starts.
func (u *UI) headerBar(c *Canvas, title string) int {
	u.header(c)
	y := u.headH()
	c.Fill(0, y, c.W, u.tabH(), th(u).BG)
	u.text(c, u.fBody, u.px(14), y+(u.tabH()-u.fBody.height)/2, "» "+title, u.th.FG)
	c.HLineDashed(0, y+u.tabH()-1, c.W, u.px(4), u.px(3), u.th.Faint)
	return y + u.tabH()
}

func th(u *UI) Theme { return u.th }

func (u *UI) drawTabs(c *Canvas) {
	t := u.th
	y := u.headH()
	h := u.tabH()
	x := u.px(10)
	for i, name := range tabNames {
		label := " " + name + " "
		if i == tabAlerts && u.unread > 0 {
			label = fmt.Sprintf(" %s(%d) ", name, u.unread)
		}
		if i == tabMail && u.mailUnread() > 0 {
			label = fmt.Sprintf(" %s(%d) ", name, u.mailUnread())
		}
		w := u.fBody.Width(label)
		ty := y + (h-u.fBody.height)/2
		if i == u.tab {
			c.Fill(x, y+u.px(3), w, h-u.px(5), t.SelBG)
			c.Text(u.fBody, x, ty, label, t.SelFG)
		} else {
			c.Text(u.fBody, x, ty, label, t.Dim)
		}
		x += w + u.px(8)
	}
	// breadcrumb for drilled-in views
	st := u.tabs[u.tab]
	if len(st) > 1 {
		crumb := st[len(st)-1].title
		c.TextRight(u.fBody, c.W-u.px(14), y+(h-u.fBody.height)/2, u.fBody.Ellipsize("› "+crumb, c.W/2-u.px(20)), t.Hi)
	}
	c.HLineDashed(0, y+h-1, c.W, u.px(4), u.px(3), t.Faint)
}

// footer lists the buttons that do something here.
func (u *UI) footer(c *Canvas, keys [][2]string) {
	t := u.th
	y := u.footerY()
	c.Fill(0, y, c.W, c.H-y, t.Panel)
	c.Fill(0, y, c.W, 1, t.Faint)
	x := u.px(10)
	ty := y + (c.H-y-u.fSmall.height)/2
	for _, k := range keys {
		kw := u.fSmall.Width(k[0]) + u.px(8)
		lw := u.fSmall.Width(k[1])
		if x+kw+lw > c.W-u.px(6) {
			break
		}
		c.Fill(x, ty+u.px(1), kw, u.fSmall.height-u.px(1), t.Dim)
		c.Text(u.fSmall, x+u.px(4), ty, k[0], t.BG)
		x += kw + u.px(4)
		c.Text(u.fSmall, x, ty, k[1], t.FG)
		x += lw + u.px(12)
	}
}

func (u *UI) scrollbar(c *Canvas, top, bot, first, shown, total int) {
	if total <= shown || total == 0 {
		return
	}
	x := c.W - u.px(7)
	h := bot - top
	c.Fill(x+u.px(2), top, 1, h, u.th.Faint)
	th := max(u.px(14), h*shown/total)
	ty := top + (h-th)*first/max(1, total-shown)
	c.Fill(x, ty, u.px(5), th, u.th.Dim)
}

func (u *UI) centerMsg(c *Canvas, lines []string, col color.RGBA) {
	top, bot := u.contentTop(), u.footerY()
	y := (top+bot)/2 - len(lines)*u.fBody.height/2
	for _, l := range lines {
		c.TextCenter(u.fBody, c.W/2, y, l, col)
		y += u.fBody.height
	}
}

func (u *UI) spinner() string {
	s := []string{"|", "/", "-", "\\"}
	return s[u.frame/2%4]
}

// ---------- lists ----------

func (u *UI) rowH(v *listView) int {
	switch v.kind {
	case lvTopics, lvRooms:
		return u.fBody.height + u.px(6)
	case lvAlerts, lvMail:
		return u.fBody.height*2 + u.px(8)
	}
	return u.fBody.height*3 + u.px(8)
}

func (u *UI) pageRows(v *listView) int {
	return max(1, (u.footerY()-u.contentTop())/u.rowH(v))
}

func (u *UI) drawList(c *Canvas) {
	t := u.th
	u.header(c)
	u.drawTabs(c)
	v := u.view()
	top, bot := u.contentTop(), u.footerY()-u.px(2)
	rh := u.rowH(v)
	per := max(1, (bot-top)/rh)
	n := v.count()
	if v.sel >= n {
		v.sel = max(0, n-1)
	}
	if v.sel < v.scroll {
		v.scroll = v.sel
	}
	if v.sel >= v.scroll+per {
		v.scroll = v.sel - per + 1
	}
	pad := u.px(14)
	w := c.W - pad*2 - u.px(8)
	for i := v.scroll; i < n && i < v.scroll+per; i++ {
		y := top + (i-v.scroll)*rh
		sel := i == v.sel
		if sel {
			c.Fill(pad-u.px(6), y, c.W-pad*2+u.px(6), rh-u.px(4), t.Panel)
			c.Fill(pad-u.px(6), y, u.px(3), rh-u.px(4), t.FG)
		}
		x := pad + u.px(4)
		switch v.kind {
		case lvTopics:
			tp := v.topics[i]
			col := t.FG
			if sel {
				col = t.Hi
			}
			c.Text(u.fBody, x, y+u.px(2), "#"+tp.Name, col)
			c.TextRight(u.fBody, x+w, y+u.px(2), fmt.Sprintf("%d", tp.Count), t.Dim)
		case lvAlerts:
			nt := v.notifs[i]
			mark := "  "
			if !nt.Read {
				mark = "● "
			}
			col := t.FG
			if sel {
				col = t.Hi
			}
			if !nt.Read {
				c.Text(u.fBody, x, y+u.px(2), "●", t.Warn)
			}
			when := ago(nt.CreatedAt.Time, u.now())
			ww := u.fBody.Width(when)
			c.Text(u.fBody, x+u.fBody.Width(mark), y+u.px(2), u.fBody.Ellipsize(describe(nt), w-ww-u.fBody.Width(mark)-u.px(10)), col)
			c.TextRight(u.fBody, x+w, y+u.px(2), when, t.Dim)
			sub := nt.Metadata.ReplyContent
			if sub == "" {
				sub = nt.Metadata.PostContent
			}
			if sub != "" {
				c.Text(u.fBody, x+u.fBody.Width(mark), y+u.px(2)+u.fBody.height, u.fBody.Ellipsize("“"+preview(sub)+"”", w-u.fBody.Width(mark)), t.Dim)
			}
		case lvMail:
			r := v.mail[i]
			col := t.FG
			if sel {
				col = t.Hi
			}
			if r.unread > 0 {
				c.Text(u.fBody, x, y+u.px(2), "●", t.Warn)
			}
			c.Text(u.fBody, x+u.fBody.Width("● "), y+u.px(2), "@"+r.other, col)
			when := ago(time.UnixMilli(r.at), u.now())
			if r.at == 0 {
				when = ""
			}
			if r.unread > 0 {
				when = fmt.Sprintf("%d new  %s", r.unread, when)
			}
			c.TextRight(u.fBody, x+w, y+u.px(2), when, t.Dim)
			c.Text(u.fBody, x+u.fBody.Width("● "), y+u.px(2)+u.fBody.height, u.fBody.Ellipsize(preview(r.last), w-u.fBody.Width("● ")), t.Dim)
		case lvRooms:
			rm := v.rooms[i]
			col := t.FG
			if sel {
				col = t.Hi
			}
			c.Text(u.fBody, x, y+u.px(2), "#"+str(rm, "slug"), col)
			nx := x + u.fBody.Width("#"+str(rm, "slug")) + u.px(14)
			c.Text(u.fBody, nx, y+u.px(2), u.fBody.Ellipsize(str(rm, "name"), w-(nx-x)-u.px(90)), t.Dim)
			c.TextRight(u.fBody, x+w, y+u.px(2), fmt.Sprintf("%d here", int(num(rm, "onlineCount"))), t.Dim)
		default:
			p := v.posts[i]
			col := t.FG
			if sel {
				col = t.Hi
			}
			meta := "@" + p.Author
			mx := c.Text(u.fBody, x, y+u.px(2), meta, col)
			info := ago(p.CreatedAt.Time, u.now())
			if p.RepliesCount > 0 {
				info += fmt.Sprintf("  ↳%d", p.RepliesCount)
			}
			c.Text(u.fBody, mx+u.px(10), y+u.px(2), info, t.Dim)
			if len(p.Topics) > 0 {
				tps := "#" + strings.Join(p.Topics, " #")
				room := x + w - (mx + u.px(10) + u.fBody.Width(info)) - u.px(16)
				if room > u.px(40) {
					c.TextRight(u.fBody, x+w, y+u.px(2), u.fBody.Ellipsize(tps, room), t.Faint)
				}
			}
			body := preview(p.Content)
			if p.Title != "" {
				body = strings.ToUpper(p.Title) + " — " + body
			}
			if p.IsNSFW {
				body = "[NSFW] " + body
			}
			lines := u.fBody.Wrap(body, w)
			for j := 0; j < 2 && j < len(lines); j++ {
				l := lines[j]
				if j == 1 && len(lines) > 2 {
					l = u.fBody.Ellipsize(l+" …", w)
				}
				bc := t.Dim
				if sel {
					bc = t.FG
				}
				c.Text(u.fBody, x, y+u.px(2)+(j+1)*u.fBody.height, l, bc)
			}
		}
	}
	u.scrollbar(c, top, bot, v.scroll, per, n)

	switch {
	case v.err != "" && n == 0:
		u.centerMsg(c, append([]string{"NO CARRIER", ""}, u.fBody.Wrap(v.err, c.W-u.px(80))...), t.Warn)
	case v.loading && n == 0:
		u.centerMsg(c, []string{"RECEIVING " + u.spinner()}, t.FG)
	case v.loaded && n == 0:
		msg := "NOTHING HERE YET."
		switch v.kind {
		case lvFeed:
			msg = "YOUR FEED IS EMPTY - FOLLOW PEOPLE ON THE WEB, OR BROWSE TOPICS."
		case lvAlerts:
			msg = "NO ALERTS."
		case lvMail:
			msg = "NO CONVERSATIONS YET. PRESS X TO WRITE TO SOMEONE."
		case lvRooms:
			msg = "NO ROOMS ARE OPEN TO YOU RIGHT NOW."
		}
		u.centerMsg(c, u.fBody.Wrap(msg, c.W-u.px(80)), t.Dim)
	case v.loading:
		c.TextRight(u.fSmall, c.W-u.px(14), bot-u.fSmall.height, "LOADING MORE "+u.spinner(), t.Dim)
	case v.err != "":
		c.Fill(0, bot-u.fSmall.height-u.px(2), c.W, u.fSmall.height+u.px(2), t.BG)
		c.TextCenter(u.fSmall, c.W/2, bot-u.fSmall.height, u.fSmall.Ellipsize(v.err, c.W-u.px(20)), t.Warn)
	}

	keys := [][2]string{{"A", "OPEN"}}
	switch v.kind {
	case lvAlerts:
		keys = append(keys, [2]string{"X", "ALL READ"})
	case lvMail:
		keys = append(keys, [2]string{"X", "NEW MESSAGE"})
	case lvRooms:
		// rooms are joined by opening them; nothing to write here
	default:
		keys = append(keys, [2]string{"X", "NEW ENTRY"})
	}
	if len(u.tabs[u.tab]) > 1 {
		keys = append(keys, [2]string{"B", "BACK"})
	}
	keys = append(keys, [2]string{"Y", "REFRESH"}, [2]string{"L1/R1", "TABS"}, [2]string{"SEL", "THEME"}, [2]string{"MENU", "QUIT"})
	u.footer(c, keys)
}

// ---------- post ----------

func (u *UI) drawPost(c *Canvas) {
	t := u.th
	pv := u.pv
	title := "ENTRY"
	if pv.post.Author != "" {
		title = "ENTRY BY @" + strings.ToUpper(pv.post.Author)
	}
	u.headerBar(c, title)
	if pv.builtFor != len(pv.replies) || pv.lines == nil || (pv.loading && pv.post.Content == "") {
		u.buildPostLines()
	}
	top, bot := u.postArea()
	if pv.post.Content == "" && pv.loading {
		u.centerMsg(c, []string{"RECEIVING " + u.spinner()}, t.FG)
		u.footer(c, [][2]string{{"B", "BACK"}})
		return
	}
	if pv.err != "" && pv.post.Content == "" {
		u.centerMsg(c, append([]string{"NO CARRIER", ""}, u.fBody.Wrap(pv.err, c.W-u.px(80))...), t.Warn)
		u.footer(c, [][2]string{{"Y", "RETRY"}, {"B", "BACK"}})
		return
	}
	vis := u.visibleLines()
	u.clampScroll()
	pad := u.px(16)
	lh := u.fBody.height
	for i := pv.scroll; i < len(pv.lines) && i < pv.scroll+vis; i++ {
		l := pv.lines[i]
		y := top + (i-pv.scroll)*lh
		x := pad + u.px(8) + l.indent*u.fBody.cell*2
		sel := l.item == pv.sel && l.item >= 0
		if sel {
			c.Fill(pad-u.px(6)+l.indent*u.fBody.cell*2, y, u.px(3), lh, t.FG)
		}
		if l.indent > 0 && l.item > 0 {
			for d := 0; d < l.indent; d++ {
				c.Fill(pad+u.px(8)+d*u.fBody.cell*2+u.fBody.cell/2, y, 1, lh, t.Faint)
			}
		}
		if l.item == -1 && strings.HasPrefix(l.text, " REPLIES") || strings.HasPrefix(l.text, " LOADING REPLIES") {
			label := l.text
			if pv.loading {
				label = " LOADING REPLIES " + u.spinner() + " "
			}
			c.HLineDashed(pad, y+lh/2, c.W-pad*2, u.px(6), u.px(4), t.Faint)
			lw := u.fBody.Width(label)
			c.Fill(pad+u.px(20), y, lw, lh, t.BG)
			c.Text(u.fBody, pad+u.px(20), y, label, t.Dim)
			continue
		}
		col := t.FG
		switch {
		case l.meta:
			col = t.Hi
			if !sel {
				col = t.Dim
			}
			if l.item == 0 {
				col = t.Hi
			}
		case l.kind == lnHead:
			col = t.Hi
		case l.kind == lnDim:
			col = t.Dim
		case l.kind == lnQuote:
			col = t.Dim
		}
		if l.meta {
			u.text(c, u.fBody, x, y, l.text, col)
		} else {
			c.Text(u.fBody, x, y, l.text, col)
		}
	}
	u.scrollbar(c, top, bot, pv.scroll, vis, len(pv.lines))
	if pv.err != "" {
		c.TextCenter(u.fSmall, c.W/2, bot-u.fSmall.height, u.fSmall.Ellipsize(pv.err, c.W-u.px(20)), t.Warn)
	}
	label := "REPLY"
	if pv.sel > 0 {
		label = "REPLY TO THIS"
	}
	keys := [][2]string{{"A", label}}
	if pv.sel > 0 {
		keys = append(keys, [2]string{"X", "REPLY TO ENTRY"})
	}
	keys = append(keys, [2]string{"▲▼", "READ"}, [2]string{"L1/R1", "PAGE"}, [2]string{"Y", "RELOAD"}, [2]string{"B", "BACK"})
	u.footer(c, keys)
}

// ---------- compose ----------

func (u *UI) drawCompose(c *Canvas) {
	t := u.th
	top := u.headerBar(c, "NEW ENTRY")
	pad := u.px(16)
	y := top + u.px(10)
	w := c.W - pad*2
	row := func(i int, label string, h int, draw func(x, y, w int, sel bool)) {
		sel := u.comp.row == i
		if sel {
			c.Fill(pad-u.px(6), y-u.px(3), w+u.px(12), h+u.px(6), t.Panel)
			c.Fill(pad-u.px(6), y-u.px(3), u.px(3), h+u.px(6), t.FG)
		}
		lc := t.Dim
		if sel {
			lc = t.Hi
		}
		c.Text(u.fBody, pad+u.px(4), y, label, lc)
		lx := pad + u.px(4) + u.fBody.cell*9
		draw(lx, y, w-(lx-pad)-u.px(4), sel)
		y += h + u.px(10)
	}
	bodyLines := 6
	row(cmpBody, "BODY", u.fBody.height*bodyLines, func(x, y, w int, sel bool) {
		c.Border(x-u.px(6), y-u.px(2), w+u.px(6), u.fBody.height*bodyLines+u.px(4), 1, t.Faint)
		if strings.TrimSpace(u.comp.body) == "" {
			c.Text(u.fBody, x, y, "(empty - press A to write)", t.Faint)
			return
		}
		ls := u.fBody.Wrap(u.comp.body, w-u.px(8))
		for i := 0; i < bodyLines && i < len(ls); i++ {
			s := ls[i]
			if i == bodyLines-1 && len(ls) > bodyLines {
				s = u.fBody.Ellipsize(s+" …", w-u.px(8))
			}
			c.Text(u.fBody, x, y+i*u.fBody.height, s, t.FG)
		}
	})
	field := func(v, empty string) func(x, y, w int, sel bool) {
		return func(x, y, w int, sel bool) {
			if v == "" {
				c.Text(u.fBody, x, y, empty, t.Faint)
				return
			}
			c.Text(u.fBody, x, y, u.fBody.Ellipsize(v, w), t.FG)
		}
	}
	row(cmpTitle, "TITLE", u.fBody.height, field(u.comp.title, "(none)"))
	tp := ""
	if ts := cleanTopics(u.comp.topics); len(ts) > 0 {
		tp = "#" + strings.Join(ts, " #")
	}
	row(cmpTopics, "TOPICS", u.fBody.height, field(tp, "(none)"))
	toggle := func(on bool, yes, no string) func(x, y, w int, sel bool) {
		return func(x, y, w int, sel bool) {
			a, b := "[ ]", "[*]"
			if on {
				a, b = "[*]", "[ ]"
			}
			nx := c.Text(u.fBody, x, y, b+" "+no, pick(!on, t.FG, t.Faint))
			c.Text(u.fBody, nx+u.px(24), y, a+" "+yes, pick(on, t.FG, t.Faint))
		}
	}
	row(cmpPublic, "SHOW TO", u.fBody.height, toggle(u.comp.public, "EVERYONE", "MEMBERS"))
	row(cmpNSFW, "NSFW", u.fBody.height, toggle(u.comp.nsfw, "YES", "NO"))
	// transmit button
	sel := u.comp.row == cmpSend
	label := "  TRANSMIT  "
	bw := u.fBody.Width(label)
	bx := pad + u.px(4) + u.fBody.cell*9
	if sel {
		c.Fill(bx, y-u.px(2), bw, u.fBody.height+u.px(4), t.SelBG)
		c.Text(u.fBody, bx, y, label, t.SelFG)
	} else {
		c.Border(bx, y-u.px(2), bw, u.fBody.height+u.px(4), 1, t.FG)
		c.Text(u.fBody, bx, y, label, t.FG)
	}
	n := len([]rune(u.comp.body))
	c.Text(u.fSmall, bx+bw+u.px(16), y+u.px(3), fmt.Sprintf("%d chars · posting limit 2/min, 24/day", n), t.Faint)

	keys := [][2]string{{"A", "EDIT"}}
	if u.comp.row == cmpPublic || u.comp.row == cmpNSFW {
		keys = [][2]string{{"A/◀▶", "TOGGLE"}}
	}
	keys = append(keys, [2]string{"START", "TRANSMIT"}, [2]string{"Y", "DISCARD"}, [2]string{"B", "BACK (KEEPS DRAFT)"})
	u.footer(c, keys)
}

func pick(b bool, x, y color.RGBA) color.RGBA {
	if b {
		return x
	}
	return y
}

// ---------- login / dial ----------

var banner = []string{
	" ▄▄▄ ▄ ▄ ▄▄▄ ▄▄▄ ▄▄▄ ▄▄▄ ▄▄▄ ▄▄▄ ▄▄▄ ▄▄▄",
	" █   ▀▄▀ █▄▀ █▄  █▄▀ ▀▄  █▄█ █▄█ █   █▄ ",
	" ▀▀▀  ▀  ▀▀▀ ▀▀▀ ▀ ▀ ▀▀▀ ▀   ▀ ▀ ▀▀▀ ▀▀▀",
}

func (u *UI) drawBanner(c *Canvas, y int) int {
	f := u.fBig
	w := f.cell * len([]rune(banner[0]))
	x := (c.W - w) / 2
	for _, l := range banner {
		c.Text(f, x, y, l, u.th.FG)
		y += f.height
	}
	return y
}

func (u *UI) drawLogin(c *Canvas) {
	t := u.th
	y := u.drawBanner(c, u.px(26))
	c.TextCenter(u.fBody, c.W/2, y+u.px(4), "a text-only social network · handheld terminal", t.Dim)
	y += u.fBody.height + u.px(24)

	pad := u.px(70)
	w := c.W - pad*2
	rows := []struct{ label, val, empty string }{
		{"EMAIL", u.email, "(press A to type)"},
		{"PASSWORD", strings.Repeat("*", len([]rune(u.password))), "(press A to type)"},
	}
	for i, r := range rows {
		sel := u.loginRow == i
		c.Text(u.fBody, pad, y, r.label, pick(sel, t.Hi, t.Dim))
		fy := y + u.fBody.height + u.px(2)
		fh := u.fBody.height + u.px(8)
		c.Fill(pad, fy, w, fh, t.Panel)
		c.Border(pad, fy, w, fh, 1, pick(sel, t.FG, t.Faint))
		v := r.val
		col := t.FG
		if v == "" {
			v, col = r.empty, t.Faint
		}
		c.Text(u.fBody, pad+u.px(8), fy+u.px(4), u.fBody.Ellipsize(v, w-u.px(16)), col)
		y = fy + fh + u.px(12)
	}
	sel := u.loginRow == lgConnect
	label := "  CONNECT  "
	bw := u.fBody.Width(label)
	bx := (c.W - bw) / 2
	if sel {
		c.Fill(bx, y, bw, u.fBody.height+u.px(6), t.SelBG)
		c.Text(u.fBody, bx, y+u.px(3), label, t.SelFG)
	} else {
		c.Border(bx, y, bw, u.fBody.height+u.px(6), 1, t.FG)
		c.Text(u.fBody, bx, y+u.px(3), label, t.FG)
	}
	y += u.fBody.height + u.px(20)
	if u.loginErr != "" {
		for _, l := range u.fBody.Wrap(u.loginErr, c.W-u.px(60)) {
			c.TextCenter(u.fBody, c.W/2, y, l, t.Warn)
			y += u.fBody.height
		}
	} else {
		c.TextCenter(u.fSmall, c.W/2, y, "no account? sign up at cyberspace.online on a computer first.", t.Faint)
	}
	u.footer(c, [][2]string{{"A", "EDIT"}, {"START", "CONNECT"}, {"SEL", "THEME"}, {"MENU", "QUIT"}})
}

var handshake = []string{"ATZ", "OK", "ATDT CYBERSPACE.ONLINE", "RINGING...", "CONNECT 14400/ARQ/V42BIS"}

func (u *UI) drawDial(c *Canvas) {
	t := u.th
	y := u.drawBanner(c, u.px(40))
	y += u.px(30)
	x := u.px(120)
	el := u.frame - u.dialStart
	shown := min(len(handshake), el/6+1)
	for i := 0; i < shown; i++ {
		col := t.Dim
		if i == shown-1 {
			col = t.FG
		}
		c.Text(u.fBody, x, y, handshake[i], col)
		y += u.fBody.height
	}
	if shown == len(handshake) {
		y += u.fBody.height
		bar := (el / 2) % 12
		s := u.dialMsg + " " + strings.Repeat("▓", bar) + strings.Repeat("░", 12-bar)
		u.text(c, u.fBody, x, y, s, t.Hi)
	}
	u.footer(c, [][2]string{{"MENU", "HANG UP"}})
}

func (u *UI) drawBye(c *Canvas) {
	t := u.th
	n := u.byeFrame
	lines := []string{"", "NO CARRIER", "", "+++ATH0", "OK"}
	y := c.H/2 - u.fBody.height*2
	for i := 0; i < len(lines) && i <= n/2; i++ {
		c.TextCenter(u.fBody, c.W/2, y, lines[i], t.FG)
		y += u.fBody.height
	}
}

// ---------- overlays ----------

func (u *UI) box(c *Canvas, w, h int) (x, y int) {
	x, y = (c.W-w)/2, (c.H-h)/2
	c.Fill(x+u.px(6), y+u.px(6), w, h, mix(u.th.BG, color.RGBA{0, 0, 0, 255}, 0.6))
	c.Fill(x, y, w, h, u.th.Panel)
	c.Border(x, y, w, h, 2, u.th.FG)
	c.Border(x+u.px(4), y+u.px(4), w-u.px(8), h-u.px(8), 1, u.th.Faint)
	return
}

func (u *UI) drawModal(c *Canvas) {
	m := u.confirm
	w := c.W - u.px(120)
	lines := u.fBody.Wrap(m.text, w-u.px(40))
	h := (len(lines)+3)*u.fBody.height + u.px(30)
	x, y := u.box(c, w, h)
	ty := y + u.px(16)
	for _, l := range lines {
		c.TextCenter(u.fBody, x+w/2, ty, l, u.th.Hi)
		ty += u.fBody.height
	}
	ty += u.fBody.height
	opts := "[A] " + m.yes + "    [B] CANCEL"
	c.TextCenter(u.fBody, x+w/2, ty, opts, u.th.FG)
}

func (u *UI) drawBusy(c *Canvas) {
	w := u.px(320)
	h := u.fBody.height*2 + u.px(28)
	x, y := u.box(c, w, h)
	c.TextCenter(u.fBody, x+w/2, y+u.px(12), u.busy, u.th.Hi)
	bar := (u.frame / 2) % 16
	c.TextCenter(u.fBody, x+w/2, y+u.px(12)+u.fBody.height, strings.Repeat("▓", bar)+strings.Repeat("░", 16-bar), u.th.FG)
}

func (u *UI) drawToast(c *Canvas) {
	lines := u.fBody.Wrap(u.toast, c.W-u.px(60))
	h := len(lines)*u.fBody.height + u.px(8)
	y := u.footerY() - h - u.px(8)
	c.Fill(u.px(20), y, c.W-u.px(40), h, u.th.SelBG)
	for i, l := range lines {
		c.TextCenter(u.fBody, c.W/2, y+u.px(4)+i*u.fBody.height, l, u.th.SelFG)
	}
}
