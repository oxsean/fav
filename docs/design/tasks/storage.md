# 存储与程序划分

`tend-server` 用 SQLite 存协调器的事件和团队实体，模式一保留 JSONL；表、事务与副作用的顺序、锁和连接、schema 迁移、导入导出备份与恢复、选型的理由；以及 `tend` 与 `tend-server` 两个程序怎么划分包和部署。实现：`internal/store`（`EventLog` 的 SQLite 实现、`Team`、`migrations/sqlite/`、import / export / check / backup）、`internal/journal`（JSONL）、`internal/coord`（`EventLog` 接口、`Options.OpenLog`）、`internal/dial`、`internal/platformcheck`、`cmd/tend-server`。

## 存储：`tend-server` 用 SQLite，模式一不变

- 模式一：协调器的状态在 `coord/events.jsonl`，一份只追加的事件日志，启动时逐行回放，状态全部放在内存里（见 [runs/coordinator.md](../runs/coordinator.md)「事件日志」）。
- `tend-server`：同样的信封存进 `coord/tend.db`（`internal/store`），启动时同样全量回放，没有快照；用户、身份、准入规则、邀请、凭据和审计在同一个库的团队表里（`store.Team`）。
- 会话收藏是 JSONL；模式一的定义是文件。

多人以后，下面这些都需要数据库：

- 唯一约束和事务：用户、身份、外部 issue 映射、邀请、会话；
- 评论、同步记录、审计会持续增长，不能每次启动都全量回放；
- 按人过滤、网页多人同时分页。

这些全是模式二的需要。**所以只有 `tend-server` 用 SQLite，模式一保留 JSONL。**

- 纯 Go 驱动会让程序多出约 4.7 MB（modernc，strip 之后）。只有 server 承担这笔代价；每台机器上的 `tend` 不变。
- `coord` 通过 `EventLog` 接口选后端（见 [protocol.md](protocol.md)「各部分的位置」）。`tend-server` 传 `Options.OpenLog`；同一套 `internal/coord` 测试跑两遍，第二遍 `TEND_TEST_LOG=sqlite`，gate 里两遍都跑。

**原则：Go 的 `task.State.Apply` 仍是唯一的折叠。**

- 不用 SQL 再实现一遍 `observe` 的单调规则、abandoned 的例外、answers 剪枝、sends 收尾。
- 否则会和 TUI、测试、`journal verify` 用的折叠分叉，而等价测试只能事后才发现。

## 表

`<home>/coord/tend.db`：

