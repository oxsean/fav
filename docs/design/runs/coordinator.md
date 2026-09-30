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
       stream, requests[], answers[]（已给、节点还没取走）, sends[]{id, text, state queued|sent|seen|failed},
       caps{steer, after, interrupt, answer_scope, questions, continue, takeover}（节点说的实际能力）, doing（正在做的工具调用）,
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
- 任务书上限 256 KiB（`bad_request brief`）：它在 journal 里占一行，在 `state.get` 和快照的一批里都要放得下。
- run 冻结 `from`（任务目录所属的机器，模式一默认本机）；派发时从 `from` 映射到目标机器；`from` 还没握过手就先连它，这一轮不派。
- `want=stop` 持久化；每次连上节点先发 `run.stop`，直到节点快照是终态。
- 用时：运行时长只用节点时间，排队时长只用协调器时间。
- `attention` 不是状态，是「需要人」的标记，随节点快照整体覆盖（协调器自己合成的观察不带它）。「等你回复」= 已结束且 attention 是 asked 或 permission（`Run.Waiting`）；asked 也可以出现在运行中（`tend run ask`），stalled 只在运行中有意义。
- `run.continue`：对已结束、有会话的 run（不必在等）排一个新 run：同任务、同机器、同目录、同档案（可换同 provider 的档案），`brief` 是回复，`resume` 是会话，`runner=background`；任务还有未结束的 run 就 `conflict`。对任意已索引的会话（`session`、`provider`、`dir`、`machine`）则新建一个任务（标题取回复首行）再排 run。

## 事件

`task_created` `task_edited` `task_status_set` `task_restored{id, status, auto, start_seq, merged, stage, loops, stage_seq, stages, parent, after, held}`（撤销：逐项放回） `run_queued` `run_starting` `run_observed{state, exit_code, reason, detail, attention, ask, note, last, usage, stream, requests, sends, caps, doing, turn, session, node_rev}` `run_stop_requested` `run_canceled` `run_abandoned` `run_answered{id, answer}` `run_sent{id, send}` `run_interrupt_requested{id, turn, ask, by}`。

- `run_observed` 带节点的 `requests` 整体覆盖；`sends` 按 id 合并（节点说的为准，协调器排着的保留）；`answers` 里请求已不在 `requests` 的删掉（节点取走了，或不再等）。run 结束时 `requests`、`answers` 清空，还是 queued 的消息改 failed，但有会话时 `after`、`interrupt` 方式的留着（见下面「续接」）。
- `run_queued` 带 `takes` 时，父运行里这些消息改 sent。`run_answered` 替换同一请求之前的回答。`run_sent` 的 id 已有时只改它的 `state`（协调器宣布留着的消息失败）。`run_interrupt_requested` 只在 run 未结束时记进 `Run.interrupt`（最新的一次）。

## 命令与收据

- 写命令必带 `command_id`。信封里存收据：`{id, method, digest(params), result}`，`result` 是第一次的完整应答（事件应用之后的 task / run）。
- 收据按（调用者, `command_id`）存，别人的同一个 id 不会拿到它。重放：method 和 digest 相同 → 调用者仍看得见结果就返回收据里的结果，看不见回 `not_found`；不同 → `conflict`。
- 没有产生事件的成功命令（no-op）不写收据，直接回当前结果。
- 撤销：`task.undo{id, command}` 撤回调用者自己的一条 `task.set_status`（改成 canceled 的除外）或 `task.move`。协调器折叠日志时（启动时重放也一样）在内存里按收据记下这类命令之前任务的样子和它之后任务的 `rev`；任务之后又被改过（`rev` 不同，撤销过一次也算）回 `conflict changed`，不是这个任务的命令回 `not_found`；撤销移动时原位置按 `task.move` 的规则再查一遍（原父任务已结束、有打开的 run、太深等），放不回去回和 `task.move` 同样的错误；完成排了合并 run 的，合并还在排队就取消（`reason: undone`），否则 `conflict merge started`。规则见 [../tasks/workflows.md](../tasks/workflows.md)「撤销」。
- 超时不代表没执行：客户端重试用同一个 `command_id`（CLI 的写命令超时后同一个 id 最多再发两次）。

## 调度与对账

