package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/envcheck"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/paths"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/tend"
)

// cmdEnv: tend env [--dir d] [--json] prints this machine's environment, the answer of env; tend env diff compares.
func cmdEnv(args []string) error {
	if first(args) == "diff" {
		return cmdEnvDiff(args[1:])
	}
	fs := newFlags("env")
	dir := fs.String("dir", "", i18n.T("cli.env.flag_dir"))
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	if _, err := parseWithArgs(fs, args, 0); err != nil {
		return err
	}
	d, err := envDir(*dir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	p, err := localEnv(ctx, remote.EnvParams{Dir: d})
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(p)
	}
	printRows(envRows(p))
	return nil
}

// cmdEnvDiff: tend env diff <machine> [--session <id> | --dir d] [--there d] [--json] is what an agent would find
// different on machine; a block exits non-zero.
func cmdEnvDiff(args []string) error {
	fs := newFlags("env")
	ref := fs.String("session", "", i18n.T("cli.env.flag_session"))
	dir := fs.String("dir", "", i18n.T("cli.env.flag_dir"))
	there := fs.String("there", "", i18n.T("cli.env.flag_there"))
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	pos, err := parseWithArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if *ref != "" && *dir != "" {
		return i18n.E("cli.env.session_or_dir")
	}
	if *there != "" && !pathmap.Abs(*there) {
		return i18n.E("cli.env.there_relative", *there)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*hostTimeout)
	defer cancel()
	r, params := &tend.Rec{}, remote.EnvParams{}
	if *ref != "" {
		s, err := tend.Open()
		if err != nil {
			return err
		}
		if r, err = pick(s, *ref); err != nil {
			return err
		}
		params.Ref = &remote.Ref{Provider: r.Provider, SessionID: r.SessionID}
	} else {
		if params.Dir, err = envDir(*dir); err != nil {
			return err
		}
		r.Cwd = params.Dir
	}
	from, err := ownPeer(ctx, r.Host, "cli.env.not_mine", "cli.env.server_old")
	if err != nil {
		return err
	}
	to, err := ownPeer(ctx, pos[0], "cli.env.not_mine", "cli.env.server_old")
	if err != nil {
		return err
	}
	var src, dst envcheck.Print
	if err := from.Call(ctx, remote.MEnv, params, &src); err != nil {
		return envErr(cmp.Or(r.Host, tend.HostLocal), err)
	}
	target := *there
	if target == "" && src.Dir != "" {
		x := &remote.Handover{From: from, To: to, Facts: capture.HandoffFacts{Cwd: src.Dir, Git: capture.HandoffGit{Remote: src.Git.Remote}}}
		if target, err = handoffDir(ctx, x, r, pos[0], envDirTexts); err != nil {
			return err
		}
	}
	if err := to.Call(ctx, remote.MEnv, remote.EnvParams{Dir: target}, &dst); err != nil {
		return envErr(pos[0], err)
	}
	rep := envcheck.Compare(src, dst, from.End(), to.End())
	if *asJSON {
		if err := printJSON(rep); err != nil {
			return err
		}
	} else {
		printEnvReport(rep)
	}
	if rep.Blocked() {
		return i18n.E("cli.env.blocked", rep.Block)
	}
	return nil
}

var envDirTexts = dirTexts{"cli.env.no_counterpart", "cli.env.target", "cli.env.dirs"}

// envDir is dir as an absolute path on this machine; "" stays the machine alone.
func envDir(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	return filepath.Abs(paths.Expand(dir))
}

// localEnv is this machine's answer to env, as `tend rpc` gives it.
func localEnv(ctx context.Context, p remote.EnvParams) (envcheck.Print, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return envcheck.Print{}, err
	}
	v, err := remote.NewLocal(version).Handle(ctx, remote.MEnv, raw)
	if err != nil {
		return envcheck.Print{}, envErr(tend.HostLocal, err)
	}
	return v.(envcheck.Print), nil
}

func envErr(name string, err error) error { return errors.New(remote.EnvRefusal(name, err)) }

