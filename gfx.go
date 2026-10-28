package main

// Software drawing on an RGBA canvas plus cached TrueType text
// (shared with Cablebox, Carrier, Rewind and Mixtape).

import (
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed assets/vt323.ttf
var vt323TTF []byte

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 255} }

func mix(a, b color.RGBA, t float64) color.RGBA {
	l := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t) }
	return color.RGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 255}
}

type Canvas struct {
	*image.RGBA
	W, H int
}

func NewCanvas(w, h int) *Canvas {
	return &Canvas{RGBA: image.NewRGBA(image.Rect(0, 0, w, h)), W: w, H: h}
}

func (c *Canvas) Fill(x, y, w, h int, col color.RGBA) {
	r := image.Rect(x, y, x+w, y+h).Intersect(c.Rect)
	if r.Empty() {
		return
	}
	row := make([]byte, r.Dx()*4)
	for i := 0; i < len(row); i += 4 {
		row[i], row[i+1], row[i+2], row[i+3] = col.R, col.G, col.B, 255
	}
	for yy := r.Min.Y; yy < r.Max.Y; yy++ {
		o := yy*c.Stride + r.Min.X*4
		copy(c.Pix[o:o+len(row)], row)
	}
}

func (c *Canvas) Set(x, y int, col color.RGBA) {
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return
	}
	o := y*c.Stride + x*4
	c.Pix[o], c.Pix[o+1], c.Pix[o+2], c.Pix[o+3] = col.R, col.G, col.B, 255
}

func (c *Canvas) Border(x, y, w, h, t int, col color.RGBA) {
	c.Fill(x, y, w, t, col)
	c.Fill(x, y+h-t, w, t, col)
	c.Fill(x, y, t, h, col)
	c.Fill(x+w-t, y, t, h, col)
}

func (c *Canvas) HLineDashed(x, y, w, dash, gap int, col color.RGBA) {
	for i := x; i < x+w; i += dash + gap {
		n := dash
		if i+n > x+w {
			n = x + w - i
		}
		c.Fill(i, y, n, 1, col)
	}
}

// Scanlines darkens every other row — the CRT look.
func (c *Canvas) Scanlines(strength float64) {
	k := int((1 - strength) * 256)
	for yy := 1; yy < c.H; yy += 2 {
		o := yy * c.Stride
		for xx := 0; xx < c.W; xx++ {
			c.Pix[o] = uint8(int(c.Pix[o]) * k >> 8)
			c.Pix[o+1] = uint8(int(c.Pix[o+1]) * k >> 8)
			c.Pix[o+2] = uint8(int(c.Pix[o+2]) * k >> 8)
			o += 4
		}
	}
}

// ---------- text ----------

type glyph struct {
	mask    *image.Alpha
	off     image.Point
	advance int
}

type Font struct {
	face   font.Face
	cache  map[rune]*glyph
	ascent int
	height int
	cell   int // advance of a typical character (the fonts are monospaced)
}

