# 协议

协调器、节点和客户端之间的协议：帧、握手与版本、`Conn`、方法、传输。当前 `wire.Proto` = 2。实现：`internal/wire`（帧、`Conn`、`Pipe`），方法的处理在 `internal/remote`（会话读取）、`internal/node`（`run.*`）、`internal/coord`（客户端方法）。

## 帧

一行一个 JSON：

```json
{"type":"req","id":7,"method":"run.start","params":{…},"command_id":"c_…"}
{"type":"res","id":7,"result":{…}}
{"type":"res","id":7,"error":{"code":"not_found","detail":"…"}}
{"type":"push","method":"node.changed","params":{…}}
{"type":"push","id":12,"method":"run.output","params":{…}}
{"type":"cancel","id":7}
```

- `type` 是帧类型：`req` 请求、`res` 应答、`push` 推送（不需要应答；带 `id` 的属于那个请求打开的流，见「流」）、`cancel` 取消对方正在处理的请求或打开的流。`res` 有 `error` 就是失败，否则成功，`result` 可以没有。帧类型写在显式的 `type` 里，不像 JSON-RPC 2.0 那样靠字段推断；字段名不缩写：开了 permessage-deflate 以后缩写最多再省几个百分点，却让日志、抓包和 `journal verify` 难读。
- `id` 在发送方内唯一，两个方向各自编号；应答按 `id` 回给等待者，可乱序。
- `command_id`：写操作的幂等键（见 [coordinator.md](coordinator.md)「命令与收据」）。
- 错误码稳定、不本地化；另有 `busy`、`conflict`、`unauthorized`、`canceled`、`request_gone`（请求已被回答，`detail` 是谁答的，知道时；或已不再问）、`cannot_send`（现在没有谁按这种方式收消息，`detail` 是原因）、`route_changed`（消息的去向不是发送方看到的那个，`detail` 是现在的 `to`），流另用 `lagged`（跟不上，按游标重开）、`gone`（跟的对象没了或机器断了）、`unsupported`（对方没有这个流，或协调器转问的节点没有这个方法，`detail` 是方法名）；`snapshot_changed`（运行的工作区在翻页之间变了，从头再取）。
- 单帧上限 16 MiB；超长帧断开连接。集合类应答（`list`、`run.list`）超过 8 MiB 时分页。
- 没有 `type` 或 `type` 不认识的行跳过（ssh 登录横幅、shell 警告）。
- WebSocket 上一条文本消息就是一帧；两端协商 permessage-deflate（每条连接一个压缩窗口），不支持的一端退回不压缩。ssh 用 `Compression=yes`。
- 零值字段不上线：字符串和切片 `omitempty`，数字和时间 `omitzero`，读的一端按缺省即零值处理。

## 握手与版本

- 发起方先发 `hello{proto, role, lang, version}`，应答方回机器信息（OS、home、目录、CLI、`methods`、`role`）。`proto` 不同 → 发起方报 `proto` 并断开。
- `wire.Proto` 只在**帧形状**变时 bump；方法增删靠 `hello.methods` 协商，调用前检查对方有没有这个方法。给已有方法加一个旧节点会静默忽略的字段时另要节点 feature（见 [overview.md](overview.md)「已定决策」）。
- `proto` 是整数版本号，和 protobuf 无关，名字不改；它从 1 起，因为漏发 `proto` 的对端解出来是零值 0，从 0 起就会被当成版本一致。
- 这套协议和日志格式还没进过任何发布版本（`v0.1.0` 早于它们）：进发布版本之前直接改，不给旧的一端留兼容；磁盘上已有的数据照样能读，例如 v1 日志信封照样回放（见 [coordinator.md](coordinator.md)「事件日志」）。
- 模式二的认证在 HTTP 升级时做（`Authorization: Bearer`），不在 hello 里；hello 里的 `role` 必须和 token 的角色一致。节点另在 `hello.node_id` 里带自己的身份（见 [deployment.md](deployment.md)「模式二」）。

