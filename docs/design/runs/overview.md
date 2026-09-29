# 运行：总览

tend 的运行层：为一份任务书在这台或别的机器上启动 agent、盯到结束、接手它的会话。本文件是目标与范围、名词、总体结构、包、名字与数据目录、测试、已定决策和已核实的外部事实；各部分细节见同目录的其它文件。实现：`internal/wire`、`internal/remote`、`internal/agent`、`internal/journal`、`internal/task`、`internal/coord`、`internal/node`、`internal/proc`、`internal/server`、`cmd/tend`、`cmd/tend-server`。

## 目标与范围

**要跑通的流程**

1. 在 TUI 或 CLI 里定义 task（标题、任务书、目录）。
2. 选一台机器、一个 agent 档案（provider、模型、权限），把 task 跑起来。
3. 盯进度：排队 / 启动中 / 运行中 / 等你输入 / 已结束（退出码）/ 已停止 / 未知；看这次运行的对话或输出。
4. 接手：运行结束后在当前终端恢复那个原生会话；还在跑的，先停再接手，或者本机 Herdr 里直接切过去。
5. 两种部署都能做以上四步：
   - **模式一（TUI）**：常用机器上的协调器，经 ssh 协调各节点；
   - **模式二（server）**：部署的 tend-server，节点用 WebSocket 主动连它，节点之间不通信。
6. 只有一个名字 tend（见下文「名字与数据目录」），数据目录 `~/.agent/tend`。
7. 模式二的 Web UI：server 自己提供网页，用 client token 或网页登录，见 [clients.md](clients.md)「Web UI（模式二）」。
8. 任务与编排与团队：project、agent 定义、任务树与依赖派发、每任务 worktree 与集成分支、阶段流程（开发 → 评审 → 测试 → 验收）、需求拆解、看板与首页；模式二的团队（邀请制，GitHub 或 OIDC 登录，项目成员，agent 与机器共享，部署在 tailnet 或公网均可）和 Gitea / GitLab / GitHub 工单同步；server 是单独的程序 `tend-server`，它的协调器用 SQLite（模式一用 JSONL）。设计见 [tasks/overview.md](../tasks/overview.md)「目标与非目标」。

**不做**

- 工单字段级双向同步、多工作区、开放注册、细粒度 RBAC。
- 节点之间互传、跨机迁移会话（迁移的设计见 [sessions/migration.md](../sessions/migration.md)「迁移」，未实现）。
- pi / OpenCode / Grok / Gemini 的会话层（只用 command 模板接「运行」层）。
- `claude --bg` runner、codex 交互式 runner、Tailscale 身份认证。
- 自己的二进制帧、protobuf / gRPC 和直连数据通道：帧保持 JSON 文本（protobuf 要代码生成、二进制变大、日志没法直接看），真要压缩编码时在 `hello` 协商里加 CBOR 或二进制帧。
- 仓库和 Go module 路径改名（对外动作，由仓库主人决定）。

**计划（未实现）**

- 交接与跨机续做：run 结束时自动关联交接包和 `result.md`，「运行结束」与「成果可接手」分开；接手前预演；`tend task from-session`；`Run.Brief` 改成引用。
- Web UI 里的会话浏览（Sessions 页）。
- ACP 适配器（pi、OpenCode、Grok）。
- 定时任务先检查有没有变化再跑；现在用 cron 调 `tend run start`。
- WSL 纳入模式二（要改网络 ACL，见下文「已核实 / 待核实」）。
- TUI 经协调器读节点的会话（现在直接 ssh）。

## 名词

| 词 | 含义 |
|---|---|
| 机器 / 节点 | 能跑 agent 的一处环境：本机、`config.hosts` 里的一台，或模式二里连上来的节点。WSL、容器各算一台 |
| agent 档案 | 一种跑法：provider（claude / codex / fake / command）、模型、权限、额外参数；写了机器就只在那台跑 |
| task | 一件要做的事：标题、任务书、目录、默认机器和档案、状态 |
| run | task 在某台机器上用某个档案的一次运行；最多绑定一个原生会话 |
| 协调器 | 唯一写 task / run 状态的进程：持有 `coord/lock` 的那个 TUI、CLI、`tend service` 或 `tend-server` |
| 客户端 | TUI、CLI、`/tend` 技能、Web UI；经协议找协调器 |

## 总体结构

```
         客户端（TUI / CLI）
              │ 协议（进程内 | unix socket | WebSocket）
     ┌────────▼─────────┐
     │  协调器            │  事件日志 · 状态 · 调度 · 对账 · 转发
     └──┬──────┬──────┬─┘
        │      │      │  协议（进程内 | ssh stdio | WebSocket）
     ┌──▼─┐ ┌──▼─┐ ┌──▼─┐
     │节点 │ │节点 │ │节点 │  会话读取 · run 目录 · 监督进程 · 观察
     └────┘ └────┘ └────┘
```

