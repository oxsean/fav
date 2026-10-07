package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/memory"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/shell"
	"github.com/oxsean/fav/internal/task"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// cmdMemory lists, reads, trashes, merges, compares and copies the agents' memories: tend memory [project|dir]
// [host:<name>], show, rm, merge, diff, cp.
func cmdMemory(args []string) error {
	switch first(args) {
	case "show":
		return memoryShow(args[1:])
	case "rm":
		return memoryRm(args[1:])
	case "merge":
		return memoryMerge(args[1:])
	case "diff":
		return memoryDiff(args[1:])
	case "cp":
		return memoryCp(args[1:])
	}
	return memoryList(args)
}

// hostArg splits host:<name> off the positional arguments; name is the configured host it names.
func hostArg(pos []string) (rest []string, name string, err error) {
	for _, p := range pos {
		h, ok := strings.CutPrefix(p, "host:")
		if !ok {
			rest = append(rest, p)
			continue
		}
		if name = hostName(remoteHosts(), h); name == "" {
			farHosts().reach()
			if name = hostName(remoteHosts(), h); name == "" {
				return nil, "", i18n.E("cli.memory.no_host", h)
			}
		}
	}
	return rest, name, nil
}

// projectNamed is the project whose id or name is target, nil for none.
func projectNamed(target string) *task.Project {
	if target == "" {
		return nil
	}
	for _, p := range sessionProjects().Projects {
		if p.ID == target || strings.EqualFold(p.Name, target) {
			return p
		}
	}
	return nil
}

// memoryDirs are the directories of the project named target on machine (this one when ""), else target itself.
func memoryDirs(target, machine string) ([]string, error) {
	if target != "" {
		s := sessionProjects()
		on := cmp.Or(machine, s.Here)
		if p := projectNamed(target); p != nil {
			dirs := projects.DirsOn(p, on)
			if len(dirs) == 0 {
				return nil, i18n.E("cli.memory.no_project_dir", p.Name, on)
			}
			return dirs, nil
		}
	}
	if machine != "" {
		if target == "" {
			return nil, errors.New(i18n.T("cli.memory.usage"))
		}
		return []string{target}, nil
	}
	d, err := absDir(target)
	if err != nil {
		return nil, err
	}
	return []string{d}, nil
}

func memoryList(args []string) error {
	fs := newFlags("memory")
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	pos, host, err := hostArg(pos)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return errors.New(i18n.T("cli.memory.usage"))
	}
	dirs, err := memoryDirs(first(pos), host)
	if err != nil {
		return err
	}
	var sets []memory.Set
	if host == "" {
		sets = memory.List(dirs, true)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		l, err := remoteHosts().Memories(ctx, host, remote.MemoryListParams{Dirs: dirs, Global: true})
		if err != nil {
			return errors.New(remote.MemoryRefused(host, err))
		}
		sets = l.Sets
	}
	if *asJSON {
		if sets == nil {
			sets = []memory.Set{}
		}
		return printJSON(sets)
	}
	printMemories(sets, strings.Join(dirs, ", "))
	return nil
}

func printMemories(sets []memory.Set, of string) {
	now, shown := time.Now(), false
	for _, s := range sets {
		if len(s.Items) == 0 {
			continue
		}
		if shown {
			fmt.Println()
		}
		shown = true
		switch {
		case s.Kind == memory.KindClaude:
			fmt.Print(i18n.F("cli.memory.claude_set", s.Dir, len(s.Items), s.Lines))
			if s.Over {
				fmt.Print(i18n.T("cli.memory.over"))
			}
		case s.Dir != "":
			fmt.Print(i18n.F("cli.memory.codex_set", s.Dir, len(s.Items)))
		default:
			fmt.Print(i18n.F("cli.memory.codex_loose", len(s.Items)))
		}
		for _, it := range s.Items {
			if it.Description != "" {
				fmt.Print(i18n.F("cli.memory.item", it.Title, it.Description, render.When(it.At, now)))
			} else {
				fmt.Print(i18n.F("cli.memory.item_bare", it.Title, render.When(it.At, now)))
			}
			if it.Line > 0 {
				fmt.Printf("    %s:%d\n", it.File, it.Line)
			} else {
				fmt.Printf("    %s\n", it.File)
			}
		}
	}
	if !shown {
		fmt.Print(i18n.F("cli.memory.none", of))
	}
}

