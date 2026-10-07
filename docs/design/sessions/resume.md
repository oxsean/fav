# 恢复

恢复流程与编排：provider 的恢复命令与会话身份识别、桌面 App、分叉与交接、在这里开新会话、经 Herdr 开 tab 恢复。实现：`internal/capture`（`CommandSpec`、`PlanResume`、`AppURL` / `AppAvailable`、交接包）、`internal/herdr`（`WorkspacesFor`、`KeepLabel`、`ReportLabel`）、`internal/ui/tui`（恢复框、`ovHandoff`、`ovStart`）、`cmd/tend`（`resume`、`handoff`）。

## 恢复流程

1. 用户选中记录并按 Enter（窄终端先进详情页，再次 Enter 或 `r`）。
2. 系统显示恢复确认：Provider、目录、Git 状态、Herdr 路由、最终命令、逐条校验结果。
3. 用户确认后恢复。
4. herdr 服务不可达、或记录没有 workspace：切换到记录 cwd 后在当前终端恢复（`exec` 替换自身，不多挂一层进程）。
   - 会话**已经在跑**：在某个 Herdr tab 里 → 只 `herdr tab focus` 切过去，不再开一个；是 Claude 后台会话
     （终端关了它没退，`claude agents --json` 里 `kind=background`）→ 用 `claude attach <短 id>` 接管，
     再 `--resume` 会被 Claude 拒绝。卡片和详情里标「运行中」。
5. herdr 服务可达且记录有 workspace：定位同名 workspace，新建 tab，在其中执行恢复命令 —— **tend 自己在不在 Herdr 里无所谓**。
   记录没有 workspace 时按目录找（`herdr.WorkspacesFor`）：有 pane 正好在记录目录里的 workspace 优先，没有再看同属一棵子树的；只有一个就用它，多个时恢复框弹选择（最后一项是「当前终端」），`tend resume` 报错列出候选、用 `--workspace <名字>` 指定。
   恢复发生在别的 tab 里，**TUI 不退出**：底部提示结果，用户接着找下一条。只有当前终端恢复才退出 TUI（要 `exec` 接管终端）。
6. 路径或 workspace 不存在时不得静默猜测；显示问题并提供降级选择。
7. 迁移过的会话（[migration.md](migration.md)「Claude 完整迁移」）：
   - 迁来的会话第一次恢复时，`claude --resume <id>` 后面带一条首条消息「这个会话刚从 <机器> 迁过来，先读 <迁移说明的路径>」（`migrate.FirstResume`，`capture.Plan.FirstMessage`），恢复命令发出后记下已说明，之后不再带。聚焦已有 tab、`claude attach` 后台会话时不带。
   - 最近一次迁移是从这台迁走的：`tend resume` 在 stderr 提醒「已迁往 <机器>（<时间>）」，两边都接着做会分叉；不拦。

恢复框的按钮分组和键位见 [tui.md](tui.md)「键盘与鼠标」；在跑的会话上的「切过去」「接管」见 [tui.md](tui.md)「跑着的会话（Herdr）」；其它机器的会话怎么恢复见 [remote.md](remote.md)「恢复」。

## Provider

恢复命令以结构化的 `CommandSpec{Exec, Args, Cwd}` 表达，永不把未转义字符串交给 shell。

| Provider | 命令 |
| --- | --- |
| Claude | `claude --resume <session-id>` |
| Codex | `codex resume <session-id>` |

恢复和分叉跟原会话最后记下的权限模式走（索引记下的，见 [index-and-search.md](index-and-search.md)「会话当时看到的环境」；记录里没有启动时的 argv，只有模式）。模式不在 CLI 的 `--help` 列出的取值里、或是默认的，就什么都不加：

| 记下的 | 加的 |
| --- | --- |
| Claude `bypassPermissions` | `--dangerously-skip-permissions` |
| Claude `acceptEdits` / `auto` / `dontAsk` / `manual` / `plan` | `--permission-mode <模式>` |
| Claude `default`、没记下 | 无 |
| Codex `approval_policy` 是 `on-request` / `never` | `-a <值>`（`untrusted` 等现在的 codex 不收的不加） |
| Codex `sandbox_policy` 是 `read-only` / `workspace-write` / `danger-full-access` | `-s <值>` |

