package fixture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const tsLayout = "2006-01-02T15:04:05.000Z"

func stamp(t time.Time) string { return t.UTC().Format(tsLayout) }

type transcript struct {
	path  string
	lines []obj
	t     time.Time
}

func (tr *transcript) tick(d time.Duration) string {
	tr.t = tr.t.Add(d)
	return stamp(tr.t)
}

func (tr *transcript) add(o obj) { tr.lines = append(tr.lines, o) }

func (tr *transcript) write() error {
	if err := os.MkdirAll(filepath.Dir(tr.path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(tr.path)
	if err != nil {
		return err
	}
	for _, l := range tr.lines {
		b, err := encode(l)
		if err != nil {
			f.Close()
			return err
		}
		f.Write(append(b, '\n'))
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(tr.path, tr.t, tr.t)
}

type claudeSession struct {
	transcript
	id, cwd, branch, entry string
	parent                 any
	seq                    int
}

func (s *claudeSession) uuid() string {
	s.seq++
	return fmt.Sprintf("%s-%012x", s.id[:23], s.seq)
}

func (s *claudeSession) head(typ string) (obj, string) {
	u := s.uuid()
	o := obj{{"parentUuid", s.parent}, {"isSidechain", false}, {"type", typ}}
	s.parent = u
	return o, u
}

func (s *claudeSession) tail(o obj, u string, ts string) obj {
	return o.with("uuid", u).with("timestamp", ts).with("userType", "external").with("entrypoint", s.entry).
		with("cwd", s.cwd).with("sessionId", s.id).with("version", "2.1.280").with("gitBranch", s.branch)
}

func (s *claudeSession) user(content any) {
	o, u := s.head("user")
	ts := s.tick(2 * time.Minute)
	o = o.with("promptId", u).with("message", obj{{"role", "user"}, {"content", content}})
	s.add(s.tail(o, u, ts).with("permissionMode", "default"))
}

// meta is text Claude Code injects (skill bodies, caveats).
func (s *claudeSession) meta(text string) {
	o, u := s.head("user")
	ts := s.tick(time.Second)
	o = o.with("message", obj{{"role", "user"}, {"content", []obj{{{"type", "text"}, {"text", text}}}}}).with("isMeta", true)
	s.add(s.tail(o, u, ts))
}

func (s *claudeSession) assistant(blocks ...obj) {
	o, u := s.head("assistant")
	ts := s.tick(20 * time.Second)
	msg := obj{{"model", "claude-opus-5-5"}, {"id", "msg_" + u[24:]}, {"type", "message"}, {"role", "assistant"},
		{"content", blocks}, {"stop_reason", "end_turn"}, {"stop_sequence", nil},
		{"usage", obj{{"input_tokens", 4}, {"cache_read_input_tokens", 18200}, {"output_tokens", 310}}}}
	s.add(s.tail(o.with("message", msg), u, ts))
}

func (s *claudeSession) reply(text string) {
	s.assistant(obj{{"type", "thinking"}, {"thinking", ""}, {"signature", "c2ln"}})
	s.assistant(obj{{"type", "text"}, {"text", text}})
}

func (s *claudeSession) tool(name string, input obj, output string) {
	id := "toolu_" + s.id[24:] + fmt.Sprintf("%04x", s.seq)
	s.assistant(obj{{"type", "tool_use"}, {"id", id}, {"name", name}, {"input", input}})
	o, u := s.head("user")
	ts := s.tick(5 * time.Second)
	o = o.with("message", obj{{"role", "user"}, {"content", []obj{{{"tool_use_id", id}, {"type", "tool_result"},
		{"content", output}, {"is_error", false}}}}})
	s.add(s.tail(o, u, ts))
}

func (s *claudeSession) system(subtype, content string) {
	o, u := s.head("system")
	ts := s.tick(time.Minute)
	s.add(s.tail(o.with("subtype", subtype).with("content", content).with("isMeta", false), u, ts))
}

func (s *claudeSession) mark(typ, key string, v any) {
	s.add(obj{{"type", typ}, {key, v}, {"sessionId", s.id}})
}

type codexSession struct {
	transcript
	id, cwd string
	ordinal int
	calls   int
	desktop bool
}

func (s *codexSession) callID() string {
	s.calls++
	return fmt.Sprintf("call_%s_%d", s.id[len(s.id)-4:], s.calls)
}

func (s *codexSession) line(typ string, payload obj) {
	o := obj{{"timestamp", s.tick(3 * time.Second)}}
	if s.desktop {
		o = o.with("ordinal", s.ordinal)
		s.ordinal++
	}
	s.add(o.with("type", typ).with("payload", payload))
}

func (s *codexSession) meta(originator, parent, remote string) {
	p := obj{{"session_id", s.id}, {"id", s.id}, {"timestamp", stamp(s.t)}, {"cwd", s.cwd}, {"originator", originator},
		{"cli_version", "0.156.1"}, {"source", "cli"}, {"model_provider", "openai"}}
	if parent != "" {
		p = p.with("parent_thread_id", parent)
	}
	if remote != "" {
		p = p.with("git", obj{{"commit_hash", "3f9c2d1"}, {"branch", "main"}, {"repository_url", remote}})
	}
	s.line("session_meta", p)
}

func (s *codexSession) turnContext() {
	s.line("turn_context", obj{{"cwd", s.cwd}, {"approval_policy", "on-request"},
		{"sandbox_policy", obj{{"type", "workspace-write"}, {"network_access", false}}}, {"model", "gpt-6"}})
}

func (s *codexSession) say(text string) {
	s.t = s.t.Add(2 * time.Minute)
	s.line("event_msg", obj{{"type", "task_started"}, {"turn_id", s.callID()}})
	s.line("response_item", obj{{"type", "message"}, {"role", "user"}, {"content", []obj{{{"type", "input_text"}, {"text", text}}}}})
	if s.desktop {
		s.line("event_msg", obj{{"type", "item_completed"}, {"item", obj{{"type", "UserMessage"},
			{"content", []obj{{{"type", "text"}, {"text", text}}}}}}})
	} else {
		s.line("event_msg", obj{{"type", "user_message"}, {"message", text}, {"images", []string{}}})
	}
}

func (s *codexSession) context(text string) {
	s.line("response_item", obj{{"type", "message"}, {"role", "user"}, {"content", []obj{{{"type", "input_text"}, {"text", text}}}}})
}

func (s *codexSession) reply(text string) {
	s.line("response_item", obj{{"type", "reasoning"}, {"summary", []string{}}, {"content", nil}, {"encrypted_content", "Z0FBQUFB"}})
	s.line("response_item", obj{{"type", "message"}, {"role", "assistant"}, {"content", []obj{{{"type", "output_text"}, {"text", text}}}}})
	s.line("event_msg", obj{{"type", "agent_message"}, {"message", text}})
}

func (s *codexSession) exec(cmd, out string) {
	args, _ := json.Marshal(map[string]any{"cmd": cmd, "workdir": s.cwd})
	id := s.callID()
	s.line("response_item", obj{{"type", "function_call"}, {"name", "exec_command"}, {"arguments", string(args)}, {"call_id", id}})
	s.line("response_item", obj{{"type", "function_call_output"}, {"call_id", id}, {"output", out}})
}

func (s *codexSession) patch(body string) {
	id := s.callID()
	s.line("response_item", obj{{"type", "custom_tool_call"}, {"status", "completed"}, {"call_id", id}, {"name", "apply_patch"},
		{"input", "*** Begin Patch\n" + body + "*** End Patch\n"}})
	s.line("response_item", obj{{"type", "custom_tool_call_output"}, {"call_id", id}, {"output", "Success. Updated the following files."}})
}

func (s *codexSession) done(last string) {
	s.line("event_msg", obj{{"type", "task_complete"}, {"last_agent_message", last}, {"duration_ms", 93000}})
}