- **一个核心两种部署**：协调器、节点、协议是同一份代码；两种模式只差协调器跑在哪、连接由谁发起。
- **协议是对称的**：连上之后双方都能发请求；传输只认「一条双向字节流」。
- **期望状态 + 观察事实**：协调器记「想让它跑 / 想让它停」，节点的 run 目录记「实际发生了什么」；每次连上就按快照收敛两者（按电平对账）。
- **run 不依赖协调器存活**：run 由节点上的监督进程 `tend _run` 带着；协调器不在时只是暂停派发和观察。
- **会话数据留在 tend 的 store**（`internal/tend`），run 用 `provider + 机器 + sid` 引用会话。

## 包结构

| 包 | 内容 |
|---|---|
| `internal/wire` | 协议：帧、`Conn`（多路复用、双向请求、推送、取消、保活）、`Pipe` |
| `internal/remote` | 会话读取方法挂在 `wire.Conn` 上；ssh 拨号、错误分类、`Hosts` 缓存、`Source` |
| `internal/agent` | provider 适配器：claude、codex、fake、command；档案；启动 / 恢复 / 分叉 / attach 命令 |
| `internal/journal` | 事件日志：追加、校验、续读、命令收据 |
| `internal/task` | 领域：Task、Run、事件、`Apply`、转移表、decide |
| `internal/coord` | 协调器：命令执行、幂等、订阅、调度、对账、节点连接池、转发；持锁即协调器 |
| `internal/node` | 节点：run 目录、`run.*` 方法、观察、监督进程 `_run` |
| `internal/proc` | 平台包：分离启动（Windows 加 `CREATE_BREAKAWAY_FROM_JOB`）、结束进程树（unix 进程组、Windows Job）、存活检查；这类代码只在这里 |
| `internal/paths` | socket 路径规则：路径到 100 字节（给后缀留余量；macOS 上限 104，Linux 和 Windows AF_UNIX 108）就退到 `os.TempDir()/tend-<目录 sha256 前 8 位十六进制>-<名字>` |
| `internal/ui/tui` | Tasks 视图，task / run 对话框，run 右栏 |
| `cmd/tend` | 子命令 `task` `run` `agent` `machine` `service` `node` `server` `_run` `_fake-agent` |

`platformcheck` 的规则：`Setsid|DETACHED_PROCESS|CREATE_BREAKAWAY|CreateJobObject|OpenProcess|syscall\.Kill` 只能出现在 `internal/proc`。

## 名字与数据目录

- 只有一个名字 tend：二进制 `tend`（Windows `tend.exe`），收藏技能 `/tend`，收藏命令 `tend favorite` / `unfavorite`，环境变量 `TEND_*`，远端命令配置 `config.hosts[].tend`（`tend hosts add --tend <路径>`），hook 只认 `tend*`，错误码 `no_tend`。仓库和 Go module 路径仍叫 fav。
- 数据目录：`$TEND_HOME`，否则 `~/.agent/tend`；没有回退、没有迁移命令。testkit、fixture 只隔离 `TEND_HOME`。
- 版本号来自 `git describe --tags --match "v*"`（`mise run install` 和 `hosts install` 一致），`backup/*` 等本地 tag 不影响。

## 测试

| 层 | 内容 |
|---|---|
| 单元 | `wire`（乱序、取消、断线、并发上限、`busy`、超长帧、EOF 答完）；`journal`（残缺尾行、校验失败、收据）；`task`（转移表、乱序事件）；`coord`（幂等、对账、调度、订阅交接，进程内节点 + fake）；`node`（启动权、监督进程死、停止、墓碑、双向流）；`agent`（各 provider 命令行、codex 协议逐帧）；`proc`（分离、杀树） |
| 故障注入 | 协调器在 starting 时重启、丢失的启动后又停止、节点目录消失、停止发给离线节点、断连接后重连、迟到的监督进程、停止先于监督进程、子进程占着输出。监督进程停在切点（`TEND_CRASH_AT`：claimed → not_launched；running → 停止时 `orphan_stopped`；启动时间对不上 → 不杀、仍 unknown；ending → 停止时 `supervisor_gone`；started → 仍 unknown）；Windows 上短暂读不到快照时 abandoned 的 run 仍占目录；同目录、超 slot 的启动被拒后重发 |
| 网页脚本 | `internal/server` 的测试用 node 跑页面脚本（`web/` 的 `core`、`ui`、`pages`，用例在 `webtest/`）；`mise.toml` 的 `gate` 和 `test-host` 两个任务固定 node 版本，找不到 node 或它不是 Node.js 时测试失败，不跳过 |
| fixture | `tools/test-host` 冒烟：fixture 上 `task add` → `run start --wait`（fake，background）→ `exited 0` |
| 端到端 | `scripts/tend-e2e.sh`（模式一）和 `scripts/tend-e2e-server.sh`（模式二）：节点一律是 fixture 启动器；各远端跑 fake run；检查状态流转、会话读取、停止、断线重连，各有一次远端作答加插话；模式二另查网页和登录 |
| 多平台 | `scripts/test-hosts.sh` 全量 |
| 真跑 | 在本机和一台远端 Mac 上各用 claude 最便宜的模型跑一个一句话 task，验证接手 |