Claude 的放在 CLI 名字后面（`claude --dangerously-skip-permissions --resume <id>`），Codex 的放在子命令后面（`codex resume -a never -s danger-full-access <id>`，`resume` 和 `fork` 都收这两个参数）。新开会话没有原会话，不加。

设置 `session_args`（provider → 参数列表）加在恢复、分叉、新会话命令的 CLI 名字后面，也就是 alias 放的位置：`claude <参数> --resume <id>`、`codex <参数> resume <id>`。tend 直接执行 CLI，用户 shell 里的 alias 管不到它，又不去读 alias（各 shell 写法不同，也不该悄悄放大权限）。`session_args` 里已有权限类参数（`agent.SetsPermission`，和判断越权的 `BypassArgv` 是同一张表：`--dangerously-skip-permissions`、`--permission-mode`、`--yolo`、`-a`、`-s`、`--full-auto`、`-c sandbox_mode=…` / `approval_policy=…` 等）时它说了算，不再加原会话的模式。

只在 `agent.ResumeOf` / `ForkOf` / `StartOf` 一处加（每次读 `config.json`；模式由 provider 的命令构造按 `Rec.Permission` 拼），TUI、`tend resume` / `handoff`、当前终端和 Herdr tab 都经过它，恢复框和 `--dry-run` 显示的命令就是带参数的那条；`claude attach` 和任务运行（`LaunchOf`，用档案的 `args`）不加。别的机器的会话：谁拼命令用谁的设置和索引。经 ssh 恢复时是那台的 `tend resume` 拼（[remote.md](remote.md)「恢复」），用那台的设置和它记下的模式；server 模式给出「在那台机器上执行」的命令是本机拼的，用本机的 `session_args` 和 `Session` 带来的模式，旧节点不带模式就不加。

会话身份识别按可靠性三级降级（依据见 [external-behaviour.md](external-behaviour.md)「Session ID 获取」）：

1. `herdr pane current` —— 两个 provider 通吃，顺带给出 workspace/tab/cwd。
2. `CLAUDE_CODE_SESSION_ID` 环境变量 —— 非 Herdr 环境下的 Claude。
3. Codex rollout 文件反查 —— Codex 没有 session id 环境变量。按 `mtime 近 5 分钟 AND cwd == $PWD` 匹配；命中多条时列出候选报错，**不猜**。

## 桌面 App

