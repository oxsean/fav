// Package workflow reads workflow definitions (Markdown with a YAML front matter: the stages, then a "## <stage>"
// section per stage for its brief), holds the built-in ones, and writes what a stage's run is told: its brief from the
// template, and the task's workpad.
package workflow

import (
	"embed"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/defs"
	"github.com/oxsean/fav/internal/task"
)

//go:embed builtin/*.md
var builtin embed.FS

// Roles a stage may take its agent by.
var Roles = []string{"planner", "implement", "review", "test"}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

type head struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Stages      []task.Stage `yaml:"stages"`
	MaxLoops    *int         `yaml:"max_loops"`
	Budget      *task.Budget `yaml:"budget"`
}

// Parse reads a workflow definition.
func Parse(text string) (task.Flow, error) {
	h, body, err := defs.Split([]byte(text))
	if err != nil {
		return task.Flow{}, err
	}
	var d head
	if err := yaml.Unmarshal(h, &d); err != nil {
		return task.Flow{}, fmt.Errorf("front matter: %w", err)
	}
	f := task.Flow{Name: d.Name, Stages: d.Stages, MaxLoops: 2, Budget: d.Budget}
	if d.MaxLoops != nil {
		f.MaxLoops = *d.MaxLoops
	}
	sections := sectionsOf(string(body))
	for i := range f.Stages {
		f.Stages[i].Prompt = sections[f.Stages[i].Name]
	}
	return f, Check(f)
}