- 协调器循环：启动时、节点 `node.changed`、每 5 s。
- 派发：机器已连上且没超过并发上限（`machines.<名>.slots`，默认 2）、目录不冲突 → `run_starting` → `run.start{spec}`（`command_id` = run id）。
- 对账：连上节点或每 5 s，对「有未结束 run、有待 ack 的 run、或有还在节点上跑的 abandoned run」的机器调 `run.list{coordinator, ack, runs}`，按 `node_rev` 差分，生成 `run_observed`。`runs` 只列协调器还关心的 run（这台机器上没 ack 的：未结束、abandoned、没有结束时间、或结束不到 7 天）；一个都没有时发 `["-"]`（不匹配任何 run），旧节点忽略它、照旧全列。每个节点调用各自 20 s 超时；超时只记错误，不断连接。
- 节点拒绝启动（`conflict`，detail 以 `dir_busy <run>` 或 `slots n/m` 开头，见 [node.md](node.md)「run 目录」）不算失败：run 留在 starting，10 s 后重发。
- 回答和消息：
  - `run.answer`：run 已结束或请求不在 `requests` 里 → `request_gone`；已经有人回答、回答也送到了 → `request_gone`，`detail` 是先答的人（两人同时答，后到的拿到这个）；回答没送到（请求标了 `failed`）时，新的回答（选项可以不同）替换旧的。问题类允许时每个问题都要有回答（否则 `bad_request answers`）；`allow_run` 要请求带 `allow_run`、能力里有 `answer_scope`（否则 `bad_request decision`），已连上的节点没有 `answer_scope` feature → `proto node_outdated`。回答记下 `by`。
  - `run.send{run, text, mode}`：`steer`（默认）插进这一轮，要 running、`stream`、能力 `steer`；`after` 等这一轮完、run 退出后续接它的会话，要 running、能力 `after`；`interrupt` 先打断这一轮，再同 `after`，另要能力 `interrupt`、`run.turn` ≥ 1，已连上的节点没有 `interrupt` feature → `proto node_outdated`。条件不满足 → `cannot_send`，`detail` 是方式。消息 id 是 `m_` + 随机，记下 `mode` 和 `by`；`interrupt` 同时写 `run_interrupt_requested`（当前轮次）。
  - `run.interrupt{run, turn}`：`turn` 缺省取 `run.turn`；run 不在跑、没有 `interrupt` 能力 → `conflict cannot_interrupt`；`turn` 已过去 → `conflict turn_over`；同一轮已经要求过 → 不写事件，直接回。节点那边的 id 是 `int_<turn>`，重发是同一个请求。
  - 都是带收据的命令，只写事件。对账时对节点列表里还在跑的 run：请求仍在节点 `requests` 里的回答发 `run.answer`；节点 `sends` 里没有的 `steer` 消息发 `run.send`（`after`、`interrupt` 的消息不发给节点）；`Run.interrupt` 的轮次不早于节点的 `turn`、节点有 `interrupt` feature 时发 `run.interrupt{run, turn, id}`。同一项 10 s 内不重发，应答的快照直接折成 `run_observed`。
- 续接：`flow()` 每次先看已结束、还留着 `after` / `interrupt` 消息的 run，按 `seq` 顺序：以第一条消息的发送人身份续接它的会话（同 `run.continue`），任务书是这些消息的原文，空行隔开，新 run 的 `takes` 是它们的 id。run 是被 `run.stop` 停的、任务已结束、任务在它之后排过别的 run、或续接不了（没有权限、不能续会话、旧节点）时，这些消息写 `run_sent` 改 failed。续接先提交，任务才不会在这之前算作结束。
- abandoned 的 run 在节点快照成终态之前（节点这次没列出它也算）仍占着目录和 slot，同目录的下一个 run 不派发。
- 协调器已结束（含 abandoned / canceled）、节点是终态或 unknown 的 run 进 `ack`。
- 退避期内的机器，`run.tail` / `run.output.page` / `node.call` 直接回 `offline`，`run.output.watch` 回 `gone`，不重拨；只有 `machine.list{connect}` 清退避。
- 节点连不上：失败后 5 s 起翻倍，最多 5 分钟；`machine.list` 回原因和下次重试时间。
- 模式一：有未结束 run 的机器保持连接；其余空闲 5 分钟断开。
- 连上节点后在后台调一次 `node.agents`，结果挂在机器上（`machine.list` 的 `agents`）；`machine.list{connect}` 和 `run.preview` 会重新取。
- 预检 `run.preview`：先按 dispatch 同样的规则算出 run（错误照样回），再连机器（等拨号结束），给出机器状态、映射后的目录、`blockers`（`cli_missing`、`auth_missing`、`node_outdated`：跑了也会立刻失败）和 `notes`（`offline`、`connecting`、`slots`、`dir_busy`、`auth_unknown`、`unchecked`、`herdr`、`background`、`continues`）。CLI 有 blocker 就不派发（`--force` 例外）；TUI 和 Web 只显示。节点在 `run.start` 里自己再查一次（有 5 分钟缓存，坏结果只缓存 30 s），是最终裁决：没装 → `failed{cli_missing}`，确定没登录 → `failed{auth_missing}`；看不出登录状态不拦。

