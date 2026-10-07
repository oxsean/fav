package envcheck

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/paths"
)

// ⚠️ The CLIs' configuration holds account details, API keys and MCP servers' tokens beside the names read here: each
// file is decoded as a token stream that keeps the whitelisted keys only, every other value skipped without being held.

// claudeJSONPath is Claude's global configuration: .claude.json in CLAUDE_CONFIG_DIR when set, else in the home.
func claudeJSONPath() string {
	if h := os.Getenv("CLAUDE_CONFIG_DIR"); h != "" {
		return filepath.Join(h, ".claude.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude.json")
}

// claudeJSON reads the MCP server names of .claude.json and, for dir, its project entry's tool names, enabled
// .mcp.json servers, trust and own servers.
func claudeJSON(dir string, p *Print) {
	err := decodeFile(claudeJSONPath(), func(dec *json.Decoder) error {
		return object(dec, func(key string) (err error) {
			switch key {
			case "mcpServers":
				return keys(dec, &p.MCP)
			case "projects":
				return object(dec, func(path string) error {
					if !sameDir(path, dir) {
						return skip(dec)
					}
					return claudeProject(dec, p)
				})
			}
			return skip(dec)
		})
	})
	if err != nil {
		p.unknown("claude_json")
	}
}

func claudeProject(dec *json.Decoder, p *Print) error {
	return object(dec, func(key string) error {
		switch key {
		case "allowedTools":
			var rules []string
			if err := stringList(dec, &rules); err != nil {
				return err
			}
			for _, r := range rules {
				name, _, _ := strings.Cut(r, "(") // ⚠️ a rule's arguments can hold command text with secrets
				p.Project.AllowedTools = append(p.Project.AllowedTools, strings.TrimSpace(name))
			}
			return nil
		case "enabledMcpjsonServers":
			return stringList(dec, &p.Project.MCPJSON)
		case "hasTrustDialogAccepted":
			return boolean(dec, &p.Project.Trusted)
		case "mcpServers":
			return keys(dec, &p.MCP)
		}
		return skip(dec)
	})
}

// mcpJSON reads the server names of dir's .mcp.json.
func mcpJSON(dir string, p *Print) {
	if dir == "" {
		return
	}
	err := decodeFile(filepath.Join(dir, ".mcp.json"), func(dec *json.Decoder) error {
		return object(dec, func(key string) error {
			if key == "mcpServers" {
				return keys(dec, &p.MCP)
			}
			return skip(dec)
		})
	})
	if err != nil {
		p.unknown("mcp_json")
	}
}

// claudeSettings reads the names under env in Claude's settings, the user's and dir's, and ANTHROPIC_BASE_URL's host.
func claudeSettings(dir string, p *Print) {
	files := []string{filepath.Join(capture.ClaudeHome(), "settings.json")}
	if dir != "" {
		files = append(files, filepath.Join(dir, ".claude", "settings.json"), filepath.Join(dir, ".claude", "settings.local.json"))
	}
	for _, f := range files {
		err := decodeFile(f, func(dec *json.Decoder) error {
			return object(dec, func(key string) error {
				if key != "env" {
					return skip(dec)
				}
				return object(dec, func(name string) error {
					p.Provider.ClaudeEnv = append(p.Provider.ClaudeEnv, name)
					if name != "ANTHROPIC_BASE_URL" {
						return skip(dec)
					}
					var u string
					if err := str(dec, &u); err != nil {
						return err
					}
					p.Provider.ClaudeHost = hostOf(u)
					return nil
				})
			})
		})
		if err != nil {
			p.unknown("claude_settings")
		}
	}
}

// sameDir: key, a path the CLIs keep settings under, is dir, through a symlink too: they key them by the resolved path
// (macOS /var is /private/var).
func sameDir(key, dir string) bool {
	return dir != "" && (paths.Same(key, dir) || paths.SameFile(key, dir))
}

// installedPlugins maps each plugin installed for this user or dir to its install directories.
func installedPlugins(path, dir string) (map[string][]string, error) {
	out := map[string][]string{}
	err := decodeFile(path, func(dec *json.Decoder) error {
		return object(dec, func(key string) error {
			if key != "plugins" {
				return skip(dec)
			}
			return object(dec, func(id string) error {
				name, _, _ := strings.Cut(id, "@")
				return array(dec, func() error {
					var scope, project, install string
					err := object(dec, func(k string) error {
						switch k {
						case "scope":
							return str(dec, &scope)
						case "projectPath":
							return str(dec, &project)
						case "installPath":
							return str(dec, &install)
						}
						return skip(dec)
					})
					if install != "" && (project == "" || sameDir(project, dir)) {
						out[name] = append(out[name], install)
					}
					return err
				})
			})
		})
	})
	return out, err
}

func hostOf(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

// decodeFile runs read over path's JSON; a file that is not there is no error.
func decodeFile(path string, read func(*json.Decoder) error) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	return read(json.NewDecoder(bufio.NewReader(f)))
}

var errShape = errors.New("unexpected JSON")

func delim(dec *json.Decoder, want json.Delim) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := t.(json.Delim); !ok || d != want {
		return errShape
	}
	return nil
}

// object reads an object, calling each with its keys in turn: each reads or skips the key's value.
func object(dec *json.Decoder, each func(key string) error) error {
	if err := delim(dec, '{'); err != nil {
		return err
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := t.(string)
		if !ok {
			return errShape
		}
		if err := each(key); err != nil {
			return err
		}
	}
	return delim(dec, '}')
}

