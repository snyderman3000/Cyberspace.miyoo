package main

// Turning Cyberspace's markdown into lines for a 640×480 terminal.

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type lineKind int

const (
	lnText lineKind = iota
	lnHead
	lnQuote
	lnCode
	lnDim
)

type textLine struct {
	s    string
	kind lineKind
}

var (
	reImage  = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	reBold   = regexp.MustCompile(`(\*\*|__)(.+?)(\*\*|__)`)
	reItal   = regexp.MustCompile(`(^|[\s(])[*_]([^*_\s][^*_]*?)[*_]([\s).,!?:;]|$)`)
	reStrike = regexp.MustCompile(`~~(.+?)~~`)
	reCode   = regexp.MustCompile("`([^`]+)`")
)

// inline strips inline markdown down to readable text.
func inline(s string) string {
	s = reImage.ReplaceAllStringFunc(s, func(m string) string {
		alt := reImage.FindStringSubmatch(m)[1]
		if alt == "" {
			alt = "image"
		}
		return "[IMG: " + alt + "]"
	})
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		p := reLink.FindStringSubmatch(m)
		if p[1] == p[2] {
			return p[2]
		}
		return p[1] + " <" + p[2] + ">"
	})
	s = reBold.ReplaceAllString(s, "$2")
	s = reStrike.ReplaceAllString(s, "$1")
	s = reCode.ReplaceAllString(s, "$1")
	s = reItal.ReplaceAllString(s, "$1$2$3")
	s = strings.ReplaceAll(s, "\t", "    ")
	return s
}

// renderMarkdown lays content out into lines that fit width w.
func renderMarkdown(content string, f *Font, w int) []textLine {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	var out []textLine
	inFence := false
	blank := 0
	for _, raw := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(raw)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || strings.HasPrefix(raw, "    ") || looksLikeArt(raw) {
			for _, piece := range f.Chop(strings.ReplaceAll(strings.TrimRight(raw, " "), "\t", "    "), w) {
				out = append(out, textLine{piece, lnCode})
			}
			blank = 0
			continue
		}
		if trim == "" {
			if blank == 0 && len(out) > 0 {
				out = append(out, textLine{"", lnText})
			}
			blank++
			continue
		}
		blank = 0
		switch {
		case strings.HasPrefix(trim, "#"):
			h := strings.TrimSpace(strings.TrimLeft(trim, "#"))
			for _, l := range f.Wrap(strings.ToUpper(inline(h)), w) {
				out = append(out, textLine{l, lnHead})
			}
		case strings.HasPrefix(trim, ">"):
			q := strings.TrimSpace(strings.TrimLeft(trim, ">"))
			for _, l := range f.Wrap(inline(q), w-f.cell*2) {
				out = append(out, textLine{"│ " + l, lnQuote})
			}
		case trim == "---" || trim == "***" || trim == "___":
			out = append(out, textLine{strings.Repeat("─", max(1, w/max(1, f.cell))), lnDim})
		case strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* ") || strings.HasPrefix(trim, "+ "):
			for i, l := range f.Wrap(inline(trim[2:]), w-f.cell*2) {
				p := "  "
				if i == 0 {
					p = "• "
				}
				out = append(out, textLine{p + l, lnText})
			}
		default:
			for _, l := range f.Wrap(inline(trim), w) {
				out = append(out, textLine{l, lnText})
			}
		}
	}
	for len(out) > 0 && out[len(out)-1].s == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		out = append(out, textLine{"(empty)", lnDim})
	}
	return out
}

// looksLikeArt spots ASCII-art lines that would be ruined by reflowing:
// runs of spaces inside the line, or mostly drawing characters.
func looksLikeArt(s string) bool {
	t := strings.TrimSpace(s)
	if len([]rune(t)) < 3 {
		return false
	}
	if strings.Contains(t, "   ") {
		return true
	}
	sym := 0
	n := 0
	for _, r := range t {
		n++
		if strings.ContainsRune(`|/\_-=+*#@<>()[]{}^~.:'"`+"`", r) || (r >= 0x2500 && r <= 0x259f) {
			sym++
		}
	}
	return n >= 4 && sym*10 >= n*7
}

// preview collapses content into one plain line for list rows.
func preview(content string) string {
	var parts []string
	inFence := false
	for _, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			if inFence {
				parts = append(parts, "[art]")
			}
			continue
		}
		if inFence || t == "" {
			continue
		}
		if looksLikeArt(l) {
			if len(parts) == 0 || parts[len(parts)-1] != "[art]" {
				parts = append(parts, "[art]")
			}
			continue
		}
		t = strings.TrimLeft(t, "#> ")
		parts = append(parts, inline(t))
	}
	return strings.Join(parts, " ")
}

func ago(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	if t.Year() == now.Year() {
		return t.Format("Jan 2")
	}
	return t.Format("Jan 2006")
}

// describe says what a notification is about in a line.
func describe(n Notification) string {
	who := "@" + n.Actor
	if n.Actor == "" || n.Actor == "system" {
		who = "SYSTEM"
	}
	switch n.Type {
	case "reply":
		return who + " replied to your entry"
	case "thread_reply":
		return who + " replied in a thread you watch"
	case "bookmark":
		return who + " bookmarked your entry"
	case "new_follower":
		return who + " followed you"
	case "unfollowed":
		return who + " unfollowed you"
	case "new_post_following", "new_post_friend":
		return who + " posted a new entry"
	case "poke":
		return who + " poked you"
	case "post_mention", "reply_mention":
		return who + " mentioned you"
	case "chat_mention":
		if n.Metadata.RoomName != "" {
			return who + " mentioned you in #" + n.Metadata.RoomName
		}
		return who + " mentioned you in chat"
	case "dm_message":
		return who + " sent you c-mail"
	case "guild_new_thread":
		return who + " started a thread in " + n.Metadata.GuildName
	case "post_cooldown":
		return "posting cooldown in effect"
	case "rate_limit_warning":
		return "rate limit warning"
	}
	return who + " · " + strings.ReplaceAll(n.Type, "_", " ")
}

// opensPost reports whether a notification leads to an entry we can show.
func opensPost(n Notification) bool {
	if n.TargetID == "" {
		return false
	}
	switch n.Type {
	case "new_follower", "unfollowed", "poke", "chat_mention", "dm_message", "guild_chat_message":
		return false
	}
	return n.TargetType == "post" || n.TargetType == "reply" || n.TargetType == ""
}

// cleanTopics turns "Retro, Linux  miyoo" into ["retro","linux","miyoo"] (max 3).
func cleanTopics(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '#' }) {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) == 3 {
			break
		}
	}
	return out
}
