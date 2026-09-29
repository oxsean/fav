# 协议与包

任务层每一块落在哪个包、只在什么时候抽接口，以及新方法和已有方法的语义变化怎么在新旧节点、客户端之间协商。实现：`internal/task`、`internal/coord`、`internal/defs`、`internal/workflow`、`internal/node`、`internal/agent`、`internal/remote`、`internal/store`、`internal/auth`、`internal/tracker`、`internal/server`、`internal/dial`、`internal/ui/tui`、`cmd/tend`。

## 各部分的位置

| 变化 | 位置 |
|---|---|
| Task 字段、事件、推导的处境 | `internal/task` |
| 依赖派发、阶段推进、gate、预算、父任务 | `internal/coord`（`tree.go`、`workflow.go`、`work.go`） |
| Project、AgentDef、Workflow 的加载、校验、编译 | Project 是 `internal/task` 折叠的事件；AgentDef 在 `internal/defs`；Workflow 在 `internal/workflow` |
| 方法：`project.*`、`agentdef.*`、`task.start|gate|move|plan|plan_save|plan_apply|message|merge` | `internal/coord`，`Methods` 协商 |
| `run.start` 的 spec 带工作区（`agent.Workspace`：`checkout, branch, chain, base, remote, read_only, merge, setup, cleanup`）、`check`、`files`（settings、mcp、skills） | `internal/node`；节点方法变了 → `tend hosts install` |
| `node.agents` 报告各 CLI 的安装、版本、登录，不报告 skills、MCP 名字（见 [agent-definitions.md](agent-definitions.md)「skills、MCP 和密钥怎么到目标机器」） | `internal/node`、`internal/agent` |
| `tend run verdict`、`tend run plan` | `cmd/tend` |
| 看板、树、首页、定义编辑 | `internal/ui/tui`、`internal/server/web` |
| 事件日志接口 `EventLog`（`Append / ReadAfter / Seq / ReadOnly / Close`），由 `coord.Options.OpenLog` 打开，打开时按序把每个信封交给折叠；不给就是 JSONL | `internal/coord`；JSONL 实现在 `internal/journal`（模式一），SQLite 实现在 `internal/store`（只被 `tend-server` 引用；见 [storage.md](storage.md)）。收据不单独存取，回放信封时由协调器重建 |
| 信封带 `actor`：信封版本 `V` 为 2，回放 v1 信封时 actor 视为 `system` | `internal/journal` |
| 调用者 `Principal`、方法授权表、可见性过滤 `visibleState` / `sees` | `internal/coord`（`access.go`；见 [team.md](team.md)「权限：两层，外加三种归属」） |
| 身份、会话、邀请、OAuth / OIDC | `internal/auth`，以及 `internal/server` 的 `/auth/*`（只被 `tend-server` 引用；见 [team.md](team.md)） |
| Gitea / GitLab / GitHub 适配、webhook、同步 worker | `internal/tracker`（只被 `tend-server` 引用；见 [trackers.md](trackers.md)）。接口覆盖 issue、评论、标签、关单和 PR / MR；各家能力不同（比如子 issue）时在 `internal/tracker` 里消化；用 `Caps` 声明能力、调用方按能力降级未实现 |
| 通知：收件箱、浏览器、个人 webhook、工单 @提及、`notify_command` | 协调器只出 `Options.Notice func(Notice)`；个人 webhook 的投递 `Notifier` 在 `internal/server`，投递记录和去重在 `deliveries` 表（见 [team.md](team.md)「人在任务里」） |
| git 操作：worktree、fetch / push、合并、冲突检测 | 节点在 `internal/node/work.go` 里包一层 exec 直接调 `git`，没有单独的包，`platformcheck` 不管（见 [execution.md](execution.md)） |
| 团队实体的读写 | `internal/store` 对外只给有类型的方法（例如 `Team.Users`、`Team.AddTracker`），SQL 不出这个包 |
| 客户端拨号（节点、TUI、CLI 连 server） | `internal/dial`（见 [storage.md](storage.md)「进程与代码划分」） |
| `hello.features`：节点报告自己支持的执行语义 | `internal/remote`、`internal/coord`（下文） |

Web 在客户端折叠信封（`web/core/fold.js`），规则逐条照搬 `task.State.Apply`，用 Go 生成的同一批信封和随机日志对照测试（`internal/server/fold_test.go`、`foldfuzz_test.go`，需要 node）。

## 抽象原则

- 只有已经有、或马上会有第二个实现时才抽成接口（`EventLog`、`Tracker`）。
- 只有一个实现的外部命令（Herdr、git）包一层薄 exec，把平台差异和测试替身集中在一处。
- 不为 Postgres、Tailscale 身份、KMS 预先抽象。Tailscale 身份以后可以作为只在 tailnet 部署启用的登录 provider 加进来。

## 版本协商

帧和握手的通用规则见 [runs/wire.md](../runs/wire.md)「握手与版本」。任务层在它之上：

- wire 帧的形状不变，`wire.Proto` 不需要 bump。
- 新方法靠 `hello.methods` 协商。
- **已有方法的语义变化要靠 `hello.features` 协商**：给 `run.start` 加工作区、hooks、文件这类字段就是例子。旧节点会静默忽略这些字段，在错误的目录里开跑，而它的 `methods` 并没有变，协调器察觉不到。
  - 协调器在 `run.preview`、派发、重连时都检查所需的 feature；
  - 缺 feature 就标 `node_outdated`，不降级执行；
  - feature 名字是稳定的字符串，节点现有八个：`dispatcher` `agentdef` `verdict` `check` `worktree` `files` `plan` `before_run`；
  - 节点在 hello 里报告 `node.Features`；一个 run 需要哪些由协调器的 `runFeatures(run)` 按 run 的发起人、档案、阶段和工作区推算。每加一种执行语义就各加一个名字。
- 协调器不读客户端的 `hello.features`，不按它剥掉事件：推送对所有客户端一样，只按人过滤。
- 客户端折叠时跳过未知的事件类型，只推进 `seq`。协调器自己的折叠仍然遇到未知类型就报错。
- 执行契约（features）、事件 schema、数据库 schema 各有自己的版本规则，互不借用。
- `tend` 和 `tend-server` 版本不同时怎么配合，见 [storage.md](storage.md)「进程与代码划分」。