func loadFont(ttf []byte, size float64) *Font {
	f, err := opentype.Parse(ttf)
	if err != nil {
		panic(err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
	m := face.Metrics()
	fo := &Font{face: face, cache: map[rune]*glyph{}, ascent: m.Ascent.Ceil(), height: m.Height.Ceil()}
	fo.cell = fo.glyph('M').advance
	return fo
}

func (f *Font) glyph(r rune) *glyph {
	if g, ok := f.cache[r]; ok {
		return g
	}
	if g := f.boxGlyph(r); g != nil {
		f.cache[r] = g
		return g
	}
	dr, mask, mp, adv, ok := f.face.Glyph(fixed.P(0, 0), r)
	if !ok || (dr.Empty() && adv == 0) {
		if r < 0x20 {
			f.cache[r] = &glyph{advance: 0}
			return f.cache[r]
		}
		if r != '?' {
			g := f.glyph('?')
			f.cache[r] = g
			return g
		}
		f.cache[r] = &glyph{advance: adv.Round()}
		return f.cache[r]
	}
	a := image.NewAlpha(image.Rect(0, 0, dr.Dx(), dr.Dy()))
	draw.Draw(a, a.Bounds(), mask, mp, draw.Src)
	g := &glyph{mask: a, off: dr.Min, advance: adv.Round()}
	f.cache[r] = g
	return g
}

// boxGlyph synthesises block and shade characters cell-sized, so the ASCII art
// people post on Cyberspace lines up even where the font lacks the glyph.
func (f *Font) boxGlyph(r rune) *glyph {
	if f.cell == 0 {
		return nil
	}
	w, h := f.cell, f.height
	var on func(x, y int) bool
	switch r {
	case '█':
		on = func(x, y int) bool { return true }
	case '▀':
		on = func(x, y int) bool { return y < h/2 }
	case '▄':
		on = func(x, y int) bool { return y >= h/2 }
	case '▌':
		on = func(x, y int) bool { return x < w/2 }
	case '▐':
		on = func(x, y int) bool { return x >= w/2 }
	case '░':
		on = func(x, y int) bool { return (x+y*2)%4 == 0 }
	case '▒':
		on = func(x, y int) bool { return (x+y)%2 == 0 }
	case '▓':
		on = func(x, y int) bool { return (x+y)%4 != 0 }
	case '─', '━':
		on = func(x, y int) bool { return y == h/2 }
	case '│', '┃':
		on = func(x, y int) bool { return x == w/2 }
	case '┌':
		on = func(x, y int) bool { return (y == h/2 && x >= w/2) || (x == w/2 && y >= h/2) }
	case '┐':
		on = func(x, y int) bool { return (y == h/2 && x <= w/2) || (x == w/2 && y >= h/2) }
	case '└':
		on = func(x, y int) bool { return (y == h/2 && x >= w/2) || (x == w/2 && y <= h/2) }
	case '┘':
		on = func(x, y int) bool { return (y == h/2 && x <= w/2) || (x == w/2 && y <= h/2) }
	case '├':
		on = func(x, y int) bool { return x == w/2 || (y == h/2 && x >= w/2) }
	case '┤':
		on = func(x, y int) bool { return x == w/2 || (y == h/2 && x <= w/2) }
	case '┬':
		on = func(x, y int) bool { return y == h/2 || (x == w/2 && y >= h/2) }
	case '┴':
		on = func(x, y int) bool { return y == h/2 || (x == w/2 && y <= h/2) }
	case '┼':
		on = func(x, y int) bool { return y == h/2 || x == w/2 }
	case '═':
		on = func(x, y int) bool { return y == h/2-2 || y == h/2+1 }
	case '║':
		on = func(x, y int) bool { return x == w/2-2 || x == w/2+1 }
	case '●', '•':
		on = func(x, y int) bool {
			dx, dy := 2*x-w+1, 2*y-h+1
			rr := w * 6 / 10
			if r == '•' {
				rr = w * 4 / 10
			}
			return dx*dx+dy*dy <= rr*rr
		}
	case '▶', '◀':
		on = func(x, y int) bool {
			s := w * 7 / 10
			top := (h - s) / 2
			yy := y - top
			if yy < 0 || yy >= s {
				return false
			}
			xx := x - (w-s*6/10)/2
			if r == '◀' {
				xx = s*6/10 - 1 - xx
			}
			half := s / 2
			d := yy - half
			if d < 0 {
				d = -d
			}
			return xx >= 0 && xx < (half-d)*12/10
		}
	case '▲', '▼':
		on = func(x, y int) bool {
			s := w * 8 / 10
			top := (h - s*6/10) / 2
			yy := y - top
			if r == '▼' {
				yy = s*6/10 - 1 - yy
			}
			if yy < 0 || yy >= s*6/10 {
				return false
			}
			half := yy * 10 / 12
			d := x - w/2
			if d < 0 {
				d = -d - 1
			}
			return d <= half
		}
	case '←', '→', '↑', '↓', '↵', '↳':
		mid, cy := w/2, h/2+1
		ar := max(2, w/3)
		on = func(x, y int) bool {
			ax := x
			if r == '←' {
				ax = w - 1 - x
			}
			switch r {
			case '←', '→':
				if y == cy && ax >= 1 && ax < w-1 {
					return true
				}
				d := w - 2 - ax
				return d >= 0 && d < ar && (y == cy-d || y == cy+d)
			case '↑', '↓':
				ay := y - (h/2 - w/2)
				if r == '↓' {
					ay = (h/2 + w/2) - y
				}
				if x == mid && ay >= 0 && ay < w {
					return true
				}
				d := ay
				return d >= 0 && d < ar && (x == mid-d || x == mid+d)
			case '↵', '↳':
				top := h/2 - w/2
				if r == '↵' {
					if x == w-2 && y >= top && y <= cy {
						return true
					}
				} else if x == 1 && y >= top && y <= cy {
					return true
				}
				if y == cy && x >= 1 && x <= w-2 {
					return true
				}
				if r == '↵' {
					return x >= 1 && x-1 < ar && (y == cy-(x-1) || y == cy+(x-1))
				}
				d := w - 2 - x
				return d >= 0 && d < ar && (y == cy-d || y == cy+d)
			}
			return false
		}
	case '♪':
		on = func(x, y int) bool {
			stem := w*6/10 + 0
			top := h / 4
			bot := h * 7 / 10
			if x == stem && y >= top && y <= bot {
				return true
			}
			if y >= top && y <= top+1 && x >= stem && x <= stem+w/4 {
				return true
			}
			dx, dy := 2*(x-(stem-w/5)), 3*(y-bot)
			return dx*dx+dy*dy <= (w/2)*(w/2)
		}
	default:
		return nil
	}
	m := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if on(x, y) {
				m.Pix[y*m.Stride+x] = 255
			}
		}
	}
	return &glyph{mask: m, off: image.Pt(0, -f.ascent), advance: w}
}

