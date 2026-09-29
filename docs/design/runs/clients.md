# 客户端

运行层的三个客户端：CLI、TUI 的 Tasks 视图、模式二的 Web UI。任务层加的界面（任务树、看板、首页等）见 [tasks/ui.md](../tasks/ui.md)「界面」。实现：`cmd/tend`、`cmd/tend-server`（CLI）、`internal/ui/tui`（TUI）、`internal/server/web/`（Web UI）。

## CLI

```
tend task add "标题" [--brief-file f | --brief s] [--dir d] [--machine m] [--agent a] [--project p] [--parent t] [--after t,…] [--backlog] [--workflow 名称|none]
tend task gate <id> --pass | --rework "意见"
tend task merge <id>                # 在父任务工作树里解决冲突后重新合并
tend task plan <id> [--agent a] [--machine m] | draft <id> [--save f | --apply | --discard]
tend task message <id> <text> [--mode steer|after|interrupt]
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
- 右栏：任务书、最近 4 次 run、最近一次 run 的输出（`run.output.watch` 推来的事件，经 `render.RunOutputLines` 读成可读的行再 `render.Sanitize`：前缀 `> ` 用户、`+ ` 工具调用、`$ ` 命令、`~ ` 改动的文件、`? ` 提问和审批、`- ` 警告、`= ` 结果、`! ` 错误和失败的调用的最后一行；思考、系统行和成功的结果不显示；摘要略掉的行数、文件数、问题数由 i18n 写出；原始行用 `tend run logs` 看）；最近一次 run 下面是原因（`render.RunReason` + 原话）、提问或进展、下一步（`render.RunHint`）。
- 状态文字后面带 attention（等你回复 / 等你批准 / 长时间没有输出），图标换成警示。
- 运行对话框打开和改选项时调 `run.preview`，在目录下面列出检查结果、blocker（红）和 notes；不拦 Enter。
- 作答：运行中的 run 有请求时 task 对话框的主按钮是「回答」，打开回答对话框（scope `inTaskRun`）：权限请求显示工具和摘要，按钮「允许」（主）/「拒绝」/ 取消；提问每个问题一个选项选择器（← → 改选项，Tab 在问题和按钮间移动），按钮「回答」（主）/「不回答」/ 取消。running 的 stream run 另有「发消息」按钮，打开和回复同样的多行输入框，发 `run.send`。右栏和对话框列出等着的请求和最近两条消息的状态；运行中的 run 显示最后一句话，结束后显示用量。
- 回复：run 在等时 task 对话框的主按钮是「回复」，别的已结束且有会话的 run 也有「回复」按钮；回复框是多行输入（scope `inTaskForm`：Enter 换行、Ctrl+S 发送、Tab 到按钮、Esc 放弃），发 `run.continue`。
- 接手：打开该会话的恢复对话框；run 还在跑时对话框提示先停（`resume.check.running_run`）。
- 状态：视图打开时连协调器（连 socket，没人持锁就自己持锁），开 `state.watch`（带任务书，搜索要查），推送由 `coord.StateFold` 折进状态（见 [coordinator.md](coordinator.md)「订阅」）：`lagged` 按副本的 seq 重开，副本接不上时不带 `after_seq` 重开。机器由 `machines.watch` 推来（协调器没有这个方法时读一次 `machine.list`），agent 列表每次连上读一次；视图没有定时轮询。选中任务的最近一次 run 和「盯在旁边」的 run 各开一个 `run.output.watch`，不再显示的就取消；带 `key` 的事件原地替换前一条（agent 正在写的消息就这样一段段长出来，最终的那条换上去），没有 `text` 的临时事件把它去掉，每个 run 最多留 1000 个事件；流结束（`done`）就不再开，断了（机器离线等）过 5 s 在下一次更新时从游标重开，`unauthorized` 丢掉这个 run 的输出。连接结束（协调器退出、保活超时）时，视图开着就马上重连并重新订阅，否则等下次打开；退出 TUI 时放锁，run 照常跑。
- 协调器不可用（别的进程持锁且 socket 不通）：Tasks 视图显示原因，其它视图不受影响。

## Web UI（模式二）

页面的合并和改名（计划）见 [ui.md](../tasks/ui.md)「页面结构与用词（计划）」。

- 文件：`internal/server/web/`（`index.html`、`app.css`、`api.js`、`fold.js`、`team.js`、`tree.js`、`home.js`、`look.js`、`palette.js`、`app.js`），`go:embed` 进二进制，无构建步骤、无外部依赖。新界面的底座（`core/`、`vendor/`）见下文「新界面的底座」。
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
- 数据：进入时 `state.get{no_briefs}` + `machine.list` + `agent.list`，然后 `state.watch{after_seq, no_briefs}`（`api.watchState`）；推送的信封由 `fold.js` 折进状态，50 ms 去抖后重绘；seq 断档或折叠遇到不认识的对象时，100 ms 去抖后重新 `state.get{no_briefs}`。流推来一份新快照（续传不了，或 `reset`）时整份换上，并重读机器；`lagged` 时按当前 seq 重开。改过任务书的 task 丢掉缓存；任务书用 `task.get` 按需取。机器每 5 s `machine.list`；选中 run 的输出每 2 s `run.output.page`（最后 200 个事件，同一份日志时从上次的 `to` 接上，一页装不下就往前补页；「原始行」时带 `raw`）；对话用 `run.messages`（每页 40 条，按时间正序显示，往前翻页）。
- run 详情：等着的请求各一个表单（权限：工具和摘要，「允许」/「拒绝」；提问：每个问题一组单选，「回答」/「不回答」；选中的选项在重新渲染时保留），running 的 stream run 有发消息框（草稿按 run 存），最近三条消息和状态；运行中显示最后一句话，有用量就显示用量。其余：attention 徽章、原因和原话、提问（Markdown）或进展、下一步；已结束且有会话、任务没有未结束 run 时有回复框（草稿按 run 存在内存里，重新渲染不丢、保持焦点），发 `run.continue`。列表行多一个 attention 徽章。「待处理」计数 = 需要你的 task 数（同上文「CLI」里的定义），点它把 run 筛选设为「等你处理」；需要你的 task 排在列表最前、等得最久的在前。
- run 的输出是 `run.output.page` 给的事件（[output.md](output.md)「运行输出」），`app.js` 的 `renderEvents` 画成时间线：调用和它的结果（按 `ref`）合成一张卡，失败的卡默认展开；codex 单独报的用量显示在它后面的结果上；思考和认不出的行收在「思考」「原始行」里。暂停跟随时，状态栏的「N 条新事件」数的是新一页会在时间线上多画出的项（`drawn`），不含并进已有卡片的结果和单独的用量。`output_test.go` 用 node 跑 `renderEvents` 和暂停时的 `fetchOutput`，事件由 `internal/output` 生成。
- 派发框：选好机器和档案后调 `run.preview`，列出 blockers 和 notes（不禁用提交）。
- 写操作都带 `command_id`（每次打开对话框生成一个，重试沿用）：新建、编辑（先 `task.get`，只提交改过的字段）、派发（`runner=background`）、停止、放弃（只对 unknown 的 run 提供）、完成（有进行中的 run 时先确认）、重开、取消。
- 断线：横幅提示、写操作禁用，1 s 起翻倍到 30 s 带抖动重连，重连后重新取全量；401 回登录页。
- 任务树：列表按父子缩进（最多三层），行上有处境徽章（`Fold.situation`，没开始的手动任务不标）；详情有处境、父任务、前置任务、子任务、验收标准、负责人与验收人，「开始」和「调整位置」；新建任务可以选父任务、前置任务、验收标准，或放进待办。地址 `#task-<id>` 打开对应任务（webhook 里的链接就是它）。「等你」计数在每次推送后 300 ms 去抖重读 `inbox.list`。
- 工单同步（见 [tasks/trackers.md](../tasks/trackers.md)「工单同步」）：项目卡片的「工单同步」列出绑定和状态，能绑定 Gitea 仓库、重新同步、换凭据、解绑（`/api/trackers*`）；需求任务在列表里标「需求」，详情显示来源 issue 和版本，有新版本时可以「采用新版本 / 维持本轮范围」，issue 在外面关掉后可以「继续做」或取消。
- 布局：任务列表 + 详情（输出 / 对话 / 任务书 / 运行记录）、「等你」页、agent 定义页、机器页（主人、分享、添加机器、机器凭据）、项目页、账号页、管理页（只有管理员）；新建任务可以选项目；键全部来自 `palette.js` 的操作表（`?` 列出全部）；850 px 以下侧栏变横条，680 px 以下列表和详情分屏、隐藏键帽。

