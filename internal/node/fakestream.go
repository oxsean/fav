package node

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/fixture"
)

type fakeOpts struct {
	sid, dir, final, stderr, note, askReport, permission, question, verdicts string
	steps, exit                                                              int
	every, leave                                                             time.Duration
	ask                                                                      bool
}

// fakeStream is `_fake-agent --stream`: claude's stream-json both ways. It answers initialize, takes the first user
// message as its brief, may ask for a tool or ask a question after its first step, says what it heard from messages
// that came while it worked, and ends each turn with a result; after a turn, a new message starts another, and the
// end of its input ends it.
func fakeStream(o fakeOpts) error {
	if o.sid == "" {
		return fmt.Errorf("--session is required")
	}
	in := make(chan map[string]any, 64)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				in <- m
			}
		}
		close(in)
	}()
	out := json.NewEncoder(os.Stdout)
	emit := func(v any) { out.Encode(v) }
	var heard []string
	interrupted := false
	// next waits for the next message that is not a control response; nil when input ended
	next := func(want string) map[string]any {
		for m := range in {
			switch {
			case m["type"] == "control_request":
				req, _ := m["request"].(map[string]any)
				switch req["subtype"] {
				case "initialize":
					emit(controlResponse(fmt.Sprint(m["request_id"]), map[string]any{}))
				case "interrupt":
					interrupted = true
					emit(controlResponse(fmt.Sprint(m["request_id"]), map[string]any{}))
					if want == "" {
						return m
					}
				}
			case m["type"] == "control_response":
				if want != "" {
					resp, _ := m["response"].(map[string]any)
					if resp["request_id"] == want {
						return resp
					}
				}
			case m["type"] == "user":
				if want == "" {
					return m
				}
				heard = append(heard, userText(m))
			}
		}
		return nil
	}
	first := next("")
	if first == nil {
		return nil
	}
	cwd := o.dir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	t := fixture.NewLiveClaude(capture.ClaudeHome(), o.sid, cwd, "sdk-cli")
	if err := t.User(strings.TrimSpace(userText(first))); err != nil {
		return err
	}
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": o.sid})
	if o.note != "" {
		AddReport(os.Getenv(EnvRunDir), ReportNote, o.note)
	}
	if o.askReport != "" {
		AddReport(os.Getenv(EnvRunDir), ReportAsk, o.askReport)
	}
	fakeVerdict(cwd, o.verdicts)
	say := func(text string) {
		emit(map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant",
			"content": []any{map[string]any{"type": "text", "text": text}}}})
		t.Reply(text)
	}
	// drain takes what came while it worked, without waiting
	drain := func() {
		for {
			select {
			case m, ok := <-in:
				if !ok {
					return
				}
				switch {
				case m["type"] == "user":
					heard = append(heard, userText(m))
				case m["type"] == "control_request":
					if req, _ := m["request"].(map[string]any); req["subtype"] == "interrupt" {
						interrupted = true
						emit(controlResponse(fmt.Sprint(m["request_id"]), map[string]any{}))
					}
				}
			default:
				return
			}
		}
	}
	tell := func() {
		for _, h := range heard {
			say("heard: " + h)
		}
		heard = nil
	}
	var denials []any
	turns, last := 0, ""
	result := func(text string) {
		turns++
		sub, isErr := "success", false
		if interrupted {
			sub, isErr, text = "error_during_execution", true, ""
		}
		emit(map[string]any{"type": "result", "subtype": sub, "is_error": isErr, "result": text, "num_turns": 1,
			"total_cost_usd": 0.001 * float64(turns), "usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
			"permission_denials": denials})
	}
	for i := 1; i <= o.steps && !interrupted; i++ {
		time.Sleep(o.every)
		drain()
		last = fmt.Sprintf("fake step %d of %d", i, o.steps)
		say(last)
		tell()
		if i == 1 && o.permission != "" && !interrupted {
			tool, what, _ := strings.Cut(o.permission, ":")
			emit(controlRequest("perm-1", map[string]any{"subtype": "can_use_tool", "tool_name": tool,
				"input": map[string]any{"command": what}, "description": what}))
			resp := next("perm-1")
			if r, _ := resp["response"].(map[string]any); r["behavior"] == "allow" {
				last = "allowed " + tool
			} else {
				last = "denied " + tool
				denials = append(denials, map[string]any{"tool_name": tool})
			}
			say(last)
		}
		if i == 1 && o.question != "" && !interrupted {
			parts := strings.Split(o.question, "|")
			var opts []any
			for _, p := range parts[1:] {
				opts = append(opts, map[string]any{"label": p})
			}
			emit(controlRequest("ask-1", map[string]any{"subtype": "can_use_tool", "tool_name": "AskUserQuestion",
				"input": map[string]any{"questions": []any{map[string]any{"question": parts[0], "header": "Pick", "options": opts,
					"multiSelect": false}}}}))
			resp := next("ask-1")
			last = "no answer"
			if r, _ := resp["response"].(map[string]any); r["behavior"] == "allow" {
				upd, _ := r["updatedInput"].(map[string]any)
				answers, _ := upd["answers"].(map[string]any)
				last = fmt.Sprint("answer: ", answers[parts[0]])
			}
			say(last)
		}
	}
	if o.ask {
		t.Ask("Continue?")
		for {
			time.Sleep(time.Hour) // ⚠️ not select{}: with no other goroutine the runtime ends it as a deadlock
		}
	}
	if o.stderr != "" {
		fmt.Fprintln(os.Stderr, o.stderr)
	}
	switch {
	case strings.HasPrefix(o.final, "{"):
		fmt.Println(o.final)
		if !strings.Contains(o.final, `"type":"result"`) { // another CLI's line: the turn still ends
			result(last)
		}
	case o.final != "":
		say(o.final)
		result(o.final)
	default:
		result(last)
	}
	for m := next(""); m != nil; m = next("") { // another turn for each message after a result
		if m["type"] != "user" {
			continue
		}
		heard = append(heard, userText(m))
		tell()
		result("heard")
	}
	if o.leave > 0 {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		c := exec.Command(self, "_fake-agent", "--sleep", o.leave.String())
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		if err := c.Start(); err != nil {
			return err
		}
	}
	os.Exit(o.exit)
	return nil
}

func userText(m map[string]any) string {
	msg, _ := m["message"].(map[string]any)
	switch c := msg["content"].(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, p := range c {
			if pm, _ := p.(map[string]any); pm["type"] == "text" {
				b.WriteString(fmt.Sprint(pm["text"]))
			}
		}
		return b.String()
	}
	return ""
}
