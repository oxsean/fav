# 客户端

运行层的三个客户端：CLI、TUI 的 Tasks 视图、模式二的 Web UI。任务层加的界面（任务树、看板、首页等）见 [tasks/ui.md](../tasks/ui.md)「界面」。实现：`cmd/tend`、`cmd/tend-server`（CLI）、`internal/ui/tui`（TUI）、`internal/server/web/`（Web UI）。

## CLI

```
tend task add "标题" [--brief-file f | --brief s] [--dir d] [--machine m] [--agent a] [--project p] [--parent t] [--after t,…] [--backlog] [--workflow 名称|none]
tend task gate <id> --pass | --rework "意见"
tend task merge <id>                # 在父任务工作树里解决冲突后重新合并
tend task plan <id> [--agent a] [--machine m] | draft <id> [--save f | --apply | --discard]
tend task message <id> <text> [--to-run]
tend task list [--all] | show <id> | edit <id> … | done <id> | reopen <id> | cancel <id> | start <id> | move <id> [--parent t] [--after t,…]
tend run start <task> [--machine m] [--agent a] [--wait] [--force] | stop <run> | abandon <run> | list [--task t] | show <run> | logs <run> [-f]
tend run continue <run> <text> | --session <id> <text> [--file f] [--agent a] [--wait]
tend run ask|note <text>            # 只在 run 里用（读 TEND_RUN_DIR）
tend run verdict pass|rework|blocked <摘要>   # 只在 run 里用：评审或测试阶段的结论
tend run note --pr <url>            # 只在 run 里用：报告成果所在的 PR
tend run plan <file|->              # 只在拆解 run 里用：校验并提交计划
tend run answer <run> [--request id] --allow | --deny [--message m] | --answer [问题=]回答…
tend run send <run> <text> [--file f]
tend agent list | defs | import <file> [--project p] | export <name> [-o f] | check <file> | rm <name> | share <name> [--users u,…] [--projects p,…] [--all] [--view]
tend machine list
tend service | tend node --stdio | --connect URL --token-file f | node install-service --connect URL --token-file f [--print] | uninstall-service
tend-server [--listen addr] | token add --node n | --client n [--owner u] | token rm n | token rebind n | token list
tend-server admin add <email | 域名 | provider:username> [--role admin|member] | rm v | invite [--role r] | disable u | enable u | role u r | audit | list
tend-server import [file] | export [-o file] | backup [dir] | db check [--json]
tend inbox [--json]                 # 所有机器上需要你的 run：在等、运行中提问、无进展、失败、结局不明；等得最久的在前
tend journal verify [--json] | repair [-y]
```

- `tend run start` 派发前先打印 `run.preview`：将在哪台机器、用哪个档案、在哪个目录跑，CLI 的检查结果、blockers 和 notes；有 blocker 就不派发（`--force` 例外）。`tend run show` 给出原因（`render.RunReason` + 原话）、进展、最后一句话、用量、提问、工作树结果、下一步（`render.RunHint`），以及等着的请求和消息的状态。
- `tend doctor` 有一节协调器：日志条数、末 seq、损坏时提示 `tend journal repair`，未结束的 run 数和需要你的 run 数。
- 「需要你」（`Run.NeedsYou`，`State.NeedsYou` 只取未完成 task 的最近一个 run）：结束在等（asked / permission）、failed、unknown、非 0 退出，或运行中 asked / stalled；按开始需要人的时间（结束时间，否则开始时间）升序。它在 CLI 是 `tend inbox`，TUI 是列表置顶加计数，Web 是可点的计数、筛选和「等你」页。
- CLI 一次性协调器：回答前先对账，最多等 2 s（本机和复用的 ssh 够用，连不上的机器不等）；这条命令写过东西（经 `Client.CallCommand`）才在退出前 `Settle`（最多 30 s），否则立即放锁。

## TUI

