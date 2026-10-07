package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/memory"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/shell"
)

// cmdMemory lists, reads, trashes and merges the agents' memories: tend memory [project|dir] [host:<name>], show, rm, merge.
func cmdMemory(args []string) error {
	switch first(args) {
	case "show":
		return memoryShow(args[1:])
	case "rm":
		return memoryRm(args[1:])
	case "merge":
		return memoryMerge(args[1:])
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

// memoryDirs are the directories of the project named target on machine (this one when ""), else target itself.
func memoryDirs(target, machine string) ([]string, error) {
	if target != "" {
		s := sessionProjects()
		on := cmp.Or(machine, s.Here)
		for _, p := range s.Projects {
			if p.ID != target && !strings.EqualFold(p.Name, target) {
				continue
			}
			var dirs []string
			for _, r := range p.Repos {
				if d := r.Dirs[on]; d != "" {
					dirs = append(dirs, d)
				}
			}
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
