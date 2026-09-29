# 协议

协调器、节点和客户端之间的协议：帧、握手与版本、`Conn`、方法、传输。当前 `wire.Proto` = 1。实现：`internal/wire`（帧、`Conn`、`Pipe`），方法的处理在 `internal/remote`（会话读取）、`internal/node`（`run.*`）、`internal/coord`（客户端方法）。

## 帧

一行一个 JSON：

```json
{"type":"req","id":7,"method":"run.start","params":{…},"command_id":"c_…"}
{"type":"res","id":7,"result":{…}}
{"type":"res","id":7,"error":{"code":"not_found","detail":"…"}}
{"type":"push","method":"journal","params":{…}}
{"type":"cancel","id":7}
```

- `type` 是帧类型：`req` 请求、`res` 应答、`push` 推送（不需要应答）、`cancel` 取消对方正在处理的请求。`res` 有 `error` 就是失败，否则成功，`result` 可以没有。帧类型写在显式的 `type` 里，不像 JSON-RPC 2.0 那样靠字段推断；字段名不缩写：开了 permessage-deflate 以后缩写最多再省几个百分点，却让日志、抓包和 `journal verify` 难读。
- `id` 在发送方内唯一，两个方向各自编号；应答按 `id` 回给等待者，可乱序。
- `command_id`：写操作的幂等键（见 [coordinator.md](coordinator.md)「命令与收据」）。
- 错误码稳定、不本地化；另有 `busy`、`conflict`、`unauthorized`、`canceled`。
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

- 写：单写锁，写超时（30 s）断开连接。
- 读：读协程永不阻塞；`req` 交给处理器（最多 16 个同时处理，待处理超过 64 个回 `busy`）；`res` 找等待者；`push` 回调；`cancel` 取消请求的 `ctx`。`ping` 的应答和 `busy` 应答不占处理槽位：处理器全满时 `ping` 照样回（它证明的是连接，不是处理器）。
- 调用超时：发 `cancel`，连接保留。连接断开：所有等待者返回 `closed`。
- 对方关了发送方向（EOF）：已读到的请求答完再关，最多等 1 分钟（`drainWait`）；`Done()` 在这之后才触发，所以一个还在答的慢调用（如正在探测 CLI 的 `node.agents`）会把被顶掉的连接多拖几秒。
- 保活：空闲 30 s 发 `ping`，60 s 没有任何帧就断开（节点侧同样执行，Mac 睡眠后远端 `node --stdio` 会自己退出）。

## 方法

| 方向 | 方法 |
|---|---|
| 任意 → 节点：会话读取 | `hello` `list` `messages` `text` `steps` `pulse` `checks` `live` `echo` |
| 协调器 → 节点：run | `run.start{spec}` `run.resume{spec, resume}`（同一会话续跑） `run.stop{run, coordinator}` `run.list{coordinator, ack, runs}` `run.tail{run, before, n, file}` `node.agents{fresh}`（各 agent 命令行的安装、版本、登录） `run.answer{run, answer}` `run.send{run, send}`（见 [node.md](node.md)「双向流（stream）」） |
| 节点 → 协调器 | push `node.changed`（有 run 状态变了，协调器随后 `run.list`） |
| 客户端 → 协调器 | `state.get{no_briefs?}` `task.get{id}` `task.create` `task.edit` `task.set_status` `run.dispatch` `run.preview`（同 dispatch 参数，只回怎么跑、为什么跑不了） `run.continue{run \| session…, text}` `run.answer{run, request, allow, message, answers}` `run.send{run, text}` `run.stop` `run.abandon` `run.tail` `agent.list` `machine.list` `subscribe{after_seq}` `node.call{machine, method, params}`（机器主人和管理员） `run.messages{run, before, n, file}`（按项目权限读 run 的对话） `project.create{id?, name, owner?}`（管理员） `project.edit{id, name?, owner?}` `project.member{project, user, role}` `machine.share{machine, users, projects, approve}`（后三个：项目负责人 / 机器主人或管理员） `task.start{id}`（整棵子树按依赖自动派发） `task.move{id, parent?, after?}` `inbox.list`（等我处理的任务） `agentdef.list` `agentdef.get{name}` `agentdef.save{text, owner?}` `agentdef.remove{name}` `agentdef.share{name, share}` `user.offboard{user, to}`（管理员；`tend-server` 的 `/api/users/offboard` 调它后停用并吊销凭据） `task.source_ack{id, accept}`（需求的新版本采用或不采用，issue 外部关闭后继续） `task.gate{id, pass, notes, expected_rev}`（workflow 的人工闸门：放行或打回） `task.message{id, text, to_run}`（按任务状态路由的留言，回 `{to, run}`） `task.merge{id}`（冲突解决后重新把任务分支合进父任务的分支） `task.plan{id, agent?, machine?}`（排拆解 run） `task.plan_save{id, plan?, expected_rev}`（改草稿，不带 plan 即丢弃） `task.plan_apply{id, expected_rev}`（草稿建成 backlog 子树） `task.sync`（只有 server 进程内的同步 worker 能调） |
| 协调器 → 客户端 | push `journal`（事件信封，按人过滤，空的也发）；push `refetch`（可见范围变了，重读状态） |

新方法按 `hello.methods` 协商：节点没有 `run.send` 时回答和消息留在协调器里不投递；节点没有 `run.resume` 时续跑的 run 直接 `failed{node_outdated}`，`run.continue` 在已知时就拒绝；没有 `node.agents` 时预检回 `unchecked`。`node.call` 只转发会话读取方法。发送方不发超过 `MaxFrame` 的帧：请求直接回 `bad_request`，应答换成 `internal` 错误，连接不断。

## 传输

| 传输 | 用在 |
|---|---|
| 进程内 `wire.Pipe` | 协调器 ↔ 本机节点；TUI ↔ 自己持锁时的协调器；测试 |
| ssh 子进程 stdio | 模式一：协调器 → 远端节点（`ssh -T <别名> <tend argv> node --stdio`）；TUI 读远端会话 |
| unix socket | 客户端 ↔ 本机持锁的协调器（`paths.Socket(home)`，0600） |
| WebSocket | 模式二：节点 → server（`/node`），客户端 → server（`/client`） |

HTTP/3 / WebTransport 以后只作为可选传输加进来，不替代 WebSocket：它要引入 quic-go，有些网络封 UDP。
