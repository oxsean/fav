package render

import (
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/task"
)

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
		if !task.Open(r.State) {
			return i18n.T("run.attention.permission")
		}
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
	"continues": "why.continues",
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
