# tend

**All your Claude Code / Codex sessions in one place — favorite, search, resume in one keystroke — and tasks that agents run on any of your machines.**
Every AI coding session on the machine goes into one list; the ones worth keeping get a `/tend` while the context is still fresh;
a week later you find them by *what was done* and pick up in the right directory and Herdr workspace.
Write a task once, send it to an agent on this machine or another one, follow its output and take its session over when it is done.

[中文说明](README.zh.md)

```
 * tend                    [ Favorites 58 ]   Sessions 505   Projects 57   Agents 3
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
 │ ✓ tend incremental index scan         │ │                                           │
 │   Claude  ·  tend  ·  31 turns  21:38 │ │ Resume target                             │
 │   #index #perf                       │ │ Herdr webapp  ->  new tab  ->  Codex CLI  │
 ╰──────────────────────────────────────╯ │ + provider on PATH                        │
 ╭──────────────────────────────────────╮ │ + directory exists                        │
 │ ✓ shell-init: Ctrl+G opens tend fzf   │ │ + transcript available                    │
 │   Codex  ·  tend  ·  6 turns    20:11 │ │ ! branch is main, favorited on feat/oauth │
 │   #shell #zsh                        │ │                                           │
 ╰──────────────────────────────────────╯ │ ── chat · 40 of 128 · J/K page · \ find ─ │
                                          │ You  ·  17:01  ~ 42                       │
                                          │   still losing state after the redirect   │
                                          │ AI  ·  17:02  ~ 1.2k                      │
                                          │   The middleware rewrites the cookie on   │
                                          │   every request, including /callback ...  │
                                          ╰───────────────────────────────────────────╯
 Enter actions │ / search  > messages │ f/* unfavorite                   Tab view  ? help
```

## The problem

A dozen Claude Code / Codex sessions a day, and a week later:

| Before | With tend |
|---|---|
| Auto titles read "continue", "ok", "take a look"; no idea which is which | `/tend` runs while the AI still has the whole conversation: title, summary and tags written once, recognisable a week later |
| You remember "I had it debug OAuth", not the date, project or tool | Keywords search the prompts and summaries in the index; `#tag` `project:` `last:7d` narrow it down |
| Claude lives in `~/.claude/projects`, Codex in `~/.codex/sessions`, and each tool's history only shows the current directory | Every session on the machine in one table, by time or by project, tool-agnostic |
| Found it — now `cd` to the right directory, recall `claude --resume` vs `codex resume`, switch to that Herdr workspace | `Enter`: directory, command and workspace are picked for you; a session that is already running is focused instead |
| Move a project directory and every old session stops resuming; Claude quietly deletes transcripts after 30 days | `M` moves the sessions with the directory; `!` marks the broken ones, `tend fix` / `tend clean` repair or clear them in bulk; `tend pin` keeps the important ones |
| Six agents running, one tab at a time | The Agents page: who is waiting for you, who is working, who has been idle for how long, refreshed every 3 s |

tend is not a chat client, does not replace Claude / Codex and uploads nothing: it is a **local, grep-able personal session index**
plus a resume button that lands in the right place.

## What it does