- Tasks 视图：第 5 个 tab（`5`）。一行一个 task：状态图标、标题、最近 run 的状态和机器；筛选行位置显示谁在协调、每台机器的连接和负载。需要你的 task 排最前（等得最久的在前、图标换成警示），然后是未完成、已结束；标题带「N 个等你处理」。
- 键：`w` 新建 task（表单：标题、目录、机器、档案、任务书）；`e` 编辑；`x` 标完成 / 重新打开；Enter 打开 task 对话框：`跑起来`（选机器、档案，Enter 执行）、`停止`、`放弃`（已开始的 run）、`接手`、`查看会话`；`X` 停止当前 run（确认）；`Space` 到会话视图看最近一次 run 的对话（现有 probe）。表单和运行对话框的键在键表里（scope `inTaskForm` / `inTaskRun`），← → 改选项，Space 不改。
- 编辑表单打开前先 `task.get` 取整条 task，保存时只提交改过的字段；标题上限 1 KiB，任务书 256 KiB。模式二的新建表单不预填本机目录。
- 右栏：任务书、最近 4 次 run、最近一次 run 的输出（`run.tail`，经 `render.RunOutput` 读成可读的行再 `render.Sanitize`：claude stream-json 和 codex app-server / exec JSON 各取一句话，协议自己的请求、应答和半截文字不显示；前缀 `> ` 用户、`+ ` 工具调用、`$ ` 命令、`~ ` 改动的文件、`- ` 系统事件、`= ` 结果、`! ` 错误；原始行用 `tend run logs` 看）；最近一次 run 下面是原因（`render.RunReason` + 原话）、提问或进展、下一步（`render.RunHint`）。
- 状态文字后面带 attention（等你回复 / 等你批准 / 长时间没有输出），图标换成警示。
- 运行对话框打开和改选项时调 `run.preview`，在目录下面列出检查结果、blocker（红）和 notes；不拦 Enter。
- 作答：运行中的 run 有请求时 task 对话框的主按钮是「回答」，打开回答对话框（scope `inTaskRun`）：权限请求显示工具和摘要，按钮「允许」（主）/「拒绝」/ 取消；提问每个问题一个选项选择器（← → 改选项，Tab 在问题和按钮间移动），按钮「回答」（主）/「不回答」/ 取消。running 的 stream run 另有「发消息」按钮，打开和回复同样的多行输入框，发 `run.send`。右栏和对话框列出等着的请求和最近两条消息的状态；运行中的 run 显示最后一句话，结束后显示用量。
- 回复：run 在等时 task 对话框的主按钮是「回复」，别的已结束且有会话的 run 也有「回复」按钮；回复框是多行输入（scope `inTaskForm`：Enter 换行、Ctrl+S 发送、Tab 到按钮、Esc 放弃），发 `run.continue`。
- 接手：打开该会话的恢复对话框；run 还在跑时对话框提示先停（`resume.check.running_run`）。
- 状态：视图打开时连协调器（连 socket，没人持锁就自己持锁），先 `state.get`，再 `subscribe{after_seq}`，之后把推来的日志信封按 seq 折进状态；seq 断档或推送通道（512）溢出 → 下一拍重新 `state.get`。机器每 5 s `machine.list`，选中 run 的输出每 2 s `run.tail`。连接结束（协调器退出、保活超时）就重连并重新订阅；退出 TUI 时放锁，run 照常跑。
- 协调器不可用（别的进程持锁且 socket 不通）：Tasks 视图显示原因，其它视图不受影响。

## Web UI（模式二）

页面的合并和改名（计划）见 [ui.md](../tasks/ui.md)「页面结构与用词（计划）」。

- 文件：`internal/server/web/`（`index.html`、`app.css`、`api.js`、`fold.js`、`team.js`、`tree.js`、`home.js`、`look.js`、`palette.js`、`app.js`），`go:embed` 进二进制，无构建步骤、无外部依赖。
  - `api.js` 是唯一的通信层（wire 帧走 `/client` WebSocket，每条请求 30 s 超时后发 `cancel`，应答 server 的 `ping`）。
  - `fold.js` 把推送的信封折进页面的状态（照搬 `task.State.Apply`，`fold_test.go` 用 Go 生成的信封对照）。
  - `team.js` 是团队相关的页面（登录方式、项目与成员、机器的主人与分享、账号、管理）。
  - `tree.js` 是任务树和它要用的页面（每个任务的处境、父子与依赖、开始和调整位置、「等你」收件箱、agent 定义、项目设置、个人 webhook 和浏览器通知、交接并停用）。
  - `home.js` 是首页和 runs 页。
  - `look.js` 是这位访客的皮肤、强调色、对比度、密度和改它们的设置页。
  - `palette.js` 是唯一的操作表，`⌘K` 命令面板、按键、快捷键页和按钮上的键提示都来自它。
  - `app.js` 是其余的页面。
  - 界面文字在它们自己的 `zh` / `en` 表里（不走 Go 的 i18n）。
