# Fav Session Manager

**All your Claude Code / Codex sessions in one place: favorite, search, resume in one keystroke.** The command is `fav`.
Every AI coding session on the machine goes into one list; the ones worth keeping get a `/fav` while the context is still fresh;
a week later you find them by *what was done* and pick up in the right directory and Herdr workspace.

[中文说明](README.zh.md)

```
 * Fav Session Manager     [ Favorites 58 ]   Sessions 505   Projects 57   Agents 3
 ╭────────────────────────────────────────────────────────────────────────────────────╮
 │ /  webapp oauth last:7d                                                            │
 ╰────────────────────────────────────────────────────────────────────────────────────╯
 ╭ # project webapp ╮ ╭ # tags all ╮ ╭ > source all ╮ ╭ + status open ╮ ╭ @ 7 days ╮
 favorites 4 · by last activity · o cycles╭───────────────────────────────────────────╮
 Today ────────────────────────────────── │ OAuth callback loses state after the 302  │
 ╭──────────────────────────────────────╮ │ * doing  ·  Codex CLI  ·  today 17:03     │
 │ * OAuth callback loses state (302)   │ │ ────────────────────────────────────────  │
 │   Codex  ·  webapp  ·  14 turns 17:03│ │ Summary                                   │
 │   #oauth #middleware                 │ │ The state cookie is cleared by the auth   │
 ╰──────────────────────────────────────╯ │ middleware before the 302 redirect; fix   │
 ╭──────────────────────────────────────╮ │ is to skip the rewrite on /callback. Next │
 │ * notes-api cursor pagination dupes  │ │ step: add a regression test.              │
 │   Claude  ·  notes-api  ·  9 t 15:03 │ │                                           │
 │   #pagination #cursor                │ │ # project    webapp                       │
 ╰──────────────────────────────────────╯ │ @ branch     feat/oauth                   │
 Yesterday ────────────────────────────── │ ~ directory  ~/dev/webapp                 │
 ╭──────────────────────────────────────╮ │ @ last activity  today 17:03              │
 │ ✓ fav incremental index scan         │ │                                           │
 │   Claude  ·  fav  ·  31 turns  21:38 │ │ Resume target                             │
 │   #index #perf                       │ │ Herdr webapp  ->  new tab  ->  Codex CLI  │
 ╰──────────────────────────────────────╯ │ + provider on PATH                        │
 ╭──────────────────────────────────────╮ │ + directory exists                        │
 │ ✓ shell-init: Ctrl-G opens fav fzf   │ │ + transcript available                    │
 │   Codex  ·  fav  ·  6 turns    20:11 │ │ ! branch is main, favorited on feat/oauth │
 │   #shell #zsh                        │ │                                           │
 ╰──────────────────────────────────────╯ │ ── chat · 40 of 128 · J/K page · \ find ─ │
                                          │ You  ·  17:01  ~ 42                       │
                                          │   still losing state after the redirect   │
                                          │ AI  ·  17:02  ~ 1.2k                      │
                                          │   The middleware rewrites the cookie on   │
                                          │   every request, including /callback ...  │
                                          ╰───────────────────────────────────────────╯
 Enter act │ Space resume │ f unfav │ x done │ a archive │ e edit │ M move │ D del
```

## The problem

A dozen Claude Code / Codex sessions a day, and a week later:

| Before | With fav |
|---|---|
| Auto titles read "continue", "ok", "take a look"; no idea which is which | `/fav` runs while the AI still has the whole conversation: title, summary and tags written once, recognisable a week later |
| You remember "I had it debug OAuth", not the date, project or tool | Keywords search the prompts and summaries in the index; `#tag` `project:` `last:7d` narrow it down |
| Claude lives in `~/.claude/projects`, Codex in `~/.codex/sessions`, and each tool's history only shows the current directory | Every session on the machine in one table, by time or by project, tool-agnostic |
| Found it — now `cd` to the right directory, recall `claude --resume` vs `codex resume`, switch to that Herdr workspace | `Enter`: directory, command and workspace are picked for you; a session that is already running is focused instead |
| Move a project directory and every old session stops resuming; Claude quietly deletes transcripts after 30 days | `M` moves the sessions with the directory; `!` marks the broken ones, `fav fix` / `fav clean` repair or clear them in bulk; `fav pin` keeps the important ones |
| Six agents running, one tab at a time | The Agents page: who is waiting for you, who is working, who has been idle for how long, refreshed every 3 s |

