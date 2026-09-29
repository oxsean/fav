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

- 文件：`internal/server/web/`（`index.html`、`app.css`、`api.js`、`fold.js`、`team.js`、`tree.js`、`home.js`、`look.js`、`palette.js`、`app.js`），`go:embed` 进二进制，无构建步骤、无外部依赖。新界面的底座（`core/`、`ui/`、`css/`、`vendor/`）见下文「新界面的底座」。
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

- **内嵌的第三方文件**：`web/vendor/`，放 npm 上的 `preact.mjs`、`hooks.mjs`、`htm.mjs`、`signals-core.mjs`，许可证在 `LICENSES/`。只给测试用的 `preact-render-to-string` 和 preact 的 `test-utils` 放在 `webtest/vendor/`，不进二进制。
  - `tools/vendorweb` 按各自的 `manifest.json` 下载 tarball，核对 npm 的 `integrity`，取出文件和 LICENSE，把上游和写出后的 sha256 记回 manifest。
  - 只允许一种改写：manifest 里某个文件的 `imports` 声明「裸模块名 → 相对路径」，比如 `hooks.mjs` 的 `preact` → `./preact.mjs`。render-to-string 和 test-utils 指向 `../../web/vendor/preact.mjs`，这样它和组件用的是同一个 preact 实例，hooks 才挂得上。
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
      - `open{mode: resume}` 表示接着已应用的 seq 继续。`affordances` 这个 part 和之后的 `affordances` 推送按人计算，不折叠，放进 `affordances` 这个 signal：`{runs: {运行: [操作]}, tasks: {任务: {actions, route}}}`。推送按键合并，值为 `null` 的删掉；新快照开始时先暂存，`live` 时换上。`route` 是这个任务的消息此刻会去哪：`{to: run | reply | workpad, run?, stage?, version?, why?}`；运行的操作里有 `steer`、`interrupt`、`allow_run`。
    - 状态原地折叠，每张表一个版本号 signal，一帧最多加一次（页面上用 `requestAnimationFrame`）。
    - 折叠遇到自己没有的对象，或者不认识的 part：结束这个流，不带 `after_seq` 重开。重开时的参数：已经 live 过就带 `after_seq`；`no_briefs` 时任务不带任务书，由 `brief(id)` 用 `task.get` 按需取回并填进状态；`briefOf(id)` 同步地说有没有：有就是任务书，开着 `no_briefs` 但还没取回是 `undefined`，没开 `no_briefs` 而任务没有任务书是空字符串。
    - `machines.watch` 推 `machines{items}`，`inbox.watch` 推 `inbox{items}`，都是整份替换。
    - `output(run)` 按运行引用计数：第一个持有者开 `run.output.watch`（可暂停，续传带 `from`），最后一个释放时 `cancel`。`run.output{events, cursor}` 里带 `key` 的事件替换前一条同 `key` 的；`open{mode: gap}` 在事件里插一条 `gap{from, to}`。
    - `more()` 取最早一个事件之前的一页：`run.output.page{before, file, n: 200}`，一个都没有时 `before: -1`，按 id 去重；翻到这一代日志的开头而有 `prev` 时，下一页从上一代的末尾取。`head` 这个 signal 说前面还有什么：`{more}`、`{start}`（运行的开头）、`{gone}`（`gone` / `not_found`，已清理），取页时带 `loading`，失败带 `failed`，`stale` 时从最后一页重新找。`output(run, {watch: false})` 只翻页不开流；server 没有 `run.output.watch`（`unsupported`）时也改成 `more()` 取最后一页。往前取的页加起来超过 `OUTPUT_BYTES`（32 MiB）时，`trim()` 丢掉最早的几页。`raw(run)` 取最后一页的原始行。
  - `keys.js`：作用域栈，顺序是 `modal` → `drawer` → `list` → `page` → `global`，同一层里后推入的先查；`blocks` 的作用域挡住下面各层，`when` 为假的绑定让给下一层。
    - 键名的写法和操作表一致：`n`、`Shift+D`、`Mod+K`（⌘ 或 Ctrl）、`g h`（先按 g，1.2 s 内按 h）、`Esc`、`Space`；`Alt` 组合不认。
    - 正在组字的按键不处理。输入框里只有 `Esc` 和 `Mod+Enter` 会交给作用域。`；` `，` `？` `、` 当作 `;` `,` `?` `/`。
    - 同一个作用域里一个键只能绑一次，一个键也不能同时是另一个序列的开头。
    - `active()` 列出此刻按下会生效的绑定，每个键一次，按查找顺序；`changed` 这个 signal 在推入、弹出作用域时加一，键栏据此重画。
  - `layout.js`：宽度不到 `PHONE_BELOW`（720 px）是手机形态，否则是电脑形态，放在 `form` 这个 signal 里，由入口按 media query 设置；电脑上宽度不到 `NAV_OPEN_FROM`（1200 px）时侧栏默认收起。`createNav` 管侧栏开合：用户选过一次就记在浏览器里（`tend-nav`），之后不再随宽度变；存储不可用时照常工作，只是不记。`mac` 决定 `Mod` 显示成 ⌘ 还是 Ctrl。
  - `toasts.js`：底部的提示。带撤销的留 `UNDO_WAIT`（6 s），普通的 4 s；`undo()` 撤销最新一条可撤销的。
  - `router.js`：地址 ↔ `{page, task?, run?, view?, event?, auth?}`。页面有 `home`、`tasks`（`view` 取 `list` / `board` / `tree`）、`runs`、`machines`、`agents`、`team`、`me`。旧的 `inbox` → 首页，`settings` → 我，`projects` → 团队；`#task-<id>` 打开任务页并选中它；`#task-<id>/r-<运行>/e-<事件>`（`link()` 生成）还打开那次运行的对话、滚到那一步，`event` 只用一次，生成地址时不带；`run` 写在查询里（`?page=tasks&task=…&run=…`），没有 `task` 时不认；`#device-`、`#invite-`、`#signin-<结果>[?参数]` 解析成 `auth`，生成地址时不带它们。
  - `i18n.js`：每个模块用 `register(模块, {键: [zh, en]})` 注册自己的词表。两个模块用了同一个键、缺一种语言、两种语言的 `%s` / `%d` 顺序不同，都会报错。
  - `actions.js`：页面唯一的操作表。每个操作有 id、键、作用域层级、分组；`bar` 的进键栏，`palette: false` 的不进命令面板（移动、数字、面板自己）。键位就是设计稿 §6.6 的键表：`g h/t/b/r/m/a/p/s` 去各页，`Mod+K` 命令面板，`?` 快捷键，`/` 搜索，`n` 新建，`[` 侧栏，`Mod+Z` 撤销，`Shift+T/L/M` 主题 / 语言 / 密度（全局）；`v` `d` `e` `x` `Shift+D`（页面）；`j` `k`（别名方向键）`Space` `Enter` `1`–`9`（列表）；`End` `Home` `Shift+O` `Mod+F`（输出）。一个键只属于一个操作。
    - 组件不直接写键：用 `useActions(层级, {id: {run, when?, label?}})` 绑定自己能做的操作，键从表里来；`label` 让页面换一个更贴切的说法（首页的数字叫「作答」，`d` 叫「重试」）。哪些绑定生效（`when`）随页面变了，作用域就重新推入，键栏跟着变。
    - 命令面板列出此刻生效的操作（`runnable(keys.active())`），按中文名、英文名、id、键都能搜到，开头匹配的排前面；快捷键页按分组列出整张表。
  - `commands.js`：页面的写操作。发出时按 key 记为 pending；可撤销的写（标记完成）在列表里先藏起来，应答之后等那张列表下一次变化再放出来，免得闪回；没收到应答（`timeout` / `offline` / `closed`）记为 `unknown`，`retry` 用同一个 command id 重发，coordinator 的回执保证不做两次。状态本身只来自 journal 的折叠。
  - `http.js`：普通 HTTP：`/session`（未登录是 null）、`/login`（表单提交 token）、`/logout`、`/auth/logins`、`/auth/invite`、`/api/device`。写请求带 `X-Tend`，网络不通报 `offline`。
  - `prefs.js`：语言、主题（跟随系统 / 浅色 / 深色）、密度（紧凑 / 标准 / 宽松）和皮肤，沿用旧页面的存储键（`tend-lang`、`tend-theme`、`tend-look`），切换界面不丢；输出的密度（简洁 / 标准 / 详细，默认标准）记在 `tend-output-density`；存储不可用时用默认值。
  - `format.js`：数字的写法（`312k` tok、`$3.18`、`1h 04m`、`14:32`）。token 数是 input + cache_write + output，不含读缓存。
  - `select.js`：页面上的数，全是纯函数，输入是 store 的状态、机器、inbox 和当前时间。首页每个数的来源见下面「首页」。
  - `tasks.js`：任务页的纯函数：处境归到哪一列、筛选、列表 / 看板 / 树的行、看板上一次拖放对应的写、目录候选、默认的机器和 agent、派发前的判断、拆解草稿的编辑和检查、一个任务此刻能做哪些操作。规则见下面「任务页」。
  - `output.js`：一段对话的事件怎么画，纯函数，见下面「对话与输出」。`items` 和 Go 的 `output.Items` 同一个规则：结果并进它的调用，同一个 `parent` 下连着的读和搜并成一步，临时事件不算。`fold.js` 的 `conversation(state, run)` 是这次运行所在的对话：同一任务里顺着 `parent` 回到同一个起点的运行，按 `seq` 排。
  - `follow.js`：长对话怎么滚，纯函数，见下面「对话与输出」。
