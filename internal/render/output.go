package render

import (
	"encoding/json"
	"strconv"
	"strings"
)

// RunOutput is a run's output as lines to read: what claude's stream-json and codex's app-server or exec JSON say, one
// line each, with the protocol's own traffic (requests, answers, partial text) left out; other text stays as it is.
// Prefixes: "> " the user, "+ " a tool call, "$ " a command, "~ " changed files, "- " a system event, "= " the result,
// "! " an error.
func RunOutput(text string) []string {
	var out []string
	for l := range strings.Lines(text) {
		l = strings.TrimRight(l, "\r\n")
		if strings.TrimSpace(l) == "" {
			continue
		}
		for _, x := range outputLine(l) {
			for s := range strings.Lines(x) {
				if s = strings.TrimRight(s, "\r\n"); s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

type outputEvent struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype"`
	Result  json.RawMessage `json:"result"`
	Message struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
	Method string          `json:"method"`
	ID     json.RawMessage `json:"id"`
	Params struct {
		Item    outputItem `json:"item"`
		Message string     `json:"message"`
	} `json:"params"`
	Item outputItem `json:"item"`
}

type outputItem struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Command   string `json:"command"`
	ExitCode  *int   `json:"exitCode"`
	ExitCode2 *int   `json:"exit_code"`
	Changes   []struct {
		Path string `json:"path"`
	} `json:"changes"`
}

func outputLine(l string) []string {
	var e outputEvent
	if !strings.HasPrefix(l, "{") || json.Unmarshal([]byte(l), &e) != nil {
		return []string{l}
	}
	switch {
	case e.Method != "":
		switch e.Method {
		case "item/completed":
			return itemLine(e.Params.Item)
		case "error", "warning":
			return []string{"! " + e.Params.Message}
		}
		return nil
	case e.ID != nil && e.Type == "":
		return nil
	case e.Type == "assistant" || e.Type == "user":
		var out []string
		for _, b := range e.Message.Content {
			switch {
			case b.Type == "text" && e.Type == "user":
				out = append(out, "> "+b.Text)
			case b.Type == "text":
				out = append(out, b.Text)
			case b.Type == "tool_use":
				out = append(out, "+ "+b.Name+" "+string(b.Input))
			}
		}
		return out
	case e.Type == "system":
		return []string{"- " + e.Subtype}
	case e.Type == "result":
		var result string
		json.Unmarshal(e.Result, &result)
		return []string{"= " + firstNonEmpty(result, e.Subtype)}
	case e.Type == "item.completed":
		return itemLine(e.Item)
	case e.Type == "stream_event" || strings.HasPrefix(e.Type, "control_") || strings.Contains(e.Type, "."):
		return nil
	}
	return []string{l}
}

func itemLine(it outputItem) []string {
	switch it.Type {
	case "agentMessage", "agent_message", "reasoning":
		return []string{it.Text}
	case "userMessage":
		return []string{"> " + it.Text}
	case "commandExecution", "command_execution":
		s := "$ " + it.Command
		if c := firstCode(it.ExitCode, it.ExitCode2); c != nil {
			s += " (exit " + strconv.Itoa(*c) + ")"
		}
		return []string{s}
	case "fileChange", "file_change":
		var paths []string
		for _, c := range it.Changes {
			paths = append(paths, c.Path)
		}
		return []string{"~ " + strings.Join(paths, " ")}
	}
	return nil
}

func firstCode(cs ...*int) *int {
	for _, c := range cs {
		if c != nil {
			return c
		}
	}
	return nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