## 订阅

- `state.get` 返回整份状态 `{seq, tasks, runs, projects, shares, agent_defs}`，给一次性读取（CLI）；`no_briefs` 去掉任务书，任务书按需 `task.get`；编辑一律先 `task.get`。
- `state.watch{after_seq?, no_briefs?}` 是一个流（[wire.md](wire.md)「流」），跟随状态的客户端（TUI、Web UI、CLI 的 `--wait`）都用它，流在 `ClassState` 一级：
  - 锁内登记实时通道并记下日志的尾 seq H；锁外决定怎么开始，再接上实时通道，丢掉 seq ≤ 已发出部分的。
  - 能续传就续传：带 `after_seq`、它不超过 H、`(after_seq, H]` 不多于 `maxReplay`（10000）条、其中没有 `reshapes` 的事件。推 `open{mode: resume}`，回放这一段，再接实时信封。
  - 否则推 `open{mode: snapshot}`，然后按表推 `snapshot{part, items}`（`part` 是 `task.State` 的 JSON 名：`projects`、`tasks`、`runs`、`shares`、`agent_defs`，`items` 按 id；每批不超过 `snapshotBatch`（1 MiB），每张表至少推一次，空表也推），接着是 `affordances` 这个 part（这个人能做的事，见下面「能做什么、等谁」），最后 `live{seq}`，之后是实时信封。
  - 实时信封是 `journal`，按人过滤（`visibleEnv`），每个 seq 都到，没有可见事件时是空的。`reshapes` 的信封不单独推：推 `reset{}`，接着推新的快照和 `live`，这个信封只体现在新快照里。所以客户端的副本停在它的 seq 上时一定是它之后做的快照，断线续传不会跳过撤权。
  - 推送用 `PushWait`：客户端读得慢时协调器等，不丢；实时通道积压超过 `stateQueue`（256）条，流以 `lagged` 结束，客户端按自己的 seq 重开。协调器关闭时流以 `gone` 结束。常数在 `internal/coord/limits.go`。
  - 每推一个实时信封之后，协调器为它碰到的任务（`touched`：提交时、事件折叠之前算好，随信封交给每个流；包括事件点名的任务和它的父任务，新建、移动、撤销移动的还有事件里的新父任务，移动和撤销移动的还有它下面的整棵子树，因为深度变了）和这些任务的运行，按这个人重算 `affordances`，只推变了的：`affordances{runs: {id: [动作] | null}, tasks: {id: {actions, route} | null}}`，`null` 是这一项没了（没有可做的也算没了）。每个流记着自己发过什么。续传的流在回放之后推一次全量（看得见的每一项，空的推 `null`），因为不知道客户端手里的那份。`affordances` 不经 journal 折叠，也不带 seq。
  - journal 之外的变化也会改动作：机器主人、用户停用（`tend-server` 的 `sweep`）、节点连上换了版本（`hello` 的 feature）。这时调 `Coord.Reaffirm()`，每个流全部重算一遍，只推差异。
  - 客户端这边的折叠是 `coord.StateFold`：`open` 为 snapshot 或收到 `reset` 时开始一份新副本，`live` 时换上，`journal` 按 seq 折进去，`affordances` 的 part 和推送收进 `Aff`；遇到不认识的 part、seq 断档或折叠出错，丢掉副本，不带 `after_seq` 重开。
