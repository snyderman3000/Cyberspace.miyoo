// Cyberspace.miyoo: a handheld terminal for cyberspace.online, for OnionOS on the Miyoo Mini Plus.
package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const version = "0.3.0"
const userAgent = "Cyberspace.miyoo/" + version + " (OnionOS cyberspace client)"

// Config lives in App/Cyberspace.miyoo/data/config.json.
type Config struct {
	Theme    string `json:"theme"`              // cyber, c64 or vt320
	Email    string `json:"email"`              // pre-fills the login screen
	Password string `json:"password,omitempty"` // optional: logs in once at launch, then is erased from this file
	API      string `json:"api,omitempty"`
}

type Env struct {
	AppDir  string
	DataDir string
	Config  Config
	API     *Client
	Now     func() time.Time
}

func newEnv(dataDir string) *Env {
	env := &Env{DataDir: dataDir, Now: time.Now}
	os.MkdirAll(dataDir, 0o755)
	env.Config = loadConfig(env.configPath())
	base := strings.TrimRight(env.Config.API, "/")
	if base == "" {
		base = defaultAPI
	}
	env.API = &Client{Base: base, UA: userAgent,
		HTTP:   &http.Client{Transport: newTransport(), Timeout: 30 * time.Second},
		Stream: &http.Client{Transport: newTransport()}} // live feeds stay open, so no timeout
	if t, ok := loadSession(env.sessionPath()); ok {
		env.API.tok = t
	}
	env.API.OnSave = func(t Tokens) { saveSession(env.sessionPath(), t) }
	return env
}

func (e *Env) configPath() string  { return filepath.Join(e.DataDir, "config.json") }
func (e *Env) sessionPath() string { return filepath.Join(e.DataDir, "session.json") }

func newTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{NextProtos: []string{"http/1.1"}},
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		TLSHandshakeTimeout:   15 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
}

func loadConfig(path string) Config {
	cfg := Config{Theme: "cyber"}
	b, err := os.ReadFile(path)
	if err != nil {
		writeConfig(path, cfg, true)
		return cfg
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		log.Printf("config.json is not valid JSON (%v); using defaults", err)
	}
	cfg.Email = strings.TrimSpace(cfg.Email)
	return cfg
}

// writeConfig saves the config; a fresh file shows the optional password field too.
func writeConfig(path string, cfg Config, starter bool) {
	var b []byte
	if starter {
		b, _ = json.MarshalIndent(map[string]string{"theme": cfg.Theme, "email": cfg.Email, "password": ""}, "", "  ")
	} else {
		cfg.Password = ""
		b, _ = json.MarshalIndent(cfg, "", "  ")
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		log.Printf("config: %v", err)
	}
}

func (e *Env) saveConfig() {
	if e.DataDir != "" {
		writeConfig(e.configPath(), e.Config, false)
	}
}

// forgetPassword removes a password typed into config.json, keeping the email.
func (e *Env) forgetPassword(email string) {
	e.Config.Password = ""
	if email != "" {
		e.Config.Email = email
	}
	e.saveConfig()
}

func loadSession(path string) (Tokens, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Tokens{}, false
	}
	var t Tokens
	if json.Unmarshal(b, &t) != nil || t.RefreshToken == "" {
		return Tokens{}, false
	}
	return t, true
}

func saveSession(path string, t Tokens) {
	if t.RefreshToken == "" {
		os.Remove(path)
		return
	}
	b, _ := json.Marshal(t)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		log.Printf("session: %v", err)
	}
}

func main() {
	shot := flag.String("shot", "", "render a demo scene to PNG instead of running on the device")
	out := flag.String("out", "shot.png", "PNG path for -shot")
	theme := flag.String("theme", "", "theme for -shot")
	w := flag.Int("w", 640, "screen width for -shot")
	h := flag.Int("h", 480, "screen height for -shot")
	icon := flag.String("icon", "", "write the OnionOS app icon to this PNG path and exit")
	flag.Parse()
	if *icon != "" {
		f, err := os.Create(*icon)
		if err != nil {
			log.Fatal(err)
		}
		png.Encode(f, appIcon(74))
		f.Close()
		return
	}
	if *shot != "" {
		tmp, _ := os.MkdirTemp("", "cyberspace-shot")
		defer os.RemoveAll(tmp)
		env := newEnv(tmp)
		if *theme != "" {
			env.Config.Theme = *theme
		}
		env.Now = func() time.Time { return demoNow }
		c := NewCanvas(*w, *h)
		ui := NewUI(env, c.W, c.H)
		if err := demoScene(ui, *shot); err != nil {
			log.Fatal(err)
		}
		ui.Draw(c)
		f, err := os.Create(*out)
		if err != nil {
			log.Fatal(err)
		}
		png.Encode(f, c)
		f.Close()
		return
	}

	exe, _ := os.Executable()
	appDir := filepath.Dir(exe)
	dataDir := filepath.Join(appDir, "data")
	if d := os.Getenv("CYBERSPACE_DATA"); d != "" {
		dataDir = d
	}
	os.MkdirAll(dataDir, 0o755)
	logPath := filepath.Join(dataDir, "cyberspace.log")
	if st, err := os.Stat(logPath); err == nil && st.Size() > 512<<10 {
		os.Rename(logPath, logPath+".1")
	}
	if logf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); logf != nil {
		log.SetOutput(logf)
	}
	env := newEnv(dataDir)
	env.AppDir = appDir
	log.Printf("cyberspace.miyoo %s starting, api=%s", version, env.API.Base)

	fb, err := OpenFramebuffer()
	if err != nil {
		log.Fatalf("framebuffer: %v", err)
	}
	defer fb.Close()
	c := NewCanvas(fb.W, fb.H)
	keys := make(chan int, 32)
	if err := ReadInput("/dev/input/event0", keys); err != nil {
		log.Fatalf("input: %v", err)
	}
	ui := NewUI(env, c.W, c.H)
	ui.Start()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	lastMin := -1
	dirty := true
	for !ui.quit {
		select {
		case k := <-keys:
			ui.Key(k)
			dirty = true
		case f := <-ui.post:
			f()
			dirty = true
		case <-tick.C:
			ui.frame++
			ui.Tick()
			if ui.Animating() {
				dirty = true
			}
			if m := time.Now().Minute(); m != lastMin {
				lastMin = m
				dirty = true
			}
		}
		if dirty {
			ui.Draw(c)
			fb.Present(c)
			dirty = false
		}
	}
	ui.Shutdown()
	ui.saveDraft()
	ui.scr = scrBye
	for i := 0; i < 12; i++ {
		ui.frame++
		ui.byeFrame = i
		ui.Draw(c)
		fb.Present(c)
		time.Sleep(60 * time.Millisecond)
	}
	fb.Clear()
	fmt.Fprintln(os.Stderr, "bye")
}