| 表 | 内容 |
|---|---|
| `envelopes` | `seq`（主键）、`v`、`at`、`actor_kind`、`actor_id`：一个 seq 一个信封，和 JSONL 一样；v1 信封没有 actor，`actor_kind` 为空 |
| `events` | `seq, idx, project, type, data`：一个信封里的多个事件，按 `(seq, idx)` 排序；`project` 列暂时留空，项目在推送时按当前状态解析（见 [team.md](team.md)「权限：两层，外加三种归属」），等要按项目导出或分页时再填 |
| `receipts` | `(principal, command_id)` 主键，另有 `seq`（唯一）、`method, digest, result`：`principal` 就是 `actor_id`；完整保存第一次的结果；成功但没有产生变化的命令不写收据。读信封时按 `seq` 拼回 `command` |
| `imports` | `source, sum, last_seq, at`：导入过的日志，按内容摘要识别重复导入 |
| `snapshots`（未实现） | `seq, state`：Go 状态的 JSON；启动 = 读最新快照 + 回放其后的信封。等实测启动变慢再加 |
| 团队实体 | `users`（含内置 `local`）、`identities`（`(provider, issuer, subject)` 唯一）、`admits`、`invites`、`credentials`（`web / token / node`，只存 sha256，节点名在未吊销的凭据里唯一）、`audit`：迁移 `0002_team.sql` |
| 团队实体 | `deliveries`（通知投递，0003，0009 起是发件箱）；`trackers`、`tracker_issues`、`tracker_deliveries`（工单绑定、每个 issue 的回写状态、webhook 去重，0004） |
| 加列 | `tracker_issues.parent`、`pr`（子 issue 和 PR 链接，0005）；`invites.project`、`access`（邀请带项目，0006）；`tracker_issues.synced`（任务级同步状态，0007）；`tracker_issues.applied`（tend 自己做的关单或标签，任务重开时撤回，0010） |
| server 自己的密钥 | `secrets`（`name` 主键、`value`、`at`，0008）：值是 `seal.go` 封存过的，密钥是 `server.key` 或 `TEND_SERVER_KEY`，不在库里；先写的赢（`KeepSecret`）。有 Web Push 的密钥对 `vapid` 和推送按钮令牌的签名密钥 `act` |
| 推送设备与发件箱 | `push_devices`（0009）：`id`、`user_id`、`kind`（现在只有 `webpush`）、`target`（`seal.go` 封存的 endpoint + `p256dh` + `auth`）、`target_hash`（endpoint 的 sha256，唯一，续期时凭它认出同一个浏览器）、`name`、`created_at`、`renewed_at`、`last_ok_at`、`failures`，以及 0011 加的 `prefs`（这台设备要哪些通知，JSON，`{}` 是默认）和 `credential_id`（登记或最近续期它的凭据，设备只在它有效时推送；0011 之前登记的设备没有，下一次 `sweep` 去掉，页面打开时重新登记），见 [team.md](team.md)「人在任务里」的实现。`deliveries` 在 0009 重建成发件箱（SQLite 改不了主键，拷表）：`id`（rowid）、`seq, user_id, event, device_id`（空是个人 webhook）、`status`（`pending` / `ok` / `gone` / `failed` / `canceled`）、`attempts`、`next_at`、`notice`（通知的 JSON，不含收件人）、`result`（最后一次的 HTTP 码，或错误的类别，如 `dial: connect: connection refused`、`Post: timeout`；不记地址，推送 endpoint 的路径就是设备的密钥；0011 把之前记下的带地址的改写成类别：超时、连接被拒、找不到主机，其余 `Post: unreachable`，行本身不动）、`at`；`(seq, user_id, event, device_id)` 在 `seq > 0` 时唯一（server 自己的通知 seq 是 0，每次都投）；0009 之前的行照拷，2xx 记 `ok`，其余记 `failed`，都不再投 |
| 团队实体（未实现） | `comments, inbox_reads`；agent 定义、项目、成员和分享是事件，不另建表 |

- tasks 和 runs 不做 SQL 投影，状态在内存里，`state.get` 从内存出。等内存或分页真成了问题，再加存 JSON 的投影表，另加少量索引列（`id, project, status, machine, updated_seq`）；投影随时可以丢掉、从事件重建。
- 易变的观察（`last`、运行中的用量）不移出事件：一个 3 步的 fake run 是 `run_observed` × 3，6 步带一次权限请求的是 × 6，量级和步数相当。等真实 agent 的长 run 让启动回放明显变慢，再改放 live 表。

## 事务和副作用的顺序

1. 先在状态副本上验证并应用这次变更；
2. 在一个事务里写信封、事件、收据和实体表，然后提交；
3. 提交成功后，才替换内存状态、发布推送。

- 外部动作（调节点、调工单 API、发通知）都在事务之外做，靠持久化的意图重试：`run_queued`，按状态对账的工单回写（见 [trackers.md](trackers.md)「回写」），以及通知的发件箱（`deliveries`，见 [team.md](team.md)「人在任务里」的实现）。发件箱的行在信封提交之后、仍在协调器的锁里写，是同一个库上紧接着的第二次本地提交：进程恰好死在两次提交之间，这一条通知就丢了，和 `synchronous=NORMAL` 下断电丢最后几个信封同一个口径，不在启动回放时补算。
- 「提交之后、更新内存之前」崩溃，重启回放就能恢复。
- 从事件重建投影时，不触发通知，也不派发。

