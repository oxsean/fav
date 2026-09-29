# 节点

一台机器上的 run：run 目录、监督进程 `tend _run`、观察、会话绑定、双向流。实现：`internal/node`（run 目录、`run.*` 方法、监督进程、快照）、`internal/proc`（分离启动、进程树、存活检查，按 OS）。

## run 目录

`<home>/node/runs/<run_id>/`：

| 文件 | 写的人 | 内容 |
|---|---|---|
| （目录本身） | `run.start` / `run.stop` | 先在 `runs/.new-<run_id>-<随机>` 里写好 `spec.json`、`prompt.md`（墓碑还有 `claim` 和 `state.json`），再改名到位：出现在 `runs/` 里的目录总是完整的。改名成功 = 拿到启动权；已存在 → 回现状，绝不再启动。`run.stop` 遇到没有的目录也这样发布一个墓碑 |
| `claim` | 监督进程 / 节点 | `O_EXCL` 创建：谁先建谁决定这个 run 的第一个 state（监督进程写 starting；节点写 not_launched 或墓碑的 stopped） |
| `spec.json`（0600） | `run.start` | 冻结的启动参数：argv、env、目录、provider、预设会话 id、runner、stdin 文件、`coordinator` |
| `prompt.md`（0600） | `run.start` | 任务书；background 方式在末尾加运行约定：没人看、要人拍板就停下并让最后一条消息以 `ASK:` 开头，可以用 `tend run note` / `tend run ask`（写成 tend 的绝对路径） |
| `lock` | 监督进程 | 终生持有；`filelock.Held` = 监督进程活着 |
| `state.json` | 监督进程或拿到 `claim` 的节点（`fileio.WriteAtomic`） | `{rev, state, pid, pid_start, pane, session, exit_code, reason, detail, attention, ask, note, last, usage, stream, requests, sends, started_at, ended_at}`；`pid_start` 是 agent 进程的启动时间（`proc.StartTime`：macOS sysctl、Linux `/proc/<pid>/stat` 第 22 项、Windows `GetProcessTimes`），和 pid 一起认出同一个进程 |
| `stop` | `run.stop` | 存在 = 请求停止 |
| `output.log` | 监督进程 | background 方式的 stdout / stderr，16 MB 轮转为 `.1` |
| `settings.json` / `mcp.json`（0600） | `run.start` | agent 定义的 claude hooks 和 MCP 服务器（feature `files`） |
| `reports.jsonl` | agent（`tend run ask\|note\|verdict\|plan`、`note --pr`） | 只追加 `{at, kind ask\|note\|verdict\|pr\|plan, text, verdict}`；监督进程每秒读新增的整行，verdict（pass / rework / blocked）进 `state.json` 的 `verdict` |
| `answers.jsonl` | `run.answer` | 只追加 `{request, allow, message, answers}`；监督进程每 250 ms 读新增的整行 |
| `inbox.jsonl` | `run.send` | 只追加 `{id, text, state, at}`；监督进程每 250 ms 读新增的整行 |

- `run.list{coordinator, runs?}` 回这个协调器派发的 run 的快照（给了 `runs` 就只回这些）；读快照只读不写。快照在 `state.json` 之上补一条：`!Held` 且 state 不是终态 → `unknown{reason: supervisor_gone}`（没人持锁时再读一次 state，防监督进程刚写完就走）；没有 `state.json`、`!Held`、且创建超过 30 s（`spec.created`）→ 报 `failed{reason: not_launched}`，结束时间是创建后 30 s，不落盘。迟到的监督进程发现创建已超过 30 s，自己写下 `failed{not_launched}` 退出，不起 agent（agent 只在第一个 state 写成之后才启动）。`state.json` 读失败重试 5 次、间隔 20 ms。
- 准入：`run.start` 在 `node/admit.lock` 下检查并发布：同一真实目录已有未结束的 run → `conflict "dir_busy <run>"`；未结束的 run 数已到 `node.slots`（0 = 不限）→ `conflict "slots n/m"`。节点预检已判定跑不了（blocker）的 run 不占名额，直接发布成 failed。
- `run.list` 每 10 分钟顺带清掉超过 1 小时的 `.new-*` 残留。
- 监督进程取 `lock` 失败会在 1 s 内重试（`Held` 的探测会短暂持锁）。
- `run.start` 先查约束再看目录是否存在（`allow_dirs` 外的路径一律 unauthorized，不泄露是否存在；约束见 [deployment.md](deployment.md)「模式二」）。
- 终态或 unknown 的 run 被协调器 ack 后保留 7 天再删目录；删之前把它的会话登记进 `<home>/node/sessions.jsonl`（只追加），会话视图仍认它是 run 的会话。两种情况不删：state 不是终态且 agent 进程还活着（会话守卫还要它）；登记会话失败（下次再试）。

## 监督进程 `tend _run <dir>`

