package agent

import "testing"

func TestClassifyNamesWhyAnAgentFailed(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"Invalid API key · Please run /login", ReasonAuth},
		{`API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"OAuth token has expired."}}`, ReasonAuth},
		{"Error: Not logged in. Run codex login", ReasonAuth},
		{"Claude AI usage limit reached|1758790800", ReasonQuota},
		{"You've hit your usage limit. Upgrade to Pro or try again at 3:05 PM.", ReasonQuota},
		{"Credit balance is too low", ReasonQuota},
		{`{"error":{"code":"insufficient_quota"}}`, ReasonQuota},
		{"exceeded retry limit, last status: 429 Too Many Requests", ReasonRateLimit},
		{`API Error: 429 {"type":"error","error":{"type":"rate_limit_error"}}`, ReasonRateLimit},
		{`API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, ReasonOverloaded},
		{"Prompt is too long", ReasonContext},
		{"Codex ran out of room in the model's context window. Start a new thread", ReasonContext},
		{"stream disconnected before completion: error sending request", ReasonNetwork},
		{"API Error: Connection error. (ECONNREFUSED)", ReasonNetwork},
		{"getaddrinfo ENOTFOUND api.anthropic.com", ReasonNetwork},
		{"thread/resume failed: no rollout found for thread id 0000 (code -32600)", ReasonSessionMissing},
		{"No conversation found with session ID: 1234", ReasonSessionMissing},
		{"fake step 2 of 2", ""},
		{"", ""},
		{"the tests failed: 3 of 40", ""},
	} {
		if got := Classify(c.text); got != c.want {
			t.Errorf("Classify(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestAskOfFindsTheQuestionAFinalMessageEndsWith(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"ASK: Which database should I migrate first?", "Which database should I migrate first?"},
		{"I looked at both.\n\nASK: Keep the old API?\nOptions: yes / no", "Keep the old API?\nOptions: yes / no"},
		{"  ASK:  trimmed?  ", "trimmed?"},
		{"Done. Nothing to ask.", ""},
		{"The ASK: marker must start a line", ""},
		{"ask: lowercase does not count", ""},
		{"ASK:", ""},
	} {
		if got := AskOf(c.text); got != c.want {
			t.Errorf("AskOf(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}
