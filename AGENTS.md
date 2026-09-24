# AGENTS.md

Guidance for coding agents (Claude Code, Codex) working in this repository.

## What this is

tend (binary `tend`, `fav` links to it; the repository and Go module keep the name fav) is a Go CLI + bubbletea TUI that indexes every Claude Code / Codex session on this machine and on configured remote hosts, lets the user favorite them (the `/fav` skill), filter and search them, and resume them in the right directory / Herdr workspace. It also runs tasks: agents started for a written brief on this machine or others, followed to their end, over ssh (mode 1) or through `tend server` (mode 2).

- Design of record: the owner's local note titled 「fav技术方案」, found with `rg -l fav技术方案 ~/Documents/notes`. It holds the data model, query syntax, keys, resume orchestration, config, the remote protocol and each module's mechanics. Read the relevant section before changing behaviour; update it in the same task.
- `README.md` is the user-facing summary, `README.zh.md` its translation; keep both in step.
- Code, comments, docs and commit messages are English; user-visible strings go through i18n with `en` and `zh` texts.

## Workflow

- Commit on `main` as soon as a change passes the gate, without being asked: one commit per logical change, subject `type(scope): summary` plus at most a few bullets stating the current change. Push only when the user says so. Never push tags: a `v*` tag is a release (`release.yml`); `backup/*` tags stay local.
- After every change run `mise run install`: the gate, then `~/.local/bin/tend` stamped with `git describe` (with `fav` linked to it), then `tend version`. The user runs that binary.
- When `wire.Proto` or an rpc handler changes: commit, run `tend hosts install <name>` for every host `tend hosts` lists, and report each result.
- Test tiers:

| When | Run |
|---|---|
| while changing code | `go test` on the affected packages |
| before every commit | `mise run gate`; read its exit status, never through a pipe |
| high-risk stages, or a full regression the user asks for | `scripts/test-hosts.sh` |
| only when the user asks | multi-host end to end; propose it when `internal/remote`, `Proto` or `tend hosts` changed |

## Commands

```bash
mise run gate                                       # build, vet (darwin, linux, windows), all tests; CI runs the same task
mise run install                                    # gate, then install ~/.local/bin/tend and link fav
go test ./internal/ui/tui -run TestName -v          # single test
FAV_DUMP=120x34 go test ./internal/ui/tui -run TestDumpFrame -v   # print a frame; FAV_DUMP_OV=<overlay> opens one
scripts/tui-drive.py down enter dump                # drive tend tui on a fixture dataset in a pty and print screens
FAV_TRACE=1 tend tui                                # event trace in the data directory's trace.log (or FAV_TRACE=<path>)
HERDR_LIVE=1 go test ./internal/herdr -run TestLiveCreateTabAndRun   # inside Herdr only; opens a real tab
```

`skills/fav/SKILL.md` is symlinked into `~/.claude/skills` and `~/.codex/skills`, so edits are live. If a link is missing: `tend install-skill --from skills/fav` (without `--from` it writes the copy embedded in the binary).

## Test data and manual checks

