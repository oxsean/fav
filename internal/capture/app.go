package capture

import (
	"bufio"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/oxsean/fav/internal/fav"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/paths"
	"uuid"
)

// Desktop apps open a session by URL: Claude's resumes a CLI session (claude://resume?session=<uuid>), ChatGPT's Codex
// opens a thread (codex://threads/<id>). Both read the CLI's own session files.
// ⚠️ Neither URL is documented: they were found in the apps, so a failure has to fall back to the terminal.

// AppURL opens r in its desktop app; "" when there is none for it.
func AppURL(r *fav.Rec) string {
	if !canonicalUUID(r.SessionID) || appBlock(r) != "" {
		return ""
	}
	switch r.Provider {
	case fav.ProviderClaude:
		return "claude://resume?session=" + url.QueryEscape(r.SessionID)
	case fav.ProviderCodex:
		return "codex://threads/" + r.SessionID
	}
	return ""
}

// appBlock is why the desktop app cannot run r (an i18n key), or "". The apps read only the default ~/.claude/projects
// and ~/.codex/sessions, and start the session in its working directory.
func appBlock(r *fav.Rec) string {
	if fi, err := os.Stat(r.Cwd); r.Cwd == "" || err != nil || !fi.IsDir() {
		return "resume.app_no_cwd"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "resume.app_elsewhere"
	}
	root := filepath.Join(home, ".claude", "projects")
	if r.Provider == fav.ProviderCodex {
		root = filepath.Join(home, ".codex", "sessions")
	}
	if !paths.Under(r.TranscriptPath, root) {
		return "resume.app_elsewhere"
	}
	if _, err := os.Stat(r.TranscriptPath); err != nil {
		return "resume.app_elsewhere"
	}
	return ""
}

// AppName is the desktop app that opens this provider's sessions.
func AppName(provider string) string {
	if provider == fav.ProviderCodex {
		return "ChatGPT"
	}
	return "Claude"
}

// appNames must occur in the scheme handler's name or bundle id: another program may own the scheme.
var appNames = map[string][]string{
	fav.ProviderClaude: {"claude"},
	fav.ProviderCodex:  {"codex", "chatgpt"},
}

var appSchemes = map[string]string{fav.ProviderClaude: "claude", fav.ProviderCodex: "codex"}

var appAvail sync.Map // provider → bool

// AppAvailable: the system hands the provider's scheme to its desktop app (macOS Launch Services, Windows
// AssocQueryString, Linux xdg-mime); cached per run.
func AppAvailable(provider string) bool {
	if v, ok := appAvail.Load(provider); ok {
		return v.(bool)
	}
	ok := false
	if s := appSchemes[provider]; s != "" {
		ok = handlerIs(provider, schemeHandler(s))
	}
	v, _ := appAvail.LoadOrStore(provider, ok)
	return v.(bool)
}

func handlerIs(provider, handler string) bool {
	h := strings.ToLower(handler)
	for _, n := range appNames[provider] {
		if strings.Contains(h, n) {
			return true
		}
	}
	return false
}

// AppKnown reports AppAvailable without looking it up; known is false until a lookup finished.
func AppKnown(provider string) (ok, known bool) {
	v, known := appAvail.Load(provider)
	return known && v.(bool), known
}

// ForgetAppAvailable drops the cached answer (tests).
func ForgetAppAvailable(provider string) { appAvail.Delete(provider) }

// SetAppAvailable overrides the installed-app detection (tests).
func SetAppAvailable(provider string, ok bool) { appAvail.Store(provider, ok) }

// OpenApp hands r to its desktop app.
func OpenApp(r *fav.Rec) error {
	if k := appBlock(r); k != "" {
		return i18n.E(k, AppName(r.Provider))
	}
	u := AppURL(r)
	if u == "" || !AppAvailable(r.Provider) {
		return i18n.E("resume.app_unavailable", AppName(r.Provider))
	}
	if err := openURL(u); err != nil {
		return i18n.E("resume.app_failed", AppName(r.Provider), err.Error())
	}
	return nil
}

// desktop Codex originators: threads started in the app.
var codexAppOriginators = map[string]bool{"Codex Desktop": true, "codex_work_desktop": true}

// CodexFromApp: a Codex session_meta originator of the desktop app.
func CodexFromApp(originator string) bool { return codexAppOriginators[originator] }

var claudeDesktop struct {
	sync.Mutex
	files map[string]desktopFile // local_*.json → its CLI session id, by mtime
}

type desktopFile struct {
	mtime int64
	id    string
}

// ClaudeDesktopIDs are the CLI session ids Claude's desktop app keeps as its own sessions
// (<config dir>/Claude/claude-code-sessions/<account>/<org>/local_*.json, field cliSessionId). Files are re-read only
// when they change.
func ClaudeDesktopIDs() map[string]bool {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "Claude", "claude-code-sessions", "*", "*", "local_*.json"))
	claudeDesktop.Lock()
	defer claudeDesktop.Unlock()
	if claudeDesktop.files == nil {
		claudeDesktop.files = map[string]desktopFile{}
	}
	out := make(map[string]bool, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		seen[p] = true
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		f, ok := claudeDesktop.files[p]
		if !ok || f.mtime != fi.ModTime().UnixNano() {
			var meta struct {
				CliSessionID string `json:"cliSessionId"`
			}
			if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &meta) == nil {
				f = desktopFile{fi.ModTime().UnixNano(), meta.CliSessionID}
				claudeDesktop.files[p] = f
			}
		}
		if f.id != "" {
			out[f.id] = true
		}
	}
	for p := range claudeDesktop.files {
		if !seen[p] {
			delete(claudeDesktop.files, p)
		}
	}
	return out
}

// StartedInApp: r was started in its desktop app — Codex by the session_meta originator (first line of the rollout),
// Claude by the desktop app's session list.
func StartedInApp(r *fav.Rec) bool {
	switch r.Provider {
	case fav.ProviderClaude:
		return ClaudeDesktopIDs()[r.SessionID]
	case fav.ProviderCodex:
		f, err := os.Open(r.TranscriptPath)
		if err != nil {
			return false
		}
		defer f.Close()
		var meta struct {
			Payload struct {
				Originator string `json:"originator"`
			} `json:"payload"`
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		if sc.Scan() {
			json.Unmarshal(sc.Bytes(), &meta)
		}
		return CodexFromApp(meta.Payload.Originator)
	}
	return false
}

// canonicalUUID: the 36-character hyphenated form only (uuid.Parse also takes braces, urn: and bare hex).
func canonicalUUID(s string) bool {
	_, err := uuid.Parse(s)
	return len(s) == 36 && err == nil
}