- **`ui/`**（共用组件）：每个组件从一开始就有电脑和手机两种形态，读 `form` 决定画哪一种，页面不用分两份写。样式在 `css/base.css`（尺寸变量、字体、外框）和 `css/components.css`，颜色只用皮肤 token；手机形态的样式挂在外框的 `data-form="phone"` 下，密度挂在 `:root[data-density]` 上。
  - `Shell`：电脑上是 56 px 顶栏（品牌、搜索框和 `Mod+K`、在跑 / 排队 / 离线计数、当前用户）、左侧栏（七个页面，`[` 或底部按钮开合，收起时只剩图标，等你的数目变成角标；下方是新建任务）和 32 px 键栏；手机上是 52 px 顶栏（server 名可点开切换）和底部四个标签：等你、任务、运行、我。机器页归在「运行」标签下，Agent 和团队归在「我」下。连接断开时顶部出横幅，可立即重连；server 已升级时提示刷新。它也提供按键的上下文。
  - 键栏只列此刻生效、操作表里标了 `bar` 的操作；同一说明的键合成一项（`j k 上下一条`），连续的数字写成 `1–9`。手机上不画键帽。
  - `Button`（primary / quiet / danger，`on` 是按下的开关）、`Chip`、`Segmented`、`Tabs`（方向键移动，只有选中的一项在 Tab 顺序里）、`Status`（形状加颜色区分状态，屏幕阅读器读状态名）、`Panel`、`Stat`。手机上按钮高 44 px，筛选项横向滚动。
  - `Table`：列表都用它。电脑上是表格，表头可排序（升、降、取消）；手机上每行变成一张卡片，列的 `mobile` 决定它在卡片的哪个位置。选中按 id 记，排序和推送之后不丢。`j` / `k` / 方向键移动，`Enter` 打开，`Space` 展开，`1`–`9` 选择。超过 `VIRTUAL_ABOVE`（200）行只画可见的一段，选中项移出视野时滚过去。
  - `ExpandItem`：原地展开的一条（等你处理的事）。收起时显示状态、问题、等了多久和快捷操作，标题一行放不下时截断，悬停看全文；展开后标题和说明换行显示全文。手机上只留第一个操作，展开 / 收起写成文字。
  - `Modal`：电脑上是居中的对话框；手机上从底部升起（sheet），长表单（`full`）占满整屏并带返回。它推一个 `blocks` 的 `modal` 作用域：`Esc` 关闭，`Mod+Enter` 执行主操作，页面的键被挡住；焦点移进来、`Tab` 在里面循环，关闭后回到打开前的元素。`Drawer` 在电脑上是右侧面板，手机上占满整屏。整屏的页头有 `extra` 一格，任务页在这里放上一个 / 下一个。
  - `Toasts`：`Mod+Z` 或按钮撤销最新一条。
  - 手机上左右切换用上一个 / 下一个按钮，不用滑动手势。
  - `Menu`：按钮下弹出的一小列选项，打开时占住按键（方向键移动、`Esc` 关闭，焦点回到按钮）。顶栏的用户菜单放语言、主题、密度和退出登录。
  - `TextInput` / `TextArea`：上面是标签，下面是说明或错误。
  - `Markdown`：任务书这类文字。认标题（画成 h3–h6）、段落、有序 / 无序列表、引用、围栏代码，行内的代码、粗体、斜体、链接和裸 URL；全部生成节点，不用 innerHTML，其余原样当文字。链接只放行 http(s) 和本站的路径、查询、片段（`//` 开头的不算），别的链接连同括号当文字显示。
  - `Picker`：从一列里挑一个或几个（`multi`）：父任务、前置任务、机器、agent。电脑上在按钮下弹出，手机上占满整屏；输入即搜（按标签、值、说明和 `words`），方向键移动，`Enter` 选中。每项可带状态、说明和提示。
  - `TreeList`：缩进的树（最多画三级缩进），`Space` 或行首按钮展开 / 收起，键和选中同 `Table`；手机上点一下直接打开。
  - `Board`：看板，按列排卡片，键按列的顺序走。拖动时每列标出能不能放（`canDrop`），放在不能放的列上由 `onRefused` 说明原因。只在电脑上用。
  - 图表（`charts.js`）：`Spark`（数字下面的小折线，可画成阶梯）、`Bars`（按天并排、每天按部分堆叠，部分按顺序取色）、`Timeline`（每台机器一条泳道，运行按重叠排成几行，颜色是运行的状态；右端是现在）、`Meter`（一条按部分分色的横条）。尺寸用 style 对象设置，走 CSSOM，不产生 style 属性。
  - `Palette` / `Help`：命令面板和快捷键页，手机上占满整屏。键栏右端提示 `⌘K 全部命令 · ? 快捷键`。
  - `Output`（时间线）、`AnswerForm`（原地作答）、`Composer`（输入框）：见下面「对话与输出」。