- 认证：登录页列出 `/auth/logins` 给的登录方式（GitHub、OIDC），另有 token 表单。`POST /login`（表单 `token`）建一条网页会话，写 cookie `tend_session`（HttpOnly、SameSite=Strict、TLS 下 Secure、30 天）；`GET /session` 回 `{id, name, email, username, role, session}` 或 401；`POST /logout` 吊销这条会话。地址里的 `#invite-<secret>` 让登录按钮带上邀请，`#signin-<结果>` 显示登录失败的原因。`/client` 接受 header 里的 token 或这个 cookie；WebSocket 握手校验 Origin（同源）。会话被吊销或用户被停用，连接立即断开，页面回到登录页。
- 响应头：CSP `default-src 'self'`（不允许内联脚本和 `style` 属性，宽度等动态样式由脚本经 CSSOM 设置）、`frame-ancestors 'none'`、`nosniff`、`no-referrer`、`Cache-Control: no-cache`。
- 数据：进入时 `state.get{no_briefs}` + `machine.list` + `agent.list`，然后 `subscribe{after_seq}`；推送的信封由 `fold.js` 折进状态，50 ms 去抖后重绘；seq 断档、折叠遇到不认识的对象、或收到 `refetch` 推送时，100 ms 去抖后重新 `state.get{no_briefs}`（`refetch` 另外重读机器）。改过任务书的 task 丢掉缓存；任务书用 `task.get` 按需取。机器每 5 s `machine.list`；选中 run 的输出每 2 s `run.tail`（`max` 64 KiB）；对话用 `run.messages`（每页 40 条，按时间正序显示，往前翻页）。
- run 详情：等着的请求各一个表单（权限：工具和摘要，「允许」/「拒绝」；提问：每个问题一组单选，「回答」/「不回答」；选中的选项在重新渲染时保留），running 的 stream run 有发消息框（草稿按 run 存），最近三条消息和状态；运行中显示最后一句话，有用量就显示用量。其余：attention 徽章、原因和原话、提问（Markdown）或进展、下一步；已结束且有会话、任务没有未结束 run 时有回复框（草稿按 run 存在内存里，重新渲染不丢、保持焦点），发 `run.continue`。列表行多一个 attention 徽章。「待处理」计数 = 需要你的 task 数（同上文「CLI」里的定义），点它把 run 筛选设为「等你处理」；需要你的 task 排在列表最前、等得最久的在前。
- run 的输出按 provider 归一成时间线（`app.js` 的 `normalizeOutput`，`output_test.go` 用 node 跑它）：codex app-server 的 JSON-RPC 行取 `item/completed` 的 userMessage / agentMessage / reasoning 摘要 / commandExecution（带退出码）/ fileChange / mcpToolCall，外加审批请求、警告、线程和轮次开始；`item/started`、各种 delta 和 `hook/` `mcpServer/` `account/` `remoteControl/` `serverRequest/` `thread/status/` 丢弃；不认识的行和 JSON-RPC 错误原样显示。结果行的 token 取最后一条 `thread/tokenUsage/updated`，input 扣掉 `cachedInputTokens`、cached 单列（codex exec 的 JSON 同样）。
- 派发框：选好机器和档案后调 `run.preview`，列出 blockers 和 notes（不禁用提交）。
- 写操作都带 `command_id`（每次打开对话框生成一个，重试沿用）：新建、编辑（先 `task.get`，只提交改过的字段）、派发（`runner=background`）、停止、放弃（只对 unknown 的 run 提供）、完成（有进行中的 run 时先确认）、重开、取消。
- 断线：横幅提示、写操作禁用，1 s 起翻倍到 30 s 带抖动重连，重连后重新取全量；401 回登录页。
- 任务树：列表按父子缩进（最多三层），行上有处境徽章（`Fold.situation`，没开始的手动任务不标）；详情有处境、父任务、前置任务、子任务、验收标准、负责人与验收人，「开始」和「调整位置」；新建任务可以选父任务、前置任务、验收标准，或放进待办。地址 `#task-<id>` 打开对应任务（webhook 里的链接就是它）。「等你」计数在每次推送后 300 ms 去抖重读 `inbox.list`。
- 工单同步（见 [tasks/trackers.md](../tasks/trackers.md)「工单同步」）：项目卡片的「工单同步」列出绑定和状态，能绑定 Gitea 仓库、重新同步、换凭据、解绑（`/api/trackers*`）；需求任务在列表里标「需求」，详情显示来源 issue 和版本，有新版本时可以「采用新版本 / 维持本轮范围」，issue 在外面关掉后可以「继续做」或取消。
- 布局：任务列表 + 详情（输出 / 对话 / 任务书 / 运行记录）、「等你」页、agent 定义页、机器页（主人、分享、添加机器、机器凭据）、项目页、账号页、管理页（只有管理员）；新建任务可以选项目；键全部来自 `palette.js` 的操作表（`?` 列出全部）；850 px 以下侧栏变横条，680 px 以下列表和详情分屏、隐藏键帽。