func memoryShow(args []string) error {
	pos, host, err := hostArg(args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New(i18n.T("cli.memory.usage"))
	}
	if host != "" {
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		t, err := remoteHosts().MemoryRead(ctx, host, pos[0])
		if err != nil {
			return errors.New(remote.MemoryRefused(host, err))
		}
		fmt.Print(t.Text)
		return nil
	}
	file, err := absDir(pos[0])
	if err != nil {
		return err
	}
	text, _, _, err := memory.Read(file)
	if errors.Is(err, memory.ErrOutside) {
		return i18n.E("cli.memory.outside", file)
	}
	if err != nil {
		return err
	}
	fmt.Print(text)
	return nil
}

func memoryRm(args []string) error {
	fs := newFlags("memory rm")
	yes := fs.Bool("y", false, i18n.T("cli.rm.flag_yes"))
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	pos, host, err := hostArg(pos)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New(i18n.T("cli.memory.usage"))
	}
	target := pos[0]
	if host == "" {
		if target, err = absDir(target); err != nil {
			return err
		}
	}
	if ok, err := confirmErr(i18n.F("cli.memory.rm_confirm", target), *yes); !ok {
		return err
	}
	if host != "" {
		if err := farWritable(host); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
		defer cancel()
		e, err := remoteHosts().MemoryTrash(ctx, host, target)
		if err != nil {
			return errors.New(remote.MemoryRefused(host, err))
		}
		fmt.Print(i18n.F("cli.memory.rm_done_far", host, target, e.Entry))
		return nil
	}
	id, err := memory.Trash(target)
	if errors.Is(err, memory.ErrOutside) {
		return i18n.E("cli.memory.outside", target)
	}
	if err != nil {
		return err
	}
	fmt.Print(i18n.F("cli.memory.rm_done", target, shell.User().Join([]string{"tend", "trash", "--restore", id})))
	return nil
}

func memoryMerge(args []string) error {
	if len(args) != 2 || slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "host:") }) {
		return errors.New(i18n.T("cli.memory.merge_usage"))
	}
	var dirs [2]string
	for i, a := range args {
		d, err := absDir(a)
		if err != nil {
			return err
		}
		dirs[i] = memory.DirOf(d)
	}
	if _, err := os.Stat(dirs[0]); err != nil {
		return i18n.E("cli.memory.outside", dirs[0])
	}
	m, err := memory.Merge(dirs[0], dirs[1])
	if errors.Is(err, memory.ErrOutside) {
		return i18n.E("cli.memory.outside", dirs[0])
	}
	if err != nil {
		return err
	}
	fmt.Print(i18n.F("cli.memory.merged", dirs[1], len(m.Copied), len(m.Same), len(m.Incoming)))
	if m.Trashed != "" {
		fmt.Print(i18n.F("cli.memory.merge_trashed", shell.User().Join([]string{"tend", "trash", "--restore", m.Trashed})))
	} else {
		fmt.Print(i18n.F("cli.memory.merge_kept", strings.Join(m.Left, ", ")))
	}
	return nil
}

// memoryEnds are the two machines of tend memory diff and cp, From the one copied from, and the directories paired on
// them.
type memoryEnds struct {
	from, to remote.Peer
	pairs    []remote.DirPair
}

func (e memoryEnds) name(p remote.Peer) string { return cmp.Or(p.Name, tend.HostLocal) }