- 深链（都没有公开文档，是从 App 里找出来的，失败要能退回终端）：Claude 桌面版 `claude://resume?session=<uuid>`，把命令行会话导入桌面版的 Code 页并打开（自己去 `~/.claude/projects` 找记录；桌面版没登录会拒绝，App 里弹提示）；ChatGPT 桌面版（内置 Codex）`codex://threads/<id>`，打开这个线程（App 和命令行共用 `~/.codex`，命令行建的线程也能打开，已实测）。只收 UUID 格式的 id。
- App 能不能跑这个会话（`capture.appBlock`，不满足时 `AppURL` 为空，按钮、App 默认、`tend resume --app` 都不走 App，`--app` 报原因）：工作目录还在（App 在那里启动会话，目录没了会起不来）；记录文件在默认位置 `~/.claude/projects` 或 `~/.codex/sessions` 下且还在（App 不看 `CLAUDE_CONFIG_DIR`/`CODEX_HOME`，也看不到 WSL、远程的会话；Codex 已归档的 `archived_sessions` 未验证，一并排除；原文件被删只剩 pin 硬链接的也排除）。
- 能不能开（`capture.AppAvailable`，每次运行查一次并缓存）：TUI 第一次打开恢复框时后台查（`probeApp`），查到之前不显示按钮，结果到了（`appProbedMsg`）按钮再出现；只有「恢复方式」是 App 或跟来源走的人，启动时就后台预查，因为 Enter 走哪边打开时就要定：问系统这个 scheme 归谁处理，处理者的名字或 bundle id 里要有 `claude`（Claude）或 `codex`/`chatgpt`（Codex），否则不给 App 按钮。macOS 用 `osascript` JXA 调 `NSWorkspace.URLForApplicationToOpenURL`（约 0.1 秒，App 装在哪都能找到；ChatGPT 桌面版的 bundle id 是 `com.openai.codex`，没有 `codex://` 的旧版 ChatGPT 自然排除）；Windows 调 `AssocQueryStringW`（`ASSOCF_IS_PROTOCOL`，取程序名和可执行文件，商店安装的打包应用也认）；Linux 等用 `xdg-mime query default x-scheme-handler/<scheme>`（官方没有 Linux 版，非官方包注册了就能用）。打开：macOS `open`，Windows `ShellExecute`，Linux `xdg-open`。
- 来源（`Rec.App`，不落盘）：Codex 看 rollout 第一行 `session_meta` 的 originator 是 `Codex Desktop` 或 `codex_work_desktop`（索引记在 `File.App`）；Claude 看桌面版的会话清单 `<配置目录>/Claude/claude-code-sessions/*/*/local_*.json` 的 `cliSessionId`（`capture.ClaudeDesktopIDs`，按 mtime 缓存，`Index.Attach` 时套上，续接链的旧 id 也算）。卡片元信息写 `Claude App`、`Codex App`。
- 设置「恢复方式」`resume_in`：终端（默认）/ 桌面 App / 跟来源走。App 为先时恢复框第一个按钮是「Enter 在 Claude 中打开」，终端恢复是「r 恢复」；否则 App 按钮是「p Claude App」。`tend resume` 同样遵循：`--app` 强制 App，`--terminal` 强制终端；按设置走 App 失败时退回终端。
- 在终端里跑着的会话（不是 App 建的）不在 App 里再开，提示先切过去或关掉；Herdr tab 里跑着的，App 不当第一按钮。tend 拿不到 App 内部的结果，提示写「已交给 … 打开」。

## 分叉与交接

- 分叉（恢复框 `b`，`tend resume --fork <id>`）：Claude `claude --resume <id> --fork-session`，Codex `codex fork <id>`；新会话带着原历史，原会话不动。检查项和恢复相同（记录文件要在）；去向同恢复（Herdr 新 tab / 多个 workspace 时选 / 当前终端），但不算一次恢复（`herdrDoneMsg.resumed` 为假，不加 `resume_count`）。
- 交接（恢复框 `s`，`tend handoff <id> [--to claude|codex]`）：后台写交接包 `~/.agent/tend/handoff/<sid 前 8 位>-<时间>.md`（0600，写新包时删 30 天前的），内容：来源（provider、会话 id、目录、分支、最近活动）、记录文件路径（让新 AI 需要时自己读）、摘要（`Rec.Summary`）、最近 5 条用户要求（每条 600 字封顶）、最后一条回复（2000 字封顶）、改过的文件（最近 80 条消息里 Write/Edit/MultiEdit/NotebookEdit 的路径、apply_patch 的文件，目录下的写相对路径，最多 30 个）、`git status --short --branch`（30 行）。不放工具输入和输出（可能带密钥）。
- 建成任务（恢复框「另开会话」一行的「建成任务」，没有键）：不在终端里开新会话，而是经协调器建一个任务，后台运行接着原会话写（`run.continue{session}`，Claude `--resume`、Codex `exec resume`），不分叉：之后在终端里恢复这个会话，会看到 agent 的轮次。所以会话正在跑时不让建，不然两边同时写同一个记录。只对本机的会话，也只给这台机器的主人。表单、项目选项和不能建的几种原因见 [tui.md](tui.md)「建成任务」。
- 交接弹窗（`ovHandoff`）：预览全文（↑↓ / PgUp PgDn / 滚轮），`e` 用 `$VISUAL`/`$EDITOR` 编辑（TUI 让出终端，回来重读），没设就用 VS Code 打开（不等）；`y` 复制全文；`1` Claude Code / `2` Codex CLI 开新会话（只列装了的，原会话的 provider 在前、Enter 就是它）。新会话在原目录，第一条消息是「先读 <路径>，说说理解和下一步」——包本身不进命令行，编辑过的内容就是新会话读到的内容。检查只看 CLI 装没装、目录在不在。
- `tend handoff <id>` 不带 `--to` 时把包打到 stdout、路径打到 stderr；`--to` 同 resume 的去向规则（`--dry-run` 也会写包）。
- 交接包分两步写：`capture.HandoffFactsOf(r)` 在会话所在的机器上取事实（`HandoffFacts`），`capture.RenderHandoff(facts, target)` 在看的人这台用自己的语言写成 Markdown。`target` 为零值时就是上面本机交接的成稿（金样测试守着逐字不变）。

