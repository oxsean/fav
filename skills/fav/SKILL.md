---
name: fav
description: Save the current Claude Code or Codex session into fav (the local session index) with a recognizable title, summary and tags. Use when the user says /fav, "favorite this session", "save this session", "bookmark this", 收藏这个会话, 存一下这个 session, 把这次排查记下来.
---

# Fav Session Manager — favorite the current session

While the context is still complete, turn what this session actually accomplished into a structured record and hand it to `fav add`. Do it directly; do not ask for confirmation.

## Steps

1. Review the whole conversation: what problem it really solved, where it stands, what comes next.
2. Write the JSON below in the language the conversation is in (Chinese conversation → Chinese title/summary; English → English). Tags stay lowercase kebab-case regardless.
3. Pipe it to `fav add` with a quoted heredoc (single quotes around JSON break on apostrophes):

```bash
fav add <<'EOF'
{ ...JSON... }
EOF
```

4. Report the line `fav add` prints (saved / updated / continued + title). If `fav` is missing, say so and stop; do not write the data file yourself.

Never write `~/.agent/fav/records.jsonl` directly and never guess the session id or paths: provider, session id, cwd, git and Herdr context are collected by `fav` itself, anything you pass for them is ignored.

## Output

```json
{
  "schema_version": 1,
  "title": "notes-api search cursor pagination returning duplicates",
  "label": "notes cursor dupes",
  "summary": "Search results sorted by updated_at repeated the same item across pages. Cause: records updated in the same second share a cursor, so the backend now falls back to id as a tiebreaker. Repro steps written up; next step is a ticket for the backend team.",
  "tags": ["pagination", "cursor"],
  "project": "notes-api",
  "work_type": "debug",
  "status": "done"
}
```

- `title` required. 12–40 CJK characters or 6–14 English words: the object plus what was done to it. Never reuse the automatic session title; that is the problem this tool exists to fix. It must be recognizable a week later, so no filler like "continue previous work".
- `label` short title, ≤ 10 CJK characters or ≤ 20 columns of English, a compression of `title` rather than a different phrasing. Shown where only a word or two fits (the Herdr tab bar). Omitted → `title` truncated.
- `summary` required. 80–250 CJK characters or 50–150 English words: goal, key conclusions, current state or next step. Conclusions, not a play-by-play.
- `tags` 2–5 topic words, lowercase kebab-case. Do not repeat the project name or the work type (they have their own fields) and do not put ticket numbers in tags (pr-382, issue-369 belong in the summary). Reuse existing tags before inventing one: `fav list --json status:all | jq '[.[].tags[]] | unique'` (go, not golang; worktree, not branch-cleanup).
- `project` project name; `work_type` one of debug / design / implementation / research / ops or similar.
- `status` `todo` / `doing` / `done`; default `done` (/fav is usually called when a piece of work is finished; write `doing` if it is not).
- Omit anything you are not sure about; never invent. Never include passwords, tokens, connection strings or the full text of private files.

## Running /fav again

Running `/fav` again in the same session **updates** the existing record; the idempotency key is provider + session id, and a normal resume keeps the session id. Re-read the conversation and rewrite the summary and status from the current state instead of replaying the previous JSON; the favorited time is refreshed too.

Only `/clear` or `--fork-session` moves the same work onto a new session id. Then find the old record with `fav list --json` and run:

```bash
fav add --supersede <old record id> <<'EOF'
{ ...JSON... }
EOF
```

so the new session inherits the old record instead of splitting into two.

## After saving

- In Claude Code, after a successful `fav add`, remind the user to run `/rename <label>` (the short label, not the title): Claude's own session name (the `/resume` list, the terminal title, the agent name in Herdr) then matches fav. Codex has no equivalent.
- `fav` / `fav fzf` find and resume; `fav list '#tag project:x last:7d'` queries; `fav pin <id>` hard-links the transcript so upstream cleanup cannot remove it.
