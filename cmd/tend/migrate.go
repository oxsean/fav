package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/oxsean/fav/internal/envcheck"
	"github.com/oxsean/fav/internal/i18n"
	"github.com/oxsean/fav/internal/pathmap"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/render"
	"github.com/oxsean/fav/internal/tend"
	"github.com/oxsean/fav/internal/wire"
)

// cmdMigrate copies a Claude session whole to another of the viewer's machines (tend migrate <id|host:sid> --to
// <machine>), or drops a migration of it left unfinished (--abandon).
func cmdMigrate(args []string) error {
	fs := newFlags("migrate")
	to := fs.String("to", "", i18n.T("cli.migrate.flag_to"))
	dir := fs.String("dir", "", i18n.T("cli.migrate.flag_dir"))
	move := fs.Bool("move", false, i18n.T("cli.migrate.flag_move"))
	dryRun := fs.Bool("dry-run", false, i18n.T("cli.migrate.flag_dry_run"))
	abandon := fs.Bool("abandon", false, i18n.T("cli.migrate.flag_abandon"))
	rest, err := parseMixed(fs, args)
	if err != nil {
		return err
	}
	s, err := tend.Open()
	if err != nil {
		return err
	}
	r, err := pick(s, first(rest))
	if err != nil {
		return err
	}
	if r.Provider != tend.ProviderClaude {
		return errors.New(i18n.T("cli.migrate.claude_only"))
	}
	ctx := context.Background()
	from, err := migratePeer(ctx, r.Host)
	if err != nil {
		return err
	}
	ref := remote.Ref{Provider: r.Provider, SessionID: r.SessionID}
	if *abandon {
		return migrateAbandon(ctx, from, ref, *to)
	}
	if *to == "" {
		return errors.New(i18n.T("cli.migrate.needs_to"))
	}
	target, err := migratePeer(ctx, *to)
	if err != nil {
		return err
	}
	m, err := remote.StartMigration(ctx, from, target, ref, *move)
	if err != nil {
		return err
	}
	resume := migrateResumeLine(*to, r.SessionID)
	if m.MoveOnly {
		fmt.Fprint(os.Stderr, i18n.F("cli.migrate.move_only", target.Label(), render.WhenFull(m.Last.At.Local())))
		if *dryRun {
			return nil
		}
		res, err := m.Run(ctx, remote.MigrateOptions{Move: true})
		migrateTrashed(from, res)
		return err
	}
	if m.Resumed {
		fmt.Fprint(os.Stderr, i18n.F("cli.migrate.resumed", m.ID))
	}
	d := *dir
	if d == "" {
		if d, err = handoffDir(ctx, m.Handover, r, *to, migrateDirTexts); err != nil {
			return err
		}
	}
	if !pathmap.Abs(d) {
		return i18n.E("cli.handoff.dir_abs", d)
	}
	migratePlan(m, r.Title, *to, d)
	rep, err := m.Check(ctx, d)
	if err != nil {
		return i18n.E("remote.migrate.env_unread", remote.Reason(err))
	}
	fmt.Fprint(os.Stderr, i18n.F("cli.handoff.env", *to, rep.Summary()))
	for _, it := range rep.Items {
		if it.Level != envcheck.LevelHint {
			fmt.Fprintln(os.Stderr, "  "+envcheck.LevelText(it.Level)+": "+it.What)
		}
	}
	if *dryRun {
		if rep.Blocked() {
			return i18n.E("remote.migrate.blocked", rep.Block, *to)
		}
		fmt.Fprint(os.Stderr, i18n.T("cli.migrate.dry_run"))
		return nil
	}
	there := *to
	if target.Name == "" {
		there = ""
	}
	pairs := sessionProjects().DirPairs(r, there)
	res, err := m.Run(ctx, remote.MigrateOptions{Dir: d, Pairs: pairs, Move: *move, Progress: migrateProgress()})
	if term.IsTerminal(os.Stderr.Fd()) {
		fmt.Fprintln(os.Stderr)
	}
	if res.Row != nil {
		fmt.Fprint(os.Stderr, i18n.F("cli.migrate.done", *to, d, res.Files, render.Bytes(res.Bytes), render.Bytes(res.Sent)))
		if len(res.Unmapped) > 0 {
			fmt.Fprint(os.Stderr, i18n.F("cli.migrate.unmapped", len(res.Unmapped)))
		}
		if res.Behind {
			fmt.Fprint(os.Stderr, i18n.F("cli.migrate.behind", *to))
		}
	}
	migrateTrashed(from, res)
	if res.Row != nil {
		fmt.Fprint(os.Stderr, i18n.T("cli.migrate.resume"))
		fmt.Println(resume)
	}
	return err
}

// migratePeer is the machine name, one of the viewer's own (ownPeer).
func migratePeer(ctx context.Context, name string) (remote.Peer, error) {
	return ownPeer(ctx, name, "cli.migrate.not_mine", "cli.migrate.server_old")
}

var migrateDirTexts = dirTexts{"cli.migrate.no_dir", "cli.handoff.dir", "cli.handoff.dirs"}

