package agent

import "strings"

// Why a run failed, read from what its agent or CLI said.
const (
	ReasonAuth           = "auth"
	ReasonQuota          = "quota"
	ReasonRateLimit      = "rate_limit"
	ReasonOverloaded     = "overloaded"
	ReasonContext        = "context_overflow"
	ReasonNetwork        = "network"
	ReasonSessionMissing = "session_missing"
	ReasonPermission     = "permission_denied"
)

// failures are checked in order: a quota message may also carry a 429, an auth one a network word.
var failures = []struct {
	reason string
	marks  []string
}{
	{ReasonSessionMissing, []string{"no rollout found", "no conversation found", "session not found"}},
	{ReasonAuth, []string{"invalid api key", "/login", "not logged in", "authentication_error", "oauth token has expired",
		"api error: 401", "unauthorized", "codex login"}},
	{ReasonQuota, []string{"usage limit", "hit your limit", "credit balance", "insufficient_quota", "quota exceeded", "billing"}},
	{ReasonContext, []string{"prompt is too long", "context window", "context_length_exceeded", "maximum context length"}},
	{ReasonOverloaded, []string{"overloaded", "api error: 529"}},
	{ReasonRateLimit, []string{"rate limit", "rate_limit", "429", "too many requests"}},
	{ReasonNetwork, []string{"stream disconnected", "econnrefused", "econnreset", "enotfound", "etimedout", "connection error",
		"connection refused", "connection reset", "network is unreachable", "fetch failed", "error sending request"}},
}

// Classify names why a run failed from its error text, "" when the text says nothing known.
func Classify(text string) string {
	low := strings.ToLower(text)
	for _, f := range failures {
		for _, m := range f.marks {
			if strings.Contains(low, m) {
				return f.reason
			}
		}
	}
	return ""
}

// AskMark starts the line of a final message that asks the user something (the run convention).
const AskMark = "ASK:"

// AskOf is the question a final message ends with: from its line starting with AskMark to the end, "" when none.
func AskOf(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, l := range lines {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(l), AskMark); ok {
			return strings.TrimSpace(strings.Join(append([]string{rest}, lines[i+1:]...), "\n"))
		}
	}
	return ""
}