// envRows are a Print as label and value lines: names, versions and short hashes.
func envRows(p envcheck.Print) [][2]string {
	var rows [][2]string
	add := func(key, value string) {
		if value != "" {
			rows = append(rows, [2]string{i18n.T(key), value})
		}
	}
	switch {
	case p.Dir == "":
		add("cli.env.dir", i18n.T("cli.env.machine"))
	case p.NoDir:
		add("cli.env.dir", i18n.F("cli.env.not_here", p.Dir))
	default:
		add("cli.env.dir", p.Dir)
	}
	for _, c := range p.CLIs {
		v := c.Version
		switch {
		case !c.Found:
			v = i18n.T("cli.env.not_installed")
		case v == "":
			v = i18n.T("cli.env.no_version")
		}
		rows = append(rows, [2]string{c.Name, v})
	}
	if p.Dir != "" && !p.NoDir {
		add("cli.env.git", gitText(p.Git))
	}
	for i, f := range p.Files {
		label := ""
		if i == 0 {
			label = i18n.T("cli.env.files")
		}
		rows = append(rows, [2]string{label, f.Kind + ":" + f.Name + "  " + f.SHA[:min(len(f.SHA), 8)]})
	}
	add("cli.env.skills", strings.Join(p.Skills, " "))
	add("cli.env.mcp", strings.Join(p.MCP, " "))
	add("cli.env.codex_mcp", strings.Join(p.CodexMCP, " "))
	add("cli.env.project", projectText(p.Project))
	add("cli.env.provider", providerText(p.Provider))
	add("cli.env.unknown", strings.Join(p.Unknown, " "))
	return rows
}

func gitText(g envcheck.Git) string {
	if !g.Repo {
		return i18n.T("cli.env.not_repo")
	}
	parts := []string{g.Branch, g.Head[:min(len(g.Head), 8)]}
	if g.Dirty > 0 {
		parts = append(parts, i18n.F("cli.env.dirty", g.Dirty))
	}
	if g.Unpushed > 0 {
		parts = append(parts, i18n.F("cli.env.unpushed", g.Unpushed))
	}
	return strings.Join(append(parts, g.Remote), " · ")
}

// projectText and providerText name the settings as the CLIs spell them.
func projectText(p envcheck.Project) string {
	var parts []string
	if len(p.AllowedTools) > 0 {
		parts = append(parts, "allowedTools "+strings.Join(p.AllowedTools, " "))
	}
	if len(p.MCPJSON) > 0 {
		parts = append(parts, "enabledMcpjsonServers "+strings.Join(p.MCPJSON, " "))
	}
	if p.Trusted {
		parts = append(parts, "hasTrustDialogAccepted")
	}
	if p.CodexTrust != "" {
		parts = append(parts, "trust_level "+p.CodexTrust)
	}
	return strings.Join(parts, " · ")
}

func providerText(p envcheck.Provider) string {
	var parts []string
	if len(p.ClaudeEnv) > 0 {
		parts = append(parts, "env "+strings.Join(p.ClaudeEnv, " "))
	}
	if p.ClaudeHost != "" {
		parts = append(parts, "ANTHROPIC_BASE_URL "+p.ClaudeHost)
	}
	if p.Codex != "" || p.CodexHost != "" {
		parts = append(parts, strings.TrimSpace("model_provider "+p.Codex+" "+p.CodexHost))
	}
	return strings.Join(parts, " · ")
}

func printRows(rows [][2]string) {
	w := 0
	for _, r := range rows {
		w = max(w, render.Width(r[0]))
	}
	for _, r := range rows {
		fmt.Println(strings.TrimRight(render.Pad(r[0], w)+"  "+r[1], " "))
	}
}

// printEnvReport is the summary line, then each item with where its evidence comes from and what to do by hand.
func printEnvReport(r envcheck.Report) {
	fmt.Println(r.Summary())
	w := 0
	for _, it := range r.Items {
		w = max(w, render.Width(envcheck.LevelText(it.Level)))
	}
	now := time.Now()
	for _, it := range r.Items {
		evidence := envcheck.EvidenceText(it.Evidence)
		if when := render.When(it.At, now); when != "" && it.Evidence != envcheck.EvidenceUnknown {
			evidence += " " + when
		}
		fmt.Println("  " + render.Pad(envcheck.LevelText(it.Level), w) + "  " + it.What + "  (" + evidence + ")")
		if it.Fix != "" {
			fmt.Println("  " + strings.Repeat(" ", w) + "  → " + it.Fix)
		}
	}
}