## 已定决策

| 决定 | 理由或范围 |
|---|---|
| 模式一：持锁的进程当协调器，只监听本机 socket，常驻与否由用户决定；模式二：用户部署 server，开一个端口 | 模式一不给机器加常驻服务和开放端口；要多人或常驻时才用模式二 |
| 允许远程启动 / 停止 run | 操作幂等，档案和目录受节点本机约束（见 [deployment.md](deployment.md)「模式二」） |
| 会话留在 store，run 只引用会话键 | 收藏记录不迁成事件，会话层和运行层各管各的数据 |
| `--listen` 非回环、非 tailnet 地址必须同时给 TLS，另允许显式 `--plain` | `--plain` 给容器里监听、由宿主只在 tailnet 地址上转发的部署用 |
| 协议或节点方法变了就重装远端（`tend hosts install`）；帧形状变才 bump `wire.Proto` | 方法靠 `hello.methods` 协商，帧形状才需要版本号 |
| 「需要人」是 `attention` 标记，不是 run 的新状态 | 旧协调器不认识新状态值，标记只是多出的字段 |
| 通知配置是 `config.notify_command` + `notify_events` | `notify` 已是 TUI 的提示音设置 |
| 后台 claude 走双向流时放开 `AskUserQuestion`；只有非双向的无界面启动还禁它 | 双向流里提问能远程作答 |
| 后台 codex 跑 `app-server`，不跑 `exec --json` | 审批、提问、插话、中断都走协议 |
| 做 plan、依赖图、流水线阶段、自动派发下游、worktree | 设计见 [tasks/overview.md](../tasks/overview.md)「目标与非目标」 |
| 团队使用；部署在 tailnet 或公网由管理员选，公网必须 TLS | 见 [tasks/team.md](../tasks/team.md)「范围与部署」 |
| 给已有方法加一个旧节点会静默忽略的字段（如 `run.start` 的 `work`、`check`、`verdict`、`dispatcher`），就要加一个节点 feature：名字进 `node.Features`，`runFeatures` / `stageFeatures` 要求它，缺它的节点在预检和派发时回 `node_outdated`，不降级执行 | 只靠 `hello.methods` 协商时，旧节点会忽略新字段照跑，结果不对；见 [tasks/protocol.md](../tasks/protocol.md)「协议与包」 |

## 已核实 / 待核实

| 项 | 结论 |
|---|---|
| Windows 分离后 ssh 断开 | 必须 `CREATE_BREAKAWAY_FROM_JOB`（实测） |
| WSL、macOS、Linux 容器 `setsid` 分离 | 存活（实测） |
| 经 ssh 派发的 fake run（macOS、Linux 容器、WSL、Windows 远端） | ssh 连接关闭后监督进程照常跑完、写终态（`tend-e2e.sh` 实测） |
| 模式二网络 | 测试网络的 tailnet ACL 不许那台 Windows 机器主动连 server 所在的 Mac：测试里 Windows 节点经 `ssh -R` 转到它自己的回环；WSL（NAT）够不着 Windows 回环，要纳入模式二需改 ACL 或加 portproxy |
| run 会话在会话列表 | 列出，但受 `min_turns`（默认 3）过滤；任务书只算一轮，Tasks 视图直接从 run 找会话 |
| `claude --bg` | 目录未信任时拒绝（实测）；不用 |
| claude `--session-id`、`-p`、`--output-format stream-json`、`--add-dir` | `--help` 有（2.1.281） |
| codex `exec --json`、`-C`、`-s`、`resume` | `--help` 有（0.155.1） |
| claude 双向 stream-json（2.1.282） | 同一进程里：权限请求能作答；一轮进行中送的 user 消息并入这一轮；`result` 之后再送消息开下一轮；`interrupt` 让这一轮以 `error_during_execution` 结束；`result.usage` 是这一轮的、`total_cost_usd` 是累计的（实测） |
| codex `app-server`（0.155.1） | 审批请求能作答；`turn/steer` 在同一轮生效；`thread/tokenUsage/updated` 给累计用量；关 stdin 后退出码 0（实测） |
| `claude --resume` 能否恢复 `-p` 会话 | 能（实测：`-p` 跑完后 `--resume` 续上）；`codex resume` 恢复 exec 会话待核实 |
| 经 ssh 在远端 Mac 上跑 `claude -p` 能否取得凭据 | 不能：ssh 会话访问不了登录钥匙串（`User interaction is not allowed`），claude 报 Not logged in。Mac 远端要跑 claude，节点用 LaunchAgent（`tend node install-service`，`dev.tend.node`）跑在图形登录会话里，claude 在那台 Mac 的图形界面里 `claude /login` 一次（ssh 里查不到登录态） |
