# 依赖的外部行为

实现依赖的 Claude Code / Codex CLI / Herdr 的文件布局与命令行为：会话 id 从哪来、恢复命令、transcript 位置与体积、Herdr 编排、skill 安装位置。都是实测过的事实，不是文档推断；它们的版本升级时要重验。实现：`internal/capture`、`internal/herdr`、`internal/index`（`ClaudeProjectDir`）、`cmd/tend`（`install-skill`）。

## Session ID 获取

三条路径，按可靠性排序，实现中依次降级（降级顺序见 [resume.md](resume.md)「Provider」）。

### 1. Herdr（最可靠，两个 provider 通吃）

`herdr pane current` 返回 JSON：

```json
{"result":{"pane":{
  "agent":"claude",
  "agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"<session-id>"},
  "cwd":"/home/me/dev/fav",
  "pane_id":"w4N:p2B","tab_id":"w4N:t22","workspace_id":"w4N"
}}}
```

一次调用拿到 provider、session id、cwd、workspace、tab。

Codex pane 的 `agent_session` 形状未单独验证：实现按相同结构解析，失败则降级到第 3 条。

### 2. 环境变量（仅 Claude）

`CLAUDE_CODE_SESSION_ID` 在 Bash 工具环境里直接可读。

**Codex 没有对应变量。** 对 codex 主二进制做 `strings` 扫描，`CODEX_*` 只有：
`CODEX_HOME`、`CODEX_APPLY_GIT_CFG`、`CODEX_INTERNAL_APP_SERVER_REMOTE_CONTROL_DISABLED`、`CODEX_INTERNAL_ORIGINATOR_OVERRIDE`。

### 3. Rollout 文件反查（仅 Codex 兜底）

`$CODEX_HOME/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`，首行：

```json
{"timestamp":"...","ordinal":0,"type":"session_meta","payload":{
  "session_id":"019eb6b8-...","cwd":"/home/me/dev/resume",
  "originator":"Codex Desktop","cli_version":"0.138.0-alpha.7","source":"vscode"}}
```

文件在会话期间持续 append，mtime 可作活跃度判据。

**歧义是常态**：同一时刻常有多个 codex session 在写盘，其中有 cwd 不同的，也有同 repo 不同 worktree 的。所以匹配必须是 `mtime 近 5 分钟 AND cwd == $PWD`，命中多条时列出候选报错，不猜。

**性能约束**：单个 rollout 可达上百 MB，**只能用 bufio 读首行**，且只扫今天和昨天两个日期目录（`~/.codex/sessions` 总量可达 GB 级）。

## Resume 命令

| Provider | 命令 | 备注 |
| --- | --- | --- |
| Claude | `claude --resume <session-id>` | `-r` 亦可；不带 `--fork-session` 时**沿用原 session id**（`--fork-session` 的说明为「resume 时创建新 ID 而非复用原有的」） |
| Codex | `codex resume <SESSION_ID>` | 也接受 session name；`--last` 续最近一个 |

对幂等的含义：正常 resume 不会产生新记录，`(provider, session_id)` 作为幂等 key 成立。只有 `/clear` 和显式 `--fork-session` 才切断 id 连续性，由 `tend add --supersede` 处理（见 [favorites.md](favorites.md)「幂等」）。

## Transcript 位置与体积