## 锁和连接

- 保留 `coord/lock`，它决定谁是唯一的写者；拿到锁之后才打开数据库。
- 写连接只有 1 个，事务用 `BEGIN IMMEDIATE`；`journal_mode=WAL`、`synchronous=NORMAL`、`busy_timeout=5000`、`foreign_keys=1`。读走另一个 `query_only` 的连接池，按 256 个信封一批、每批一个短读事务。
- `synchronous=NORMAL` 下，进程崩溃不丢已提交的信封，断电可能丢最后几个；节点那边的 run 仍在，重连对账能补回观察。
- 数据库只放在本地盘或容器自己的 named volume 上，不 bind mount 到宿主的 macOS 目录（WAL 的 `-shm` 在这类挂载上不可靠）。
- 驱动用 `modernc.org/sqlite`。`hosts install` 和 release 都用 `CGO_ENABLED=0` 交叉编译，纯 Go 是硬性要求。
- schema 版本记在 `PRAGMA user_version`：
  - 迁移文件 `migrations/sqlite/NNNN_*.sql` 嵌入二进制，编号必须连续，每个文件一个事务；
  - 已有 schema 的库升级前自动 `VACUUM INTO` 一份 `tend.db.v<旧版本>-<时间>.bak`；
  - 遇到比自己新的 schema 只读打开，拒绝写入；回放时 seq 断号或折叠失败也只读，和 JSONL 的 `ErrReadOnly` 一样；
  - 需要真正迁移的只有 `envelopes/events/receipts` 和团队实体表，快照和投影可以直接重建。

## 迁移

从模式一搬到 server：

- `tend-server import [events.jsonl]`（默认是本目录的 `coord/events.jsonl`）：拿 `coord/lock`，所以 server 必须先停。
  - 整个导入是一个事务：逐行校验和、seq 连续、每个信封都能折叠，全部通过才提交，中断或失败都不留半截。
  - `imports` 表记下来源的摘要和末尾 `seq`：同一份日志再导一次什么都不改；库里已有别的信封就拒绝（`ErrNotEmpty`）。
  - 导入本目录的日志后把它改名为 `events.jsonl.imported-<时间>`。
- `coord/events.jsonl` 里有事件、而 `tend.db` 还不存在时，`tend-server` 拒绝启动，提示先 import。
- 不做「JSONL 和 SQLite 同时是权威」的双写。

## 检查、导出、备份

- `tend-server db check [--json]`：`quick_check`、schema 版本、`seq` 连续、每个信封都能折叠、没有无主的事件和收据（有快照以后再加「重新折叠和快照比对」）。
- `tend-server db rebuild`：从事件重建快照和投影，有了快照和投影再做（未实现）。
- 坏了就只读、不派发，和 `ErrReadOnly` 规则一样。
- `tend-server export [-o 文件]`：按 JSONL 的信封格式写出（带 `sum`，编码和 `journal.Line` 同一个函数），导入后再导出和原日志逐字节一致，导出件可以直接跑 `tend journal verify`。备份、导出、测试夹具、模式一与 server 之间的搬迁都用这一种格式。`--project` 等项目归属做好再加（未实现）。
- `tend-server backup [目录]`（默认 `<home>/backups/<时间>`）：`VACUUM INTO` 是一个读事务，所以和运行中的 server 并行就行，不用停机，也不用经过 server 进程。备份目录内含：
  - 数据库（工单凭据在里面，已加密）；
  - `coord/id`（丢了它，节点上的 run 就不再被列出）；
  - `config.json`（客户端和节点凭据在数据库里，只有哈希）。

  加密密钥单独保管，不进备份。
- `export`、`backup`、`db check` 都只读，server 运行时也能用；`import` 要停机。
- **恢复**：先以「不派发」模式启动，对账节点和工单，再放行；原实例必须停掉（「不派发」启动模式未实现）。节点的 run 目录和输出不在备份里，它们按节点的保留期清理；团队真正的成果是共享 remote 上的分支和数据库。