1. 拿 `lock`；已有 `state.json` 或拿不到 `claim` 就退出。已有 `stop` → 直接写 `stopped{asked}`，不起 agent。写 `state=starting`，写不成就退出（不起 agent）。
2. 启动 agent，写 `state=running` + pid（Herdr 方式另写 pane）。Windows 上挂起启动，加入 Job 后再恢复。
3. 等 agent（进程本身）退出；期间每秒看 `stop`：存在就结束 agent 的进程组（unix）/ Job（Windows），10 s 后强杀。agent 退出后它留下的子进程还占着输出：2 s 后结束整棵树。
4. 写终态（失败重试 10 s）：有 `stop` → `stopped`（清掉 attention 和 ask）；否则 `exited{code}`，并按输出定结论：最后一条消息有以 `ASK:` 开头的行 → `attention=asked`、`ask` 是这行起的内容；claude `result.permission_denials` 非空 → `attention=permission`、`reason=permission_denied`、`detail` 列工具；非 0 退出或有错误事件 → `agent.Classify` 从错误文本（没有就用 `output.log` 末尾 4 KB）分出 `auth` `quota` `rate_limit` `overloaded` `context_overflow` `network` `session_missing`，`detail` 是最后一条能分类成同一原因的原话（stdout 和 stderr 在 `output.log` 里交错，末行常常不是原因），分不出原因时取最后一行非空文字。
5. 放锁、退出。

- 监督进程死后（run 是 unknown）的 `run.stop`：写 `stop` 后拿 run 的 `lock` 收尾：agent 进程还在、且 pid 和 `pid_start` 都对得上 → 结束整棵进程树，写 `stopped{orphan_stopped}`；agent 已不在 → `stopped{supervisor_gone}`；认不出是不是同一个进程（没有 `pid_start`，或还在 starting 没有 pid）→ 不动，仍是 unknown。`proc.Alive` 不把僵尸进程算活着（容器的 init 不回收子进程，否则停止和 unknown 收尾会永远卡住）：Linux 读 `/proc/<pid>/stat`、macOS 用 sysctl，其它 unix 认不出僵尸。
- `TEND_CRASH_AT=claimed|started|running|ending` 让监督进程在对应切点立即退出（测试用：写完 claim、agent 已起但没记 pid、已记 pid、agent 已退出但没记终态）。
- agent 的环境多了 `TEND_RUN` 和 `TEND_RUN_DIR`（run 目录）。
- stdout 逐行解析（JSON 才解析）：claude `result`（最后一条消息、`is_error`、`permission_denials`），codex `thread.started`（会话 id）、`item.completed` 的 `agent_message`（最后一条消息）、`error` / `turn.failed`（错误）；claude / codex 以外的 agent，最后一行普通输出当最后一条消息。
- 运行中每秒：读 `reports.jsonl` 新增（ask → `attention=asked` + `ask`；note → `note`，并清掉 stalled）；background 方式下 stdout / stderr 超过 `spec.stall_after`（节点 `node.stall_after`，默认 15 分钟，`off` 关闭）没有字节 → `attention=stalled`，再有输出就清掉；只标记，不停。herdr 方式每 3 s 看 agent 是否在等人：Herdr 里它的 pane 是 `blocked`，或（claude）会话 transcript 末尾是没回答的 `AskUserQuestion` / `ExitPlanMode`，或最新的 Claude hook 事件是 `Notification` / `PermissionRequest` 且 transcript 之后没再长（`tend install-hook`）→ `attention=asked`（没有 `ask` 原文）；不再等时清掉，但只清自己标的，`tend run ask` 的提问留着。Herdr pane 状态和 Claude hook 这两个来源只用于 herdr 方式。
- 输出按 64 KiB 块写盘，超长行不解析；`output.log` 轮转改名失败（Windows 上有读者开着）就继续追加，下次再轮转。

runner 方式：

| 方式 | 条件 | 做法 |
|---|---|---|
| herdr | 只给 claude；节点 Herdr 可达，且恰好一个 workspace 覆盖目录（零个或多个都走 background） | 新 tab 里跑 `tend _run <dir>`；agent 交互式、留在前台进程组；tab 被关（SIGHUP）先写 `stopped{tab_closed}`；启动时 workspace 已不在 → run 失败（不改成 background，命令行不同） |
| background | 其它 | `proc.StartDetached` 启动 `tend _run <dir>`（unix `setsid`，Windows `DETACHED_PROCESS \| CREATE_NEW_PROCESS_GROUP \| CREATE_BREAKAWAY_FROM_JOB`，父进程的 Job 不许脱离（`ERROR_ACCESS_DENIED`）时去掉 `CREATE_BREAKAWAY_FROM_JOB` 再启动一次）；agent 无界面，claude / codex / fake 走双向流（见下文「双向流（stream）」），command 模板的任务书经 stdin 或 `{prompt_file}`，输出进 `output.log` |

