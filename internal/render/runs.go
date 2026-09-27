package render

import (
	"cmp"
	"strconv"
	"strings"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/task"
)

// Tokens is a token count for the eye: 950, 12k, 509k, 1.2M.
func Tokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
	case n >= 1000:
		return strconv.FormatInt(n/1000, 10) + "k"
	}
	return strconv.FormatInt(n, 10)
}

var sendStates = map[string]string{agent.SendQueued: "send.queued", agent.SendSent: "send.sent", agent.SendFailed: "send.failed"}

// SendState is how far a message for a run got, as a word.
func SendState(state string) string {
	if k, ok := sendStates[state]; ok {
		return i18n.T(k)
	}
	return state
}

// RunUsage is what a run's agent spent as one line, "" when nothing is known.
func RunUsage(u *agent.Usage) string {
	if u == nil || *u == (agent.Usage{}) {
		return ""
	}
	in, cached, out := Tokens(u.Input+u.CacheWrite), Tokens(u.CacheRead), Tokens(u.Output)
	if u.CostUSD > 0 {
		return i18n.F("run.usage_cost", in, cached, out, u.Turns, strconv.FormatFloat(u.CostUSD, 'f', 2, 64))
	}
	return i18n.F("run.usage", in, cached, out, u.Turns)
}

var runReasons = map[string]string{
	"cli_missing": "run.reason.cli_missing", "auth_missing": "run.reason.auth_missing", "auth": "run.reason.auth",
	"quota": "run.reason.quota", "rate_limit": "run.reason.rate_limit", "overloaded": "run.reason.overloaded",
	"context_overflow": "run.reason.context_overflow", "network": "run.reason.network",
	"session_missing": "run.reason.session_missing", "permission_denied": "run.reason.permission_denied",
	"node_outdated": "run.reason.node_outdated", "not_launched": "run.reason.not_launched",
	"supervisor_gone": "run.reason.supervisor_gone", "missing": "run.reason.missing", "asked": "run.reason.asked",
	"signal": "run.reason.signal", "tab_closed": "run.reason.tab_closed", "never_started": "run.reason.never_started",
}

// RunReason is why a run ended as a phrase; a reason tend does not know is shown as it came.
func RunReason(code string) string {
	if k, ok := runReasons[code]; ok {
		return i18n.T(k)
	}
	return code
}

// RunHint is what the user can do next about run r, "" when nothing.
func RunHint(r *task.Run) string {
	if r.Waiting() {
		return i18n.F("run.hint.waiting", r.ID)
	}
	if task.Open(r.State) {
		return ""
	}
	switch r.Reason {
	case "cli_missing":
		return i18n.F("run.hint.cli_missing", r.Profile.Provider)
	case "auth_missing", "auth":
		return i18n.F("run.hint.auth", r.Profile.Provider)
	case "quota":
		return i18n.T("run.hint.quota")
	case "rate_limit", "overloaded", "network":
		return i18n.T("run.hint.later")
	case "context_overflow":
		return i18n.T("run.hint.context_overflow")
	case "session_missing":
		return i18n.T("run.hint.session_missing")
	case "node_outdated":
		return i18n.F("run.hint.node_outdated", r.Machine)
	}
	return ""
}

// RunAttention is why run r wants someone, "" when it does not.
func RunAttention(r *task.Run) string {
	switch r.Attention {
	case task.AttentionAsked:
		return i18n.T("run.attention.asked")
	case task.AttentionPermission:
		return i18n.T("run.attention.permission")
	case task.AttentionStalled:
		if task.Open(r.State) {
			return i18n.T("run.attention.stalled")
		}
	}
	return ""
}

var whys = map[string]string{
	"cli_missing": "why.cli_missing", "auth_missing": "why.auth_missing", "node_outdated": "why.node_outdated",
	"offline": "why.offline", "connecting": "why.connecting", "slots": "why.slots", "dir_busy": "why.dir_busy",
	"auth_unknown": "why.auth_unknown", "unchecked": "why.unchecked", "herdr": "why.herdr", "background": "why.background",
	"continues": "why.continues", "no_access": "why.no_access", "def_pending": "why.def_pending",
}

// Why is one line of a dispatch preview (coord.Why) as a sentence.
func Why(code, detail string) string {
	k, ok := whys[code]
	if !ok {
		return code
	}
	switch code {
	case "connecting", "unchecked", "herdr", "background":
		return i18n.T(k)
	}
	return i18n.F(k, detail)
}

// RunWork is what run r did to its task's branch, in one line; "" when it worked in a plain directory.
func RunWork(r *task.Run) string {
	w, done := r.Work, r.Worked
	if w == nil {
		return ""
	}
	head := ""
	if done != nil {
		head = shortHead(done.Head)
	}
	var parts []string
	switch {
	case w.Merge != "" && done != nil && done.Merged:
		parts = append(parts, i18n.F("run.work.merged", w.Merge, w.Branch))
	case w.Merge != "" && done != nil && len(done.Conflict) > 0:
		parts = append(parts, i18n.F("run.work.conflict", w.Merge, w.Branch, strings.Join(done.Conflict, ", ")))
	case w.Merge != "":
		return ""
	case w.ReadOnly:
		parts = append(parts, i18n.F("run.work.copy", w.Branch, cmp.Or(head, "-")))
		if done != nil && done.Discarded > 0 {
			parts = append(parts, i18n.F("run.work.discarded", done.Discarded))
		}
	case done != nil && done.Head != "":
		parts = append(parts, i18n.F("run.work.branch", w.Branch, head, done.Commits))
		if done.Diffstat != "" {
			parts = append(parts, done.Diffstat)
		}
	default:
		parts = append(parts, w.Branch)
	}
	if done != nil {
		if done.PR != "" {
			parts = append(parts, i18n.F("run.work.pr", done.PR))
		}
		for _, x := range done.Warnings {
			parts = append(parts, i18n.F("run.work.warning", x))
		}
	}
	return strings.Join(parts, " · ")
}

func shortHead(h string) string {
	if len(h) > 10 {
		return h[:10]
	}
	return h
}
