package main

// Demo scenes for screenshots (go run . -shot feed). The users and entries are
// invented, so screenshots never show anyone's real posts.

import (
	"fmt"
	"image"
	"time"
)

var demoNow = time.Date(2026, 10, 24, 12, 30, 0, 0, time.Local)

func ft(d time.Duration) flexTime { return flexTime{demoNow.Add(-d)} }

func demoPosts() []Post {
	return []Post{
		{ID: "p1", Author: "phosphor_kid", Content: "Finally got my old 486 to boot off a CF card. The hard drive click of death is gone and so is my weekend. Worth it.", Topics: []string{"retro", "hardware"}, RepliesCount: 4, CreatedAt: ft(12 * time.Minute)},
		{ID: "p2", Author: "nightdesk", Title: "Static on channel 3", Content: "Anyone else remember tuning the VCR to channel 3 and waiting for the picture to roll in? Writing a short story about it.", Topics: []string{"writing"}, RepliesCount: 2, CreatedAt: ft(47 * time.Minute)},
		{ID: "p3", Author: "lowbaud", Content: "```\n  _____  ____  _  _ \n |_   _|| __ )| \\| |\n   | |  |  _ \\| .` |\n   |_|  |____/|_|\\_|\n```\nmade this on a terminal from 1987", Topics: []string{"ascii"}, RepliesCount: 9, CreatedAt: ft(2 * time.Hour)},
		{ID: "p4", Author: "mossgarden", Content: "Planted garlic today. Tell me your favorite fall vegetable and I'll tell you what kind of BBS sysop you'd have been.", RepliesCount: 0, CreatedAt: ft(5 * time.Hour)},
		{ID: "p5", Author: "copperline", Content: "Reading Where the Wizards Stay Up Late again. Every time I learn something new about the IMPs.", Topics: []string{"books", "history"}, RepliesCount: 1, CreatedAt: ft(26 * time.Hour)},
	}
}

func demoReplies() []Reply {
	return Thread([]Reply{
		{ID: "r1", PostID: "p1", Author: "lowbaud", Content: "CF cards in IDE adapters are the best thing that ever happened to retro PCs.", CreatedAt: ft(10 * time.Minute)},
		{ID: "r2", PostID: "p1", Author: "phosphor_kid", ParentID: "r1", Content: "Silent, fast, and I can swap it into a reader on my laptop. No more floppies.", CreatedAt: ft(8 * time.Minute)},
		{ID: "r3", PostID: "p1", Author: "nightdesk", Content: "What DOS are you running? I'm stuck on 6.22 forever.", CreatedAt: ft(6 * time.Minute)},
		{ID: "r4", PostID: "p1", Author: "copperline", ParentID: "r3", Content: "6.22 gang.", CreatedAt: ft(3 * time.Minute)},
	})
}

func demoScene(u *UI, name string) error {
	u.sync = true
	u.me = User{Username: "keith"}
	u.unread = 3
	u.scr = scrList
	feed := u.tabs[tabFeed][0]
	feed.posts, feed.loaded = demoPosts(), true
	al := u.tabs[tabAlerts][0]
	al.loaded = true
	al.notifs = []Notification{
		{ID: "n1", Type: "reply", Actor: "lowbaud", TargetID: "p9", TargetType: "post", CreatedAt: ft(4 * time.Minute)},
		{ID: "n2", Type: "new_follower", Actor: "mossgarden", CreatedAt: ft(40 * time.Minute)},
		{ID: "n3", Type: "reply_mention", Actor: "copperline", TargetID: "p5", TargetType: "post", CreatedAt: ft(3 * time.Hour), Read: true},
		{ID: "n4", Type: "poke", Actor: "nightdesk", CreatedAt: ft(30 * time.Hour), Read: true},
	}
	al.notifs[0].Metadata.ReplyContent = "That cassette deck is gorgeous. Where did you find it?"
	al.notifs[2].Metadata.ReplyContent = "@keith you'd like this one"
	tp := u.tabs[tabTopics][0]
	tp.loaded = true
	tp.topics = []Topic{{"retro", 412}, {"music", 377}, {"writing", 298}, {"linux", 241}, {"ascii", 186}, {"cyberdeck", 120}, {"books", 98}, {"handhelds", 61}}
	switch name {
	case "login":
		u.scr = scrLogin
		u.me = User{}
		u.unread = 0
		u.email = "keith@example.com"
		u.loginRow = lgPassword
	case "dial":
		u.dial("DIALING CYBERSPACE.ONLINE")
		u.frame = 60
	case "feed":
		feed.sel = 0
	case "feed2":
		feed.sel = 2
	case "topics":
		u.tab = tabTopics
		tp.sel = 1
	case "alerts":
		u.tab = tabAlerts
	case "post", "post2":
		u.pv = &postView{post: demoPosts()[0], replies: demoReplies(), builtFor: -1}
		u.scr = scrPost
		if name == "post2" {
			u.keyPost(BtnDown)
			u.keyPost(BtnDown)
		}
	case "art":
		u.pv = &postView{post: demoPosts()[2], builtFor: -1}
		u.scr = scrPost
	case "compose":
		u.scr = scrCompose
		u.comp = compose{body: "Writing this from a Miyoo Mini Plus. The d-pad keyboard is slow but it feels like typing on a pager in 1996.", topics: "handhelds, retro"}
		u.comp.row = cmpSend
	case "editor":
		u.scr = scrPost
		u.pv = &postView{post: demoPosts()[0], replies: demoReplies(), builtFor: -1}
		u.pv.sel = 3
		u.openReply()
		for _, r := range "I'm on 6.22 too" {
			u.ed.insert(string(r))
		}
		u.ed.row, u.ed.col = 2, 4
	case "mail":
		demoMail(u)
		u.tab = tabMail
	case "dm":
		demoMail(u)
		demoDM(u)
	case "rooms":
		demoRooms(u)
		u.tab = tabRooms
	case "room":
		demoRooms(u)
		demoRoom(u)
	case "users":
		demoRooms(u)
		demoRoom(u)
		u.chat.showUsers = true
	case "confirm":
		u.scr = scrCompose
		u.comp = compose{body: "hello from the handheld"}
		u.sendEntry()
	default:
		return fmt.Errorf("unknown scene %q (login, dial, feed, feed2, topics, alerts, post, post2, art, compose, editor, confirm, mail, dm, rooms, room, users)", name)
	}
	return nil
}