- **`pages/`**：
  - `boot.js`：页面启动。读偏好并写到 `<html>`（`lang`、`data-theme`、`data-density`，皮肤样式表的地址），按 media query 设形态，建按键、路由、HTTP；问 `/session`：没登录画登录页，`#device-<码>` 画终端登录确认，否则连 `/client`、开 store 的三个 watch，画应用。登录结果、邀请这类一次性片段用过就从地址里去掉；页面隐藏时交给 `wire.setVisible`。socket、fetch、存储和时钟都可以从参数传入，预览和测试靠它换成假的。入口 `main.js` 只调用它。
  - `auth.js`：登录页（各登录方式的按钮，邀请时写明谁邀请、什么身份、加入哪个项目、何时失效，下面是 token 登录）；登录被拒（`not_admitted` / `disabled`）时说明是哪个账号、为什么，可以换账号或复制账号信息；终端登录确认（核对设备码，显示设备名、来源地址、请求时间，允许或拒绝；码已用过或过期时说明）。
  - `app.js`：登录后的页面：外框、全局操作（去各页、命令面板、快捷键、主题、语言、密度）、命令面板里的任务 / 运行 / 机器搜索；`n` 和 `/` 在任何页都转到任务页，打开新建表单或搜索框。还没做的页面先显示「这一页还在做」。
  - `home.js`：首页，见下。
  - `conversation.js`：一个任务的对话，见下面「对话与输出」。
  - `tasks.js`（列表、看板、树和每个写操作）、`task.js`（一个任务的详情）、`taskforms.js`（新建 / 子任务 / 复制 / 编辑、派发、调整位置、审拆解、验收）、`taskwords.js`（它们的词表）：任务页，见下。
