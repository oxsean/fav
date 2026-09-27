package defs

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/oxsean/fav/internal/tend"
)

const reviewer = `---
name: reviewer-codex
description: 审 diff，对照验收标准，只写发现不改代码
role: review
provider: codex
model: gpt-6-astra
effort: xhigh
permission: read-only
skills: [code-review]
tools: {deny: [Edit, Write]}
mcp: [gitea]
hooks:
  Stop: [{command: "tend run check"}]
machines: {prefer: [mba], require: []}
output: verdict
budget: {usd: 3, minutes: 40}
team_note: keep it
---
你是评审者。读 workpad 和分支 diff，逐条对照验收标准……
`

func TestADefinitionReadsAndWritesBackTheSame(t *testing.T) {
	d, err := Parse([]byte(reviewer))
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "reviewer-codex" || d.Effort != "xhigh" || !slices.Equal(d.Tools.Deny, []string{"Edit", "Write"}) ||
		d.Budget.USD != 3 || d.Machines.Prefer[0] != "mba" || !strings.HasPrefix(d.Body, "你是评审者") || d.Extra["team_note"] != "keep it" {
		t.Fatalf("%+v", d)
	}
	again, err := Parse(Format(d))
	if err != nil || !reflect.DeepEqual(again, d) {
		t.Fatalf("round trip:\n%s\n%+v\n%+v %v", Format(d), again, d, err)
	}
	errs, warns := Check(d)
	if len(errs) != 0 || len(warns) != 2 {
		t.Fatalf("%v %v", errs, warns)
	}
}

func TestAClaudeCodeSubagentIsADefinition(t *testing.T) {
	d, err := Parse([]byte("---\r\nname: code-reviewer\r\ndescription: Reviews code\r\ntools: Read, Grep, Glob\r\nmodel: sonnet\r\ncolor: blue\r\n---\r\nYou review code.\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.Tools.Allow, []string{"Read", "Grep", "Glob"}) || d.Model != "sonnet" || d.Body != "You review code." {
		t.Fatalf("%+v", d)
	}
	withImport := AgentDef{Name: "reviewer", Role: "review", Import: "~/.claude/agents/code-reviewer.md"}
	got, err := Import(withImport, func(p string) ([]byte, error) {
		if !strings.HasSuffix(filepath.ToSlash(p), "/.claude/agents/code-reviewer.md") || strings.HasPrefix(p, "~") {
			return nil, errors.New("not found " + p)
		}
		return Format(d), nil
	})
	if err != nil || got.Provider != tend.ProviderClaude || got.Body != "You review code." || got.Model != "sonnet" || got.Role != "review" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAMalformedDefinitionSaysWhatIsWrong(t *testing.T) {
	for text, want := range map[string]string{
		"name: x\n":           "no front matter",
		"---\nname: x\n":      "never ends",
		"---\nname: [\n---\n": "front matter",
		"---\nname: Bad Name\nprovider: x\n---\n":                         "name",
		"---\nname: a\n---\n":                                             "provider or profile",
		"---\nname: a\nprovider: claude\neffort: huge\nrole: boss\n---\n": "effort",
	} {
		d, err := Parse([]byte(text))
		if err == nil {
			errs, _ := Check(d)
			if len(errs) > 0 {
				err = errors.New(strings.Join(errs, "; "))
			}
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", text, err)
		}
	}
}

func TestADefinitionCompilesOverTheProfileItStartsFrom(t *testing.T) {
	base := func(name string) (tend.AgentProfile, bool) {
		if name == "codex-high" {
			return tend.AgentProfile{Name: "codex-high", Provider: tend.ProviderCodex, Model: "gpt-5", Args: []string{"-c", "x=1"}}, true
		}
		return tend.AgentProfile{}, false
	}
	d := AgentDef{Name: "rev", Profile: "codex-high", Effort: "high", Permission: "read-only", Tools: Tools{Deny: []string{"Edit"}},
		Machines: Machines{Require: []string{"linux"}}}
	p, err := Compile(d, base)
	want := tend.AgentProfile{Name: "rev", Provider: tend.ProviderCodex, Model: "gpt-5", Args: []string{"-c", "x=1"}, Permission: "read-only",
		Effort: "high", Deny: []string{"Edit"}, Machine: "linux"}
	if err != nil || !reflect.DeepEqual(p, want) {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := Compile(AgentDef{Name: "x", Profile: "nope"}, base); err == nil {
		t.Fatal("an unknown profile")
	}
	if _, err := Compile(AgentDef{Name: "x", Profile: "codex-high", Provider: tend.ProviderClaude}, base); err == nil {
		t.Fatal("a provider other than its profile's")
	}
}