### 交接到另一台机器

`tend handoff <id|机器:sid> --host <机器>`：会话在 A，新会话开在 B，两台都要是自己的机器（模式二由协调器只给机器主人，见 [team.md](../tasks/team.md) 第 6 条；CLI 先拦下别人共享来的机器）。驱动函数 `remote.Handoff` 只认 `remote.Peer`（`peer.go`）：本机是 `Here`（进程内，不经 `share_sessions`；CLI 另接上 `node.repos`），别的机器是 `(*Hosts).Peer`（模式一 ssh、模式二 `node.call`），协调器替网页时用 `PeerOf`。`Peer.Call` 只发 hello 里有的方法，没有就回 `unknown_method`，界面写「<机器> 的 tend 旧：先 `tend hosts install <机器>`」。模式二的 server 的 hello 没有 `migrate` 时写「server 旧」。

1. A 回答 `handoff.facts`。A 或 B 的 `share_sessions` 不是 `all` 时那台回 `unauthorized`，CLI 写明是这个设置。A 和 B 是同一台（`endpoint` 相同）时，成稿和本机交接一样。
2. 定 B 上的目录：`--dir` 给了就用它（要是 B 写法的绝对路径）；会话归某个项目、项目在 A 和 B 上都有目录时，用 `pathmap.Rebase` 把会话目录从 A 的项目目录接到 B 的；否则拿会话的 remote（facts 里 `git.remote`，没有就是记录的 `GitRemote`，再没有就是本机索引里 Codex 记下的 origin）问 B 的 `node.repos`：只有一个检出就用它，几个就列出来让用户用 `--dir` 选，没有就要 `--dir`。
3. 成稿按 B 写（`HandoffTarget`）：不写 A 的 transcript 路径，改成「原会话在 <A> 上，这台机器读不到；要细节就问用户」；多一段「目录对应」：A 的目录 → B 的目录，两边的 home；再一段「环境差异」：会话在 A 上看到的环境和 B 上这个目录的比较（[migration.md](migration.md)「迁移前的环境诊断」的「展示」）。
4. 开法：
   - 不带开法：`handoff.put` 写到 B 的 `~/.agent/tend/handoff/<id>.md`（0600，30 天后清掉），stdout 打 `tend handoff --open <id>`，在 B 上执行它。
   - `--to claude|codex`：写到 B，再开终端：B 是本机就直接开；模式一 `ssh -t <别名> <tend argv> handoff --open <id> --no-herdr`，在 Herdr 里开新 tab，否则用当前终端（同远端恢复）；模式二只在有同名 ssh 项、`node_id` 对得上时这样做，否则打出那条命令。命令行上只有 `id`（`[A-Za-z0-9._-]`），目录和交接包都不进远端 shell。
   - `--task [--agent <档案>] [--project <项目>]`：不写到 B，经协调器 `task.create` 建成 B 上的任务（标题「交接：<原标题>」，任务书是成稿加一句「先说说你的理解和下一步。」，目录是第 2 步的，agent 默认是会话自己的 CLI，项目默认是会话所在的项目），再 `run.dispatch`；协调器原有的规则照用。
   - `--print`：只把成稿打到 stdout。