// Check says what is wrong with f.
func Check(f task.Flow) error {
	var errs []string
	if !nameRe.MatchString(f.Name) {
		errs = append(errs, fmt.Sprintf("name %q", f.Name))
	}
	if len(f.Stages) == 0 {
		errs = append(errs, "no stages")
	}
	if f.MaxLoops < 0 || f.MaxLoops > 10 {
		errs = append(errs, "max_loops: 0 to 10")
	}
	seen := map[string]bool{}
	for _, s := range f.Stages {
		switch {
		case !nameRe.MatchString(s.Name):
			errs = append(errs, fmt.Sprintf("stage name %q", s.Name))
		case seen[s.Name]:
			errs = append(errs, "stage "+s.Name+" twice")
		case s.Gate != "" && s.Gate != task.GateHuman:
			errs = append(errs, "stage "+s.Name+": gate is human or nothing")
		case s.Gate == "" && s.Role == "" && s.Agent == "":
			errs = append(errs, "stage "+s.Name+": a role, an agent or a gate")
		case s.Role != "" && !slices.Contains(Roles, s.Role):
			errs = append(errs, "stage "+s.Name+": role "+s.Role)
		case s.Output != "" && s.Output != task.OutputVerdict:
			errs = append(errs, "stage "+s.Name+": output is verdict or nothing")
		}
		seen[s.Name] = true
	}
	for _, s := range f.Stages {
		if s.OnRework != "" && !seen[s.OnRework] {
			errs = append(errs, "stage "+s.Name+": on_rework "+s.OnRework+" is no stage")
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// sectionsOf are body's "## <name>" sections by name.
func sectionsOf(body string) map[string]string {
	out := map[string]string{}
	name := ""
	var b strings.Builder
	flush := func() {
		if name != "" {
			out[name] = strings.TrimSpace(b.String())
		}
		b.Reset()
	}
	for _, line := range strings.Split(body, "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			flush()
			name = strings.TrimSpace(h)
			continue
		}
		b.WriteString(line + "\n")
	}
	flush()
	return out
}

// Builtins are the built-in workflows by name.
func Builtins() map[string]task.Flow {
	out := map[string]task.Flow{}
	files, _ := builtin.ReadDir("builtin")
	for _, f := range files {
		b, _ := builtin.ReadFile("builtin/" + f.Name())
		if fl, err := Parse(string(b)); err == nil {
			out[fl.Name] = fl
		}
	}
	return out
}

// Names are the built-in workflows and custom's, sorted.
func Names(custom map[string]string) []string {
	var out []string
	for n := range Builtins() {
		out = append(out, n)
	}
	for n := range custom {
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Resolve is workflow name: one of custom (name → Markdown) first, else a built-in one.
func Resolve(name string, custom map[string]string) (task.Flow, error) {
	if text, ok := custom[name]; ok {
		f, err := Parse(text)
		if err == nil && f.Name != name {
			err = fmt.Errorf("workflow %s is named %s inside", name, f.Name)
		}
		return f, err
	}
	if f, ok := Builtins()[name]; ok {
		return f, nil
	}
	return task.Flow{}, fmt.Errorf("no workflow %s", name)
}

// Default briefs of a stage whose workflow gives none, by role.
var defaultPrompt = map[string]string{
	"implement": "{{task.brief}}\n\n{{#acceptance}}Acceptance criteria:\n{{task.acceptance}}\n{{/acceptance}}{{#rework}}\n" +
		"This is round {{loops}}: the work was sent back. Address this, then stop:\n\n{{rework.notes}}\n{{/rework}}",
	"review": "Review the work done for the task below in this directory (see `git status`, `git diff` and the recent " +
		"`git log`). Judge it against the brief and the acceptance criteria; do not change any file.\n\n## The task\n\n" +
		"{{task.brief}}\n\n{{#acceptance}}Acceptance criteria:\n{{task.acceptance}}\n{{/acceptance}}",
	"test": "Test the work done for the task below in this directory: build it, run its tests, try what it claims to " +
		"do. Do not change any file.\n\n## The task\n\n{{task.brief}}\n\n{{#acceptance}}Acceptance criteria:\n" +
		"{{task.acceptance}}\n{{/acceptance}}",
	"planner": "Plan how to do the task below; do not change any file.\n\n{{task.brief}}",
}

// Brief is what the run of t's current stage is told: its stage's template filled in, then the workpad.
func Brief(st *task.State, t *task.Task) string {
	stage := t.Flow.StageOf(t.Stage)
	if stage == nil {
		return t.Brief
	}
	tpl := stage.Prompt
	if tpl == "" {
		tpl = defaultPrompt[stage.Role]
	}
	if tpl == "" {
		tpl = "{{task.brief}}"
	}
	brief := strings.TrimSpace(t.Brief)
	if brief == "" {
		brief = t.Title
	}
	var acc []string
	for _, a := range t.Accept {
		acc = append(acc, "- "+a)
	}
	notes := ReworkNotes(st, t)
	out := block(tpl, "rework", t.Loops > 0 && notes != "")
	out = block(out, "acceptance", len(acc) > 0)
	out = strings.NewReplacer("{{task.brief}}", brief, "{{task.title}}", t.Title, "{{task.acceptance}}", strings.Join(acc, "\n"),
		"{{rework.notes}}", notes, "{{loops}}", fmt.Sprint(t.Loops+1), "{{stage}}", t.Stage, "{{task.id}}", t.ID).Replace(out)
	if wp := Workpad(st, t); wp != "" && !strings.Contains(tpl, "{{workpad}}") {
		out = strings.TrimRight(out, "\n") + "\n\n" + wp
	}
	return strings.ReplaceAll(out, "{{workpad}}", Workpad(st, t))
}

// block keeps or drops the {{#name}}…{{/name}} parts of s.
func block(s, name string, keep bool) string {
	open, end := "{{#"+name+"}}", "{{/"+name+"}}"
	for {
		i := strings.Index(s, open)
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], end)
		if j < 0 {
			return s[:i] + s[i+len(open):]
		}
		inner := s[i+len(open) : i+j]
		if !keep {
			inner = ""
		}
		s = s[:i] + inner + s[i+j+len(end):]
	}
}

// ReworkNotes are why t was last sent back: the verdict or failed check of the run that sent it, and a gate's words.
func ReworkNotes(st *task.State, t *task.Task) string {
	for i := len(t.Notes) - 1; i >= 0; i-- {
		if n := t.Notes[i]; n.Kind == task.NoteRework || n.Kind == task.NoteGate {
			return n.Text
		}
	}
	return ""
}

// maxWorkpad bounds the workpad a brief carries.
const maxWorkpad = 8 << 10

// Workpad is what the stages of t so far said, in order: each stage run's verdict or how it ended, failed checks, and
// the notes (messages, gate decisions). The newest part is kept when it is long.
func Workpad(st *task.State, t *task.Task) string {
	type line struct {
		at   string
		text string
	}
	var lines []line
	for _, r := range st.Runs {
		if r.Task != t.ID || r.Stage == "" || task.Open(r.State) || r.QueuedAt.IsZero() {
			continue
		}
		var s strings.Builder
		fmt.Fprintf(&s, "- [%s] run %s", r.Stage, r.ID)
		switch {
		case r.Verdict != nil:
			fmt.Fprintf(&s, " verdict %s: %s", r.Verdict.Verdict, oneLine(r.Verdict.Summary))
		case r.State == task.Exited && r.ExitCode != nil && *r.ExitCode == 0:
			s.WriteString(" finished")
			if r.Last != "" {
				s.WriteString(": " + oneLine(r.Last))
			}
		default:
			fmt.Fprintf(&s, " ended %s %s", r.State, r.Reason)
		}
		if c := r.Checked; c != nil && c.Exit != 0 {
			fmt.Fprintf(&s, "\n  check `%s` failed (exit %d):\n  %s", strings.Join(c.Argv, " "), c.Exit, indent(tail(c.Tail, 1500)))
		}
		lines = append(lines, line{r.QueuedAt.UTC().Format("2006-01-02T15:04:05.000"), s.String()})
	}
	for _, n := range t.Notes {
		if n.Kind == task.NoteRework { // the run it came from says why
			continue
		}
		who := ""
		if n.By != "" {
			who = " (" + n.By + ")"
		}
		lines = append(lines, line{n.At.UTC().Format("2006-01-02T15:04:05.000"), fmt.Sprintf("- [%s] %s%s: %s", n.Stage, n.Kind, who, oneLine(n.Text))})
	}
	if len(lines) == 0 {
		return ""
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].at < lines[j].at })
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.text + "\n")
	}
	out := b.String()
	if len(out) > maxWorkpad {
		out = "…\n" + out[len(out)-maxWorkpad:]
	}
	return "## Workpad (what happened so far)\n\n" + out
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}

func tail(s string, n int) string {
	if len(s) <= n {
		return strings.TrimSpace(s)
	}
	return "…" + strings.TrimSpace(s[len(s)-n:])
}

func indent(s string) string { return strings.ReplaceAll(s, "\n", "\n  ") }

// Verdicts are the words a verdict may be.
var Verdicts = []string{agent.VerdictPass, agent.VerdictRework, agent.VerdictBlocked}
