package main

// The on-screen keyboard: a full text editor driven by the d-pad.
//   A type · B delete · X space · Y shift · L1/R1 move cursor
//   L2 symbols · R2 new line · START done · SELECT cancel (keeps the draft)

import (
	"fmt"
	"strings"
)

var kbLayers = [3][4]string{
	{"1234567890-", "qwertyuiop'", "asdfghjkl;:", "zxcvbnm,.?!"},
	{"!@#$%^&*()_", "QWERTYUIOP\"", "ASDFGHJKL;:", "ZXCVBNM<>?/"},
	{"@#$%&*()-+=", "[]{}<>/\\|_~", "`^\"';:,.?!=", "█▓▒░▀▄─│┼┌┐"},
}

const kbCols = 11

var kbSpecials = []struct {
	label      string
	start, end int
}{{"SHIFT", 0, 1}, {"SYM", 2, 3}, {"SPACE", 4, 6}, {"↵", 7, 7}, {"DEL", 8, 8}, {"DONE", 9, 10}}

const (
	shiftOff = iota
	shiftOnce
	shiftLock
)

type Editor struct {
	Title     string
	Hint      string // shown when empty
	Text      []rune
	Cur       int
	Max       int
	Multiline bool
	Mask      bool
	Lower     bool // force lowercase (topics)
	DoneLabel string

	row, col int
	layer    int // 0 letters, 2 symbols
	shift    int
	scroll   int // first visible wrapped line

	OnDone   func(string)
	OnCancel func(string)
}

func newEditor(title, text string, max int, multiline bool) *Editor {
	r := []rune(text)
	return &Editor{Title: title, Text: r, Cur: len(r), Max: max, Multiline: multiline, row: 1, col: 0, DoneLabel: "DONE"}
}

func (e *Editor) String() string { return string(e.Text) }

func (e *Editor) activeLayer() int {
	if e.layer == 2 {
		return 2
	}
	if e.shift != shiftOff {
		return 1
	}
	return 0
}

func specialIndex(col int) int {
	for i, k := range kbSpecials {
		if col >= k.start && col <= k.end {
			return i
		}
	}
	return 0
}

func (e *Editor) insert(s string) {
	for _, r := range s {
		if e.Max > 0 && len(e.Text) >= e.Max {
			return
		}
		if r == '\n' && !e.Multiline {
			continue
		}
		if e.Lower {
			r = []rune(strings.ToLower(string(r)))[0]
		}
		e.Text = append(e.Text[:e.Cur], append([]rune{r}, e.Text[e.Cur:]...)...)
		e.Cur++
	}
}

func (e *Editor) backspace() {
	if e.Cur > 0 {
		e.Text = append(e.Text[:e.Cur-1], e.Text[e.Cur:]...)
		e.Cur--
	}
}

func (e *Editor) typeKey() {
	ch := string([]rune(kbLayers[e.activeLayer()][e.row])[e.col])
	e.insert(ch)
	if e.shift == shiftOnce && e.layer != 2 {
		e.shift = shiftOff
	}
}

func (e *Editor) cycleShift() {
	if e.layer == 2 {
		e.layer = 0
	}
	e.shift = (e.shift + 1) % 3
}

func (e *Editor) toggleSym() {
	if e.layer == 2 {
		e.layer = 0
	} else {
		e.layer = 2
	}
}