### 新界面的底座

新界面（Preact + htm + signals，原生 ES 模块，无构建链）的底座和旧页面放在同一个目录里，被一起 embed，但旧的 `index.html` 不加载它。页面接上以后，旧文件一次删掉。

- **内嵌的第三方文件**：`web/vendor/`，放 npm 上的 `preact.mjs`、`hooks.mjs`、`htm.mjs`、`signals-core.mjs`，许可证在 `LICENSES/`。只给测试用的 `preact-render-to-string` 放在 `webtest/vendor/`，不进二进制。
  - `tools/vendorweb` 按各自的 `manifest.json` 下载 tarball，核对 npm 的 `integrity`，取出文件和 LICENSE，把上游和写出后的 sha256 记回 manifest。
  - 只允许一种改写：manifest 里某个文件的 `imports` 声明「裸模块名 → 相对路径」，比如 `hooks.mjs` 的 `preact` → `./preact.mjs`。render-to-string 指向 `../../web/vendor/preact.mjs`，这样它和组件用的是同一个 preact 实例，hooks 才挂得上。
  - 文件里出现没有声明的裸 import，或者声明的改写一处都没命中，工具就报错。
  - 版本升级：改 manifest 里的 `version`、清掉 `integrity`，再运行 `go run ./tools/vendorweb`。
