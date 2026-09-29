# AGENTS.md

Guidance for coding agents (Claude Code, Codex) working in this repository.

## What this is

tend (the repository and Go module keep the name fav) is a Go CLI + bubbletea TUI that indexes every Claude Code / Codex session on this machine and on configured remote hosts, lets the user favorite them (the `/tend` skill), filter and search them, and resume them in the right directory / Herdr workspace. It also runs tasks: agents started for a written brief on this machine or others, followed to their end, over ssh (mode 1) or through `tend-server`, a second program in the same module (mode 2).

- Design of record: `docs/design/` (index: `docs/design/README.md`; which file covers which package: the table under Architecture). Read the files for what you change before changing behaviour, and update them in the same commit. They state only the current design: no history, dates or progress.
- `README.md` is the user-facing summary, `README.zh.md` its translation; keep both in step.
- Code, comments, docs and commit messages are English, except `docs/design/`, which is Chinese; user-visible strings go through i18n with `en` and `zh` texts.

## Workflow

- Commit on `main` as soon as a change passes the gate, without being asked: one commit per logical change, subject `type(scope): summary` plus at most a few bullets stating the current change. Push only when the user says so. Never push tags: a `v*` tag is a release (`release.yml`); `backup/*` tags stay local.
- After every change run `mise run install`: the gate, then `~/.local/bin/tend` stamped with `git describe`, then `tend version`. The user runs that binary.
- Formatting is not in the gate: run `gofmt -l` on the files you changed before committing.
- Parallel coding agents get worktrees made with `git worktree add` from the current branch, outside the repository: the Agent tool's worktree isolation branches from the default branch, and a worktree inside the repository is scanned by `TestEveryKeyTranslated`.
- While `scripts/test-hosts.sh` or `tend hosts install` runs, leave the working tree alone: test-hosts packs it for the remote targets and tests `local` in place, install builds it.
- When `wire.Proto`, an rpc handler or what a node does changes (`internal/node`, `internal/agent`, `internal/proc`: a run on another machine executes that machine's tend): commit, run `tend hosts install <name>` for every host `tend hosts` lists, and report each result. A change to `tend-server` alone (`internal/server`, its web files) goes to the host that serves it: `tend hosts install <name> --server`, then restart the server.
- Test tiers:

| When | Run |
|---|---|
| while changing code | `go test` on the affected packages |
| before every commit | `mise run gate` (it runs the coord tests twice, the second time on SQLite: `TEND_TEST_LOG=sqlite`); read its exit status, never through a pipe |
| high-risk stages, or a full regression the user asks for | `scripts/test-hosts.sh`; after a failure rerun only the failed targets |
| only when the user asks | multi-host end to end; propose it when `internal/remote`, `Proto` or `tend hosts` changed |

  High-risk means: the platform packages (`internal/paths`, `internal/shell`, `internal/filelock`, `internal/fileio`, `internal/pathmap`), input and key handling, the remote protocol, a major dependency upgrade. In the middle of a multi-step change, `go build` / `go vet` only; the affected packages' tests once it is done.

## Commands

```bash
mise run gate                                       # build, vet (darwin, linux, windows), all tests; CI runs the same task
mise run install                                    # gate, then install ~/.local/bin/tend
go test ./internal/ui/tui -run TestName -v          # single test
TEND_DUMP=120x34 go test ./internal/ui/tui -run TestDumpFrame -v   # print a frame; TEND_DUMP_OV=<overlay> opens one
scripts/tui-drive.py down enter dump                # drive tend tui on a fixture dataset in a pty and print screens
TEND_TRACE=1 tend tui                                # event trace in the data directory's trace.log (or TEND_TRACE=<path>)
HERDR_LIVE=1 go test ./internal/herdr -run TestLiveCreateTabAndRun   # inside Herdr only; opens a real tab
```

A hung TUI: on macOS `sample <pid>` prints its stacks; SIGQUIT prints them to the alternate screen, which the exit clears.

`skills/tend/SKILL.md` is symlinked into `~/.claude/skills` and `~/.codex/skills`, so edits are live. If a link is missing: `tend install-skill --from skills/tend` (without `--from` it writes the copy embedded in the binary).

## Test data and manual checks

- Tests use `t.TempDir()` + `TEND_HOME` / `CLAUDE_CONFIG_DIR` / `CODEX_HOME` (`TEND_HOME` unset), never the real `~/.agent/tend`, `~/.claude` or `~/.codex`. A package whose tests reach user state has `func TestMain(m *testing.M) { testkit.Main(m) }` (home, OS config dirs and agent homes in a temp root; failing `herdr` / `claude` / `codex` first on PATH).
- Hand-written transcript lines are compact JSON, as the CLIs write them: the index prefilters lines by byte patterns such as `"type":"user"`. Fixtures: `min_turns` defaults to 3, and `Store.Put` stamps `UpdatedAt` with the current time, so a record that needs a fixed time is written to `records.jsonl` directly.
- Tests never reach the user's clipboard: TUI copies go through `copyText`, which the tests replace. `scripts/tui-drive.py` does not stub it, so a driven copy or drag-select writes the real clipboard.
- Tests that capture stdout or stderr use `captured` (`stdoutOf`, `stderrOf`): a Windows pipe holds a few KB, so writing everything before reading hangs.
- Nodes in tests replace `n.Probe`: a real probe runs the agent CLIs, and the connection it answers on stays open until it returns.
- Tests comparing paths git prints resolve `t.TempDir()` with `filepath.EvalSymlinks` (macOS `/var` is `/private/var`) and skip when git is missing. On Windows a killed process's files close late: tests that read a run's state or remove run directories retry (`clearRuns`).
- `internal/fixture` builds a synthetic machine (transcripts in the real on-disk shapes, favorites, project dirs, native paths). A new transcript shape or session state gets a scenario there, asserted in `fixture_test.go`. `go run ./tools/fixture -o DIR [-tend BIN]` writes it with `tend.sh` / `tend.cmd` launchers that point all three homes into it. The root must not sit in an agent scratch dir (`claude-…` under temp) or every session is hidden.
- Manual checks of the TUI, fzf or CLI run through a fixture launcher with Herdr and the agent CLIs isolated (`HERDR_*` unset; failing `herdr`, `claude` and `codex` first on PATH), never against the user's data; so does a hand-run `tend-server` with nodes (a throwaway `TEND_HOME`, the fake agent). `scripts/tui-drive.py` does both and answers the terminal queries tend waits for.
- `tend show`, `tend preview` and every command that finds a session through `pick()` read the cached index without refreshing it: run `tend sessions` first when checking a fresh transcript by hand.
- The Web UI is embedded in `tend-server`: a JS or CSS change shows once it is rebuilt and restarted.
- Other platforms: `scripts/test-hosts.sh [target…]` (targets `local`, `ssh:HOST`, `docker:HOST:CTR`, `wsl:HOST:DISTRO`, `win:HOST`; default: the gitignored `.test-hosts`) tars the working tree to each target, which runs `mise run test-host` natively (`tools/test-host`: vet, test, fixture dataset, `tend sessions` / `doctor`, a hosts smoke over `tend rpc`). It prints one `RESULT` per target and fails unless all are `vet=ok test=ok smoke=ok`. Never cross-compile for it; logs in `~/.cache/tend-test/logs`.
- Traps when scripting the targets: Windows over ssh runs `cmd` (multi-line PowerShell goes through a copied file and `powershell -File`; in `if exist X a & b` the `& b` belongs to the `if`, so split it and bracket the branch); WSL's non-interactive shell lacks `~/.local/bin` on PATH and its output carries NUL bytes; the Linux container has no `pkill` or `kill` program, its init reaps no children, and a `/proc` search matches the searching shell unless the pattern starts with a `[x]` class; macOS `/bin/sh` `echo` expands `\t` (use `printf '%s'`), and copying over a signed binary gets it `Killed: 9` when run (remove first or rename a `.new`). Driving the TUI on Windows goes through conpty, which repaints the screen and answers OSC 11 itself: compare only cells with text or a background colour.
- Versions live only in `mise.toml`: `[tools]` go for everyone; the `gate` task pins node, the `test-host` task claude, codex, fzf and node, for those tasks only (never top-level, or they shadow the developer's own CLIs). The Web UI's script tests in `internal/server` fail, never skip, where node is missing or is not Node.js.
- Runs end to end over ssh: `scripts/tend-e2e.sh [host…]` (default mba linux wsl win) makes this Mac the coordinator in a throwaway home, points each remote's installed tend at throwaway homes, runs a fake agent on each, stops one, and checks outputs and listed sessions; remotes need the current build (`tend hosts install`).
- Mode 2 end to end: `scripts/tend-e2e-server.sh [host…]` (default mba linux win) runs `tend-server` in the mba container behind a forwarder on mba's tailnet address, nodes dialing in with tokens, this Mac as the client; it checks runs, output, a refused token and a server restart.
- Multi-host end to end: after `test-hosts.sh`, run a fixture launcher on this Mac whose `config.json` lists each target's fixture launcher as that host's `tend` (the container via `docker exec -i`, WSL via `wsl -d <distro> -e`), then `tend hosts check`, `tend sessions host:all` and the TUI.

## Architecture

`internal/index` scans transcripts incrementally into `sessions.jsonl`; `Index.Attach` merges them with the favorites store, reusing the same `tend.Rec` objects across refreshes (the TUI keys state by pointer). One lister, `index.Rows.List`, serves the TUI, `tend sessions` / `list`, the fzf tabs and `tend clean`. `internal/task` never imports `internal/node`: data both use (`Verdict`, `CheckResult`, `Workspace`) lives in `internal/agent`.

| Package | Owns |
|---|---|
| `internal/tend` | `Rec`, `Store` (append-only JSONL, last line per id wins), `Query`, the trash manifest |
| `internal/index` | the session index, `Rows`, trash / restore, `PlanMove` / `Apply` for moving a project directory |
| `internal/capture` | current-session detection, resume commands (`CommandSpec`), paged transcript reading (`Messages`), who is running (`live.go`) |
| `internal/wire` | the protocol: JSON frames over any two-way stream, either end may call, answers out of order, cancel, keepalive |
| `internal/remote` | other machines' sessions over `wire`: methods and types (`proto.go`), answering side (`local.go`), ssh `Client`, `Hosts` cache, `Source` |
| `internal/agent` | provider adapters (claude, codex, fake, command): launch, resume, fork, capabilities |
| `internal/output` | a run's `output.log` read into events: the one reader of claude, codex and plain text, tool families and titles, turns, the timeline's grouping; `agent.OwnReport` tells the run's own reports |
| `internal/tracker` | issue trackers behind one `Tracker` interface: Gitea and GitHub (`gitea.go`), GitLab, a shared REST layer; issues, comments, close, label; webhook checks per kind (`hook.go`); `trackertest` is a fake speaking all three with faults for tests; tend-server only |
| `internal/defs` | agent definitions: Markdown with a YAML front matter (`Parse`, `Format`, `Check`, `Import` on the client only, `Compile` into a profile) |
| `internal/workflow` | workflows: Markdown definitions (`Parse`, `Check`, the embedded built-ins, `Resolve`), a stage's brief from its template, the task's workpad; the stage rules themselves are `task` (`flow.go`) and `coord` (`workflow.go`) |
| `internal/node` | runs on this machine: run directories, the `_run` supervisor, snapshots, `run.*` methods |
| `internal/proc` | detached starts, process trees, liveness, per OS |
| `internal/journal`, `internal/task` | the coordinator's event log and the task / run state folded from it |
| `internal/coord` | the coordinator (whoever holds `coord/lock`): client commands with receipts, dispatch, reconcile, subscribe, socket; who sees and does what (`access.go`: principals, the method table, projects, machine owners and shares; pushes filtered per subscriber, `refetch` when that changes); task trees run by their dependencies (`tree.go`, `flow()` after each commit), agent definitions, notices and the inbox |
| `internal/dial` | the client side of mode 2: nodes, TUIs and CLIs dial a server with a token |
| `internal/server` | mode 2, only in `cmd/tend-server` (`platformcheck` fails when `cmd/tend` depends on it): the coordinator over HTTP / WebSocket (`/node`, `/client`), sign-in (`/auth/*`, browser sessions), `/api/*` for people, credentials and machines, listen-address rule; the tracker sync (`sync.go`: one worker, scans and webhooks only mark issues, write-back reconciles the journal against `tracker_issues`; tokens sealed by `seal.go`); the Web UI (`web/`, embedded: `api.js` speaks the wire protocol, `fold.js` folds pushed envelopes as `task.State.Apply` does (`fold_test.go` checks both on the same envelopes, with node), `team.js` holds the sign-in, project, account and admin pages, `look.js` the viewer's skin, contrast and density and the settings page, `home.js` the home and runs pages, `palette.js` the one action table behind `⌘K`, the keys, the shortcuts page and key hints (`palette_test.go` checks every action has its own key and both names find it), `tree.js` task trees, the inbox, agent definitions and project settings, `app.js` the rest; each keeps its own `zh` / `en` strings; the CSP allows no inline script or `style` attribute) |
| `internal/skin` | skins: semantic colour tokens derived in CIE LCh from a base, an accent and a contrast level, light and dark, held to WCAG ratios; presets; CSS for the Web UI (`/theme/<name>.css`) |
| `internal/fulltext` | message search: text mirror, parallel scan, BM25 |
| `internal/ui/tui` | bubbletea `Model`; key table `keys.go` |
| `internal/ui/fzf` | fzf orchestration only, no business logic |
| `internal/render` | fzf lines, preview, CLI cards, shared text helpers and glyphs |
| `internal/i18n` | `T` / `F` / `E`, `locales/en.json` + `zh.json` |
| `internal/herdr` | exec + JSON wrapper over the `herdr` CLI |
| `cmd/tend` | subcommands; `add` reads the `/tend` skill JSON defined in `skills/tend/SKILL.md` |
| `internal/store` | tend-server's SQLite database (modernc, pure Go, file mode 0600): the coordinator's `EventLog` (`coord/tend.db`), `Team` (users, sign-in identities, admission rules, invitations, hashed credentials, audit), numbered migrations in `migrations/sqlite/`, import / export / check / backup; SQL stays in this package |
| `internal/auth` | tend-server's sign-in providers: GitHub OAuth and OIDC (discovery, PKCE, userinfo, `email_verified` from the ID token when userinfo lacks it) |
| `cmd/tend-server` | mode 2: `--listen` serves the coordinator, `token` manages credentials, `admin` people and admission, `import` / `export` / `backup` / `db check` |

| Changing | Design |
|---|---|
| `internal/tend`, `internal/index`, `internal/fulltext`, `skills/tend` | `docs/design/sessions/favorites.md`, `docs/design/sessions/index-and-search.md` |
| `internal/ui/tui` | `docs/design/sessions/tui.md`; task views `docs/design/tasks/ui.md` |
| `internal/ui/fzf`, `internal/render` | `docs/design/sessions/fzf.md` |
| `internal/capture`, `internal/herdr` | `docs/design/sessions/resume.md`, `docs/design/sessions/external-behaviour.md` |
| `cmd/tend` | `docs/design/sessions/cli-and-config.md`; task and run commands `docs/design/runs/clients.md` |
| `internal/remote`, `tend hosts` | `docs/design/sessions/remote.md`, `docs/design/sessions/migration.md` |
| `internal/wire`, `internal/dial` | `docs/design/runs/wire.md` |
| `internal/journal`, `internal/task`, `internal/coord` | `docs/design/runs/coordinator.md`; the task features they carry in `docs/design/tasks/` |
| `internal/node`, `internal/proc` | `docs/design/runs/node.md`, `docs/design/tasks/execution.md` |
| `internal/agent` | `docs/design/runs/agents.md` |
| `internal/output` | `docs/design/runs/output.md` |
| `internal/defs`, `internal/workflow` | `docs/design/tasks/agent-definitions.md`, `docs/design/tasks/workflows.md`, `docs/design/tasks/planning.md` |
| `internal/server`, `internal/auth`, `cmd/tend-server` | `docs/design/runs/deployment.md`, `docs/design/tasks/team.md`, `docs/design/tasks/storage.md`; the Web UI `docs/design/runs/clients.md`, `docs/design/tasks/ui.md`, `docs/design/tasks/board.md` |
| `internal/tracker` | `docs/design/tasks/trackers.md` |
| `internal/store` | `docs/design/tasks/storage.md` |
| `internal/skin` | `docs/design/tasks/ui.md` |

Goals, terms and settled decisions: `docs/design/sessions/overview.md`, `docs/design/runs/overview.md`, `docs/design/tasks/overview.md`; protocol shapes of the task layer: `docs/design/tasks/protocol.md`; projects: `docs/design/tasks/projects.md`.

Platform rules have one package each: `internal/paths` (this machine's paths), `internal/pathmap` (another machine's paths), `internal/shell` (quoting, the user's shell), `internal/filelock`, `internal/fileio` (atomic writes, `Lines`, file identity `ID`; renames go through `fileio.Rename`, which retries while a Windows reader holds the file), `internal/testkit`. `internal/platformcheck` fails when one of these rules is repeated elsewhere; `/dev/null` appears only in `internal/shell`, generated scripts included (test for a command with `[ -n "$(command -v x)" ]`).

## Rules that are easy to get wrong

- Comments: baseline none. Keep only external formats, magic values and hard constraints (`⚠️ …`). Docs describe only the current design.
- Scanner and transcript code never reads whole files (sessions reach hundreds of MB): read heads, tails, or from a recorded offset.
- State files are rewritten only through `fileio.WriteAtomic` / `WriteFile`; between compactions the store and the index append lines.
- TUI record edits go through `editRec` (`syncStore`, then `Store.Update`); the one exception is `doResume` saving an edited title with `Store.Put`. Background work reads a copy of the record, never the shared row.
- Every transcript read for a record goes through `hosts.Source(r)`. Remote rows (`Rec.Host != ""`) are read-only: a new write path must refuse them (`Store` refuses too).
- Protocol: a frame that changes shape bumps `wire.Proto`; methods are negotiated by `hello.methods`, and a method's params or result only gain fields an older end ignores (a machine sees a new method once `tend hosts install` updated it). A new method needs its name in `methods`, a handler in `local.go`, and, if it reads a transcript, a `Source` method on both `local` and `far`. A field added to an existing node method that an older node would silently ignore needs a node feature: its name in `node.Features`, required through `runFeatures` / `stageFeatures`, so a node without it fails the run as `node_outdated`. Zero fields are left out of frames: decode into a fresh value, never a reused one. `Session` is a whitelist: a field the lists or filters need goes into `SessionOf` and `Session.Rec` (times converted to local). Error codes are stable and never localized.
- i18n: whole sentences with `%s` / `%d`, never fragments joined in code; `coverage_test.go` fails on a missing, orphan or mismatched key. A key appears in non-test code as one whole string literal (a key built by concatenation counts as orphan: use a map of literals). `en` and `zh` carry the same verbs in the same order, never `%[n]s`: reword the sentence instead. Locale files keep insertion order: add a key beside its neighbours, never re-sort. Tests compare user-visible text with `i18n.T` / `F`, never English literals, since the locale follows `LANG`.
- The Web UI's word tables from all its files merge into one (`app.js`): a key two files define silently overrides, so search `internal/server/web` before adding one.
- A new journal event type needs a rule in `sees` (without one it reaches admins only), an entry in `reshapes` when it changes who sees what, and a case in `fold.js` covered by `fold_test`. A command's answer is recomputed by applying its envelope to shallow copies of what it touches (the receipt's `Answer` in `internal/coord/commands.go`): a pointer or map field an event changes in place is deep-copied there.
- Subcommands take positional arguments before flags; parse with `parseWithArgs` (tend) or `parseOne` (tend-server), since `flag` stops at the first positional argument.
- `internal/docscheck`, in the gate, fails when AGENTS.md, the READMEs or the skill name a file, symbol, environment variable, test or subcommand that no longer exists: change them with the code; a backticked word that is not code goes into `notCode`.
- Never print token or credential files (`*.tok`, a `--token-file` target, the agent CLIs' auth files): pass their paths to commands. The repository is public: no real company, client or project names, and no home-directory user names, in code, tests, docs or commits.
- Keys live in one table, `bindings` in `internal/ui/tui/keys.go`; dispatch, footer and button labels, the help page (`helpLayout`), the IME note and the trash guard derive from it, so label texts never carry a key. `keys_test.go` enforces the rules. The full key tables in `README.md` and `README.zh.md` are written by hand: change them with `bindings`.
- Letter semantics: a lowercase letter is a reversible record action with the same meaning on every page; uppercase is only for confirmed heavy actions (`D` `M` `X` `Z`) and vim-style pairs (`N` `G` `J` `K`); starting actions (launch, open a tab, send) have no list key and run from a dialog with Enter; digits only select; `Ctrl`+letter only stands in for an existing action under an IME (never `Ctrl+A`, never `Alt`).
- CJK input methods swallow letters: every lowercase-only list key needs an IME route to a non-letter key. `；` `，` `？` `、` are accepted as `;` `,` `?` `/`, and `Ctrl+S` stands in for `\`.
- Every text input registers a click zone that focuses it and places the cursor via `placeCursor`. Every overlay button is reachable by keyboard (`←` / `→` / `Tab` + Enter) and by click; the button drawn as primary is what Enter does.
- Key names in user-visible text: `Enter` `Esc` `Tab` `Space`, `Ctrl+S` / `Alt+F` / `Shift+Tab`. Buttons and footer hints are `<key> <text>` with the key in the accent colour (`keyedLabel`, `renderFoot`).
- Rendering never exceeds terminal width, and overlays are composited column-wise, so every line is exactly terminal width (snapshot tests enforce it); measure with `fit` / `render.Pad`, never `len()`. `render.Pad` counts escape codes as width, so pad plain text and style it afterwards; measure a styled string with `fit`.
- Charm v2 (`charm.land/…`): lipgloss `Width` / `Height` include border and padding; keys are `msg.String()` of a `tea.KeyPressMsg` (space is `"space"`); a paste is a separate `tea.PasteMsg`; colours are light/dark pairs picked on `tea.BackgroundColorMsg`; inputs are built with `newInput` / `newArea` and drawn with `inputView`. Tests send keys with `press("…")`.
- Tests that put paths into JSON use `testkit.JSONString`; tests spelling POSIX paths call `testkit.PosixOnly`.
- Icons default to ASCII; Nerd Font codepoints are opt-in; never East Asian Ambiguous glyphs. Width math goes through `go-runewidth`.
- Claude background sessions (`kind=bg` in `~/.claude/sessions/<pid>.json`) resume with `claude attach <jobId>`, never `--resume`.