// migrateResumeLine is the command resuming sid, migrated to the machine name, from here.
func migrateResumeLine(name, sid string) string {
	if name == "" || name == tend.HostLocal {
		return "tend resume " + sid
	}
	return "tend resume " + name + ":" + sid
}

// migratePlan says on stderr what m copies, from where to where, and which of the session's files stay behind (what
// git leaves behind is the environment check's).
func migratePlan(m *remote.Migration, title, to, dir string) {
	fmt.Fprint(os.Stderr, i18n.F("cli.migrate.plan", title, m.From.Label(), to, dir))
	fmt.Fprint(os.Stderr, i18n.F("cli.migrate.files", len(m.Plan.Manifest.Files), render.Bytes(m.Plan.Manifest.Size())))
	if left := m.Plan.Manifest.Left; len(left) > 0 {
		fmt.Fprint(os.Stderr, i18n.F("cli.migrate.left", strings.Join(left, ", ")))
	}
}

// migrateProgress redraws one line on stderr when it is a terminal; nothing otherwise.
func migrateProgress() func(remote.Transfer) {
	if !term.IsTerminal(os.Stderr.Fd()) {
		return nil
	}
	return func(p remote.Transfer) {
		fmt.Fprint(os.Stderr, "\r"+i18n.F("cli.migrate.progress", p.FilesDone, p.Files, render.Bytes(p.BytesDone), render.Bytes(p.Bytes)))
	}
}

func migrateTrashed(from remote.Peer, res remote.Migrated) {
	if res.Trashed > 0 {
		fmt.Fprint(os.Stderr, i18n.F("cli.migrate.trashed", from.Label(), res.Trashed))
	}
}

// migrateAbandon drops the session's unfinished migrations on from (to the machine named to, when given): each
// target that answers drops what it staged.
func migrateAbandon(ctx context.Context, from remote.Peer, ref remote.Ref, to string) error {
	pending, err := remote.PendingMigrations(ctx, from, ref)
	if err != nil {
		return err
	}
	n := 0
	for _, c := range pending {
		if to != "" && c.Peer.Name != to {
			continue
		}
		n++
		var target *remote.Peer
		p, err := copyPeer(ctx, c.Peer)
		if err == nil {
			target = &p
		} else {
			fmt.Fprint(os.Stderr, i18n.F("cli.migrate.abandon_unreached", c.Peer.Name, remote.Reason(err)))
		}
		if err := remote.Abandon(ctx, from, target, ref, c.Migration); err != nil {
			return err
		}
		fmt.Fprint(os.Stderr, i18n.F("cli.migrate.abandoned", c.Migration, c.Peer.Name))
	}
	if n == 0 {
		return errors.New(i18n.T("cli.migrate.none_pending"))
	}
	return nil
}

// copyPeer is the machine at the other end of a migration: this one, else the configured or server's machine of the
// name it was migrated under, while it is still that machine (its endpoint).
func copyPeer(ctx context.Context, ref remote.PeerRef) (remote.Peer, error) {
	if here := herePeer(); ref.Endpoint != "" && here.Hello.Endpoint == ref.Endpoint {
		return here, nil
	}
	p, err := migratePeer(ctx, ref.Name)
	switch {
	case err != nil:
		return remote.Peer{}, &wire.Error{Code: wire.CodeNotFound, Detail: ref.Name}
	case ref.Endpoint != "" && p.Hello.Endpoint != ref.Endpoint:
		return remote.Peer{}, &wire.Error{Code: wire.CodeNotFound, Detail: ref.Name}
	}
	return p, nil
}

// showCopies lists r's migrations under tend show, each done one with how both copies stand (asked of both
// machines now); nothing when it has none or its machine cannot tell.
func showCopies(r *tend.Rec) {
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout)
	defer cancel()
	at, err := migratePeer(ctx, r.Host)
	if err != nil || !at.Has(remote.MCopies) {
		return
	}
	states, err := remote.CheckCopies(ctx, at, remote.Ref{Provider: r.Provider, SessionID: r.SessionID},
		func(p remote.PeerRef) (remote.Peer, error) { return copyPeer(ctx, p) })
	if err != nil || len(states) == 0 {
		return
	}
	fmt.Println()
	fmt.Println(i18n.T("cli.migrate.show_title"))
	now := time.Now()
	for _, s := range states {
		mark := render.Copies([]tend.Copy{{Role: s.Role, State: s.State, Peer: s.Peer.Name, Endpoint: s.Peer.Endpoint, At: s.At}}, now)
		status := s.StatusText()
		switch s.State {
		case tend.CopyPending:
			status = i18n.F("cli.migrate.show_pending", s.Migration)
		case tend.CopyAborted:
			status, mark = i18n.T("cli.migrate.show_aborted"), cmp.Or(s.Peer.Name, s.Peer.Endpoint)
		}
		fmt.Printf("  %s  %s  %s\n", mark, render.WhenFull(s.At.Local()), status)
	}
}