## 观察

- 节点连接存活期间，每 3 s 看本机未结束的 run：监督进程锁、`state.json` 的 `rev`；有变化推 `node.changed`。
- 「需要人」用 `attention`（见 [coordinator.md](coordinator.md)「状态」）表达，来源：run 自己的输出、报告、沉默时长，herdr 方式另有 Herdr 的 pane 状态、transcript 和 Claude hook 事件（见上文「监督进程 `tend _run <dir>`」）。

## 会话绑定

| provider | 方式 |
|---|---|
| claude | 启动时 `--session-id <uuid>` 预设；续跑用 `--resume <sid>`，会话 id 不变 |
| codex | background 跑 `app-server`：`thread/start` 回的 thread id 写进 `state.json`；续跑用 `thread/resume`，spec 里已带会话 id |
| fake | 预设 id，写 Claude 形状的 transcript（在节点的 `CLAUDE_CONFIG_DIR` 下，端到端一律用 fixture 启动器，不碰真实目录） |
| command | 不绑定，只有 `output.log` |

- 索引对「run 目录里登记过的会话 id」不置 Skip（`claude -p` 的 `sdk-cli`、`codex exec` 平时会被跳过）；这些会话在会话视图里带 run 标记。
- 运行守卫：`PlanResume` 除了本机 live 表，还看这个会话有没有未结束的 run（没有终态，且监督进程或 agent 进程还活着）；有就不给「直接恢复」。

## 双向流（stream）

- background 方式下 claude、codex、fake 的 run 是 stream run（`spec.stream`）：监督进程持有 agent 的 stdin（管道），`prompt.md` 不作 stdin 文件。协议按 provider 分：
  - claude：`-p --input-format stream-json --output-format stream-json --verbose --permission-prompt-tool stdio`（不禁 `AskUserQuestion`）。启动后发 `initialize` 控制请求，再把 `prompt.md` 作为第一条 user 消息；`control_request{can_use_tool}` 成为请求（`AskUserQuestion` 是 question，其余是 permission，摘要取 `command` / `file_path` / `url` 等字段），回答写 `control_response`（允许带 `updatedInput`，问题的回答放进 `updatedInput.answers`；拒绝带 `message`）；其它控制请求回 error。消息是 user 消息，claude 在当前这一轮读到。
  - codex：`codex app-server -c approval_policy=on-request [-c model=… -c sandbox_mode=…]`（值不加引号：`codex.cmd` 拒绝引号），JSON-RPC 2.0。`initialize` → `initialized` → `thread/start{cwd, approvalPolicy}`（续跑用 `thread/resume{threadId}`），thread id 即会话 → `turn/start` 带任务书。`item/commandExecution/requestApproval`、`item/fileChange/requestApproval`、`item/permissions/requestApproval` 成为 permission，`item/tool/requestUserInput` 成为 question，其它服务端请求回 error；回答是 `decision accept|decline`、授予所请求的权限、或按问题 id 给 `answers`。消息在一轮进行中发 `turn/steer{expectedTurnId}`，太晚（报错）或在两轮之间就 `turn/start`。用量取 `thread/tokenUsage/updated` 的 total，最后一句话和最终消息取 `item/completed` 的 agentMessage。
- 请求进 `state.requests`，同时标 attention：有问题 → `asked`（`ask` 是第一个问题），否则 `permission`；请求都答完就清掉自己标的。等请求时不算沉默（回答或送出消息后沉默计时重置）。
- 一轮结束（claude `result`、codex `turn/completed`）后：从没送过消息就立即关 stdin，agent 随之退出；送过消息则等 2 s，期间没有新输出、也没有新消息才关（新消息会开下一轮）。
- 停止：先中断当前这一轮（claude `interrupt` 控制请求、codex `turn/interrupt`）并关 stdin，3 s 后还没退出再结束进程树，之后照常 10 s 强杀。
- 结束时：还没送出的消息记 failed，没回答的请求清掉；用户拒绝过的工具不算「等你批准」（只有权限模式没问就拒的才算）。
- 节点 `run.answer`：请求不在 `state.requests` 里 → `conflict request_gone`；同一请求已在 `answers.jsonl` 里 → 直接回快照。`run.send`：不是 stream run 或已不在 starting / running → `conflict cannot_send`；同 id 已有 → 直接回快照。快照的 `sends` = `state.sends` 加上 `inbox.jsonl` 里监督进程还没取的（queued；run 已结束或 unknown 时算 failed）。
- 最后一句话（`last`，500 字节内）和用量（`usage{input, cache_read, cache_write, output, cost_usd, turns}`）：claude 取 assistant 文本和每个 `result` 的 usage 累加、`total_cost_usd` 取最新；codex exec 取 `turn.completed` 的 usage；普通命令行取最后一行。每秒最多写一次 state。