// memoryEndsOf reaches from (this machine for "") and machine, the viewer's own both, and pairs target's directories
// on them: a project's repositories found on both, else the directory target on from with there, or with what its
// project's directory on machine makes of it.
func memoryEndsOf(ctx context.Context, target, machine, from, there string) (memoryEnds, error) {
	var e memoryEnds
	var err error
	if e.from, err = ownPeer(ctx, from, "cli.memory.not_mine", "cli.memory.server_old"); err != nil {
		return e, err
	}
	if e.to, err = ownPeer(ctx, machine, "cli.memory.not_mine", "cli.memory.server_old"); err != nil {
		return e, err
	}
	s := sessionProjects()
	fromKey, toKey := cmp.Or(e.from.Name, s.Here), cmp.Or(e.to.Name, s.Here)
	if p := projectNamed(target); p != nil && there == "" {
		if e.pairs = projects.Pairs(p, fromKey, toKey); len(e.pairs) == 0 {
			return e, i18n.E("cli.memory.no_pair", p.Name, e.name(e.from), e.name(e.to))
		}
		return e, nil
	}
	src := target
	if e.from.Name == "" {
		if src, err = absDir(target); err != nil {
			return e, err
		}
	} else if !pathmap.Abs(src) {
		return e, i18n.E("cli.handoff.dir_abs", src)
	}
	if there != "" {
		if !pathmap.Abs(there) {
			return e, i18n.E("cli.handoff.dir_abs", there)
		}
		e.pairs = []remote.DirPair{{From: src, To: there}}
		return e, nil
	}
	if p := s.Holding(fromKey, src); p != nil {
		for _, r := range p.Repos {
			if d, ok := pathmap.Rebase(src, r.Dirs[fromKey], r.Dirs[toKey], e.from.End(), e.to.End()); ok && r.Dirs[toKey] != "" {
				e.pairs = []remote.DirPair{{From: src, To: d}}
				return e, nil
			}
		}
	}
	return e, i18n.E("cli.memory.no_dir_there", e.name(e.to), src)
}

// refused says why the machine of e that err came from did not answer a memory method.
func (e memoryEnds) refused(err error) error {
	p := e.to
	var pe *remote.PeerError
	if errors.As(err, &pe) {
		p = pe.Peer
	}
	return errors.New(remote.MemoryRefusal(p, e.name(p), err))
}

func memorySyncFlags(name string) (*flag.FlagSet, *string, *string) {
	fs := newFlags(name)
	from := fs.String("from", "", i18n.T("cli.memory.flag_from"))
	dir := fs.String("dir", "", i18n.T("cli.memory.flag_dir"))
	return fs, from, dir
}