| Provider | 路径 | 量级 |
| --- | --- | --- |
| Claude | `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl` | 总量 GB 级，单文件几十 MB |
| Codex | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` | 总量 GB 级，单文件上百 MB |

Claude 的目录名是 cwd 里非字母数字都换成 `-`（`/home/me/dev/fav` → `-home-me-dev-fav`，`index.ClaudeProjectDir`）。定位已有会话用 glob `~/.claude/projects/*/<id>.jsonl`；只有移动项目时才按这个规则生成新目录。

两家的记录文件都是 JSONL，每条顶层带 ISO 8601 `timestamp`。Codex 首行（`session_meta`）就有；Claude 开头几条是
`last-prompt` / `mode` 之类不带时间戳的状态行，第一条带戳的才是会话创建时间。`capture.SessionStart` 按行扫前 50 行取第一条。

**结论**：复制 transcript 不可行。默认只存路径 + 失效检测，`tend pin` 显式硬链（硬链本身不占额外空间，但会把大文件永久钉住，所以必须是显式动作）。

## Herdr 编排

所有 `herdr` 子命令输出结构化 JSON，无需解析人类可读文本。

| 能力 | 命令 |
| --- | --- |
| 在位判断 | 环境变量 `HERDR_ENV=1`（只说明「当前终端是不是一个 Herdr pane」） |
| 服务可达 | `herdr workspace list` 成功即可编排 —— **`HERDR_*` 全部 unset 时 CLI 照样连默认 socket `~/.config/herdr/herdr.sock`**，所以从普通终端也能把会话送进 Herdr |
| 当前上下文 | `HERDR_WORKSPACE_ID` / `HERDR_TAB_ID` / `HERDR_PANE_ID` / `HERDR_SOCKET_PATH`，或 `herdr pane current` |
| workspace | `herdr workspace list \| create \| get \| focus \| rename \| close` |
| tab | `herdr tab list \| create \| get \| focus \| rename \| close` |
| pane | `herdr pane list \| current \| get \| send-text \| send-keys \| read \| split \| close` |

`herdr tab create --workspace <WORKSPACE_ID> --cwd <PATH> --label <TEXT> --env <K=V> --focus`

**`tab create` 没有 `--command`**，所以恢复是两步：建 tab 拿到新 pane → `herdr pane send-text <命令>` + `herdr pane send-keys Enter`。

`herdr tab list` 返回 `{tabs:[{tab_id, workspace_id, label, number, pane_count, focused, agent_status}]}`；
`herdr pane list` 返回 `{panes:[{pane_id, tab_id, workspace_id, agent, agent_session, cwd, ...}]}`。
`tab create` **直接返回新 tab 和 pane**：`{result:{type:"tab_created", tab:{tab_id,label,...}, root_pane:{pane_id,tab_id,cwd,...}}}`。

**tab 标题会被覆盖 —— 覆盖的不是 Herdr，是 Claude Code 里装的 hook**
（如 `~/.claude/hooks/herdr-tab-title.sh`，挂在 SessionStart 和 Stop 上）：每次触发都
`herdr tab rename <tab> <终端标题>`，并 `pane report-metadata --token label=<note 或终端标题>`。
Claude 刚起来时终端标题还是 shell 回显的命令行，所以 tab 会短暂变成
`claude --dangerously-skip-permissions ...`，等 `--name` 生效、下一轮 hook 跑过就变成 tend 标题。
`sleep 30` 这类非 agent 命令不触发。恢复流程在 `pane run` 之后盯着标题变掉再
`herdr tab rename` 改回短标题（`internal/herdr.KeepLabel`），撑过这个窗口。

**侧栏 agent 行显示的 `$label` 是自定义 token**（`config.toml` 的 `ui.sidebar.agents.rows`），
由上面那个 hook 报：有 `note` token 用 note，否则用终端标题。`pane rename` 和 `tab rename`
都改不动它。tend 在 `pane run` 之后 `herdr pane report-metadata <pane> --source tend
--token note=<短标题> --token label=<短标题>`（`internal/herdr.ReportLabel`）：`label` 让侧栏立刻显示，
`note` 让 hook 之后每轮都沿用。token 是按名 patch 的，别的 source 报的 `git` 不受影响。

**正在跑的会话怎么认**：`herdr agent list` 每个 agent pane 带 `agent_session.value`（= Claude session id）、
`pane_id`、`tab_id`；`claude agents --json` 列所有活着的 Claude 进程，`kind=interactive`（在某个终端里）
或 `kind=background`（带短 `id`，`claude attach <id>` 可接管）。`claude list` **不是**子命令 —— 会被当成 prompt 跑一次。

**workspace 不带 cwd**（`herdr workspace list` 只有 label/number/tab_count），但 `herdr pane list` 的 pane 带 `cwd`，
按目录找 workspace 走 pane → workspace_id。

**关 Herdr tab 不等于结束 Claude 会话**：`herdr tab close` 之后 Claude 转成后台会话，
再 `claude --resume <id>` 会报 `Session … is running as a background session … Run claude attach
<id> … or claude stop <id> first`，然后退出。tend 恢复前查 `claude agents --json`，是后台会话就改用 `claude attach`。

**`pane run` 是敲进 shell 的，不是 argv 直传**：`herdr pane run <PANE> claude --name notes 分页排障`
会被 pane 里的 shell 拆成 `--name notes` + 两个多余的位置参数（送给 Claude 当 prompt）。
必须传一条已引号化的命令行（`capture.CommandSpec.ShellLine`）。

**Herdr 里显示的「agent 名字」跟着终端标题走**（`herdr agent get` 的 `terminal_title`），
而终端标题是 Claude Code 自己设的（默认是它自动生成的会话摘要，形如 `✳ notes 分页排障`）。
`claude --name '<标题>'` 会把终端标题、提示框和 `/resume` 列表一起换成这个名字，
所以恢复命令带上 `--name <tend 标题>`，Herdr 里的 agent 名字就是 tend 的标题。
`herdr agent rename` 是另一回事：它只收 `[a-z0-9_-]{1,32}` 的 slug，是给 `herdr agent prompt <名字>`
寻址用的，不是显示名。Codex 没有对应开关。

## Skill 安装

Claude 与 Codex 都用各自配置目录下的 `skills/<name>/SKILL.md`：

- `<Claude 配置目录>/skills/<name>/SKILL.md`（`capture.ClaudeHome()`：`CLAUDE_CONFIG_DIR`，缺省 `~/.claude`）
- `<Codex 配置目录>/skills/<name>/SKILL.md`（`capture.CodexHome()`：`CODEX_HOME`，缺省 `~/.codex`）

`tend install-skill` 一份源，软链成两边的 `skills/tend`；`uninstall-skill` 只摘软链。

## 未验证的假设

- Codex pane 在 Herdr 里的 `agent_session` 字段形状。
  实现按与 claude 相同的结构解析，失败自动降级到 rollout 反查。
- Codex Desktop 起的 session（`originator: "Codex Desktop"`, `source: "vscode"`）能否被 `codex resume` 恢复。