- `machines.watch{}` 和 `inbox.watch{}` 是快照型的流（`ClassStream`，`internal/coord/topics.go`）：先推 `open{mode: snapshot}`，然后推整份列表，之后每次都推整份替换，客户端不处理增删，看不见的自然消失。每个订阅记着上次发出的 JSON，重算后一样就不推。
  - `machines{items}`：和 `machine.list` 同样的 `Machine`，按 `canSee` 过滤。重算的触发：每次提交、开始拨号、连上或断开、`Attach` / `Expect`、退避变化、`node.agents` 回来、调用出错、`Reaffirm`；200 ms 内的合成一次，订阅期间另外每 5 s 兜底重算一次。`retry_at` 照常推，由客户端倒数；立即重连仍是 `machine.list{connect}`。
  - `inbox{items}`：和 `inbox.list` 同样的 `Inbox`，在协调器算（管理员那几项要看机器是否退役，客户端不知道）。每次提交后，只唤醒这次碰到的任务（`touched`，同上：移动时旧父任务和新父任务都算）在提交前后关系到的人（`concerns`，换负责人时新旧两人都算）和管理员；`reshapes` 的提交和 `Reaffirm` 唤醒所有订阅。300 ms 内的合成一次，被唤醒的订阅按自己的人重算整份。
  - 协调器关闭时这两种流以 `gone` 结束，推送用 `PushWait`。模式一的 TUI 不订阅 `inbox.watch`，「等你」在本地由状态推导。

## 能做什么、等谁

规则只写一份：状态加能力给出候选，协调器按人筛。Web、TUI、手机和通知都用这一份，客户端只显示，不推导。

- **能力**：`Run.CapsNow()`：有节点报上来的实际能力（`Run.caps`）就用它，没有就按 provider 和 runner 算计划中的（herdr 的运行、`command` 这类不开双向流的 provider 不能插话；没报能力的旧节点以 `stream` 为准）。
- **候选动作**：`internal/task` 里的纯函数，只看状态和能力。
  - 运行：`State.RunActions(r)`，取 `steer`（运行中的双向流，能插话）、`after`（运行中，能在这一轮后续接）、`interrupt`（运行中，能打断，已经数到第一轮）、`answer`（有没答的请求，或回答没送到的）、`allow_run`（有带 `allow_run` 的权限请求，并且能力里有 `answer_scope`）、`stop`、`abandon`、`continue`（已结束、有会话、能续接、任务没有开着的运行）、`takeover`（已结束、有会话、能在终端恢复）。
  - 任务：`State.TaskActions(t)`，取 `dispatch` `start` `stop` `pass` `rework` `ack` `keep` `merge` `done` `backlog` `cancel` `reopen` `plan` `review` `edit` `move` `child` `message`，名字和 Web 的操作表一致。
- **按人筛**（`internal/coord/afford.go`）：每个候选拿对应方法的判断试跑一遍，只判不提交（`dry`），所以筛的规则就是方法本身的规则（项目角色、`canUse`、`canApprove`、机器主人、审批人、节点有没有需要的方法）。`answer` 试每个没答的请求，有一个能答就给；`takeover` 没有方法，只给机器主人。
- **消息去哪里**：`State.Route(t)`，按顺序取第一条成立的：
  1. 有运行在跑并且收消息（能插话，或者能在这一轮后续接）：`{to: run, run}`；
  2. 当前阶段（没有 workflow 就是整个任务）的上一次运行已结束、有会话、能续接：`{to: reply, run}`；
  3. 有 workflow：`{to: workpad, stage}`，下一阶段带上；
  4. 都不行：`{to: none, why}`，`why` 取 `finished`、`starting`（运行还没开始）、`busy`（运行在跑但不收消息）、`no_session`。

  `version` 是这条去向从哪个 seq 起成立：接收的运行排队时的 seq（`run.seq`），workpad 是阶段的 `stage_seq`。

  `task.message{id, text, mode, expect}` 照这张表送：`run` 走 `run.send`（`mode` 缺省时能插话就 `steer`，否则 `after`），`reply` 走 `run.continue`，`workpad` 记一条 `message` 笔记，`none` 回 `cannot_send`，`detail` 是 `why`。带了 `expect`（发送方看到的 `route`）而 `to`、`run`、`stage`、`version` 有一项不同 → `route_changed`，`detail` 是现在的 `to`，什么都不写。`task.message.preview{id}` 回 `{route}`，能读任务的人都能调。