5. `tend handoff --open <id>`（B 上）：读 `<id>.json`，目录不在就报错，再用 `HandoffPrompt(<id>.md)` 开新会话，检查和去向同 `PlanStart`（`--dry-run`、`--no-herdr`、`--workspace` 同上）。
6. 源会话不动，也不记迁移关系：交接开的是新会话。

### 在这里开新会话

`w` / `Ctrl+W`（列表里任意会话或项目页分组标题，恢复框里也有；`ovStart`）：目录 = 会话的 cwd（不在了用 `Rec.Repo`），分组标题取组里最常用且还在的目录。先列出这个目录（或以它为主仓库的 worktree）里正在跑的会话，按开始时间新的在前，↑↓ 选中后 Enter 转到它的恢复框（那里 Enter 就是切过去）；没选中时 Enter = 第一个 CLI。CLI 按这个目录里用得多的排在前，只列装了的，`1` Claude / `2` Codex；命令不带参数（`BuildStart` 空 prompt），去向同 `PlanStart`（Herdr 新 tab / 当前终端），tab 名是项目名。底栏在项目分组标题上提示「w 新会话」。

## Herdr

只做已验证链路（命令与输出形状见 [external-behaviour.md](external-behaviour.md)「Herdr 编排」）：

走不走 Herdr 取决于 **herdr 服务在不在**，而不是 tend 自己是不是从 Herdr 里启动的：
`herdr` CLI 在 `HERDR_*` 全部 unset 时照样连默认 socket，所以从普通终端跑 `tend`
也能把会话送进 Herdr 的 workspace。`HERDR_ENV` 只用来判断「当前终端是不是一个 Herdr pane」。

1. 记录没有 workspace，或 `herdr workspace list` 不通 → 当前终端 `cd` + resume。
2. 有 workspace → `herdr workspace list` 找同名 label。
3. 找到 → `herdr tab create --workspace <id> --cwd <path> --label <短标题> --focus`，
   从返回的 `root_pane.pane_id` 取新 pane，再 `herdr pane run <pane-id> <argv...>`。
4. **`pane run` 送的是一整行 shell 命令**，参数必须引号化（`CommandSpec.ShellLine`，经 `shell.POSIX.Line`），
   带空格的标题不引号化会被拆成多个参数；有目录时这一行以 `cd <目录> && ` 开头，因为 `tab create --cwd` 起来的 shell 不一定就在那个目录。
5. **`pane run` 之后要把 tab 标题改回来**：Claude Code 的 hook 在 SessionStart 时把 tab label
   改成终端标题（那一刻还是命令行回显）。盯着标题变掉再 `herdr tab rename`，等不到就直接改一次。
   之后每轮结束 hook 会把 tab 改成终端标题，也就是 `--name` 给的 tend 标题 —— 这是想要的结果。
6. **侧栏 `$label` 单独报**：`herdr pane report-metadata <pane> --source tend --token note=<短标题>
   --token label=<短标题>`。侧栏 agent 行显示的是自定义 token，不跟 tab 标题走；
   `note` 是 hook 认的「用户手设标题」，报了之后 hook 每轮都会沿用。
   tab 标题优先用记录的 `label`（/tend 时由 AI 预先写好的短标题），没有才退到
   标题的前 24 显示列 —— 完整标题在 tab 栏里只会被挤成省略号。
7. **Claude 的恢复命令带 `--name <记录标题>`**：Herdr 显示的 agent 名字取自终端标题，
   而终端标题由 Claude Code 自己设；`--name` 让它用 tend 的标题，提示框和 `/resume` 列表也跟着换。
   Codex 没有对应开关。
8. 找不到 workspace → 给「当前终端恢复 / 选 workspace / 取消」，**不自动创建 workspace**。
9. cwd 不存在 → 报错并显示记录里的 git remote，**不反查候选 repo**。
10. Herdr 路径失败时明确告知原因，再降级到当前终端，不静默切换。