- **首页**：
  - 上面四个数：
    - 等你：inbox 的条数，按「要回答（asked、permission）/ 出错 / 待验收（accept、ended、draft）/ 其他（dispatch、source_changed、source_closed）」分组计数；最久一条等了多久。
    - 在跑 / 排队：状态是 starting、running、unknown 的运行数 / queued 的运行数；已连接机器的 `active` / `slots` 之和；今天从第一个整点起每小时最多同时几个运行（小折线）和全天的峰值。
    - 今天结束：今天 0 点以后结束的运行数，按结束状态分，平均时长（开始到结束）。
    - 今天花费：今天用量的 `cost_usd` 之和（只有 claude 给估算）和 token 数，小折线是近 7 天每天的值。用量按结束时间归日，没结束的按开始时间，再没有就按排队时间。
  - 等你：inbox 按上面的分组排，组内等得最久的在前；可以只看「我负责」「我验收」（inbox 项的 `as`）。每一条收起时写原因：问题本身、要执行的工具和命令、失败的 `detail`、结束时的 `last`；快捷操作：问题只有一个单选时前三个选项（`1`–`3`，其余在「其他…」里），权限请求是允许 / 拒绝，待验收是完成（`Shift+D`，可撤销），出错是重试（`d`，先确认）。展开后显示详情、它刚做的三步（`run.output.page` 取最后 20 个事件里的工具调用）和作答（`AnswerForm`，见下面「对话与输出」）；没有请求时是一个回复框。
  - 在跑 / 排队：先是在跑的（开始早的在前），再是排队的（排得早的在前）。每行写它现在在做什么：节点报的当前动作 `doing`，没有就用 `note`，再没有用 `last`；排队的写它在等什么（等机器接手时带那台机器的 `active/slots`，等同目录的运行、等前置任务等）。还有时长、花费（没有美元就写 token）和停止（先确认）。
  - 最近结束：今天结束的最近 8 个，写怎么结束的（正常、check 没过、退出码、出错、停止等）。
  - 今天的运行：每台机器一条泳道，从 08:00（更早有运行就从那个整点）到现在；离线、满载的机器在名字下注明。
  - 近 7 天用量：每天的 token 按 provider 堆叠，另写 claude 估算的美元。近 7 天运行结果：结束状态的比例条。
  - 手机上首页只有等你和在跑 / 排队两块，上下排，图表不画。
  - 首页的数不靠定时器刷新：每次重画时取当前时间。