- **待处理项**：`State.Pending(t)`，只在 Go 里算。
  - 形状：`Pending{id, kind, reason?, task, run?, request?, version}`。
  - 开着的运行有没答的请求：每个请求一项，`kind` 为 `permission` 或 `question`，`id` 是 `<run>/<request>`。请求都答了、节点还报着提问或等批准（agent 正在接回答）时没有项。
  - 否则按处境给一项（`id` 是 `<task>/<kind>`）：
    - 验收（人工闸门，或子任务都完成）是 `gate`；
    - 运行中在终端里提问或等批准，是 `question` 或 `permission`，不带 `request`；
    - 运行结束时留下的提问或被拒的工具，是 `continue`，要续接回答；
    - 运行顺利结束，是 `ended`；
    - 运行失败、停止或状态未知，是 `failed`；
    - 其余等人的原因（草稿、需求变化、合并冲突、打回太多次、预算、没交计划、派发不了、前置任务取消）是 `waiting`，带 `reason`。
  - 只等派发（`dispatch`）的不算。
  - `version`：运行上的项取 `run.seq`（一次运行只结束一次，请求 id 在一次运行里不重复），`gate` 取 `stage_seq`（打回后再次来到验收，版本就变了）。
  - `inbox.list` 的每一项带 `pending`：这个人能处理的那些，权限请求只给能批准的人（`canApprove`）。
- **对话**：`State.Conversation(run)` 是这个运行所在的对话：先沿 `Parent` 找到根（父运行不在状态里的那个），再取同一个任务里根相同的所有运行，按 `seq`、排队时间、`id` 排序。只看状态、不涉及权限，`fold.js` 照抄一份（`conversation`），`fold_test` 对照。
- **测试**：`TestEveryActionGivenIsTakenAndNoneWithheldIs` 在团队模式（机器主人、派发人、只读成员、项目外的人、没拿到机器共享的管理员）和模式一下，对每种状态的每个候选动作真的去执行，检查「给了 ⇔ 协调器接受」。`TestThePageFoldsRandomJournalsAsTheCoordinatorDoes` 用随机日志（协调器写得出的信封，有的结尾再加一个会被拒的）对照 Go 和 `fold.js`：在哪个信封上拒收、状态、处境、对话都要一致。

## 通知

- `config.notify_command`（argv）在某个 run 变成「需要人」时由协调器启动，标准输入是一个 JSON 对象 `{event, run, task, title, machine, agent, state, exit_code, reason, detail, ask, session, dir, at}`，环境变量带 `TEND_EVENT` `TEND_RUN` `TEND_TASK`；`notify_events` 限定事件。
- 事件（每次状态迁移一次）：`run.failed`（failed，或非 0 退出且不在等）、`run.waiting`（结束时在等）、`run.asked`（运行中提问）、`run.permission`（运行中等批准）、`run.stalled`（运行中变成 stalled）。只在 commit 新事件时判断，重放日志不触发。
- 任务事件（见 [tasks/workflows.md](../tasks/workflows.md)「hooks」和 [tasks/team.md](../tasks/team.md)「人在任务里」）：`task.needs_you`（任务在 waiting、没开始的除外，并且有了之前没有的待处理项：出现、被另一个替换、同类的又来一个）、`task.done`、`task.stage` 和 `task.rework`（workflow 进入或退回某阶段，JSON 多一个 `stage`；只给 notify_command，不进个人 webhook）。`notify_events` 为空时只发 `run.*`，任务事件要点名才发。团队模式下协调器把它们作为 `Notice{seq, event, task, title, project, reason, run, items, to, at}`（`items` 是新出现的待处理项） 交给 `Options.Notice`，`tend-server` 投到收件人的个人 webhook。
- 命令分离启动、不等它：JSON 先写进管道（控制在 3.5 KB 内，Windows 管道缓冲 4 KB），一次性 CLI 协调器退出也不影响它。