## `Conn`

- 写：每条连接一个写协程，所有帧先进队列再由它写出；帧在调用方那里编码好。一次写超过写超时（30 s）断开连接。普通的调用、应答和不属于流的推送等写完才返回；流的推送只入队。
- 发送调度：四级优先（`wire.Class`），每次从有帧的最高一级取：
  1. `ClassControl`：普通的 req 和 res、`ping`、`cancel`、不属于流的推送；
  2. `ClassState`：状态流；
  3. `ClassStream`：输出流和快照型的流；
  4. `ClassBulk`：`Options.Bulk` 标记的方法的应答（协调器的 `coord.Bulk`：`run.output.page`）。
  同一级内按「道」轮流：每个流一条道，每个 bulk 应答一条道，control 共用一条道。所以再大的输出流也挡不住应答、`ping` 和状态推送，翻一页也挡不住实时推送。
- 读：读协程永不阻塞；`req` 交给处理器（最多 16 个同时处理，待处理超过 64 个回 `busy`）；`res` 找等待者或流；`push` 带 `id` 的交给那个流，不带的回调 `OnPush`；`cancel` 取消请求的 `ctx`，或结束那个流；被取消的请求连接还在就回一条 `canceled`，还在等处理槽的也回。`ping` 的应答和 `busy` 应答不占处理槽位：处理器全满时 `ping` 照样回（它证明的是连接，不是处理器）。
- 调用超时：发 `cancel`（写请求的时候就超时了也发，排在请求后面），连接保留。连接断开：所有等待者和流返回 `closed`。
- 对方关了发送方向（EOF）：已读到的请求答完再关，最多等 1 分钟（`drainWait`）；`Done()` 在这之后才触发，所以一个还在答的慢调用（如正在探测 CLI 的 `node.agents`）会把被顶掉的连接多拖几秒。
- 保活：空闲 30 s 发 `ping`，60 s 没有任何帧就断开（节点侧同样执行，Mac 睡眠后远端 `node --stdio` 会自己退出）。

## 流

一个普通请求打开流（方法名以 `.watch` 结尾，和别的方法一样出现在 `hello.methods` 和授权表里），之后的推送带这个请求的 `id`，同一个 `id` 的 `res` 一定是流的最后一帧：

```json
{"type":"req","id":12,"method":"run.output.watch","params":{"run":"r-3f1","from":{"file":"a1:9","off":48213}}}
{"type":"push","id":12,"method":"open","params":{"cursor":{"file":"a1:9","off":48213},"mode":"resume"}}
{"type":"push","id":12,"method":"run.output","params":{…}}
{"type":"cancel","id":12}
{"type":"res","id":12,"error":{"code":"canceled"}}
```

- 第一条推送是 `open{cursor?, mode, from?, to?}`（`wire.Open`）：`resume` 从游标接着来，`snapshot` 先给全量，`gap` 表示 `from` 到 `to` 这一段取不回来了。
- 正常结束是 `result{reason: "done"}`（`wire.EndDone`）；出错时 `error.code` 是 `canceled`、`lagged`、`unauthorized`、`gone`、`unsupported` 之一。
- 提供方：handler 调 `r.Stream(StreamOptions)` 拿到 `*Stream`，然后照常返回（返回值忽略，返回错误就用它结束流）。流登记在单独的表里，不占处理槽，handler 返回时也不取消；handler 在调 `Stream()` 之前那段仍然占槽，所以推送放在另一个协程里做。每条连接最多 64 个流（`MaxStreams`），多了回 `busy`。`Push` 入队，`End(result, err)` 发最后一帧；对方 `cancel` 时丢掉排队中的推送、自动回 `canceled`，流的 `Context()` 随之结束。对方在流打开之前就取消了，`Stream()` 直接回 `canceled`。
- 每个流一个按字节计的发送队列（`StreamQueue`，1 MiB），装不下一条推送时按 `StreamOptions.Full` 处理：
  - `FullLag`：丢掉排队中的推送，以 `lagged` 结束流，打开方按游标重开；
  - `FullGap`：**只丢排队中和这条同一个方法的推送**，连同这一条；`Push` 返回 `*GapError{Mark}`，`Mark` 是第一条被丢推送的标记（`PushMark` 传入，没有排队的就是这一条的），提供方据此推 `gap`，流不断；
  - `FullLatest`：只丢排队中同一个方法的推送，这一条入队，只留最新一份。
  只丢同一个方法的推送，所以已经排着的 `open` 和别的推送不会被丢，输出 hub 靠这一条插 `gap`。
