# tend 设计文档

这里是 tend 的设计记录，只写当前设计：机制、规则、格式、取舍和它们的理由。没有来历、日期和进度，那些在 Git 历史里。改了行为，就在同一个提交里改对应的文件。

文档用中文写，是仓库「英文」规则的唯一例外；代码名、路径和命令保持原样。文件之间互相引用时写成链接加「标题」。

## 会话：收藏、索引、搜索、TUI、fzf、恢复、其它机器

| 文件 | 内容 |
|---|---|
| [sessions/overview.md](sessions/overview.md) | 目标、原则、范围、两种界面、目录与模块、安全与兼容、非功能指标、验收时逐条核的行为约束 |
| [sessions/favorites.md](sessions/favorites.md) | 收藏、`/tend` skill 的职责与输出协议、记录结构与读写语义 |
| [sessions/index-and-search.md](sessions/index-and-search.md) | 会话索引、查找、最新动态与对话、查询语法、搜消息（正文库、BM25） |
| [sessions/tui.md](sessions/tui.md) | 原生 TUI：布局、视觉、键盘与鼠标、Agents 页与 Herdr |
| [sessions/fzf.md](sessions/fzf.md) | fzf 模式与预览 |
| [sessions/resume.md](sessions/resume.md) | 恢复编排：provider 命令、桌面 App、分叉与交接、Herdr |
| [sessions/cli-and-config.md](sessions/cli-and-config.md) | 子命令与配置项 |
| [sessions/remote.md](sessions/remote.md) | 其它机器：远端合同、`tend hosts`、只读聚合、安装 |
| [sessions/migration.md](sessions/migration.md) | 会话与记忆的迁移、迁移前的环境诊断 |
| [sessions/external-behaviour.md](sessions/external-behaviour.md) | 依赖的外部行为：会话 id、恢复命令、transcript 位置、Herdr、skill 安装 |

## 运行：协调器、协议、节点、agent、部署

| 文件 | 内容 |
|---|---|
| [runs/overview.md](runs/overview.md) | 目标与范围、名词、总体结构、包、名字与数据目录、测试、已定决策、已核实的外部事实 |
| [runs/wire.md](runs/wire.md) | 协议：帧、握手与版本、`Conn`、方法、传输 |
| [runs/coordinator.md](runs/coordinator.md) | 协调器：持锁、事件日志、状态、命令与收据、调度与对账、订阅、通知 |
| [runs/node.md](runs/node.md) | 节点：run 目录、监督进程、观察、会话绑定、双向流 |
| [runs/agents.md](runs/agents.md) | agent 适配器 |
| [runs/deployment.md](runs/deployment.md) | 两种部署与安全边界 |
| [runs/clients.md](runs/clients.md) | CLI、TUI、Web UI |

## 任务：项目、agent 定义、流程、拆解、执行、团队、工单、存储、界面

| 文件 | 内容 |
|---|---|
| [tasks/overview.md](tasks/overview.md) | 目标与非目标、对象总览、待核实、已定决策 |
| [tasks/projects.md](tasks/projects.md) | 项目 |
| [tasks/agent-definitions.md](tasks/agent-definitions.md) | agent 定义：格式、编译、skills 与 MCP 怎么到目标机器 |
| [tasks/workflows.md](tasks/workflows.md) | workflow 与阶段、结构化结果、hooks、消息与插话 |
| [tasks/planning.md](tasks/planning.md) | 需求与拆解 |
| [tasks/execution.md](tasks/execution.md) | 执行与隔离：分支、集成、交付物、接力、护栏 |
| [tasks/board.md](tasks/board.md) | 看板与视图的数据 |
| [tasks/protocol.md](tasks/protocol.md) | 任务层的协议与包 |
| [tasks/ui.md](tasks/ui.md) | TUI 与 Web 界面 |
| [tasks/team.md](tasks/team.md) | 团队与权限、共享、审计、成员离开 |
| [tasks/trackers.md](tasks/trackers.md) | 工单同步：Gitea、GitLab、GitHub |
| [tasks/storage.md](tasks/storage.md) | `tend-server` 的 SQLite 存储、`tend` 与 `tend-server` 的划分 |