- **任务页**：
  - 处境（`fold.js` 的 `situation`）归成五列：等你、在跑、排队、未开始、已结束（完成和取消）。筛选：我相关的（负责或验收）、项目、列、搜索（标题、id、标签）；前三项记在浏览器里（`tend-task-filter`），搜索不记。选中的任务写在地址里（`task`），电脑上换选中用 replace，手机上打开一个任务是 push。
  - 列表：按列的顺序，同列里最近更新的在前；每行是处境、标题（带子任务进度、花费）、原因、谁在哪跑、更新时间。手机上按列分节，每节一张卡片表。
  - 看板（只在电脑上）：五列卡片。拖放只有四种对应写操作：待办且没有未结束运行的拖到已结束是完成（带工作流的不行）；拖到未开始是「先不开始」（待办且没有未结束运行的，或已结束的）；从未开始拖到排队或在跑是开始（`task.start`）；从已结束拖到等你是重新打开。除开始以外都走可撤销的 `task.set_status`。别的组合不能放，放下时提示原因。
  - 树：父子关系缩进，兄弟按 `after` 再按创建时间排；搜到的任务连同它的上级一起显示，收起的节点记在页面里。
  - 详情：电脑上列表和树是右边的分栏，看板是右侧抽屉；手机上占满整屏，页头有上一个 / 下一个（按当前视图的顺序），不用滑动手势。内容：处境和工作流阶段；主操作按处境选（重新打开、开始、停止、审拆解、验收、采用新版需求、合并、完成、派发等），编辑和派发在电脑上直接显示，其余在「更多」里；需求有新版、来源已关闭、有拆解草稿时各一条提示；负责人、验收人、在哪跑（项目默认的注明）、目录、分支、全部运行的花费、issue 链接；任务书（`Markdown`，按 `briefOf` 按需取回）、验收标准、工作流各阶段、父任务和前置任务、子任务、运行记录（状态、agent @ 机器、阶段、`doing` / `note` / `last`、结论、时长、用量）、工作记录。电脑上有运行的任务，详情分「概览 / 输出」两个页签，默认是输出，选过的记在浏览器里（`tend-task-pane`）；概览里的运行记录不开 `run.output.watch`，点一次运行就在输出里打开它所在的对话（地址带 `run`）。手机上点运行，再叠一层整屏的对话。
  - 写操作都经 `commands.js`，按任务记 pending，操作中按钮变灰；完成、先不开始、重新打开和调整位置可以撤销；停止和取消先确认。失败时提示错误码，没收到应答时说明结果不明。
  - 新建（`n`）：标题、任务书（写 / 预览两个标签）、验收标准、项目、机器、agent、目录、工作流、父任务、前置任务。目录候选先是项目在这台机器上的检出目录，再是最近用过的目录；手输的目录只在 server 支持 `project.dirs` 时去那台机器上检查（停手 400 ms 后），不支持时不检查。没写完的新建表单记在浏览器里（`tend-task-draft`），发出后清掉。三种建法：先不开始、建好后拆解（`task.plan`）、建好就开始（`task.start`，主操作，`Mod+Enter`）。编辑只发改过的字段；子任务带上父任务的项目和目录；复制带上原任务除标题和工作流以外的字段。
  - 派发（`d`）：选机器和 agent（手机上是一列可点的行），先按页面自己的规则判断（已有未结束的运行、状态不对、没有目录、没选、agent 限定了机器、CLI 没装或没登录挡住；机器离线、满载只提示），没被挡住时再问 `run.preview`，显示 CLI 版本、挡住的原因和提示。确认后 `run.dispatch`；也可以「先不开始」。
  - 审拆解：左边是草稿里的任务（按父子缩进），右边编辑选中的一条（标题、key、父任务、前置、大小、说明）；手机上编辑是再叠一层整屏。上面是拆解 agent 的问题，可以直接回复（`run.continue`）。提交前按 coordinator 的规则检查（最多 50 个、key 的写法、重复、父任务和前置存在、最多三层、不依赖自己或上级、没有环），有错不能保存。改过先保存（`task.plan_save`，带 `expected_rev`），没改过就是应用（`task.plan_apply`）；也可以丢弃草稿。
  - 验收：通过，或写明原因退回（`task.gate`，退回必须写原因）；运行结束待确认完成、最后一次运行留有会话时，「退回」把原因作为 `run.continue` 发给那次运行。手机上两个按钮在上面，原因在下面。
