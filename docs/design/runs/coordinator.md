# 协调器

唯一写 task / run 状态的进程：持锁、事件日志、状态与转移表、事件、命令与收据、调度与对账、订阅、通知。实现：`internal/journal`（事件日志、收据）、`internal/task`（Task、Run、事件、`Apply`、转移表）、`internal/coord`（命令、调度、对账、订阅、socket）；模式二的日志存在 `internal/store`（见 [tasks/storage.md](../tasks/storage.md)「存储」）。

## 持锁即协调器

- `coord/lock`（`filelock.TryLock`）拿到的进程就是协调器：TUI 打开时拿；CLI 发命令时先连 socket，连不上就自己拿锁、执行、跑一轮派发和对账、退出；`tend service` 是一直拿着锁的进程。
- 拿到锁的进程监听 socket，其它客户端经它办事。
- 协调器不在时：已派发的 run 照常由节点推进；排队的 run 等下一个协调器；下次持锁时先对账。
- 关闭时最多等 5 s，让本进程里本机节点正在答的调用（如 `run.stop` 发布墓碑）结束，关闭之后不再写盘。

## 事件日志

- `<home>/coord/events.jsonl`，一行一个信封：`{v:2, seq, at, actor:{kind user|node|system, id}, command:{id, method, digest, result}?, events:[{type, data}], sum}`；v1 信封没有 `actor`，回放时算 `system`。
- `sum` = sha256（这一行去掉 `,"sum":"…"` 之后的字节），读写用同一个函数。
- 追加：写锁内 `seq+1`、写、fsync；失败截回原长度。打开时逐行校验、fold（逐行流式读，不整读文件）；最后一行没有换行 → 先备份到 `events.jsonl.torn-<unix>`，备份成功才截掉，备份或截断失败就只读打开；其它损坏 → 只读打开，不派发，报错（只读时追加回 `ErrReadOnly`）。
- `tend journal verify [--json]` 只读：逐行校验 sum、seq 连续、能 fold，报告条数、末 seq、第一处损坏（位置、原因、是否最后一行）和没有换行的残缺尾行。`tend journal repair -y` 只在协调器没运行时（拿得到 `coord/lock`）截掉残缺尾行或损坏的最后一行，先整份复制成 `events.jsonl.bak-<unix>`；损坏后面还有行 → 拒绝（`unsafe`），不丢后面的变更。
- 事件字段一律 snake_case。不做快照。
- `exec` 观察不落盘。
- 模式二的 `tend-server` 把同样的信封存在 SQLite 的 `coord/tend.db`（见 [tasks/storage.md](../tasks/storage.md)「存储」）。

## 状态

```
Task { id t_…, title, brief, dir, machine, agent, status backlog|todo|done|canceled, rev, created_at, updated_at }
Run  { id r_…, task, machine, agent, profile(冻结), brief_sum, dir(映射后),
       resume(续的会话), parent(回复的 run),
       want run|stop, state, exit_code, reason, detail, attention asked|permission|stalled, ask, note, last, usage,
       stream, requests[], answers[]（已给、节点还没取走）, sends[]{id, text, state queued|sent|failed},
       session{provider, sid}, node_rev, queued_at, started_at, ended_at }
```

任务层给 Task 加的字段（项目、父子与依赖、workflow 阶段、需求来源等）见 [tasks/overview.md](../tasks/overview.md)「对象总览」。

run `state` 转移表（终态单调，重复事件无副作用）：

| 从 | 事件 | 到 |
|---|---|---|
| — | `run_queued` | queued |
| queued | `run_starting`（已发 `run.start`） | starting |
| queued | `run_canceled` | canceled |
| starting | 节点快照 running | running |
| starting / running | 节点快照 exited(code) | exited |
| starting / running | 节点快照 stopped | stopped |
| starting | 节点快照 failed（没启动起来） | failed |
| starting / running | 节点快照 unknown（监督进程死、没有退出记录） | unknown |
| starting | 节点没有这个 run 目录 | 重发 `run.start`（仍是 starting）；已 `want=stop` 则发 `run.stop`，节点留墓碑 → stopped{never_started} |
| running / unknown | 连续两次 `run.list` 里都没有 | unknown{missing} |
| unknown | 节点快照变成 running / exited / stopped | 对应状态 |
| starting / running / unknown | 用户 `run.abandon` | abandoned（`want=stop`）；一律对节点发 `run.stop`：没有目录就留墓碑（迟到的 start 起不来），还在跑就停；节点快照成终态或 unknown 之前，它仍占着目录和 slot |