- `PushRaw(method, params, mark)` 是参数已经编码好的 `PushMark`：同一批推给很多个流的提供方（输出 hub）只编码一次。
- `PushWait(ctx, …)` 不套用 `Full`：队列装不下时等到有空位（或流结束、`ctx` 结束）再入队，给能自己放慢的提供方用（`state.watch` 的快照、回放和实时信封：跟不上由它自己的通道判 `lagged`）。
- 打开方：`conn.Watch(ctx, method, params)` 先登记接收者再发请求，所以推送不会找不到主人。`Next(ctx)` 取下一条推送；流结束后，提供方正常结束时回 `io.EOF`，否则回结束的原因；`Done()`、`Err()`（正常结束时为空）、`Result(out)`、`Cancel()`。`ctx` 结束等同于 `Cancel()`。每个 `Watch` 一个有界队列（`WatchQueue`，256 条），读的人跟不上就在本地以 `lagged` 结束并发 `cancel`。结束以后这个 `id` 迟到的推送和应答一律丢弃；本地结束（取消、跟不上）后，队列里没取走的推送也不再交出。
- 帧序列：`internal/server/webtest/frames/` 下是网页客户端的帧序列（`c` 客户端帧，`s` server 帧），`internal/wire` 的回放测试拿 Go 的两端各对照一遍。

## 方法