// memoryDiff compares a project's memories on two machines item by item: tend memory diff <project|dir> <machine>.
func memoryDiff(args []string) error {
	fs, from, dir := memorySyncFlags("memory diff")
	asJSON := fs.Bool("json", false, i18n.T("cli.flag_json"))
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return errors.New(i18n.T("cli.memory.diff_usage"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	e, err := memoryEndsOf(ctx, pos[0], pos[1], *from, *dir)
	if err != nil {
		return err
	}
	pairs, err := remote.CompareMemories(ctx, e.from, e.to, e.pairs)
	if err != nil {
		return e.refused(err)
	}
	if *asJSON {
		return printJSON(pairs)
	}
	copyable := false
	for i, p := range pairs {
		if i > 0 {
			fmt.Println()
		}
		fmt.Print(i18n.F("cli.memory.diff_pair", e.name(e.from), p.Dirs.From, e.name(e.to), p.Dirs.To))
		for _, g := range []struct {
			key     string
			machine string
			entries []memory.Entry
		}{{"cli.memory.diff_only", e.name(e.from), p.Diff.OnlyHere}, {"cli.memory.diff_only", e.name(e.to), p.Diff.OnlyThere}} {
			if len(g.entries) > 0 {
				fmt.Print(i18n.F(g.key, g.machine, len(g.entries)))
				printEntries(g.entries)
			}
		}
		if len(p.Diff.Differ) > 0 {
			fmt.Print(i18n.F("cli.memory.diff_differ", len(p.Diff.Differ)))
			printEntries(p.Diff.Differ)
		}
		loose := 0
		for _, x := range p.Diff.Same {
			if x.Loose {
				loose++
			}
		}
		fmt.Print(i18n.F("cli.memory.diff_same", len(p.Diff.Same), loose))
		for _, side := range []struct {
			machine string
			sets    []memory.Set
		}{{e.name(e.from), p.From}, {e.name(e.to), p.To}} {
			if slices.ContainsFunc(side.sets, func(s memory.Set) bool { return s.Over }) {
				fmt.Print(i18n.F("cli.memory.over_on", side.machine))
			}
		}
		copyable = copyable || slices.ContainsFunc(append(slices.Clone(p.Diff.OnlyHere), p.Diff.Differ...),
			func(x memory.Entry) bool { return x.Kind == memory.KindClaude })
	}
	if copyable {
		cp := []string{"tend", "memory", "cp", pos[0], pos[1]}
		if *from != "" {
			cp = append(cp, "--from", *from)
		}
		if *dir != "" {
			cp = append(cp, "--dir", *dir)
		}
		fmt.Print(i18n.F("cli.memory.diff_hint", shell.User().Join(cp)))
	}
	return nil
}

func printEntries(es []memory.Entry) {
	for _, x := range es {
		it := x.Here
		if it == nil {
			it = x.There
		}
		if x.Kind == memory.KindClaude {
			fmt.Print(i18n.F("cli.memory.diff_claude", x.Name, it.Title))
		} else {
			fmt.Print(i18n.F("cli.memory.diff_codex", x.Name))
		}
	}
}

// memoryCp copies Claude memories of a project from one machine to another by name, never over another: tend memory
// cp <project|dir> <machine> <name…>.
func memoryCp(args []string) error {
	fs, from, dir := memorySyncFlags("memory cp")
	pos, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 3 {
		return errors.New(i18n.T("cli.memory.cp_usage"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	e, err := memoryEndsOf(ctx, pos[0], pos[1], *from, *dir)
	if err != nil {
		return err
	}
	src, dst := e.name(e.from), e.name(e.to)
	pairs, err := remote.CompareMemories(ctx, e.from, e.to, e.pairs)
	if err != nil {
		return e.refused(err)
	}
	failed := 0
	for _, name := range pos[2:] {
		file := name
		if !strings.HasSuffix(file, ".md") {
			file += ".md"
		}
		found := false
		for _, p := range pairs {
			x, ok := memoryEntry(p.Diff, file, name)
			if !ok {
				continue
			}
			found = true
			switch {
			case x.Kind != memory.KindClaude:
				fmt.Print(i18n.F("cli.memory.cp_codex", name))
				failed++
				continue
			case x.There != nil && x.There.SHA == x.Here.SHA:
				fmt.Print(i18n.F("cli.memory.cp_same", file, dst))
				continue
			case x.Loose:
				fmt.Print(i18n.F("cli.memory.cp_same_loose", file, dst))
				continue
			}
			res, err := remote.CopyMemory(ctx, e.from, e.to, p, x)
			switch {
			case wire.Code(err) == wire.CodeStale:
				fmt.Fprint(os.Stderr, i18n.F("cli.memory.cp_stale", file, dst))
				failed++
				continue
			case err != nil:
				return e.refused(err)
			case res.Incoming:
				fmt.Print(i18n.F("cli.memory.cp_incoming", file, dst, res.File))
			default:
				fmt.Print(i18n.F("cli.memory.cp_done", file, dst, res.File))
			}
			if res.Over {
				fmt.Print(i18n.F("cli.memory.over_on", dst))
			}
		}
		if !found {
			fmt.Fprint(os.Stderr, i18n.F("cli.memory.cp_missing", name, src))
			failed++
		}
	}
	if failed > 0 {
		return i18n.E("cli.memory.cp_failed", failed)
	}
	return nil
}

// memoryEntry is the entry of c that is on the From side: a Claude memory by its file name, a Codex block by its title.
func memoryEntry(c memory.Comparison, file, title string) (memory.Entry, bool) {
	for _, l := range [][]memory.Entry{c.OnlyHere, c.Differ, c.Same} {
		for _, x := range l {
			if x.Kind == memory.KindClaude && x.Name == file || x.Kind != memory.KindClaude && x.Name == title {
				return x, true
			}
		}
	}
	return memory.Entry{}, false
}