- **对话与输出**：
  - 对话是一串运行：最新的那次开 `run.output.watch`，更早的只在往上翻到时取它的最后一页，接着往前翻。每次运行之间一条分隔线（第几次运行、续接、在哪台机器）。
  - 事件除了 [output.md](output.md) 的种类，还认节点和协调器加上的：`you{input, text, by, mode, at}`（送到 agent 的一条消息，`input` 是它的发送 id）、`resolved{request, by, decision, at}`、`interrupt{id, n, by, at}`、`mark{event: "hook", name, phase}`、`gap`。运行的 `sends` 里还没被 `you` 事件认领、排队中或没送到的消息画在最后，没送到的可以重发。
  - 一步一行：命令写命令本身和状态、用时、行数；读和搜并成一行（「读了 2 个文件 · 搜了 1 次」）；改动写文件和 `+N −M`；计划写更新了第几步，最新的计划钉在时间线上方。说的话、你的消息、问题和出错总是展开；第一次运行的第一条消息是任务书，只露前三行。
  - 三种密度：简洁把一轮里对话以外的步骤并成一行小结（出错的照样单独一行）；标准每步一行，agent 超过 `SAY_FOLD`（30）行的话折起；详细把思考、命令、改动、读和搜、计划都展开，输出只露头尾各 `DETAIL_ENDS`（10）行，diff 超过 `DIFF_CUT`（200）行截断。任何密度下，出错的步骤、还在跑的命令（露最后 `RUNNING_TAIL` 行）、没答的问题自己展开；出错的输出露最后 `FAIL_TAIL`（8）行，点开看全部。
  - 最新一轮以外的轮次折成一行：第几轮、工作了多久、几步、改了几个文件、最后一句话，点开展开。筛选：全部、只看对话、只看命令、只看改动、出错的；「下一处错误」按顺序跳。
  - 跟随（`follow.js`）：在底部时新内容把视图带到底部（增长不到一屏时平滑，超过一屏或手指按着时直接跳），程序自己的滚动不算离开。滚轮向上、手指向下拖、`↑` / `PageUp` / `Home`、点滚动条、跳到某一步都算离开：视图停住，底部的按钮数离开后新来的步骤（临时事件、还没送到的消息、标题行不算），有等你的（没答的问题、出错）时换警示色并能直接跳过去，运行在离开后结束时写「运行结束了 · 看结果」；新内容前面画一条「新的」线，滚过去就消失。滚动停下（`SETTLE_MS` 100 ms，触屏松手后 `TOUCH_MS` 150 ms）时离底部不到 `NEAR`（48 px）就恢复跟随。
  - 视图上方的缓冲：离开底部并且还在滚时，锚点（视野里第一行）以上的行保持画出时的样子，从锚点往下用最新的；滚动停下后一次换上，并按锚点的新位置补偿滚动距离，视野里的那一行不动。展开、收起一步时以它为锚点。超过 `KEEP_ROWS`（1500）行时，离视野 `SCREENS`（5）屏以外的每 `PAGE`（200）行换成等高的占位，有焦点、选中文字、正在写回答的那页不换；离顶部 `PREFETCH_SCREENS`（1.5）屏以内时往前取一页。
  - 键（操作表，时间线有焦点时生效，手机上总是）：`j` / `k` 选一步，`Space` / `Enter` 展开收起，`Shift+O` 展开或收起这一轮的每一步，`End` 回到底部并跟随，`Home` 到开头，`Mod+F` 查找。
  - 查找：在已加载的事件里找，150 ms 去抖，只折叠 ASCII 大小写；查的是文字、标题、输出的头尾、diff 预览和问题，截掉的中段不算。命中项所在的轮次、子 agent 临时展开，`Enter` / `Shift+Enter` 上下跳；「在更早的内容里找」往前最多取 8 页，没找到时问要不要接着找。
  - 位置：每段对话在这个标签页的 sessionStorage 里记着离开时的锚点和是否在跟随（`tend-out-<第一次运行>`），回来时从那里开始。深链 `#task-<id>/r-<运行>/e-<事件>` 打开时跳到那一步（组里的事件跳到它所在的组）并闪一下；选中一步后「复制这一步的链接」。
  - 作答（`AnswerForm`，时间线的问题行和首页的等你条目共用）：权限请求是允许、这次运行里都允许（运行的操作里有 `allow_run` 时，发 `decision: "allow_run"`）和带理由的拒绝；问题可以一次有几个，每个从选项里点（`multi` 的可多选）或自己写，自己写的优先，全部答完才能一起发：`run.answer{answers: {问题: 选项用「, 」连起来或自己写的话}}`，也可以不回答（`allow: false`）。只有一个单选问题时，选项在首页上带数字键。已处理的问题写谁、怎么处理的、什么时候。
  - 输入框（`Composer`）：发出之前写明这条消息会去哪，按 `route`：`run` 时可以插话（`steer`，运行的操作里有才出现）、这一轮结束后再说（`after`）、打断并改说法（`interrupt`，先确认）；`reply` 是在那台机器上接着这个会话再跑一次；`workpad` 是记下来交给下一阶段。`Mod+Enter` 发送 `task.message{id, text, mode?, expect: route}`；去向变了（`route_changed`）时不发，提示看新的去向再发，写的字留着；`cannot_send` 说原因。草稿按运行记在页面里。「打断这一轮」先确认，发 `run.interrupt{run, turn}`。
  - 手机上时间线占满整屏，工具在上面（筛选横向滚动，查找、跟随 / 暂停是按钮），输入框在下面；键盘弹出时跟随中的视图留在底部。