fav is not a chat client, does not replace Claude / Codex and uploads nothing: it is a **local, grep-able personal session index**
plus a resume button that lands in the right place.

## What it does

- **Favorite**: `/fav` inside a session; the AI writes the title / summary / tags in the conversation's language, `fav` itself collects provider, session id, cwd, git branch and Herdr workspace. `/fav` again updates the same record.
- **Every session**: unfavorited ones are listed too — the whole Claude and Codex history, indexed incrementally by reading heads, tails and new bytes only; a multi-hundred-MB session is never read whole.
- **Search**: one query syntax across the TUI, fzf and the CLI: keywords, `#tag`, `project:`, `provider:`, `status:`, `last:7d`, `turns:`.
- **Read**: the right pane shows the session's chat, paged backwards from the end, searchable; you know whether it is the one before opening it.
- **Resume**: Herdr running → a new tab in its workspace; no Herdr → `exec` in this terminal; already running → focus that tab; background session → `claude attach`. Directory, transcript and branch are checked first.
- **Organise**: todo / doing / done / archived, edit titles and tags, group by project.
- **Move and self-heal**: moving a project directory rewrites the sessions' cwd, the Claude project directory and `~/.claude.json`; sessions whose directory or transcript is gone get a `!`, the CLI fixes or clears them in bulk, deletes go to a trash you can restore from.
- **Agents panel**: sessions running right now, merged from three sources (Claude `sessions/*.json`, Codex thread locks, Herdr); works without Herdr.
- **Three front-ends**: TUI (daily use), fzf (SSH / find one in two seconds), CLI (scripts, `--json`). English and Chinese UI; every action has a non-letter key for CJK input methods.

## Quick start

```bash
# macOS / Linux: latest release into ~/.local/bin
curl -fsSL https://github.com/oxsean/fav/releases/latest/download/fav_$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz | tar xz -C ~/.local/bin fav
# or with Go:
go install github.com/oxsean/fav/cmd/fav@latest

fav install-skill      # links the /fav skill into ~/.claude/skills and ~/.codex/skills
fav                    # the first start builds the index in the background; the Sessions tab fills up in seconds
```