## 选型

- 不用 Postgres：一个小团队、上万任务，SQLite 足够；还是单个文件，不多一个要运维的服务。协调器是单写者，状态在内存里，换 Postgres 也得不到横向扩展或高可用。
  - 扩展口子：
    - 调用方只依赖 `internal/store` 提供的有类型方法，SQL 不出这个包；
    - 迁移按引擎分目录（`migrations/sqlite/`）；
    - schema 尽量不用 SQLite 独有的写法：JSON 存 TEXT，时间存整数 Unix 纳秒（信封的 `at` 要和 JSONL 逐字节一致），不用 `WITHOUT ROWID`。

    以后要加 Postgres，只是在 store 里再加一个后端，外加一个 `migrations/postgres/` 目录。
- **不用 ORM**：`database/sql` 加手写 SQL。
  - 迁移是嵌入二进制的编号 `.sql` 文件，按 `user_version` 顺序执行，不做自动迁移。
  - 查询多到手写扫描容易出错时（约 50 条以上），再考虑 sqlc：它是开发期的代码生成工具，生成的代码入库，没有运行时依赖。
- **节点不变**：run 目录、会话索引、收藏仍是文件，节点不需要数据库。

## 进程与代码划分

`tend` 和 `tend-server`：同一仓库、同一 Go module，出两个程序。

| 程序 | 包含 | 装在哪 |
|---|---|---|
| `tend` | CLI、TUI、节点、会话索引、模式一协调器（JSONL） | 所有机器（`tend hosts install`） |
| `tend-server` | 模式二：HTTP / WebSocket、网页、身份与权限、OAuth / OIDC、SQLite、webhook、工单同步 | 只装在 server |

**为什么拆**：拆分省不下体积（网页资源只有 116 KB；`tend` 是 19 MB，strip 后 13.5 MB，体积几乎全是 Go 运行时和其余各包）。要的是依赖隔离和部署边界：OAuth、OIDC、SQLite、工单客户端、webhook 都不应该进每台机器上的 `tend`。

- 客户端拨号（`Dial`、`ReadToken`、`Connect`）和公共常量（`RoleNode` 等）在 `internal/dial`，`tend node --connect`、TUI、CLI 用它；`connectCoord` 经它连 server。`internal/server`、`internal/auth`、`internal/tracker`、`internal/store` 只被 `cmd/tend-server` 引用。
- `platformcheck` 用 `go list -deps ./cmd/tend` 检查整个传递闭包，断言里面没有 `internal/server`、`internal/auth`、`internal/tracker`、`internal/store`、`modernc.org/sqlite`。只查直接 import 不够。
- 协调器（`internal/coord`）和任务模型两边共享：模式一的协调器在 `tend` 里。
- `tend-server` 的子命令是 `serve`、`token`、`admin`、`import`、`export`、`backup`、`db`、`version`、`help`，没有 `install`。`tend-server token *` 管节点和客户端凭据，网页上的「添加机器」做同样的事。`tend server` 只打印「改用 tend-server」。
- 机器列表的初始名字来自凭据表里的节点 token（`Directory.NodeNames`），没有 `machines` 表。
- **不拆仓库**：两边共享协议、协调器和任务类型；分仓得先把它们做成稳定的对外接口，现在不值得。
- **发布与部署**：
  - goreleaser 有 `tend-server` 的 build 和 archive，两边都用 `-X main.version`；
  - `tend hosts install` 只装 `tend`，它成功不代表 server 已经升级。server 用 `tend hosts install <host> --server` 单独装。
- **两边版本不同也能配合**：新方法靠 `hello.methods`，已有方法的语义变化靠 `hello.features`（见 [protocol.md](protocol.md)「版本协商」）；旧版本遇到新 schema 只读打开、不写。哪些新旧 server、节点、客户端的组合受支持，要写成明文（未写）。