- **测试**（`webtest/`，不在 `web/` 下）：
  - Go 用 `runModule` 在 node 里跑 `*_test.js`，每个 JS 用例是一个子测试。
  - 假 server（`fake.js`）在进程内模拟 WebSocket 和时钟，回放 `webtest/frames/*.jsonl`：一行一个动作。`c` 是客户端应该发出的帧，`s` 是 server 发的帧，`raw` 是原样发出的一段文字，另外还有 `connect` / `refuse` / `drop` / `dialing` / `wait_ms` / `step` / `note`。
  - 第 3 期 wire 流和 `state.watch` 的 Go 测试也读这批文件：取 `c` 当输入，拿 `s` 对照输出，其余的行属于客户端，跳过。
  - store 回放 `state-snapshot.jsonl` 得到的状态，和 Go 按同样规则折叠同一文件的结果对照；快照里的对象严格按 `task.State` 的类型解码，多出的字段算失败。
  - 组件（`ui_test.js`）：用 render-to-string 把一套样例按两种形态、两种语言各画一遍，检查每个 class 在 CSS 里有规则、没有漏译的键、两种形态各有自己的结构；交互在 `dom.js` 的假文档里用 `act` 驱动：列表的键、排序、1000 行的窗口，对话框的按键和焦点，提示的撤销和时限，侧栏开合，手机标签栏，方向键。
  - 结构测试：vendor 的校验和，改写只出现在声明过的地方；import 只用相对路径并且合乎分层；每个帧文件都被某个测试回放；皮肤 token 递归检查（跳过 `vendor/`）。
  - 首页的帧：`home-state.jsonl` 是一天的运行加前六天、三台机器（一台离线）、五条 inbox，现在是 2026-09-30T14:32Z（测试用 UTC）；`home-commands.jsonl` 接在它后面，是完成和撤销、按数字作答、重试、停止，各带 command id；`output-page.jsonl` 是展开一条时取的输出；`command-unknown.jsonl` 是没收到应答的写用同一个 command id 重发。Go 把 store 折叠 `home-state` 的结果和 `task.State` 对照，并把帧里的写请求参数、应答和 `machines` / `inbox` 的每一项严格按 coordinator 的类型解码（`task.TaskStatus`、`coord.Dispatch`、`task.RunRef`、`coord.Answer`、`coord.OutputPageParams`、`coord.Continue`；`task.Task`、`task.Run`、`coord.OutputPage`；`coord.Machine`、`coord.InboxItem`）。
  - `select_test.js`：数字写法；首页每个数对帧文件算出的值；空的一天；HTTP 请求的形状；操作表和 §6.6 的键表一致、每个操作有键和两种语言的名字、两种名字都搜得到；写操作的 pending、隐藏、未知和重发；偏好的读写。
  - 任务页的帧：`tasks-state.jsonl` 是一个项目、十一个任务（需求有新版、完成、在跑、等人工验收、未开始的子任务、拆解草稿、没目录、已取消、等派发、合并冲突、排队）和它们的运行、三台机器、inbox；`tasks-create`（新建并开始）、`tasks-dispatch`（预览后派发）、`tasks-plan`（删一条、保存、应用）、`tasks-acts`（验收退回和通过、采用新版需求、合并、调整位置和撤销）、`tasks-board`（拖到已结束、撤销、被拒的拖放）接在它后面。Go 另外严格解码其中的 `coord.TaskCreate`、`coord.TaskRef`、`task.TaskMove`、`coord.PlanSave`、`coord.PlanApply`、`coord.TaskGate`、`task.SourceAck`、`run.preview` 的 `coord.Dispatch` 和 `coord.Preview`、`coord.Agents`。
  - `tasks_test.js`：`core/tasks.js` 对 `tasks-state` 的结果：看板的列、每种拖放、树和收起、筛选 / 进度 / 花费、目录候选和默认值、派发前的判断、拆解草稿的编辑和每种错误、每种处境的操作。`taskpages_test.js`：任务页（列表、看板、树，带选中的任务）和每个表单按两种形态、两种语言画一遍，检查 class 和漏译；按上面五个帧文件走一遍；手机上的上一个 / 下一个和验收的按钮位置。
  - 对话的帧：`output-state.jsonl` 是一个任务的两次运行（第二次续接第一次的会话，一次问两个问题）、它的 `affordances` 和 inbox；`output-conv.jsonl` 接在它后面，是打开对话后 `run.output.watch` 的推送、往前一页和上一次运行的最后一页；`output-send.jsonl` 是插话、去向变了被拒、原地回答两个问题、打断；`output-answer.jsonl` 是首页上一起回答两个问题。`output_test.js`：三种密度的行、折叠和自动展开、查找和筛选、跟随的每种情形、锚点补偿和占位、深链、组件按两种形态两种语言画一遍，并按这几个帧文件走一遍。Go 另拿 `internal/output` 的 testdata 和几种它没有的形状，分别交给 `output.Items` 和 JS 的 `items`，对照结果。
  - `pages_test.js`：首页（在应用里）和登录、邀请、登录被拒、终端确认各页按两种形态、两种语言画一遍，检查 class 有规则、没有漏译；按 `home-commands` 用键盘走完完成 → 撤销 → 作答 → 重试（确认）→ 停止（确认，`Esc` 不发）；展开一条看它刚做的三步；`doing` → `note` → `last` 的先后；命令面板按中文名找操作、按标题找任务，快捷键页；token 登录（错的、对的）和终端的允许、已过期。
- **预览**：`go run ./tools/webpreview` 在 127.0.0.1:18765 用工作区里的文件起一个只给看的页面：`web/` 的文件、`webtest/preview/` 的预览页、帧文件和皮肤，响应头和 server 一样（`server.SecureHeaders`，CSP 不变）。预览页用假 socket 按方法回放上面的帧文件、用假 fetch 回答登录和终端确认，时钟固定在帧文件的时刻；`?frames=tasks` 换成任务页的帧，`?frames=output` 换成对话的帧（打开 `?page=tasks&task=t1`），`?as=signedout` 看登录页，`#device-<码>`、`#invite-<码>` 看另两页。它不连任何 coordinator。