- 「显示为运行中」= 有 state ∈ {starting, running, unknown} 的 run；task 只存 backlog / todo / done / canceled。
- 一个 task 同时最多一个未结束 run；同一（机器, 目录）同时最多一个 starting / running run，其余排队；目录比较按目标机器的规则（Windows 不分大小写、`\` 与 `/` 等同）。
- 任务书上限 256 KiB（`bad_request brief`）：它在 journal 里占一行，在 `state.get` 里占一帧。
- run 冻结 `from`（任务目录所属的机器，模式一默认本机）；派发时从 `from` 映射到目标机器；`from` 还没握过手就先连它，这一轮不派。
- `want=stop` 持久化；每次连上节点先发 `run.stop`，直到节点快照是终态。
- 用时：运行时长只用节点时间，排队时长只用协调器时间。
- `attention` 不是状态，是「需要人」的标记，随节点快照整体覆盖（协调器自己合成的观察不带它）。「等你回复」= 已结束且 attention 是 asked 或 permission（`Run.Waiting`）；asked 也可以出现在运行中（`tend run ask`），stalled 只在运行中有意义。
- `run.continue`：对已结束、有会话的 run（不必在等）排一个新 run：同任务、同机器、同目录、同档案（可换同 provider 的档案），`brief` 是回复，`resume` 是会话，`runner=background`；任务还有未结束的 run 就 `conflict`。对任意已索引的会话（`session`、`provider`、`dir`、`machine`）则新建一个任务（标题取回复首行）再排 run。

## 事件

`task_created` `task_edited` `task_status_set` `run_queued` `run_starting` `run_observed{state, exit_code, reason, detail, attention, ask, note, last, usage, stream, requests, sends, session, node_rev}` `run_stop_requested` `run_canceled` `run_abandoned` `run_answered{id, answer}` `run_sent{id, send}`。

- `run_observed` 带节点的 `requests` 整体覆盖；`sends` 按 id 合并（节点说的为准，协调器排着的保留）；`answers` 里请求已不在 `requests` 的删掉（节点取走了，或不再等）。run 结束时 `requests`、`answers` 清空，还是 queued 的消息改 failed。

## 命令与收据

- 写命令必带 `command_id`。信封里存收据：`{id, method, digest(params), result}`，`result` 是第一次的完整应答（事件应用之后的 task / run）。
- 收据按（调用者, `command_id`）存，别人的同一个 id 不会拿到它。重放：method 和 digest 相同 → 调用者仍看得见结果就返回收据里的结果，看不见回 `not_found`；不同 → `conflict`。
- 没有产生事件的成功命令（no-op）不写收据，直接回当前结果。
- 超时不代表没执行：客户端重试用同一个 `command_id`（CLI 的写命令超时后同一个 id 最多再发两次）。

## 调度与对账

- 协调器循环：启动时、节点 `node.changed`、每 5 s。
- 派发：机器已连上且没超过并发上限（`machines.<名>.slots`，默认 2）、目录不冲突 → `run_starting` → `run.start{spec}`（`command_id` = run id）。
- 对账：连上节点或每 5 s，对「有未结束 run、有待 ack 的 run、或有还在节点上跑的 abandoned run」的机器调 `run.list{coordinator, ack, runs}`，按 `node_rev` 差分，生成 `run_observed`。`runs` 只列协调器还关心的 run（这台机器上没 ack 的：未结束、abandoned、没有结束时间、或结束不到 7 天）；一个都没有时发 `["-"]`（不匹配任何 run），旧节点忽略它、照旧全列。每个节点调用各自 20 s 超时；超时只记错误，不断连接。
- 节点拒绝启动（`conflict`，detail 以 `dir_busy <run>` 或 `slots n/m` 开头，见 [node.md](node.md)「run 目录」）不算失败：run 留在 starting，10 s 后重发。
- 回答和消息：`run.answer` 只收 run 未结束、请求在 `requests` 里、还没回答过的（问题类允许时每个问题都要有回答，否则 `bad_request answers`）；`run.send` 只收 running 且 `stream` 的 run（否则 `conflict cannot_send`），消息 id 是 `m_` + 随机。两者都是带收据的命令，只写事件；对账时对节点列表里还在跑的 run，把请求仍在节点 `requests` 里的回答、节点 `sends` 里没有的 queued 消息发 `run.answer` / `run.send`（同一项 10 s 内不重发），应答的快照直接折成 `run_observed`。
- abandoned 的 run 在节点快照成终态之前（节点这次没列出它也算）仍占着目录和 slot，同目录的下一个 run 不派发。
- 协调器已结束（含 abandoned / canceled）、节点是终态或 unknown 的 run 进 `ack`。
- 退避期内的机器，`run.tail` / `run.output.page` / `node.call` 直接回 `offline`，不重拨；只有 `machine.list{connect}` 清退避。
- 节点连不上：失败后 5 s 起翻倍，最多 5 分钟；`machine.list` 回原因和下次重试时间。
- 模式一：有未结束 run 的机器保持连接；其余空闲 5 分钟断开。
- 连上节点后在后台调一次 `node.agents`，结果挂在机器上（`machine.list` 的 `agents`）；`machine.list{connect}` 和 `run.preview` 会重新取。
- 预检 `run.preview`：先按 dispatch 同样的规则算出 run（错误照样回），再连机器（等拨号结束），给出机器状态、映射后的目录、`blockers`（`cli_missing`、`auth_missing`、`node_outdated`：跑了也会立刻失败）和 `notes`（`offline`、`connecting`、`slots`、`dir_busy`、`auth_unknown`、`unchecked`、`herdr`、`background`、`continues`）。CLI 有 blocker 就不派发（`--force` 例外）；TUI 和 Web 只显示。节点在 `run.start` 里自己再查一次（有 5 分钟缓存，坏结果只缓存 30 s），是最终裁决：没装 → `failed{cli_missing}`，确定没登录 → `failed{auth_missing}`；看不出登录状态不拦。

## 订阅

- `state.get` 返回 `{tasks, runs, seq}`；`no_briefs` 去掉任务书（CLI 和 Web UI 用，任务书按需 `task.get`）。TUI 取全量（搜索要查任务书），超帧时回退到 `no_briefs`；编辑一律先 `task.get`。
- `subscribe{after_seq}`：锁内登记实时通道并记当前尾 seq H；锁外从文件补发 `(after_seq, H]`；再排空实时通道（丢弃 seq ≤ H 的）。
- 慢客户端（通道满 256）断开，客户端按 seq 重订。
- 同一条连接重复 `subscribe` → `conflict "subscribed"`，原订阅照旧有效（Web 的 `api.js` 忽略这个错误）。

## 通知

- `config.notify_command`（argv）在某个 run 变成「需要人」时由协调器启动，标准输入是一个 JSON 对象 `{event, run, task, title, machine, agent, state, exit_code, reason, detail, ask, session, dir, at}`，环境变量带 `TEND_EVENT` `TEND_RUN` `TEND_TASK`；`notify_events` 限定事件。
- 事件（每次状态迁移一次）：`run.failed`（failed，或非 0 退出且不在等）、`run.waiting`（结束时在等）、`run.asked`（运行中提问）、`run.permission`（运行中等批准）、`run.stalled`（运行中变成 stalled）。只在 commit 新事件时判断，重放日志不触发。
- 任务事件（见 [tasks/workflows.md](../tasks/workflows.md)「hooks」和 [tasks/team.md](../tasks/team.md)「人在任务里」）：`task.needs_you`（任务进入 waiting，没开始的除外）、`task.done`、`task.stage` 和 `task.rework`（workflow 进入或退回某阶段，JSON 多一个 `stage`；只给 notify_command，不进个人 webhook）。`notify_events` 为空时只发 `run.*`，任务事件要点名才发。团队模式下协调器把它们作为 `Notice{seq, event, task, title, project, reason, run, to, at}` 交给 `Options.Notice`，`tend-server` 投到收件人的个人 webhook。
- 命令分离启动、不等它：JSON 先写进管道（控制在 3.5 KB 内，Windows 管道缓冲 4 KB），一次性 CLI 协调器退出也不影响它。