// appIcon draws a little green terminal with a blinking cursor.
func appIcon(s int) *image.RGBA {
	c := NewCanvas(s, s)
	t := themes[0]
	c.Fill(0, 0, s, s, rgb(0x14, 0x18, 0x14))
	bh := s * 11 / 20 // terminal height, leaving two label lines below
	c.Fill(s/10, s/10, s-s/5, bh, t.BG)
	c.Border(s/10, s/10, s-s/5, bh, 2, t.Dim)
	f := loadFont(vt323TTF, float64(s)/3.2)
	c.Text(f, s/10+5, s/10+3, ">_", t.FG)
	g := loadFont(vt323TTF, float64(s)/6.5)
	y := s/10 + bh + s/40
	c.TextCenter(g, s/2, y, "CYBERSPACE", t.FG)
	c.TextCenter(g, s/2, y+s/7, ".MIYOO", t.Dim)
	return c.RGBA
}

// demoMail fills the MAIL tab with three invented conversations.
func demoMail(u *UI) {
	at := func(d time.Duration) float64 { return float64(demoNow.Add(-d).UnixMilli()) }
	u.convs = map[string]map[string]any{
		"c1": {"otherUsername": "alice", "lastMessage": "did the cassette deck work out?", "lastMessageAt": at(4 * time.Minute), "unreadCount": 2.0},
		"c2": {"otherUsername": "mossgarden", "lastMessage": "garlic goes in before the first frost", "lastMessageAt": at(3 * time.Hour), "unreadCount": 0.0},
		"c3": {"otherUsername": "phosphor_kid", "lastMessage": "send me the CF card image", "lastMessageAt": at(26 * time.Hour), "unreadCount": 1.0},
	}
	u.syncMail()
	u.unread = 0
}

// demoDM opens the conversation with alice.
func demoDM(u *UI) {
	at := func(d time.Duration) float64 { return float64(demoNow.Add(-d).UnixMilli()) }
	cv := newChat(false, "c1", "@alice")
	cv.loaded = true
	cv.msgs = recMap{
		"m1": {"senderUsername": "alice", "content": "Did the cassette deck work out? I saw you were hunting for a belt.", "timestamp": at(20 * time.Minute)},
		"m2": {"senderUsername": "keith", "content": "It works! Needed a new belt and a bit of contact cleaner. *Sounds* great.", "timestamp": at(12 * time.Minute)},
		"m3": {"senderUsername": "alice", "content": "Nice. Send me a clip when you get a chance.", "timestamp": at(4 * time.Minute)},
	}
	cv.presence = recMap{"u2": {"username": "alice", "typing": true, "timestamp": at(0)}}
	u.chat = cv
	u.scr = scrChat
}

// demoRooms fills the CIRC tab with two invented rooms.
func demoRooms(u *UI) {
	v := u.tabs[tabRooms][0]
	v.rooms = []map[string]any{
		{"id": "r1", "slug": "general", "name": "General", "onlineCount": 3.0},
		{"id": "r2", "slug": "retro-tech", "name": "Retro Tech", "onlineCount": 7.0},
	}
	v.loaded = true
	u.scr = scrList
}

// demoRoom opens #general with a short backlog and three people in it.
func demoRoom(u *UI) {
	at := func(d time.Duration) float64 { return float64(demoNow.Add(-d).UnixMilli()) }
	cv := newChat(true, "general", "#general")
	cv.loaded = true
	cv.msgs = recMap{
		"x1": {"username": "lowbaud", "isChatAdmin": true, "content": "ahoy, new arrivals", "timestamp": at(15 * time.Minute)},
		"x2": {"username": "copperline", "content": "hi from the cold side of the house", "timestamp": at(9 * time.Minute)},
		"x3": {"username": "keith", "content": "Miyoo Mini Plus on OnionOS. Cyberspace.miyoo works.", "timestamp": at(2 * time.Minute)},
	}
	cv.presence = recMap{
		"u1": {"username": "lowbaud", "isChatAdmin": true, "lastSeen": at(0), "lastActivity": at(1 * time.Minute)},
		"u2": {"username": "copperline", "lastSeen": at(0), "lastActivity": at(5 * time.Minute)},
		"u3": {"username": "nightdesk", "lastSeen": at(0), "lastActivity": at(30 * time.Minute)},
	}
	u.chat = cv
	u.scr = scrChat
}