func array(dec *json.Decoder, each func() error) error {
	if err := delim(dec, '['); err != nil {
		return err
	}
	for dec.More() {
		if err := each(); err != nil {
			return err
		}
	}
	return delim(dec, ']')
}

// keys appends an object's keys to out, its values skipped.
func keys(dec *json.Decoder, out *[]string) error {
	return object(dec, func(key string) error {
		*out = append(*out, key)
		return skip(dec)
	})
}

func stringList(dec *json.Decoder, out *[]string) error {
	return array(dec, func() error {
		var s string
		if err := str(dec, &s); err != nil {
			return err
		}
		*out = append(*out, s)
		return nil
	})
}

func str(dec *json.Decoder, out *string) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	s, ok := t.(string)
	if !ok {
		return errShape
	}
	*out = s
	return nil
}

func boolean(dec *json.Decoder, out *bool) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	b, ok := t.(bool)
	if !ok {
		return errShape
	}
	*out = b
	return nil
}

// skip reads past one value, keeping none of it.
func skip(dec *json.Decoder) error {
	depth := 0
	for {
		t, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		if d, ok := t.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
		if depth == 0 {
			return nil
		}
	}
}

// codexConfig reads Codex's config.toml line by line for the table headers and the three keys it needs:
// model_provider, a provider table's base_url (its host), and dir's trust_level; no other value is parsed.
func codexConfig(dir string, p *Print) {
	f, err := os.Open(filepath.Join(capture.CodexHome(), "config.toml"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		p.unknown("codex_config")
		return
	}
	defer f.Close()
	var table []string
	hosts := map[string]string{}
	multi := ""
	bad := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), fileCap)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if multi != "" { // inside a multi-line string: its lines are no headers
			if strings.Contains(line, multi) {
				multi = ""
			}
			continue
		}
		if line == "" || line[0] == '#' {
			continue
		}
		if strings.HasPrefix(line, "[[") {
			table = nil
			continue
		}
		if line[0] == '[' {
			end := strings.LastIndex(line, "]")
			t, ok := []string(nil), end > 0
			if ok {
				t, ok = tomlKeys(line[1:end])
			}
			if !ok {
				bad, table = true, nil
				continue
			}
			table = t
			if len(t) == 2 && t[0] == "mcp_servers" {
				p.CodexMCP = append(p.CodexMCP, t[1])
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, ok := tomlKeys(k)
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		for _, q := range []string{`"""`, `'''`} {
			if strings.HasPrefix(v, q) && !strings.Contains(v[3:], q) {
				multi = q
			}
		}
		path := append(append([]string{}, table...), key...)
		switch {
		case len(path) == 1 && path[0] == "model_provider":
			p.Provider.Codex = tomlString(v)
		case len(path) == 2 && path[0] == "mcp_servers":
			p.CodexMCP = append(p.CodexMCP, path[1])
		case len(path) == 3 && path[0] == "model_providers" && path[2] == "base_url":
			hosts[path[1]] = hostOf(tomlString(v))
		case len(path) == 3 && path[0] == "projects" && path[2] == "trust_level" && sameDir(path[1], dir):
			p.Project.CodexTrust = tomlString(v)
		}
	}
	if sc.Err() != nil || bad {
		p.unknown("codex_config")
	}
	p.Provider.CodexHost = hosts[p.Provider.Codex]
}

// tomlKeys splits a dotted TOML key: bare, "basic" or 'literal' parts.
func tomlKeys(s string) ([]string, bool) {
	var out []string
	s = strings.TrimSpace(s)
	for {
		var part string
		switch {
		case s == "":
			return nil, false
		case s[0] == '"':
			i := 1
			var b strings.Builder
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) {
					i++
					switch s[i] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(s[i])
					}
					continue
				}
				b.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, false
			}
			part, s = b.String(), s[i+1:]
		case strings.HasPrefix(s, "'"):
			end := strings.Index(s[1:], "'")
			if end < 0 {
				return nil, false
			}
			part, s = s[1:end+1], s[end+2:]
		default:
			end := strings.IndexAny(s, ". \t")
			if end < 0 {
				end = len(s)
			}
			part, s = s[:end], s[end:]
			for _, r := range part {
				if !(r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
					return nil, false
				}
			}
		}
		out = append(out, part)
		s = strings.TrimSpace(s)
		if s == "" {
			return out, len(out) > 0 && out[len(out)-1] != ""
		}
		if s[0] != '.' {
			return nil, false
		}
		s = strings.TrimSpace(s[1:])
	}
}

// tomlString is the string a TOML value starts with; "" when it is no string.
func tomlString(v string) string {
	if !strings.HasPrefix(v, `"`) && !strings.HasPrefix(v, "'") {
		return ""
	}
	k, ok := tomlKeys(v[:closing(v)+1])
	if !ok || len(k) != 1 {
		return ""
	}
	return k[0]
}

// closing is the index of the quote that ends the string v starts with, or the last byte.
func closing(v string) int {
	q := v[0]
	for i := 1; i < len(v); i++ {
		if q == '"' && v[i] == '\\' {
			i++
			continue
		}
		if v[i] == q {
			return i
		}
	}
	return len(v) - 1
}
