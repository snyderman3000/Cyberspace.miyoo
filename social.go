package main

// The MAIL and CIRC tabs: the list of conversations and the list of rooms.

import (
	"context"
	"sort"
	"strings"
)

// convRow is one line in the MAIL list.
type convRow struct {
	id, other, last string
	at, unread      int64
}

// convList orders the conversations unread first, then newest activity.
func (u *UI) convList() []convRow {
	out := make([]convRow, 0, len(u.convs))
	for id, m := range u.convs {
		other := str(m, "otherUsername")
		if other == "" {
			other = "unknown"
		}
		out = append(out, convRow{id: id, other: other, last: str(m, "lastMessage"),
			at: num(m, "lastMessageAt"), unread: num(m, "unreadCount")})
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].unread > 0) != (out[j].unread > 0) {
			return out[i].unread > 0
		}
		if out[i].at != out[j].at {
			return out[i].at > out[j].at
		}
		return out[i].id < out[j].id
	})
	return out
}

// syncMail copies the conversation state into the MAIL tab's list.
func (u *UI) syncMail() {
	v := u.tabs[tabMail][0]
	v.mail = u.convList()
	v.loaded = true
}

// mailUnread is the total of unread conversations, for the tab label.
func (u *UI) mailUnread() int64 {
	var n int64
	for _, m := range u.convs {
		n += num(m, "unreadCount")
	}
	return n
}

// markConvRead clears a conversation's unread count locally.
func (u *UI) markConvRead(id string) {
	if m, ok := u.convs[id]; ok {
		m["unreadCount"] = float64(0)
		u.syncMail()
	}
}

func (u *UI) refreshMail() {
	v := u.tabs[tabMail][0]
	if v.loading {
		return
	}
	v.loading = true
	u.async(func() func() {
		rows, err := u.api.MailList()
		return func() {
			v.loading = false
			if err != nil {
				v.err = Friendly(err)
				if err == ErrUnauthorized {
					u.logout()
				}
				return
			}
			v.err = ""
			u.convs.load(rows, "conversationId")
			u.syncMail()
		}
	})
}

// newDM asks who to write to, then opens that conversation.
func (u *UI) newDM() {
	e := newEditor("MESSAGE WHO?", "", 20, false)
	e.Hint = "a username"
	e.DoneLabel = "OPEN"
	e.OnDone = func(s string) {
		name := strings.TrimPrefix(strings.TrimSpace(s), "@")
		if name == "" {
			return
		}
		u.busy = "DIALING"
		u.async(func() func() {
			id, other, err := u.api.MailStart(name)
			return func() {
				u.busy = ""
				if err != nil {
					u.showToast(Friendly(err))
					return
				}
				u.openDM(id, other)
			}
		})
	}
	u.openEditor(e)
}

func (u *UI) refreshRooms() {
	v := u.tabs[tabRooms][0]
	if v.loading {
		return
	}
	v.loading = true
	u.async(func() func() {
		rows, err := u.api.Rooms()
		return func() {
			v.loading = false
			if err != nil {
				v.err = Friendly(err)
				if err == ErrUnauthorized {
					u.logout()
				}
				return
			}
			v.err = ""
			v.rooms = rows
			v.loaded = true
		}
	})
}

// followConvs keeps the conversation list live from the account-wide stream.
func (u *UI) followConvs(ctx context.Context, uid string) {
	u.follow(ctx, "/user_conversations/"+uid, "", func(ev sseEvent) {
		u.convs.apply(ev)
		u.syncMail()
	}, nil)
}