- **Favorite**: `/tend` inside a session; the AI writes the title / summary / tags in the conversation's language, `tend` itself collects provider, session id, cwd, git branch and Herdr workspace. `/tend` again updates the same record.
- **Every session**: unfavorited ones are listed too — the whole Claude and Codex history, indexed incrementally by reading heads, tails and new bytes only; a multi-hundred-MB session is never read whole.
- **Search**: one query syntax across the TUI, fzf and the CLI: keywords, `#tag`, `project:`, `provider:`, `status:`, `last:7d`, `turns:`, `file:` (sessions whose AI wrote a matching path).
- **Search keys**: `/` searches sessions (title, summary, project, tags), `>` searches the messages of every session, `\` (or `Ctrl+S`) the messages of the selected session — the same wherever the focus is; `/` typed as `、` by a CJK input method still searches sessions.
- **Search messages**: press `>` (or start the query with `>`) to search inside every message and command of the sessions the rest of the query picks; results are ranked sessions with hit counts and snippets, the right pane opens on the hit the card shows, `n`/`N` walk the hits, `→` lists every hit of the session with its surrounding text and `Enter` opens the full message at the keyword. Chinese needs no word segmentation.
- **Read**: the right pane shows the session's chat, paged backwards from the end, searchable; you know whether it is the one before opening it.
- **Resume**: Herdr running → a new tab in its workspace; no Herdr → `exec` in this terminal; already running → focus that tab; background session → `claude attach`. Directory, transcript and branch are checked first.
- **Desktop apps**: the resume dialog also opens the session in Claude's desktop app or ChatGPT's (Codex); a setting makes the app the default, or only for sessions started there — those cards say `Claude App` / `Codex App`. The button needs the session's working directory to exist and its transcript under the default `~/.claude/projects` / `~/.codex/sessions`, and appears only when the system routes `claude://` / `codex://` to that app (macOS, Windows, and Linux via xdg-mime).
- **Fork and hand off**: the resume dialog's `b` forks the session (`claude --resume … --fork-session` / `codex fork`) — a new session with the same history, the original left as it was. `s` writes a handoff pack (summary, the latest five requests, the last reply, files it changed, `git status`; no tool output) under `~/.agent/tend/handoff/`, shows it for review or editing, then starts a new Claude or Codex session in the same directory whose first message is to read it. CLI: `tend resume --fork <id>`, `tend handoff <id> [--to claude|codex]`.
- **Peek and reply**: `` ` `` (`·` under an IME) on a session running in Herdr shows its terminal (refreshed every second — permission questions show there, not in the transcript), `:` types a reply sent as its next prompt, and `1`–`3` pressed twice answer a numbered question (forgotten when the screen changes or after 5 s).
- **Close idle tabs**: in Agents, `Z` closes every Herdr tab quiet for 4 hours with nothing you have not seen (asks first, Cancel focused).
- **New session here**: `w` (`Ctrl+W`) on a Projects group header or any session opens a new Claude or Codex session (`1` / `2` select, again or Enter starts) in that directory — in a Herdr tab when a workspace is there — after listing what already runs there (↑↓ Enter goes to one of those instead).
- **Organise**: todo / doing / done / archived, edit titles and tags, group by project.
- **Move and self-heal**: moving a project directory rewrites the sessions' cwd, the Claude project directory and `~/.claude.json`; sessions whose directory or transcript is gone get a `!`, the CLI fixes or clears them in bulk, deletes go to a trash you can restore from.
- **Agents panel**: sessions running right now, merged from three sources (Claude `sessions/*.json`, Codex thread locks, Herdr); works without Herdr.
- **Three front-ends**: TUI (daily use), fzf (SSH / find one in two seconds), CLI (scripts, `--json`). English and Chinese UI; every action has a non-letter key for CJK input methods.

## Quick start

```bash
# macOS / Linux: latest release into ~/.local/bin
curl -fsSL https://github.com/oxsean/fav/releases/latest/download/tend_$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz | tar xz -C ~/.local/bin tend
# or with Go:
go install github.com/oxsean/fav/cmd/tend@latest

tend install-skill      # links the /tend skill into ~/.claude/skills and ~/.codex/skills ($CLAUDE_CONFIG_DIR / $CODEX_HOME when set)
tend install-hook       # optional: Claude Code tells tend when it asks you something, so Agents flags permission questions without Herdr (tend uninstall-hook removes it)
tend                    # the first start builds the index in the background; the Sessions tab fills up in seconds
```

Windows: grab `tend_windows_amd64.zip` from [Releases](https://github.com/oxsean/fav/releases/latest) and unzip it somewhere on PATH.

Then:

1. Finish a piece of work in any Claude Code / Codex session and type `/tend`.
2. Next week: `tend`, a few words, `Enter`.

A shell hotkey: add `eval "$(tend shell-init zsh)"` to `.zshrc` (`shell-init bash` for bash) and `Ctrl+G` opens the fzf view from any prompt.
`--key alt-f` changes the key (or pass the shell's own notation), `--ui tui` opens the TUI instead.

Optional: `fzf` (fzf mode), Herdr (resume into workspaces, see who is running, focus tabs),
a Nerd Font (switch icons from ASCII in the settings, or `TEND_ICONS=nerd`). `tend uninstall-skill` removes the skill links.

## Usage

### Favorite: `/tend`

Type `/tend` in a session (or say "favorite this session"). The skill reviews the whole conversation and writes a title (12–40 CJK characters
or 6–14 words: the object and what was done to it), a summary (conclusions and next step, not a play-by-play) and 2–5 tags, then hands them to `tend add`.
Provider, session id, cwd, git and Herdr context are collected by `tend`; the AI never guesses them.

Sessions that were never `/tend`-ed are on the Sessions tab too; press `f` to favorite one (the first prompt becomes its title), resume it and `/tend` for a proper summary. Until then the summary is Claude's own recap (goal · done · next) or Codex's last reply when there is one.

### Find and resume: `tend` (TUI)

```
tend            # TUI by default; TEND_UI=fzf changes the default
tend tui --no-mouse
```

Five tabs, `Tab` / `1`–`5`:

| Tab | Shows |
|---|---|
| Favorites | `/tend`-ed sessions, by last activity (`o` cycles the sort) |
| Sessions | every session on the machine (fewer than 3 turns hidden by default, `turns:1` shows all) |
| Projects | grouped by directory (a session in a git worktree goes under its main checkout, its card says `worktree <branch>`; `tend fix` moves the sessions of a removed worktree there): `→` expands, `←` collapses, `→` again shows project info on the right (directory / session count / sources / recent sessions); the group of the directory `tend` was started in opens by itself, scrolled to the top |
| Agents | who is running now: waiting / working / idle for how long, how long this turn has run, how full the context is, what the AI said last; refreshed every 3 s. Sessions asking you or finished and unseen are flagged (tab `Agents 5 !2`); `.` marks one handled, `H` snoozes it for an hour |
| Tasks | tasks and their runs (see [Tasks](#tasks-agents-on-your-machines)) |

A typical flow: `/` to search (`webapp oauth last:7d`) or `;` for the chip row to filter by project / tag / source / status / time;
`↑↓` to the session, the right pane shows its chat (`→` steps through messages, `Enter` opens one in full, `\` searches inside it);
`Enter` opens the action dialog — `Enter` resumes, `t` forces this terminal, `y` copies the resume command, `p` opens it in the Claude / ChatGPT desktop app, `i` / `c` / `o` open the directory in the IDE / VS Code / Finder (Explorer, the file manager) — grouped in rows: resume, project, record.
On a running session `Enter` focuses its tab; on a background session (`claude --bg`) it attaches; one running in another terminal is not resumed a second time (both would write it). `Space` does one thing: it switches to a session already in a Herdr tab, otherwise it is `Enter` and only opens the dialog — whatever starts a process or a tab is confirmed there. Dialog keys whose meaning differs from the list (`t` `p` `b` `s` `i` `c` `o`) only focus their button; the same key again or `Enter` runs it. In every dialog `Tab` / `Shift+Tab` only move focus across fields and buttons. A running session gets a "Running" row: peek / handled / snooze / close tab.

Organise: `f` / `*` favorite, `x` done, `a` archive, `e` edit title / tags / summary, `s` status filter (open / active / done / archived / all / trash).

Moved a project directory: `M` on a group header in the Projects tab (`M` on a session moves only that one); in the directory picker `Enter` / `→` descends,
the first row ("this directory") or the "Move here" button selects; the confirmation lists from / to, how many sessions and files, and whether an open Claude / Codex needs a restart; `y` moves.
Originals go to the trash first; a project with a running session is refused.

Delete and trash: `D`, confirmed with `y` (focus starts on Cancel), moves the session files into `~/.agent/tend/trash/`; the "trash" status filter shows deleted chats, `D` restores; purged after 30 days by default.
A session whose directory or transcript is gone shows a dim red `!` after its title and its dialog offers only move / delete.

CJK input methods swallow lowercase letters: favorite with `*`, `Ctrl+X` done / `Ctrl+E` edit / `Ctrl+Y` copy /
`Ctrl+N/P` up and down / `Ctrl+G` same as `Space`; uppercase `D M X` pass through; every dialog action has a button. `?` opens help in three pages: the list's keys, search syntax, mouse and IME.

Mouse on by default: click tabs, chips, cards, double-click to resume, click an input to place the cursor, wheel, overlay buttons; drag over text to copy it.
`,` opens settings: time format, default tab / sort, short-session threshold, wheel step, icons, mouse, trash retention, IDE, language.

<details>
<summary>Every key, in full</summary>

`?` in the TUI shows the short version; this is the full description of every key, dialogs included.

**Search**

| Key | What it does |
|---|---|
| `/` `、` | search sessions: title, summary, project, #tag; works from any focus |
| `>` `》` | search the messages of every session (see Query syntax) |
| `Ctrl+S` `\` | search the messages of this session; hits listed on the left |
| `n / N` | next / previous hit |
| `l` `→` | in a message search: list every hit of this session, the right pane follows |
| `o` `Ctrl+O` | switch the sort; in a message search, relevance / newest hit |

**Move**

| Key | What it does |
|---|---|
| `j / k` `↓ / ↑` `Ctrl+N / Ctrl+P` | move up / down; Ctrl+N / Ctrl+P in the search box |
| `PgDn / PgUp` `Ctrl+F / Ctrl+B` `Ctrl+D / Ctrl+U` `g / G` `Home / End` | page / half page / top and bottom; also in the chat |
| `h / l` `← / →` | switch pane; switch chip in the filter row |
| `Tab / Shift+Tab` `1` `2` `3` `4` `5` | switch tab: favorites / sessions / projects / Agents / Tasks |

**Views and filters**

| Key | What it does |
|---|---|
| `;` | enter / leave the filter chip row (arrows move, Enter opens) |
| `t / p / v / d` | tag / project / source / time filter |
| `s` | status filter: open / active / done / archived / all / trash |
| `Enter` | on a projects group header: fold / unfold it |
| `z / - / =` `+` | projects view: toggle fold all, fold all, unfold all; Tasks view: the same for the edit rows in a run's output, each opening on its first hunk (a click opens one) |

**Open and continue**

| Key | What it does |
|---|---|
| `Enter / r` | wide: the action dialog; narrow: the detail; a running session's primary button is switch or attach |
| `Space` `Ctrl+G` | switch to a session already running in a Herdr tab; otherwise the same as Enter (open the dialog); pages down in the chat |
| `w` `Ctrl+W` | start a new session in this project (shows what already runs there) |

**This record**

| Key | What it does |
|---|---|
| `f` `*` | favorite / unfavorite; the record stays, press again to restore |
| `x` `Ctrl+X` | mark done; again: back to doing |
| `a` | archive / unarchive |
| `e` `Ctrl+E` | edit title / tags / summary |
| `M` | move the project directory (see "Moved a project directory" above) |
| `D` | delete into the trash; in the trash it restores |

**Right pane**

| Key | What it does |
|---|---|
| `J / K` `Ctrl+J / Ctrl+K` | move the highlighted message from the list (older / newer); the arrows do it when the right pane has focus |
| `Enter` | the full message and every step after it |
| `y` `Ctrl+Y` | copy the highlighted message |

**Running sessions**

| Key | What it does |
|---|---|
| `` ` `` `·` | peek at an agent's terminal in Herdr and reply to it |
| `.` `。` | mark handled: a running session stays quiet until it writes something new |
| `H` | snooze for an hour |
| `X` | close its Herdr tab (asks first) |
| `Z` | close every Herdr tab idle for hours (Agents; asks first) |

**In the resume dialog**

Keys that start a process, a tab or an app only focus their button; the same key again or Enter runs it. Keys that mean the same as in the list act at once.

| Key | What it does |
|---|---|
| `r` | resume in the terminal (the key when the desktop app comes first) |
| `t` `Ctrl+T` | resume in this terminal, not in Herdr |
| `p` | open it in the Claude / ChatGPT desktop app |
| `y` `Ctrl+Y` | copy cd + the resume command |
| `b` | fork: a new session carrying this one's history |
| `s` | hand off: a new Claude / Codex session reads a summary pack |
| `i / c / o` | open the project directory in the IDE / VS Code / file manager |
| `n` | edit just the title |

**Dialogs**

| Key | What it does |
|---|---|
| `Tab / Shift+Tab` `→ / ←` `l / h` | move focus across fields and buttons |
| `Enter` | press the focused button; the primary one before focus moved |
| `Esc` `q` `n` | close, or back one level; cancels a confirmation |
| `y` | confirm at once in a confirmation |
| `1 / 2` | new session / hand off: pick Claude / Codex, the same key again starts it |
| `e` `Ctrl+E` `y` `Ctrl+Y` | hand off: edit / copy the pack |
| `1` `2` `3` | peek: answer a numbered question; the same digit twice sends it |
| `:` | peek: type a line; Enter sends it as the agent's next prompt |

**Other**

| Key | What it does |
|---|---|
| `,` | settings |
| `?` | open help |
| `Esc` | close the dialog; back to the list; clear filters |
| `q` `Ctrl+C` | quit (Ctrl+C also works in the search box and dialogs) |

</details>

### Find one in two seconds: `tend fzf`

The same three tabs (favorites / sessions / Agents; `Tab` / `Shift+Tab` cycles, `F1`–`F3` jumps), no project view; for SSH, low resources, or when you already know what you want.
Type the query syntax straight into the prompt; filtering is still done by `tend` (the same parser as the TUI); the Agents tab refreshes every 3 s. Needs fzf 0.46+, auto-refresh from 0.73.

| Key | Action |
|---|---|
| `Enter` / `Alt+Enter` | resume (a new Herdr tab if Herdr is running; a running session is focused / attached) / resume in this terminal |
| `Ctrl+X` / `Alt+A` / `Alt+F` | done ↔ reopen / archive ↔ unarchive / favorite ↔ unfavorite (toggles, like the TUI's `x` `a` `f`) |
| `Ctrl+E` / `Ctrl+Y` | edit in `$EDITOR` / copy the resume command |
| `Alt+T` / `Alt+P` / `Alt+S` / `Alt+D` | tag / project / status / time pickers, written back into the query (`Ctrl+S` is status too) |
| `Ctrl+L` / `Shift+↑↓` | reload / scroll the preview (the last 40 messages are at the bottom) |

Rule: `Ctrl+letter` = the TUI's letter, `Alt+letter` = a picker. After unfavorite / archive the row stays where it is so the same key undoes it; it disappears on the next keystroke or tab switch.

### Scripts and maintenance: the CLI

```bash
tend list '#notes-api last:7d' --json    # favorites
tend sessions 'webapp oauth' --json      # every session
tend show <id> --json
tend grep '滚轮 加速 project:tend'      # message search: keywords + filters, ranked sessions with snippets (--json, --limit)
tend today / tend week [query]            # what you worked on, by project: sessions, files the AI wrote, commits; long sessions not yet favorited (--json)
tend open <id>                           # the TUI on that session, right pane focused (sessions the lists hide too)
tend resume <id> --dry-run               # print the command and checks only
tend resume <id> --no-herdr              # resume in this terminal
tend resume <id> --workspace api         # several Herdr workspaces in that directory: pick one
tend resume <id> --app                   # open it in the desktop app (Claude, or ChatGPT for Codex); --terminal overrides the setting

tend status <id> todo|doing|done  ·  tend done <id>  ·  tend archive|unarchive <id>  ·  tend favorite|unfavorite <id>
tend edit <id>                           # title / tags / summary in $EDITOR
tend pin <id>                            # hard-link the transcript (Claude deletes transcripts after 30 days)
```

Broken sessions (directory moved / transcript gone): `fix` and `clean` share one table, list by default, act only on what you pick:

```bash
tend clean                               # list every unrecoverable session: number, reason, guessed destination
tend clean provider:codex last:30d       # same filters as tend list; a directory argument limits the scope
tend fix 1 3                             # directory moved: move to the guessed destination (prints them first, y/N)
tend fix 2 --to ~/dev/proj               # no guess or a wrong one: say where
tend fix all                             # fix everything with a unique destination, list and skip the rest
tend clean 01a07dcf -y                   # by session-id prefix, -y skips the prompt; goes to the trash, restorable
tend trash --restore 01a07dcf
```

Numbers follow the filter, so reuse the same directory / query. Without a TTY and without `-y` it errors out; any failure exits non-zero.

```bash
tend mv <old dir> <new dir>              # same as the TUI's M
tend rm <id>                             # one session to the trash; every <id> also accepts a session-id prefix
tend trash [--json]                      # list the trash; --purge removes expired entries, --purge --all empties it (asks)
tend doctor [--compact]                  # check data files, dead sessions, trash expiry, agents idle for hours, big old transcripts nobody kept; --compact rewrites the store
```

### Other machines: `hosts`

Sessions on other machines show up in the same lists, read over ssh from the tend installed there. Each machine needs an ssh alias in `~/.ssh/config` that logs in with a key (no password prompt), then:

```bash
tend hosts add mba mba --tend /Users/me/.local/bin/tend                          # name, ssh alias, tend's absolute path there
tend hosts add win win-pc --tend 'C:\Users\me\.local\bin\tend.exe'
tend hosts add wsl win-pc --wsl Debian --tend /home/me/.local/bin/tend            # a WSL distro of that Windows machine
tend hosts add box nas --docker dev --tend /usr/local/bin/tend                    # a container there (--docker-cmd podman / a full path)
tend hosts install mba [--dry-run]       # build tend from this checkout for its system and put it there — in the distro or container too — then check the version
tend hosts install win --build-there     # have the host build the pushed commit itself (git and go or mise there), for links too slow for the binary
tend hosts                               # the machines, the tend version each answered with, when each list was last fetched
tend hosts check [name…]                 # connect, versions, system, claude/codex on PATH, a round trip with Chinese text, list and message timings
tend hosts rm <name…> · tend hosts clear [name…]   # remove machines / forget cached lists
```

`add` writes `hosts` in `~/.agent/tend/config.json` and checks the machine (`--no-check` skips it); the first `install` of a machine without tend needs `--os` and `--arch`. Write tend's absolute path: the shell ssh starts there often lacks `~/.local/bin` on its PATH. The remote shell is guessed (cmd for a Windows path or `wsl`) or set with `--shell posix|cmd|powershell`.

```bash
tend sessions host:all                   # every machine; host:mba one of them; no host: this machine only
tend show mba:<id> · tend resume mba:<id> # a remote session: preview and checks come from there, resume runs `ssh -t mba tend resume …`
```

In the TUI the machine chip (`m`) picks this machine, all of them or one; remote rows carry `@name`, the preview and Agents read from their machine, and resume opens `ssh -t` in a new Herdr tab or this terminal. Remote sessions are read-only: favorite, tag, archive, delete and move are done on their own machine. The machines the filter shows are fetched in the background every 30 s (after a failure the interval doubles, up to 5 min), and cached under `~/.agent/tend/hosts/`; a machine that cannot be reached shows its cached rows and "offline since".

## Tasks: agents on your machines

A task is a piece of work written down once: a title, a brief (what the agent is to do), a directory, and by default a
machine and an agent profile. A run is one attempt at it: an agent started on a machine with the brief, followed to its end.
A task has at most one run going at a time; each run keeps its output and the session it created, so you can read it,
resume it, or run the task again elsewhere.

```bash
tend task add "Fix the flaky pager test" --dir ~/dev/webapp --brief-file brief.md --agent claude
tend run start <task> --machine mba --wait   # queue it on mba and follow it until it ends
tend run list · tend run show <run> · tend run logs <run> -f · tend run stop <run> · tend run abandon <run>
tend run continue <run> "use the main branch"   # answer a run that waits, in its own session
tend run answer <run> --allow | --deny | --answer pg   # answer the permission or question a running run waits on
tend run send <run> "also update the changelog"      # a message for a running run, in its current turn
tend task list [--all] · tend task show <id> · tend task edit <id> --title … · tend task done|reopen|cancel <id>
tend agent list · tend machine list [--connect]
tend inbox                                   # runs that need you, on every machine, longest waiting first
tend journal verify · tend journal repair    # check the coordinator's journal; repair cuts a torn or bad last line
```

In the TUI, view `5` lists the tasks: `w` new task, `e` edit, `x` done / reopen, `Enter` the task dialog (run it on a machine
with a profile, stop, abandon, take over), `X` stops the run, `Space` shows the run's conversation in the Sessions view.
The right pane shows the brief, the latest runs and the last run's output. Taking over opens the resume dialog of the run's
session; while the run still drives it, the dialog says to stop the run first.
`o` arranges the tasks as the home (what waits for you, then what runs, with the machines and 7 days of usage below
when the terminal is taller than 24 rows), a list, a tree with subtasks under their parents, or a board with a column
per situation (`h` / `l` move between columns). `:` opens a command palette that finds any list action by its Chinese or
English words; the settings (`,`) pick the skin, an accent (`#rrggbb`) and standard or high contrast, from the same
generator the Web UI uses. From the task dialog, "Plan subtasks" has the project's planner draft them; the draft is
reviewed there (subtasks under their parents, the planner's questions) and created, edited in `$EDITOR` as the JSON
`tend task draft --save` reads, or discarded. "Project settings" edits the task's project as JSON, and the run dialog
edits the chosen agent's definition or starts a new one as Markdown; what the coordinator refuses is kept for the next edit.
`Shift+↓` / `Shift+↑` mark a range of tasks for `x` or one run dialog; "Watch beside" keeps a run's output under the
detail while you look at other tasks. `u` (`Ctrl+Z`) takes back the last favorite, archive, done or reopen for 6 seconds,
and the terminal title counts what waits for you.

**Task trees.** A task can go under another (`--parent`, three levels at most) and come after others (`--after t1,t2`);
`--backlog` keeps it aside until it is started. `tend task start <id>` starts a task and everything under it: each one is
dispatched as soon as what it comes after is done, a task whose run succeeds is marked done, and a parent never runs —
once its subtasks are done it waits for someone to accept it. Every open task says where it stands: running, queued (and
for what: tasks before it, its subtasks, its machine, another run in its directory) or waiting for someone (and why). A task that could not be
dispatched says why and waits; start it again to retry. `tend task move <id> --parent … --after …` changes its place.

**Agents.** Built in: `claude` (headless `claude -p` talking stream-json both ways, or an interactive Claude in a new Herdr
tab when a Herdr workspace holds the directory), `codex` (`codex app-server`) and `fake` (for tests). More go into
`config.json`:

```json
{"agents": [
  {"name": "opus", "provider": "claude", "model": "opus", "permission": "acceptEdits"},
  {"name": "lint", "provider": "command", "command": ["my-agent", "--prompt-file", "{prompt_file}", "--dir", "{dir}"], "machine": "mba"}
]}
```

`command` runs any CLI: `{prompt_file}`, `{model}` and `{dir}` are filled in, the brief never goes on the command line
(`"stdin": true` pipes it instead); `machine` keeps a profile to that one machine. `machines.<name>.slots` (default 2) bounds the runs a machine takes at once; runs in the same
directory wait for each other.

**Agent definitions.** A definition is a Markdown file with a YAML front matter, in the shape of a Claude Code subagent:

```markdown
---
name: careful
description: slow and careful
role: implement
profile: quick          # or provider: claude / codex, model: …
effort: high
permission: acceptEdits
tools: {deny: [WebFetch]}
machines: {prefer: [mba]}
---
Read everything twice. Run the tests before you stop.
```

`tend agent import careful.md` stores it (`import: ~/.claude/agents/foo.md` inside it reuses a Claude Code subagent), and
it is used by name like a profile: its effort and denied tools go to the agent's command line, its text goes ahead of the
brief. `tend agent defs | export | check | rm | share` manage them. Without a server they are files in
`~/.agent/tend/defs/agents/`; on a server a definition is its owner's (or a project's) until it is shared with people,
projects or everyone (`--view` lets them read it too). `tend agent leave <name>` stops using one you do not manage,
however it reached you, and `tend agent transfer <name> --project <id>` gives yours to a project you own. For claude, `hooks` go into the run's settings (the node needs
`node.allow_hooks`), `mcp` names servers from the node's own `node.mcp` (their values never leave that machine) and
`skills` must be installed there; `output` and `budget` are kept but not applied yet.

**Workflows.** A task can go through stages instead of one run: `tend task add … --workflow feature` (or a project's
default workflow). Built in: `feature` (implement → review → accept), `fix` (implement → test → accept) and `docs`
(implement → accept). Each stage takes the project's agent for its role (`implement`, `review`, `test`); a review or test
run ends with `tend run verdict pass|rework|blocked "…"`, and a rework sends the task back to implementing, which goes on
in its own session with what the review said. A run's own reports (`tend run note|ask|verdict|plan`) never wait for
approval, and a codex run in a workspace-write sandbox may write its run directory. A stage with `check: true` runs the project's `hooks.check` (say
`mise run gate`) on the machine after the agent is done — a failure is a rework; the node takes such runs only with
`node.allow_hooks`. After `max_loops` reworks, a blocked verdict or a spent budget the task waits for someone. The last
stage is a human gate: its approver passes it (`tend task gate <id> --pass`), anyone on the task may send it back with
notes (`--rework "…"`). `tend task message <id> "…"` goes into the running run, continues the stage's last run, or is kept
on the task's workpad for the next stage — what every stage's brief carries along. A project defines its own workflows
as Markdown: the stages in the front matter, a `## <stage>` section per stage for its brief (`{{task.brief}}`,
`{{task.acceptance}}`, `{{#rework}}…{{rework.notes}}…{{/rework}}`, `{{workpad}}`). A task keeps the workflow it was given.

**Planning.** `tend task plan <id>` (or **Plan it**) has the project's planner agent read the task — a requirement's
issue text — and the repository, and hand in subtasks with `tend run plan`: titles, briefs, acceptance criteria, what
comes after what, at most two levels, and questions for you. Nothing is made yet: the plan is a draft you edit, answer
(the planner goes on in its session) or drop; **Make the subtasks** (`tend task draft <id> --apply`) puts them in the
backlog under the task, and starting the task runs them by their dependencies. A draft is tied to the revision of the
issue it was made for.

**Branches.** Mark a project's repository `worktrees` and each task works on its own branch, `tend/<task>`, in a
worktree beside the checkout (`<checkout>-wt/<task>`); your own checkout is left alone. The project's `hooks.setup` runs
once a worktree is made, and `hooks.before_run` in it before every run (a failing one fails the run). Whatever the agent leaves uncommitted is committed for it; a review or test runs on a read-only
copy of the branch that is thrown away after (the run says how many files it changed there); a codex reviewer may
write that copy, so it can build and run the tests, while a claude reviewer has no edit tools. Subtasks start from their
parent's branch and run side by side; each one done is merged into its parent's branch before it counts as done, so a
task that comes after another starts with that work in. A merge that conflicts is undone and waits: merge it yourself in
the parent's worktree, then `tend task merge <id>` (or **Merge again**). A top-level task done is **ready to merge**:
merging into `main` is yours. With a `remote` the branches are pushed after every run and fetched before, so stages can
run on different machines; without one a tree stays on the machine that started it. `tend run note --pr <url>` records a
pull request.

**Before a run starts.** `tend run start` first says where and how the run would go: the machine's agent CLI and
version, whether it is logged in, and whether the run waits for the machine, a slot or a directory. A CLI that is missing
or not logged in there would fail the run at once, so it is not dispatched (`--force` dispatches anyway; the node refuses
it too, with that reason). The TUI's and the web page's run dialogs show the same; `tend machine list` has an AGENTS
column.

**When a run needs you.** A background claude or codex run keeps its conversation open: a permission prompt (claude with
`"permission": "default"`, codex approvals) or a question it asks (claude's AskUserQuestion, codex's user input) waits
until you answer it with `tend run answer`, the TUI's task dialog or the web page, and `tend run send` gives it a message
while it works (claude reads it in its current turn, codex steers the turn). The run shows its latest words and what it
has spent so far (tokens, and claude's cost estimate). A background agent is also told how to end on a question: a final
message starting `ASK:`, or `tend run ask "…"` (`tend run note "…"` reports progress; both reach the run's state through
`TEND_RUN_DIR`). Tools denied without asking (by the permission mode) are named. A run that ends like that shows as
*waiting for your reply* or *needs your permission*;
`tend run continue <run> "…"` (the TUI's and the web page's **Reply**) starts a new background run in the same session
with your answer. `tend run continue --session <id> "…"` does the same for any indexed session. A failed run says why:
`cli_missing`, `auth_missing`, `auth`, `quota`, `rate_limit`, `overloaded`, `context_overflow`, `network`,
`session_missing`, `permission_denied`, with what the CLI said and what to do next (`tend run show`). A run silent
for 15 minutes is marked stalled (`node.stall_after`, `"off"`), never stopped. A run in a Herdr tab is marked as asking
while Herdr shows its pane blocked, its transcript ends on a question, or (with `tend install-hook`) Claude's latest hook
event is a prompt. `tend inbox`, the top of the TUI's task list and the web page's **Attention** count gather every run
that needs you, longest waiting first.

To hear about it, set a command: `"notify_command": ["my-notifier"]` runs with one JSON object on stdin
(`event`: `run.waiting`, `run.asked`, `run.permission`, `run.failed` or `run.stalled`, plus run, task, title, machine, agent, state,
reason, detail, ask) whenever a run comes to want someone; `notify_events` narrows the events. Task events,
`task.needs_you` and `task.done`, go out only when `notify_events` names them.

**Who coordinates.** One process at a time keeps the task journal and sends runs out: whichever holds the lock in
`~/.agent/tend/coord/` — the TUI while its Tasks view is used, a `tend task|run …` command for its length, or `tend service`
if you keep one running. Everyone else talks to it over a local socket. Runs do not need it: each run has its own supervisor
process on its machine, which records how the run ends; the next coordinator reads that and catches up.

**Machines, mode 1 (ssh).** This machine plus every configured host (`tend hosts add`, above). The coordinator reaches each
host with `ssh <alias> tend node --stdio`; runs keep going when the connection drops.

**Machines, mode 2 (a server).** A long-running `tend-server` — a program of its own, next to `tend` in each release, or `tend hosts install <host> --server` — holds the journal in a SQLite database (`coord/tend.db`); machines dial in:

```bash
# on the server (a loopback or tailnet address; any other needs --tls-cert/--tls-key, or --plain behind a forwarder)
tend-server token add --node mba          # prints a token once; only its hash is kept
tend-server token add --client laptop
tend-server --listen 100.101.8.10:7788

# on each machine that runs agents: config.json needs "node": {"allow_dirs": ["~/dev"]}
tend node --connect ws://100.101.8.10:7788 --token-file ~/.config/tend/node-token
# or keep it running from login: a LaunchAgent (macOS), a systemd --user unit (Linux) or a scheduled task (Windows)
tend node install-service --connect ws://100.101.8.10:7788 --token-file ~/.config/tend/node-token   # --print shows it first

# on a client: sign in through the browser (a device code you confirm at the printed URL)
tend login http://100.101.8.10:7788   # writes coordinator.token and config.json's coordinator on its own
tend task list   # the CLI and the TUI's Tasks view now talk to the server
```

A node only runs in `allow_dirs`. Unless `node.allow_bypass` is set, it runs `command` profiles only as its own
`config.json` defines them, takes claude / codex `args` only from its own profiles, allows the permission modes
`default` / `manual` / `acceptEdits` / `plan` / `dontAsk` (claude; not `auto`) and `read-only` / `workspace-write` (codex), and refuses a command line with a
known bypass flag. With `node.allow_profiles` it runs just those names, each as its own config defines it (define them
there, with the permission they need). `tend-server token rm <name>` revokes a token and drops its connections. A node token is bound to the first machine that connects with it; another machine is refused until `tend-server token rebind <name>`.

**People.** The server is for a team. People sign in with GitHub or any OIDC provider (Gitea, GitLab, Keycloak, Google)
listed in the server's `config.json`:

```json
{"server": {"public_url": "https://tend.example", "logins": [
  {"name": "gitea", "kind": "oidc", "display": "Gitea", "issuer": "https://git.example", "client_id": "…", "client_secret_file": "/etc/tend/gitea-secret"},
  {"name": "github", "kind": "github", "client_id": "…", "client_secret_file": "/etc/tend/github-secret"}]}}
```

The provider's callback is `<public_url>/auth/<name>/callback`. Nobody joins on their own: `tend-server admin add
ann@corp.example --role admin` makes the first admin, and after that admins add sign-in rules on the Team page or with
`tend-server admin add <email | domain | provider:username>` (a verified email, any verified email of a domain, or an
account), or send a one-time invitation link (`tend-server admin invite`, or the Team page; it lasts 3 days), which can also make
the invitee a participant or reader of a project (`--project`, `--access`).
`tend-server admin disable <user>` signs someone out everywhere at once.

Tasks belong to projects. An admin creates a project; its owner adds members as participants (create, dispatch, answer,
send) or readers (look only). Someone outside a project sees nothing of it, not even that it exists; a task outside any
project is its creator's. Machines are private too: whoever adds a machine owns it (for now a server admin gives its node token:
`tend-server token add --node <name> --owner <user>`), and only they dispatch to it until they share it with people or projects. Runs on a machine use
its owner's claude / codex login, git identity and files — share a machine set up for that (its own OS user or a
container), not your laptop. A run's permission requests are for the machine's owner and whoever dispatched it; a share
can let everyone it opens to approve them too.

A project's owner also sets what its tasks run with: a context put ahead of every brief, its repositories and where each
is on each machine (a task without a directory uses that), the default agent and machine, and hooks. A task has an owner
and an approver; a task that comes to wait for someone reaches its owner, its approver when it is to be accepted, and whoever
dispatched the run it is about — in their **Needs you** list on the web page, as a browser notification while the page is
open, and at a personal webhook (Account page; a JSON POST with a `text` field for ntfy, Slack and the like, with a link to
the task when `public_url` is set). The server posts only to public addresses; a webhook, or a tracker, on the team's own
network needs its prefix in `server.egress_allow` (`["10.0.0.0/8"]`). An admin's **Hand over and disable** on the Team page gives a leaving member's projects,
tasks and definitions to others and ends their credentials; their machines retire, and runs still open on them wait in
the admins' **Needs you**.

**Issues.** A project can follow a Gitea, GitHub or GitLab repository (Projects page, **Issue sync**): give its address
(`https://github.com` for GitHub), the repository (`owner/name`, with subgroups on GitLab) and a bot account's token (kept encrypted on the server, with the key in `server.key` or
`TEND_SERVER_KEY`). Issues with the label (`tend` by default), or optionally assigned to a member, become requirements of
the project, owned by the assignee when they signed in with that tracker. tend keeps one progress comment on each issue and
closes it once the requirement is done; when the requirement is reopened or its completion undone, tend reopens an issue
it closed itself (never one closed outside tend). When an issue changes, its requirement waits until someone takes the new revision
or keeps the current scope; when it is closed outside tend, someone decides whether to go on. The server polls (60 s by
default); a webhook to `<public_url>/hooks/<id>` with the secret shown at binding time (GitHub: content type
`application/json`; GitLab: as the secret token) makes it quicker. A refused token
stops the binding and tells the project's owner and the admins; a rate limit pauses it. The project's owner and the
admins see on each requirement how its issue syncs: when it last synced, and whether a sync is pending or failed, with
the error and the next try.

**Web UI.** The server also serves a page at its own address (`http://100.101.8.10:7788/`). Sign in with a provider, or
with a token; the browser session lasts 30 days, and signing out or revoking it ends it. The page lists tasks and their
runs; creates, edits and dispatches tasks; previews a dispatch; follows a run's output and conversation; shows why a run
ended or what it asks and takes a reply; stops or abandons runs; marks tasks done, reopens or cancels them; shows task
trees and starts them; lists what needs you; edits and shares agent definitions and project settings; shows the
machines (state, slots, agent CLIs, queue and the day's runs), who owns them and whom they are shared with; adds a
machine and shows its node token once, and moves or revokes a node token; manages projects and members; makes personal tokens for the CLI
and TUI on the Account page, or confirms one from `tend login` on a terminal-authorization page (the code, the client's
name, source address and time, Allow or Deny); and, for admins, users, admission rules, invitations and the audit log. It
follows the
journal live and reconnects on its own. **Home** puts what waits for you first, with the question or tool request
right there to answer, allow or deny, then what runs and on which machines, each project's progress with links into the
board, 7 days of tokens and cost, and the latest sessions. Tasks show as a list or as a board by where they stand, filtered by status,
machine, project, stage and run; the view, filters, selected task and tab are in the address, so a reload or a shared
link shows the same thing. `⌘K` (`Ctrl+K`) opens a command palette that finds any action or task by its Chinese
or English name; every action also has a key (`?` lists them: `n` new task, `g h` home, `g b` board, `d` dispatch, …).
**Needs you** says why each item is yours (you own it, accept it or dispatched it), filters by kind and role, and
answers in place: `1` allows, `2` denies with an optional reason, a digit picks an option, or give your own words.
**Runs** (`g r`) lists every run by state and machine with a preview of its latest output. A task's detail shows its
workflow budget against what its runs spent; an agent definition shows where it is used and the command a run of it
starts, and imports or exports as Markdown; project settings check each repository directory on its machine; a tracker
binding's **Sync log** lists its issues and previews the progress comment; the Team page lists pending invitations to
revoke, and a handover lists what goes where before it runs.
**Settings** picks the theme (light, dark or the system's), a skin (tend, Forest,
Ember, Graphite) with its own accent if you like, standard or high contrast, and one of three densities, kept in the
browser; skins are made on the server (`/theme/<name>.css`) and keep text at 4.5:1 (7:1 in high contrast) whatever the
accent.
On a phone, over HTTPS, the page installs to the home screen (the Account page says how: Share and Add to Home Screen
in Safari, or Install app in the Android browser's menu); the installed page signs in from another device, where you
open its link and allow it. With Pushes to this device on in the Account page, whatever needs you and is left on the
page a while (30 seconds for a permission, 60 for the rest) is pushed to that phone or computer, even with the page
closed, and what was not pushed yet still goes after a server restart. Each person keeps up to ten such devices, at the
browsers' own push services (`server.push_services` names others). On an address that is not HTTPS the Account page says the page cannot install or receive
pushes: put the server behind tailscale serve, or give it a certificate with `--tls-cert/--tls-key`.

**The server's database.** `tend-server import` moves a mode 1 journal (`coord/events.jsonl`) into the database, with
the server stopped; the server refuses to start while that journal holds events and no database exists. Beside a
running server, `tend-server db check` checks the database, `tend-server export -o f` writes it as a journal that
`tend journal verify` reads, and `tend-server backup [dir]` copies it with `coord/id` and the config into a new
directory. People, their sign-in accounts and credentials (only hashes) and the audit log are in the same database.

A client token or a signed-in browser can run agents on every machine shared with its user, within that node's limits:
keep the server inside a tailnet, or put it behind TLS (`--tls-cert/--tls-key`, or `--plain` behind a TLS proxy).
A proxy that is not on the server's host (one in front of a container) goes in `server.trusted_proxies` (`["172.16.0.0/12"]`),
so sign-in limits count each person's own address.

## Query syntax

`#tag`, `project:x`, `provider:claude|codex`, `host:all|local|<name>` (other machines, see above), `status:open|active|done|archived|trash|all|live|agent`,
`after:2026-09-01`, `before:…` (when the session started), `last:7d` / `last:2026-09-01` (active since — a session started last week and used today counts), `turns:3`, `file:internal/index` (the AI wrote a path containing it), plus plain keywords. The time box in the filter row also takes `09-01`, `09-01..09-15`, `..09-15`, `7d`. All ANDed; CJK matches by substring.
Starting the query with `>` (or `》`) searches message text instead: keywords are looked up in every message and tool command, the filter tokens only pick the
sessions (default `status:all turns:0`). Every keyword must occur somewhere in the session; a keyword matches when 60% of its terms do (Chinese is cut into
character pairs, so word order inside a Chinese keyword does not matter; quote a keyword — `"…"`, `“…”` or `「…」` — to require it verbatim; `a|b` matches either, `-x` drops messages holding x, `who:me`, `who:ai` or `who:tool` keep one speaker; a misspelt English word the sessions barely use also searches the known words one letter away, and the title says so); ranking is BM25 with bonuses for keywords close together, newer messages, what you said yourself (tool commands, tool output and Claude's context recaps count less) and sessions whose title, summary or tags hold the keywords; sessions with one message holding every keyword come first, `o` switches to the newest hit first.
Unarchived by default; keywords also search the prompts in the index, so remembering "I had it do X" is enough. All three front-ends share one parser. The same table is in the TUI (`?` → Search syntax), under a bare `>` in the search box, and in `tend grep --help`.

## How it works

**Index.** `internal/index` scans `~/.claude/projects/*/*.jsonl` and `~/.codex/sessions/**/rollout-*.jsonl`, recording per file the session id,
cwd, branch, start time, human turns, title and every prompt (capped at 8KB, search only). Transcripts are append-only, so the index remembers
its offset and only reads what is new; the chat preview reads backwards from the end in chunks, as far as you scroll. `-p` / SDK sessions,
Codex sub-agent threads and sessions with no human message are not listed.

**Message text.** `internal/fulltext` keeps a plain-text copy of the prose and tool commands of every transcript under `~/.agent/tend/text/`
(one TSV per transcript: offset, role, time, text), updated incrementally after every index refresh like the index itself; tool output is not kept.
A search streams the candidate files in parallel, so there is no inverted index to maintain; a few hundred MB are scanned in well under a second.

**Favorites.** The skill only understands the conversation and emits JSON; validation, environment capture, storage and idempotency (provider + session id)
live in `tend add`. `records.jsonl` is append-only, last line wins.

**Resume.** Checks first (provider on PATH, directory exists, transcript still there, branch changed?), then routing:
already in a Herdr tab → `herdr tab focus`; Claude background session → `claude attach`; Herdr reachable and a workspace found by name or directory → resume in a new tab,
the TUI stays open; otherwise `cd` to the recorded directory and `exec` in this terminal. A path or workspace that does not match is reported with candidates, never guessed.

**Running sessions.** Claude writes `~/.claude/sessions/<pid>.json` (busy / idle), open Codex threads hold `thread-writer-locks/*.lock`,
Herdr additionally knows the tab and "waiting for you"; non-empty fields are layered. When Claude Code runs out of context it hands the conversation
to a background worker under a new session id and parks the terminal process: tend folds that chain into one session (turns added, the newest id resumes,
the favorite follows) and does not count the parked process as running.

## Data

| File | What |
|---|---|
| `~/.agent/tend/records.jsonl` | favorites, append-only, one per line; grep it, edit it, sync it with git |
| `~/.agent/tend/sessions.jsonl` | index cache, prompts only; delete it and the next start rebuilds it |
| `~/.agent/tend/text/` | message text for `>` search, one file per transcript, plus `vocab.json` (English words seen, for spelling fixes); delete it and it is rebuilt |
| `~/.agent/tend/trash/` | deleted session files moved as-is, `manifest.jsonl` records where they came from |
| `~/.agent/tend/config.json` | written by the settings panel |
| `~/.agent/tend/coord/` | the coordinator's journal of tasks and runs (`events.jsonl`, one checksummed line per change) |
| `~/.agent/tend/node/runs/` | one directory per run on this machine: its frozen command, state, output log; removed a week after it ended |
| `<server home>/coord/tend.db` | `tend-server` only: the journal, people, sign-in accounts, credential hashes and the audit log (SQLite) |
| `~/.agent/tend/hosts/` | the last list fetched from each other machine (list fields: titles, summaries, tags, paths; no messages), ssh connection sockets |

Environment: `TEND_HOME` (or `TEND_HOME`) moves the data directory, `TEND_UI=fzf|tui` sets the default front-end, `TEND_ICONS=nerd|ascii` picks icons, `TEND_TRACE=1` logs a timeline of keys, wheel and background events to `~/.agent/tend/trace.log` (for reporting a slow or stuck UI).
The UI language follows the system (`LANG` etc. starting with zh → Chinese, otherwise English) and can be pinned in settings.

Chat text is kept only in the local `text/` copy for search, and nothing is uploaded; session files are only modified when you explicitly move a directory (the cwd field), and the originals go to the trash first. `tend pin` is a local hard link.

## Development

```bash
go build ./... && go vet ./... && go test ./...
HERDR_LIVE=1 go test ./internal/herdr/   # against a real Herdr: create tab → run → clean up
go run ./tools/fixture -o ~/tend-demo     # a synthetic machine (Claude + Codex sessions, favorites); ~/tend-demo/tend.sh tui runs tend on it
scripts/test-hosts.sh ssh:host wsl:host:Debian win:host docker:host:ctr   # sync the working tree, build and test natively on each
```