| 方向 | 方法 |
|---|---|
| 任意 → 节点：会话读取 | `hello` `list` `messages` `text` `steps` `pulse` `checks` `live` `echo` |
| 协调器 → 节点：run | `run.start{spec}` `run.resume{spec, resume}`（同一会话续跑） `run.stop{run, coordinator}` `run.list{coordinator, ack, runs}` `run.tail{run, before, max, file, clip?}` `run.line{run, file, off, max}` `run.output.find{run, q, file?, before, limit?}`（见 [node.md](node.md)「读输出」） `node.agents{fresh}`（各 agent 命令行的安装、版本、登录） `run.answer{run, answer}` `run.send{run, send}` `run.interrupt{run, turn, id?}`（见 [node.md](node.md)「双向流（stream）」） `run.follow.watch{run, from}`（流，推 `run.follow`，见 [node.md](node.md)「读输出」） `run.changes{run, after?, snapshot?, all?}` `run.diff{run, path, snapshot?, hunk?, line?, n?, context?, all?}` `run.blob{run, sha, off?, n?}`（见 [node.md](node.md)「改动」） |
| 节点 → 协调器 | push `node.changed`（有 run 状态变了，协调器随后 `run.list`） |
| 客户端 → 协调器 | `state.get{no_briefs?}` `task.get{id}` `task.create` `task.edit` `task.set_status` `run.dispatch` `run.preview`（同 dispatch 参数，只回怎么跑、为什么跑不了） `run.continue{run \| session…, text}` `run.answer{run, request, allow, decision?, message, answers}`（`decision` 见 [node.md](node.md)「双向流（stream）」；协调器按它定 `allow`，别的值 `bad_request decision`） `run.send{run, text, mode?}`（`mode` 取 `steer`（默认）、`after`、`interrupt`，见 [coordinator.md](coordinator.md)「回答和消息」） `run.interrupt{run, turn?}` `run.stop` `run.abandon` `run.tail` `run.output.page{run, before, n, raw?, file?}`（最后 `n` 个事件，默认 200、最多 1000，见 [output.md](output.md)「运行输出」） `run.output.watch{run, from?}`（流，见 [output.md](output.md)「实时」） `agent.list` `machine.list` `state.watch{after_seq?, no_briefs?}`（流，见 [coordinator.md](coordinator.md)「订阅」） `node.call{machine, method, params}`（机器主人和管理员） `run.messages{run, before, n, file}`（按项目权限读 run 的对话） `project.create{id?, name, owner?}`（管理员） `project.edit{id, name?, owner?}` `project.member{project, user, role}` `machine.share{machine, users, projects, approve}`（后三个：项目负责人 / 机器主人或管理员） `task.start{id}`（整棵子树按依赖自动派发） `task.move{id, parent?, after?}` `inbox.list`（等我处理的任务，每项带 `pending`，见 [coordinator.md](coordinator.md)「能做什么、等谁」） `machines.watch{}` `inbox.watch{}`（流，见 [coordinator.md](coordinator.md)「订阅」） `run.output.item{run, id}` `run.output.find{run, q, file?, before, limit?}` `run.changes` `run.diff` `run.blob`（转给节点，见 [output.md](output.md)「单个事件、查找和改动」） `project.dirs{project, machine, path?}`（见 [../tasks/team.md](../tasks/team.md)） `agentdef.list` `agentdef.get{name}` `agentdef.save{text, owner?}` `agentdef.remove{name}` `agentdef.share{name, share}` `user.offboard{user, to}`（管理员；`tend-server` 的 `/api/users/offboard` 调它后停用并吊销凭据） `task.source_ack{id, accept}`（需求的新版本采用或不采用，issue 外部关闭后继续） `task.gate{id, pass, notes, expected_rev}`（workflow 的人工闸门：放行或打回） `task.message{id, text, mode?, expect?}`（按 `task.Route` 路由的留言，回 `{to, run}`；`expect` 是发送方看到的去向） `task.message.preview{id}`（回 `{route}`） `task.merge{id}`（冲突解决后重新把任务分支合进父任务的分支） `task.plan{id, agent?, machine?}`（排拆解 run） `task.plan_save{id, plan?, expected_rev}`（改草稿，不带 plan 即丢弃） `task.plan_apply{id, expected_rev}`（草稿建成 backlog 子树） `task.sync`（只有 server 进程内的同步 worker 能调） |
| 协调器 → 客户端 | `state.watch` 流里的 `open` `snapshot` `live` `journal`（事件信封，按人过滤，空的也发） `reset`（可见范围变了，新快照随后）；`run.output.watch` 流里的 `open` `run.output`；`machines.watch` 流里的 `open{mode: snapshot}` `machines{items}`；`inbox.watch` 流里的 `open{mode: snapshot}` `inbox{items}` |

新方法按 `hello.methods` 协商：节点没有 `run.send` 时回答和消息留在协调器里不投递；节点没有 `run.resume` 时续跑的 run 直接 `failed{node_outdated}`，`run.continue` 在已知时就拒绝；没有 `node.agents` 时预检回 `unchecked`。`node.call` 只转发会话读取方法。发送方不发超过 `MaxFrame` 的帧：请求直接回 `bad_request`，应答换成 `internal` 错误，连接不断。

## 传输

| 传输 | 用在 |
|---|---|
| 进程内 `wire.Pipe` | 协调器 ↔ 本机节点；TUI ↔ 自己持锁时的协调器；测试 |
| ssh 子进程 stdio | 模式一：协调器 → 远端节点（`ssh -T <别名> <tend argv> node --stdio`）；TUI 读远端会话 |
| unix socket | 客户端 ↔ 本机持锁的协调器（`paths.Socket(home)`，0600） |
| WebSocket | 模式二：节点 → server（`/node`），客户端 → server（`/client`） |

HTTP/3 / WebTransport 以后只作为可选传输加进来，不替代 WebSocket：它要引入 quic-go，有些网络封 UDP。
