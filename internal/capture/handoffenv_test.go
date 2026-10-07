package capture

import (
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/tend"
)

// A pack read on another machine carries the comparison its sender made: a lead naming the session's machine, the
// summary line and each item it was given; unread, only why. A pack without one has no such section.
func TestAPackForAnotherMachineSaysHowItsEnvironmentDiffers(t *testing.T) {
	f := HandoffFacts{Provider: tend.ProviderClaude, SessionID: "s1", Title: "Paging", Cwd: "/home/dev/shop"}
	to := HandoffTarget{Source: "studio", SourceHome: "/home/dev", Dir: "/srv/shop", Home: "/home/ops"}
	heading := "## " + i18n.T("handoff.env")
	if text := RenderHandoff(f, to); strings.Contains(text, heading) {
		t.Errorf("no comparison, no section:\n%s", text)
	}
	to.Env = &HandoffEnv{Summary: "block 1 · unequal 1 · hint 2", Items: []string{"block: claude is not installed on the target", "unequal: CLAUDE.md differs on the target"}}
	text := RenderHandoff(f, to)
	for _, want := range []string{heading, i18n.F("handoff.env.lead", "studio"), to.Env.Summary, "- " + to.Env.Items[0], "- " + to.Env.Items[1]} {
		if !strings.Contains(text, want) {
			t.Errorf("pack lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, heading) < strings.Index(text, "## "+i18n.T("handoff.dirs")) {
		t.Errorf("the environment follows the directories:\n%s", text)
	}
	to.Env = &HandoffEnv{Unread: "timed out"}
	if text := RenderHandoff(f, to); !strings.Contains(text, i18n.F("handoff.env.unread", "timed out")) || strings.Contains(text, i18n.F("handoff.env.lead", "studio")) {
		t.Errorf("unread, it says so and nothing else:\n%s", text)
	}
}
