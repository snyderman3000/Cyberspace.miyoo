package main

// REST calls for C-Mail (direct messages) and cIRC (rooms). Sending always goes
// through REST; the live feed is read from the database stream (rtdb.go).

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// rows decodes an array of JSON objects.
func (c *Client) rows(method, path string, in any) ([]map[string]any, error) {
	env, err := c.do(method, path, in)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	if len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, &out); err != nil {
			return nil, fmt.Errorf("bad response from server: %v", err)
		}
	}
	return out, nil
}

// one decodes a single JSON object.
func (c *Client) one(method, path string, in any) (map[string]any, error) {
	env, err := c.do(method, path, in)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if len(env.Data) > 0 {
		json.Unmarshal(env.Data, &out)
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// ---- C-Mail ----

// MailList returns every conversation, unread first.
func (c *Client) MailList() ([]map[string]any, error) {
	rs, err := c.rows("GET", "/v1/cmail", nil)
	if err != nil {
		return nil, err
	}
	// flatten the other participant so every conversation reads the same way
	for _, r := range rs {
		if ou, ok := r["otherUser"].(map[string]any); ok {
			r["otherUsername"] = str(ou, "username")
			r["otherUserId"] = str(ou, "userId")
		}
	}
	return rs, nil
}

// MailStart opens (or finds) the conversation with a user.
func (c *Client) MailStart(username string) (string, string, error) {
	m, err := c.one("POST", "/v1/cmail", map[string]string{"recipientUsername": username})
	if err != nil {
		return "", "", err
	}
	other := username
	if ou, ok := m["otherUser"].(map[string]any); ok && str(ou, "username") != "" {
		other = str(ou, "username")
	}
	return str(m, "conversationId"), other, nil
}

// MailHistory loads up to 50 messages, newest page first when before is 0.
func (c *Client) MailHistory(id string, before int64) ([]map[string]any, error) {
	q := "limit=50"
	if before > 0 {
		q += fmt.Sprintf("&before=%d", before)
	}
	return c.rows("GET", "/v1/cmail/"+url.PathEscape(id)+"?"+q, nil)
}

// MailSend sends a message and returns its id.
func (c *Client) MailSend(id, content string) (string, error) {
	m, err := c.one("POST", "/v1/cmail/"+url.PathEscape(id), map[string]string{"content": content})
	return str(m, "messageId"), err
}

func (c *Client) MailRead(id string) error {
	_, err := c.do("POST", "/v1/cmail/"+url.PathEscape(id)+"/read", nil)
	return err
}

// TypingOn says we're typing and returns how often to repeat it.
func (c *Client) TypingOn(id string) (heartbeatMs, staleMs int64, err error) {
	m, err := c.one("POST", "/v1/cmail/"+url.PathEscape(id)+"/typing", nil)
	if err != nil {
		return 0, 0, err
	}
	hb, st := num(m, "heartbeatMs"), num(m, "staleAfterMs")
	if hb <= 0 {
		hb = 3000
	}
	if st <= 0 {
		st = 9000
	}
	return hb, st, nil
}

func (c *Client) TypingOff(id string) error {
	_, err := c.do("DELETE", "/v1/cmail/"+url.PathEscape(id)+"/typing", nil)
	return err
}

// ---- cIRC ----

// Rooms lists the rooms available to us.
func (c *Client) Rooms() ([]map[string]any, error) {
	return c.rows("GET", "/v1/circ", nil)
}

func (c *Client) RoomHistory(id string, before int64) ([]map[string]any, error) {
	q := "limit=50"
	if before > 0 {
		q += fmt.Sprintf("&before=%d", before)
	}
	return c.rows("GET", "/v1/circ/"+url.PathEscape(id)+"?"+q, nil)
}

// RoomSend posts a line to a room. It returns the server's reply for commands
// such as /help, which have no message of their own.
func (c *Client) RoomSend(id, content string) (messageID, reply string, err error) {
	m, err := c.one("POST", "/v1/circ/"+url.PathEscape(id), map[string]string{"content": content})
	return str(m, "messageId"), str(m, "reply"), err
}

// RoomUsers is who is in the room right now.
func (c *Client) RoomUsers(id string) ([]map[string]any, error) {
	return c.rows("GET", "/v1/circ/"+url.PathEscape(id)+"/users", nil)
}

// Presence announces us in the room and returns the timing to keep to.
func (c *Client) Presence(id string, lastActivityMs int64) (heartbeatMs, staleMs, idleMs int64, err error) {
	m, err := c.one("POST", "/v1/circ/"+url.PathEscape(id)+"/presence", map[string]int64{"lastActivity": lastActivityMs})
	if err != nil {
		return 0, 0, 0, err
	}
	hb, st, id2 := num(m, "heartbeatMs"), num(m, "staleAfterMs"), num(m, "idleAfterMs")
	if hb <= 0 {
		hb = 30000
	}
	if st <= 0 {
		st = 180000
	}
	if id2 <= 0 {
		id2 = 600000
	}
	return hb, st, id2, nil
}

func (c *Client) PresenceLeave(id string) error {
	_, err := c.do("DELETE", "/v1/circ/"+url.PathEscape(id)+"/presence", nil)
	return err
}