- Tests use `t.TempDir()` + `FAV_HOME` / `CLAUDE_CONFIG_DIR` / `CODEX_HOME` (`TEND_HOME` unset), never the real `~/.agent/tend`, `~/.claude` or `~/.codex`. A package whose tests reach user state has `func TestMain(m *testing.M) { testkit.Main(m) }` (home, OS config dirs and agent homes in a temp root; failing `herdr` / `claude` / `codex` first on PATH).
- `internal/fixture` builds a synthetic machine (transcripts in the real on-disk shapes, favorites, project dirs, native paths). A new transcript shape or session state gets a scenario there, asserted in `fixture_test.go`. `go run ./tools/fixture -o DIR [-fav BIN]` writes it with `fav.sh` / `fav.cmd` launchers that point all three homes into it. The root must not sit in an agent scratch dir (`claude-…` under temp) or every session is hidden.
- Manual checks of the TUI, fzf or CLI run through a fixture launcher with Herdr isolated (`HERDR_*` unset, a failing `herdr` first on PATH), never against the user's data. `scripts/tui-drive.py` does both and answers the terminal queries tend waits for.
- Other platforms: `scripts/test-hosts.sh [target…]` (targets `local`, `ssh:HOST`, `docker:HOST:CTR`, `wsl:HOST:DISTRO`, `win:HOST`; default: the gitignored `.test-hosts`) tars the working tree to each target, which runs `mise run test-host` natively (`tools/test-host`: vet, test, fixture dataset, `fav sessions` / `doctor`, a hosts smoke over `fav rpc`). It prints one `RESULT` per target and fails unless all are `vet=ok test=ok smoke=ok`. Never cross-compile for it; logs in `~/.cache/fav-test/logs`.
- Versions live only in `mise.toml`: `[tools]` go for everyone; the `test-host` task pins claude, codex and fzf for test targets only (never top-level, or they shadow the developer's own CLIs).
- Runs end to end over ssh: `scripts/tend-e2e.sh [host…]` (default mba linux wsl win) makes this Mac the coordinator in a throwaway home, points each remote's installed tend at throwaway homes, runs a fake agent on each, stops one, and checks outputs and listed sessions; remotes need the current build (`tend hosts install`).
- Mode 2 end to end: `scripts/tend-e2e-server.sh [host…]` (default mba linux win) runs `tend server` in the mba container behind a forwarder on mba's tailnet address, nodes dialing in with tokens, this Mac as the client; it checks runs, output, a refused token and a server restart.
- Multi-host end to end: after `test-hosts.sh`, run a fixture launcher on this Mac whose `config.json` lists each target's fixture launcher as that host's `fav` (the container via `docker exec -i`, WSL via `wsl -d <distro> -e`), then `tend hosts check`, `tend sessions host:all` and the TUI.

## Architecture

`internal/index` scans transcripts incrementally into `sessions.jsonl`; `Index.Attach` merges them with the favorites store, reusing the same `fav.Rec` objects across refreshes (the TUI keys state by pointer). One lister, `index.Rows.List`, serves the TUI, `tend sessions` / `list`, the fzf tabs and `tend clean`.

| Package | Owns |
|---|---|
| `internal/fav` | `Rec`, `Store` (append-only JSONL, last line per id wins), `Query`, the trash manifest |
| `internal/index` | the session index, `Rows`, trash / restore, `PlanMove` / `Apply` for moving a project directory |
| `internal/capture` | current-session detection, resume commands (`CommandSpec`), paged transcript reading (`Messages`), who is running (`live.go`) |
| `internal/wire` | protocol v2: JSON frames over any two-way stream, either end may call, answers out of order, cancel, keepalive |
| `internal/remote` | other machines' sessions over `wire`: methods and types (`proto.go`), answering side (`local.go`), ssh `Client`, `Hosts` cache, `Source` |
| `internal/agent` | provider adapters (claude, codex, fake, command): launch, resume, fork, capabilities |
| `internal/node` | runs on this machine: run directories, the `_run` supervisor, snapshots, `run.*` methods |
| `internal/proc` | detached starts, process trees, liveness, per OS |
| `internal/journal`, `internal/task` | the coordinator's event log and the task / run state folded from it |
| `internal/coord` | the coordinator (whoever holds `coord/lock`): client commands with receipts, dispatch, reconcile, subscribe, socket |
| `internal/server` | mode 2: the coordinator over HTTP / WebSocket (`/node`, `/client`), hashed tokens, listen-address rule, dialing |
| `internal/fulltext` | message search: text mirror, parallel scan, BM25 |
| `internal/ui/tui` | bubbletea `Model`; key table `keys.go` |
| `internal/ui/fzf` | fzf orchestration only, no business logic |
| `internal/render` | fzf lines, preview, CLI cards, shared text helpers and glyphs |
| `internal/i18n` | `T` / `F` / `E`, `locales/en.json` + `zh.json` |
| `internal/herdr` | exec + JSON wrapper over the `herdr` CLI |
| `cmd/fav` | subcommands; `add` reads the `/fav` skill JSON defined in `skills/fav/SKILL.md` |

Platform rules have one package each: `internal/paths` (this machine's paths), `internal/pathmap` (another machine's paths), `internal/shell` (quoting, the user's shell), `internal/filelock`, `internal/fileio` (atomic writes, `Lines`, file identity `ID`), `internal/testkit`. `internal/platformcheck` fails when one of these rules is repeated elsewhere.

## Rules that are easy to get wrong

- Comments: baseline none. Keep only external formats, magic values and hard constraints (`⚠️ …`). Docs describe only the current design.
- Scanner and transcript code never reads whole files (sessions reach hundreds of MB): read heads, tails, or from a recorded offset.
- State files are rewritten only through `fileio.WriteAtomic` / `WriteFile`; between compactions the store and the index append lines.
- TUI record edits go through `editRec` (`syncStore`, then `Store.Update`); the one exception is `doResume` saving an edited title with `Store.Put`. Background work reads a copy of the record, never the shared row.
- Every transcript read for a record goes through `hosts.Source(r)`. Remote rows (`Rec.Host != ""`) are read-only: a new write path must refuse them (`Store` refuses too).
- Protocol: a frame or a method's request or result that changes shape bumps `wire.Proto`. A new method needs its name in `methods`, a handler in `local.go`, and, if it reads a transcript, a `Source` method on both `local` and `far`. `Session` is a whitelist: a field the lists or filters need goes into `SessionOf` and `Session.Rec` (times converted to local). Error codes are stable and never localized.
- i18n: whole sentences with `%s` / `%d`, never fragments joined in code; `coverage_test.go` fails on a missing, orphan or mismatched key.
- Keys live in one table, `bindings` in `internal/ui/tui/keys.go`; dispatch, footer and button labels, the help page (`helpLayout`), the IME note and the trash guard derive from it, so label texts never carry a key. `keys_test.go` enforces the rules.
- Letter semantics: a lowercase letter is a reversible record action with the same meaning on every page; uppercase is only for confirmed heavy actions (`D` `M` `X` `Z`) and vim-style pairs (`N` `G` `J` `K`); starting actions (launch, open a tab, send) have no list key and run from a dialog with Enter; digits only select; `Ctrl`+letter only stands in for an existing action under an IME (never `Ctrl+A`, never `Alt`).
- CJK input methods swallow letters: every lowercase-only list key needs an IME route to a non-letter key. `；` `，` `？` `、` are accepted as `;` `,` `?` `/`, and `Ctrl+S` stands in for `\`.
- Every text input registers a click zone that focuses it and places the cursor via `placeCursor`. Every overlay button is reachable by keyboard (`←` / `→` / `Tab` + Enter) and by click; the button drawn as primary is what Enter does.
- Key names in user-visible text: `Enter` `Esc` `Tab` `Space`, `Ctrl+S` / `Alt+F` / `Shift+Tab`. Buttons and footer hints are `<key> <text>` with the key in the accent colour (`keyedLabel`, `renderFoot`).
- Rendering never exceeds terminal width, and overlays are composited column-wise, so every line is exactly terminal width (snapshot tests enforce it); measure with `fit` / `render.Pad`, never `len()`.
- Charm v2 (`charm.land/…`): lipgloss `Width` / `Height` include border and padding; keys are `msg.String()` of a `tea.KeyPressMsg` (space is `"space"`); a paste is a separate `tea.PasteMsg`; colours are light/dark pairs picked on `tea.BackgroundColorMsg`; inputs are built with `newInput` / `newArea` and drawn with `inputView`. Tests send keys with `press("…")`.
- Tests that put paths into JSON use `testkit.JSONString`; tests spelling POSIX paths call `testkit.PosixOnly`.
- Icons default to ASCII; Nerd Font codepoints are opt-in; never East Asian Ambiguous glyphs. Width math goes through `go-runewidth`.
- Claude background sessions (`kind=bg` in `~/.claude/sessions/<pid>.json`) resume with `claude attach <jobId>`, never `--resume`.