- **分层**：`vendor` ← `core` ← `ui` ← `pages`，每层只 import 自己和下面的层，`ui` 不直接 import `core/wire.js`；入口 `main.js` 不受限制。所有 import 都是相对路径，不用 import map。`web/package.json` 的 `{"type":"module"}` 让 node 把这些 `.js` 当 ES 模块跑。
- **`core/`**（不碰 DOM，全部在 node 里测）：
  - `wire.js`：`/client` 上的 wire 帧，按行拆帧。
    - 连上后先发 `hello{proto: 2, role: "client"}`，形状就是 `remote.HelloParams`。回来的 `proto` 不是 2，状态记为 `outdated`，不再重连（页面要重新加载）；回来的 `methods` 决定哪些 `.watch` 可以开，没列出的方法在本地直接结束，报 `unsupported`。
    - `call`：30 s 超时后发 `cancel`，报 `timeout`；连接中的调用等 hello 完成再发，离线时立即报 `offline`，断线时报 `closed`。server 发来的 `ping` 照答，别的请求回 `unknown_method`。
    - `watch`：发 `req` 之前先登记接收者。推送按 `id` 交给这个流，第一条是 `open{cursor, mode}`；同一个 `id` 的 `res` 是最后一帧：`result` 表示正常结束，`lagged` 按游标立即重开，其它错误码就是流的终点。`cancel` 只发一次，之后到达的帧一律丢掉。
    - 断线后 1 s 起翻倍、最长 30 s、带抖动地重连（成功 hello 后回到 1 s），然后每个流用它的主人给的参数（带游标）重开。
    - `setVisible(false)` 结束可暂停的流（输出），`state.watch` 保留；`setVisible(true)` 按游标重开它们，离线时立即重连。连接状态放在一个 signal 里：`idle` / `connecting` / `open` / `offline` / `outdated` / `closed`。
  - `fold.js`：旧 `fold.js` 的模块版，行为相同；`fold_test.go` 拿两份和 `task.State.Apply` 对照。`parts` 表列出每种事件改动状态里的哪几张表。
  - `store.js`：页面的状态。
    - `state.watch` 的推送：
      - `open{mode: snapshot}` 或 `reset` 开始一份新快照；
      - `snapshot{part, items}` 的 `part` 是 `task.State` 那几张表的 JSON 名（`tasks`、`runs`、`projects`、`shares`、`agent_defs`），`items` 是 id → 对象，同一张表可以分几批，累加起来；
      - `live{seq}` 结束快照，整份换上；
      - 之后是 `journal{信封}`，seq 不大于已应用的就跳过。
      - `open{mode: resume}` 表示接着已应用的 seq 继续。`affordances` 这个 part 按人计算，store 收下但不折叠。
    - 状态原地折叠，每张表一个版本号 signal，一帧最多加一次（页面上用 `requestAnimationFrame`）。
    - 折叠遇到自己没有的对象，或者不认识的 part：结束这个流，不带 `after_seq` 重开。重开时的参数：已经 live 过就带 `after_seq`；`no_briefs` 时任务不带任务书，由 `brief(id)` 用 `task.get` 按需取回并填进状态。
    - `machines.watch` 推 `machines{items}`，`inbox.watch` 推 `inbox{items}`，都是整份替换。
    - `output(run)` 按运行引用计数：第一个持有者开 `run.output.watch`（可暂停，续传带 `from`），最后一个释放时 `cancel`。`run.output{events, cursor}` 里带 `key` 的事件替换前一条同 `key` 的；`open{mode: gap}` 在事件里插一条 `gap{from, to}`。
  - `keys.js`：作用域栈，顺序是 `modal` → `drawer` → `list` → `page` → `global`，同一层里后推入的先查；`blocks` 的作用域挡住下面各层，`when` 为假的绑定让给下一层。
    - 键名的写法和操作表一致：`n`、`Shift+D`、`Mod+K`（⌘ 或 Ctrl）、`g h`（先按 g，1.2 s 内按 h）、`Esc`、`Space`；`Alt` 组合不认。
    - 正在组字的按键不处理。输入框里只有 `Esc` 和 `Mod+Enter` 会交给作用域。`；` `，` `？` `、` 当作 `;` `,` `?` `/`。
    - 同一个作用域里一个键只能绑一次，一个键也不能同时是另一个序列的开头。
  - `router.js`：地址 ↔ `{page, task?, view?, auth?}`。页面有 `home`、`tasks`（`view` 取 `list` / `board` / `tree`）、`runs`、`machines`、`agents`、`team`、`me`。旧的 `inbox` → 首页，`settings` → 我，`projects` → 团队；`#task-<id>` 打开任务页并选中它；`#device-`、`#invite-`、`#signin-<结果>[?参数]` 解析成 `auth`，生成地址时不带它们。
  - `i18n.js`：每个模块用 `register(模块, {键: [zh, en]})` 注册自己的词表。两个模块用了同一个键、缺一种语言、两种语言的 `%s` / `%d` 顺序不同，都会报错。
- **测试**（`webtest/`，不在 `web/` 下）：
  - Go 用 `runModule` 在 node 里跑 `*_test.js`，每个 JS 用例是一个子测试。
  - 假 server（`fake.js`）在进程内模拟 WebSocket 和时钟，回放 `webtest/frames/*.jsonl`：一行一个动作。`c` 是客户端应该发出的帧，`s` 是 server 发的帧，`raw` 是原样发出的一段文字，另外还有 `connect` / `refuse` / `drop` / `dialing` / `wait_ms` / `step` / `note`。
  - 第 3 期 wire 流和 `state.watch` 的 Go 测试也读这批文件：取 `c` 当输入，拿 `s` 对照输出，其余的行属于客户端，跳过。
  - store 回放 `state-snapshot.jsonl` 得到的状态，和 Go 按同样规则折叠同一文件的结果对照；快照里的对象严格按 `task.State` 的类型解码，多出的字段算失败。
  - 结构测试：vendor 的校验和，改写只出现在声明过的地方；import 只用相对路径并且合乎分层；每个帧文件都被某个测试回放；皮肤 token 递归检查（跳过 `vendor/`）。