// Key handles a button; it returns true when the editor should close.
func (e *Editor) Key(k int) (closed bool) {
	rows := len(kbLayers[0]) + 1
	switch k {
	case BtnUp:
		e.row = (e.row - 1 + rows) % rows
		e.fixCol()
	case BtnDown:
		e.row = (e.row + 1) % rows
		e.fixCol()
	case BtnLeft, BtnRight:
		d := 1
		if k == BtnLeft {
			d = -1
		}
		if e.row < len(kbLayers[0]) {
			e.col = (e.col + d + kbCols) % kbCols
		} else {
			i := (specialIndex(e.col) + d + len(kbSpecials)) % len(kbSpecials)
			e.col = kbSpecials[i].start
		}
	case BtnA:
		if e.row < len(kbLayers[0]) {
			e.typeKey()
			return false
		}
		switch kbSpecials[specialIndex(e.col)].label {
		case "SHIFT":
			e.cycleShift()
		case "SYM":
			e.toggleSym()
		case "SPACE":
			e.insert(" ")
		case "↵":
			e.insert("\n")
		case "DEL":
			e.backspace()
		case "DONE":
			return e.done()
		}
	case BtnB:
		e.backspace()
	case BtnX:
		e.insert(" ")
	case BtnY:
		e.cycleShift()
	case BtnL2:
		e.toggleSym()
	case BtnR2:
		e.insert("\n")
	case BtnL1:
		if e.Cur > 0 {
			e.Cur--
		}
	case BtnR1:
		if e.Cur < len(e.Text) {
			e.Cur++
		}
	case BtnStart:
		return e.done()
	case BtnSelect, BtnMenu:
		if e.OnCancel != nil {
			e.OnCancel(e.String())
		}
		return true
	}
	return false
}

func (e *Editor) done() bool {
	if e.OnDone != nil {
		e.OnDone(e.String())
	}
	return true
}

// when moving onto the special row, land on the key under the cursor
func (e *Editor) fixCol() {
	if e.row == len(kbLayers[0]) {
		e.col = kbSpecials[specialIndex(e.col)].start
	}
}

// layout wraps the text into lines of at most cols characters, preferring
// word breaks; it returns each line's start offset and the cursor's line/column.
func (e *Editor) layout(cols int) (starts []int, curLine, curCol int) {
	if cols < 4 {
		cols = 4
	}
	t := e.Text
	starts = []int{0}
	lineStart, lastSpace := 0, -1
	for i := 0; i < len(t); i++ {
		if t[i] == '\n' {
			starts = append(starts, i+1)
			lineStart, lastSpace = i+1, -1
			continue
		}
		if t[i] == ' ' {
			lastSpace = i
		}
		if i-lineStart >= cols {
			brk := i
			if lastSpace > lineStart {
				brk = lastSpace + 1
			}
			starts = append(starts, brk)
			lineStart, lastSpace = brk, -1
			if t[i] == ' ' && brk == i+1 {
				lastSpace = -1
			}
		}
	}
	curLine = 0
	for li, s := range starts {
		if e.Cur >= s {
			curLine = li
		}
	}
	curCol = e.Cur - starts[curLine]
	return starts, curLine, curCol
}

func (e *Editor) lineText(starts []int, li int) string {
	end := len(e.Text)
	if li+1 < len(starts) {
		end = starts[li+1]
	}
	s := e.Text[starts[li]:end]
	out := strings.TrimRight(string(s), "\n")
	if e.Mask {
		return strings.Repeat("*", len([]rune(out)))
	}
	return out
}