func (f *Font) Width(s string) int {
	w := 0
	for _, r := range s {
		w += f.glyph(r).advance
	}
	return w
}

// Text draws s with its top-left at (x,y). Returns the x after the text.
func (c *Canvas) Text(f *Font, x, y int, s string, col color.RGBA) int {
	base := y + f.ascent
	for _, r := range s {
		g := f.glyph(r)
		if g.mask != nil {
			gx, gy := x+g.off.X, base+g.off.Y
			b := g.mask.Bounds()
			for yy := 0; yy < b.Dy(); yy++ {
				py := gy + yy
				if py < 0 || py >= c.H {
					continue
				}
				for xx := 0; xx < b.Dx(); xx++ {
					px := gx + xx
					if px < 0 || px >= c.W {
						continue
					}
					a := int(g.mask.Pix[yy*g.mask.Stride+xx])
					if a == 0 {
						continue
					}
					o := py*c.Stride + px*4
					c.Pix[o] = uint8((int(c.Pix[o])*(255-a) + int(col.R)*a) / 255)
					c.Pix[o+1] = uint8((int(c.Pix[o+1])*(255-a) + int(col.G)*a) / 255)
					c.Pix[o+2] = uint8((int(c.Pix[o+2])*(255-a) + int(col.B)*a) / 255)
				}
			}
		}
		x += g.advance
	}
	return x
}

// TextGlow draws text with a soft phosphor halo over bg.
func (c *Canvas) TextGlow(f *Font, x, y int, s string, col, bg color.RGBA) int {
	halo := mix(bg, col, 0.3)
	for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
		c.Text(f, x+d[0], y+d[1], s, halo)
	}
	return c.Text(f, x, y, s, col)
}

func (c *Canvas) TextRight(f *Font, right, y int, s string, col color.RGBA) int {
	return c.Text(f, right-f.Width(s), y, s, col)
}

func (c *Canvas) TextCenter(f *Font, cx, y int, s string, col color.RGBA) {
	c.Text(f, cx-f.Width(s)/2, y, s, col)
}

// Ellipsize trims s to fit width w.
func (f *Font) Ellipsize(s string, w int) string {
	if f.Width(s) <= w {
		return s
	}
	for len(s) > 0 {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
		if f.Width(s+"…") <= w {
			return s + "…"
		}
	}
	return ""
}

// Wrap breaks prose into lines no wider than w.
func (f *Font) Wrap(s string, w int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, wd := range words {
			try := wd
			if line != "" {
				try = line + " " + wd
			}
			if f.Width(try) <= w {
				line = try
				continue
			}
			if line != "" {
				out = append(out, line)
			}
			for f.Width(wd) > w { // hard-break very long tokens (URLs)
				cut := f.fitPrefix(wd, w)
				out = append(out, wd[:cut])
				wd = wd[cut:]
			}
			line = wd
		}
		out = append(out, line)
	}
	return out
}

// Chop splits a preformatted line (ASCII art, code) into pieces no wider than
// w without touching its spacing.
func (f *Font) Chop(s string, w int) []string {
	if f.Width(s) <= w {
		return []string{s}
	}
	var out []string
	for s != "" && f.Width(s) > w {
		cut := f.fitPrefix(s, w)
		out = append(out, s[:cut])
		s = s[cut:]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// fitPrefix is the byte length of the longest rune prefix of s that fits in w (at least one rune).
func (f *Font) fitPrefix(s string, w int) int {
	x, cut := 0, 0
	for i, r := range s {
		x += f.glyph(r).advance
		if x > w {
			if cut == 0 {
				return i + utf8.RuneLen(r)
			}
			return cut
		}
		cut = i + utf8.RuneLen(r)
	}
	return len(s)
}
