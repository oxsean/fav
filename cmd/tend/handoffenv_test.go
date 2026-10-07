package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/envcheck"
	"github.com/oxsean/fav/internal/fixture"
	"github.com/oxsean/fav/internal/i18n"
)

// A pack for another machine says how the environment there differs from the session's; a block is written in the
// pack and on stderr, and the handoff goes on. A pack for this machine has no such section.
func TestAHandoffPackSaysHowTheEnvironmentDiffers(t *testing.T) {
	self, peer, _ := twoHomes(t)
	defer i18n.Set(i18n.ZH)
	i18n.Set(i18n.EN)
	s := self.Get("oauth")
	heading := "## " + i18n.T("handoff.env")

	var pack string
	var err error
	stderr := stderrOf(t, func() {
		pack, err = tendOut(t, "handoff", s.ID, "--host", "peer", "--dir", filepath.Join(peer.Work, "webapp"), "--print")
	})
	if err != nil || !strings.Contains(pack, heading) {
		t.Fatalf("a pack for peer: %v\n%s", err, pack)
	}
	section := pack[strings.Index(pack, heading):]
	if strings.Contains(section, fixture.Secret) || strings.Contains(section, fixture.SecretEmail) {
		t.Fatalf("names and hashes only:\n%s", section)
	}
	counts := regexp.MustCompile(strings.ReplaceAll(regexp.QuoteMeta(i18n.F("envcheck.summary", 0, 0, 0)), "0", `\d+`))
	if summary := counts.FindString(section); summary == "" || !strings.Contains(stderr, summary) {
		t.Errorf("stderr carries the summary %q:\n%s", summary, stderr)
	}

	gone := filepath.Join(peer.Work, "nowhere")
	stderr = stderrOf(t, func() { pack, err = tendOut(t, "handoff", s.ID, "--host", "peer", "--dir", gone, "--print") })
	block := "- " + envcheck.LevelText(envcheck.LevelBlock) + ": " + i18n.F("envcheck.no_dir", gone)
	if err != nil || !strings.Contains(pack, block) || !strings.Contains(stderr, i18n.F("envcheck.no_dir", gone)) {
		t.Fatalf("a block is written down, not a refusal: %v\n%s\n%s", err, pack, stderr)
	}

	if local, err := tendOut(t, "handoff", s.ID); err != nil || strings.Contains(local, heading) {
		t.Errorf("a pack for this machine: %v\n%s", err, local)
	}
}