func (u *UI) drawEditor(c *Canvas, e *Editor) {
	th := u.th
	top := u.headerBar(c, e.Title)
	pad := u.px(14)

	// text area
	boxY := top + u.px(8)
	lines := 6
	if !e.Multiline {
		lines = 1
	}
	lh := u.fBody.height
	boxH := lines*lh + u.px(12)
	boxW := c.W - pad*2
	c.Fill(pad, boxY, boxW, boxH, th.Panel)
	c.Border(pad, boxY, boxW, boxH, 1, th.Dim)
	cols := (boxW - u.px(16)) / u.fBody.cell
	starts, cl, cc := e.layout(cols)
	if cl < e.scroll {
		e.scroll = cl
	}
	if cl >= e.scroll+lines {
		e.scroll = cl - lines + 1
	}
	tx := pad + u.px(8)
	for i := 0; i < lines; i++ {
		li := e.scroll + i
		if li >= len(starts) {
			break
		}
		c.Text(u.fBody, tx, boxY+u.px(6)+i*lh, e.lineText(starts, li), th.FG)
	}
	if len(e.Text) == 0 && e.Hint != "" {
		c.Text(u.fBody, tx+u.fBody.cell*2, boxY+u.px(6), e.Hint, th.Faint)
	}
	if u.frame/6%2 == 0 {
		cx := tx + cc*u.fBody.cell
		cy := boxY + u.px(6) + (cl-e.scroll)*lh
		c.Fill(cx, cy+u.px(2), u.fBody.cell, lh-u.px(3), th.FG)
		if e.Cur < len(e.Text) && e.Text[e.Cur] != '\n' {
			ch := string(e.Text[e.Cur])
			if e.Mask {
				ch = "*"
			}
			c.Text(u.fBody, cx, cy, ch, th.BG)
		}
	}
	// counter + mode
	info := fmt.Sprintf("%d/%d", len(e.Text), e.Max)
	if e.Max <= 0 {
		info = fmt.Sprintf("%d", len(e.Text))
	}
	mode := "abc"
	switch {
	case e.layer == 2:
		mode = "SYM"
	case e.shift == shiftOnce:
		mode = "Abc"
	case e.shift == shiftLock:
		mode = "ABC"
	}
	y := boxY + boxH + u.px(3)
	c.TextRight(u.fSmall, pad+boxW, y, info, th.Dim)
	c.Text(u.fSmall, pad, y, "MODE "+mode, th.Dim)
	if len(starts) > lines {
		c.TextCenter(u.fSmall, c.W/2, y, fmt.Sprintf("LINE %d/%d", cl+1, len(starts)), th.Faint)
	}
	y += u.fSmall.height + u.px(4)

	// keys
	layer := kbLayers[e.activeLayer()]
	kw := boxW / kbCols
	kh := (u.footerY() - y - u.px(4)) / 5
	if kh > u.px(36) {
		kh = u.px(36)
	}
	for r, row := range layer {
		for col, ch := range []rune(row) {
			sel := e.row == r && e.col == col
			kx := pad + col*kw
			u.key(c, kx, y, kw-u.px(4), kh-u.px(4), sel)
			fg := th.FG
			if sel {
				fg = th.SelFG
			}
			s := string(ch)
			c.Text(u.fBody, kx+(kw-u.px(4)-u.fBody.Width(s))/2, y+(kh-u.px(4)-u.fBody.height)/2, s, fg)
		}
		y += kh
	}
	for i, k := range kbSpecials {
		sel := e.row == len(layer) && specialIndex(e.col) == i
		kx := pad + k.start*kw
		w := (k.end-k.start+1)*kw - u.px(4)
		u.key(c, kx, y, w, kh-u.px(4), sel)
		label := k.label
		if label == "DONE" {
			label = e.DoneLabel
		}
		lit := (label == "SHIFT" && e.shift != shiftOff) || (label == "SYM" && e.layer == 2)
		fg := th.FG
		if lit {
			fg = th.Hi
		}
		if sel {
			fg = th.SelFG
		}
		c.TextCenter(u.fBody, kx+w/2, y+(kh-u.px(4)-u.fBody.height)/2, label, fg)
		if lit && !sel {
			c.Fill(kx+u.px(4), y+kh-u.px(9), w-u.px(8), u.px(2), th.Hi)
		}
	}
	hints := [][2]string{{"A", "TYPE"}, {"B", "DEL"}, {"X", "SPACE"}, {"Y", "SHIFT"}, {"L/R", "CURSOR"}}
	if e.Multiline {
		hints = append(hints, [2]string{"R2", "↵"})
	}
	hints = append(hints, [2]string{"START", e.DoneLabel}, [2]string{"SEL", "BACK"})
	u.footer(c, hints)
}

// key draws one keycap: an outlined cell, inverted when selected.
func (u *UI) key(c *Canvas, x, y, w, h int, sel bool) {
	if sel {
		c.Fill(x, y, w, h, u.th.SelBG)
		return
	}
	c.Fill(x, y, w, h, u.th.Panel)
	c.Border(x, y, w, h, 1, u.th.Faint)
}