Windows: grab `fav_windows_amd64.zip` from [Releases](https://github.com/oxsean/fav/releases/latest) and unzip it somewhere on PATH.

Then:

1. Finish a piece of work in any Claude Code / Codex session and type `/fav`.
2. Next week: `fav`, a few words, `Enter`.

A shell hotkey: add `eval "$(fav shell-init zsh)"` to `.zshrc` (`shell-init bash` for bash) and `Ctrl-G` opens the fzf view from any prompt.
`--key alt-f` changes the key (or pass the shell's own notation), `--ui tui` opens the TUI instead.

Optional: `fzf` (fzf mode), Herdr (resume into workspaces, see who is running, focus tabs),
a Nerd Font (switch icons from ASCII in the settings, or `FAV_ICONS=nerd`). `fav uninstall-skill` removes the skill links.

## Usage

### Favorite: `/fav`

Type `/fav` in a session (or say "favorite this session"). The skill reviews the whole conversation and writes a title (12–40 CJK characters
or 6–14 words: the object and what was done to it), a summary (conclusions and next step, not a play-by-play) and 2–5 tags, then hands them to `fav add`.
Provider, session id, cwd, git and Herdr context are collected by `fav`; the AI never guesses them.

Sessions that were never `/fav`ed are on the Sessions tab too; press `f` to favorite one (the first prompt becomes its title), resume it and `/fav` for a proper summary.

### Find and resume: `fav` (TUI)

```
fav            # TUI by default; FAV_UI=fzf changes the default
fav tui --no-mouse
```

Four tabs, `Tab` / `1`–`4`:

| Tab | Shows |
|---|---|
| Favorites | `/fav`ed sessions, by last activity (`o` cycles the sort) |
| Sessions | every session on the machine (fewer than 3 turns hidden by default, `turns:1` shows all) |
| Projects | grouped by directory: `→` expands, `←` collapses, `→` again shows project info on the right (directory / session count / sources / recent sessions); the group of the directory `fav` was started in opens by itself, scrolled to the top |
| Agents | who is running now: waiting / working / idle for how long, refreshed every 3 s |

A typical flow: `/` to search (`webapp oauth last:7d`) or `;` for the chip row to filter by project / tag / source / status / time;
`↑↓` to the session, the right pane shows its chat (`→` steps through messages, `Enter` opens one in full, `\` searches inside it);
`Enter` opens the action dialog — `Enter` resumes, `t` forces this terminal, `y` copies the resume command, `i` / `c` open the directory in the IDE / VS Code.
On a running session `Enter` focuses its tab; on a background session (`claude --bg`) it attaches. `Space` skips the dialog and resumes.

Organise: `f` / `*` favorite, `x` done, `a` archive, `e` edit title / tags / summary, `s` status filter (open / active / done / archived / all / trash).

Moved a project directory: `M` on a group header in the Projects tab (`M` on a session moves only that one); in the directory picker `Enter` descends,
`M` again selects; the confirmation lists from / to, how many sessions and files, and whether an open Claude / Codex needs a restart; `y` moves.
Originals go to the trash first; a project with a running session is refused.

Delete and trash: `D` moves the session files into `~/.agent/fav/trash/`; the "trash" status filter shows deleted chats, `D` restores; purged after 30 days by default.
A session whose directory or transcript is gone shows a dim red `!` after its title and its dialog offers only move / delete.

CJK input methods swallow lowercase letters: favorite with `*`, `Ctrl-X` done / `Ctrl-A` archive / `Ctrl-E` edit / `Ctrl-Y` copy /
`Ctrl-N/P` up and down / `Ctrl-G` resume; uppercase `D M X` pass through; every dialog action has a button. `?` lists all keys.

Mouse on by default: click tabs, chips, cards, double-click to resume, click an input to place the cursor, wheel, overlay buttons; drag over text to copy it.
`,` opens settings: time format, default tab / sort, short-session threshold, wheel step, icons, mouse, trash retention, IDE, language.

### Find one in two seconds: `fav fzf`

The same three tabs (favorites / sessions / Agents; `Tab` / `Shift-Tab` cycles, `F1`–`F3` jumps), no project view; for SSH, low resources, or when you already know what you want.
Type the query syntax straight into the prompt; filtering is still done by `fav` (the same parser as the TUI); the Agents tab refreshes every 3 s. Needs fzf 0.46+, auto-refresh from 0.73.

| Key | Action |
|---|---|
| `Enter` / `Alt-Enter` | resume (a new Herdr tab if Herdr is running; a running session is focused / attached) / resume in this terminal |
| `Ctrl-X` / `Ctrl-A` / `Alt-F` | done ↔ reopen / archive ↔ unarchive / favorite ↔ unfavorite (toggles, like the TUI's `x` `a` `f`) |
| `Ctrl-E` / `Ctrl-Y` | edit in `$EDITOR` / copy the resume command |
| `Alt-T` / `Alt-P` / `Alt-S` / `Alt-D` | tag / project / status / time pickers, written back into the query (`Ctrl-S` is status too) |
| `Ctrl-L` / `Shift-↑↓` | reload / scroll the preview (the last 40 messages are at the bottom) |

Rule: `Ctrl-letter` = the TUI's letter, `Alt-letter` = a picker. After unfavorite / archive the row stays where it is so the same key undoes it; it disappears on the next keystroke or tab switch.

### Scripts and maintenance: the CLI

```bash
fav list '#notes-api last:7d' --json    # favorites
fav sessions 'webapp oauth' --json      # every session
fav show <id> --json
fav resume <id> --dry-run               # print the command and checks only
fav resume <id> --no-herdr              # resume in this terminal

fav status <id> todo|doing|done  ·  fav done <id>  ·  fav archive|unarchive <id>  ·  fav fav|unfav <id>
fav edit <id>                           # title / tags / summary in $EDITOR
fav pin <id>                            # hard-link the transcript (Claude deletes transcripts after 30 days)
```

Broken sessions (directory moved / transcript gone): `fix` and `clean` share one table, list by default, act only on what you pick:

```bash
fav clean                               # list every unrecoverable session: number, reason, guessed destination
fav clean provider:codex last:30d       # same filters as fav list; a directory argument limits the scope
fav fix 1 3                             # directory moved: move to the guessed destination (prints them first, y/N)
fav fix 2 --to ~/dev/proj               # no guess or a wrong one: say where
fav fix all                             # fix everything with a unique destination, list and skip the rest
fav clean 01a07dcf -y                   # by session-id prefix, -y skips the prompt; goes to the trash, restorable
fav trash --restore 01a07dcf
```

Numbers follow the filter, so reuse the same directory / query. Without a TTY and without `-y` it errors out; any failure exits non-zero.

```bash
fav mv <old dir> <new dir>              # same as the TUI's M
fav rm <id>                             # one session to the trash; every <id> also accepts a session-id prefix
fav trash [--json]                      # list the trash; --purge removes expired entries, --purge --all empties it (asks)
fav doctor [--compact]                  # check data files, dead sessions, trash expiry; --compact rewrites the store
```

## Query syntax

`#tag`, `project:x`, `provider:claude|codex`, `status:open|active|done|archived|trash|all|live`,
`after:2026-09-01`, `before:…`, `last:7d`, `turns:3`, plus plain keywords. All ANDed; CJK matches by substring.
Unarchived by default; keywords also search the prompts in the index, so remembering "I had it do X" is enough. All three front-ends share one parser.

## How it works

**Index.** `internal/index` scans `~/.claude/projects/*/*.jsonl` and `~/.codex/sessions/**/rollout-*.jsonl`, recording per file the session id,
cwd, branch, start time, human turns, title and every prompt (capped at 8KB, search only). Transcripts are append-only, so the index remembers
its offset and only reads what is new; the chat preview reads backwards from the end in chunks, as far as you scroll. `-p` / SDK sessions,
Codex sub-agent threads and sessions with no human message are not listed.

**Favorites.** The skill only understands the conversation and emits JSON; validation, environment capture, storage and idempotency (provider + session id)
live in `fav add`. `records.jsonl` is append-only, last line wins.

**Resume.** Checks first (provider on PATH, directory exists, transcript still there, branch changed?), then routing:
already in a Herdr tab → `herdr tab focus`; Claude background session → `claude attach`; Herdr reachable and a workspace found by name or directory → resume in a new tab,
the TUI stays open; otherwise `cd` to the recorded directory and `exec` in this terminal. A path or workspace that does not match is reported with candidates, never guessed.

**Running sessions.** Claude writes `~/.claude/sessions/<pid>.json` (busy / idle), open Codex threads hold `thread-writer-locks/*.lock`,
Herdr additionally knows the tab and "waiting for you"; non-empty fields are layered. When Claude Code runs out of context it hands the conversation
to a background worker under a new session id and parks the terminal process: fav folds that chain into one session (turns added, the newest id resumes,
the favorite follows) and does not count the parked process as running.

## Data

| File | What |
|---|---|
| `~/.agent/fav/records.jsonl` | favorites, append-only, one per line; grep it, edit it, sync it with git |
| `~/.agent/fav/sessions.jsonl` | index cache, prompts only; delete it and the next start rebuilds it |
| `~/.agent/fav/trash/` | deleted session files moved as-is, `manifest.jsonl` records where they came from |
| `~/.agent/fav/config.json` | written by the settings panel |

Environment: `FAV_HOME` moves the data directory, `FAV_UI=fzf|tui` sets the default front-end, `FAV_ICONS=nerd|ascii` picks icons.
The UI language follows the system (`LANG` etc. starting with zh → Chinese, otherwise English) and can be pinned in settings.

No full chat content is stored and nothing is uploaded; session files are only modified when you explicitly move a directory (the cwd field), and the originals go to the trash first. `fav pin` is a local hard link.

## Development

```bash
go build ./... && go vet ./... && go test ./...
HERDR_LIVE=1 go test ./internal/herdr/   # against a real Herdr: create tab → run → clean up
```
