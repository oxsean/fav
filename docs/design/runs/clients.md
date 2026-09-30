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
- 右栏：任务书、最近 4 次 run、最近一次 run 的输出（`run.output.watch` 推来的事件，经 `render.RunOutputLines` 读成可读的行再 `render.Sanitize`：前缀 `> ` 用户、`+ ` 工具调用、`$ ` 命令、`~ ` 改动的文件、`? ` 提问和审批、`- ` 警告、`= ` 结果、`! ` 错误和失败的调用的最后一行；思考、系统行和成功的结果不显示；正在写的消息照写，正在写的思考只写一行「思考中…」，codex 还在跑的命令写 `$ 命令（运行中）`，下面缩进露最后 3 行；摘要略掉的行数、文件数、问题数由 i18n 写出；原始行用 `tend run logs` 看）；最近一次 run 下面是原因（`render.RunReason` + 原话）、提问或进展、下一步（`render.RunHint`）。
- 状态文字后面带 attention（等你回复 / 等你批准 / 长时间没有输出），图标换成警示。
- 运行对话框打开和改选项时调 `run.preview`，在目录下面列出检查结果、blocker（红）和 notes；不拦 Enter。
- 作答：运行中的 run 有请求时 task 对话框的主按钮是「回答」，打开回答对话框（scope `inTaskRun`）：权限请求显示工具和摘要，按钮「允许」（主）/「拒绝」/ 取消；提问每个问题一个选项选择器（← → 改选项，Tab 在问题和按钮间移动），按钮「回答」（主）/「不回答」/ 取消。running 的 stream run 另有「发消息」按钮，打开和回复同样的多行输入框，发 `run.send`。右栏和对话框列出等着的请求和最近两条消息的状态；运行中的 run 显示最后一句话，结束后显示用量。
- 回复：run 在等时 task 对话框的主按钮是「回复」，别的已结束且有会话的 run 也有「回复」按钮；回复框是多行输入（scope `inTaskForm`：Enter 换行、Ctrl+S 发送、Tab 到按钮、Esc 放弃），发 `run.continue`。
- 接手：打开该会话的恢复对话框；run 还在跑时对话框提示先停（`resume.check.running_run`）。
- 状态：视图打开时连协调器（连 socket，没人持锁就自己持锁），开 `state.watch`（带任务书，搜索要查），推送由 `coord.StateFold` 折进状态（见 [coordinator.md](coordinator.md)「订阅」）：`lagged` 按副本的 seq 重开，副本接不上时不带 `after_seq` 重开。机器由 `machines.watch` 推来（协调器没有这个方法时读一次 `machine.list`），agent 列表每次连上读一次；视图没有定时轮询。选中任务的最近一次 run 和「盯在旁边」的 run 各开一个 `run.output.watch`，不再显示的就取消；带 `key` 的事件原地替换前一条（agent 正在写的消息和还在跑的命令就这样一段段长出来，最终的那条换上去），没有 `text` 也没有 `title` 的临时事件把它去掉，每个 run 最多留 1000 个事件；流结束（`done`）就不再开，断了（机器离线等）过 5 s 在下一次更新时从游标重开，`unauthorized` 丢掉这个 run 的输出。连接结束（协调器退出、保活超时）时，视图开着就马上重连并重新订阅，否则等下次打开；退出 TUI 时放锁，run 照常跑。
- 协调器不可用（别的进程持锁且 socket 不通）：Tasks 视图显示原因，其它视图不受影响。

## Web UI（模式二）

页面的合并和改名见 [ui.md](../tasks/ui.md)「页面结构与用词」。

- 文件：`internal/server/web/`，`go:embed` 进二进制。Preact + htm + signals，浏览器原生的 ES 模块，没有构建步骤。`index.html` 引入 `css/` 的三份样式、皮肤样式表（`theme/<皮肤>.css`，`id="skin"`，由 `pages/boot.js` 换成这位访客的皮肤）和入口 `main.js`，并预载 `vendor/` 的四个文件。界面文字在各模块自己的 `zh` / `en` 词表里（`core/i18n.js`，不走 Go 的 i18n）。
- 认证：登录页列出 `/auth/logins` 给的登录方式（GitHub、OIDC），另有 token 表单。`POST /login`（表单 `token`）建一条网页会话，写 cookie `tend_session`（HttpOnly、SameSite=Strict、TLS 下 Secure、30 天）；`GET /session` 回 `{id, name, email, username, role, session}` 或 401；`POST /logout` 吊销这条会话。地址里的 `#invite-<secret>` 让登录按钮带上邀请，`#signin-<结果>` 显示登录失败的原因。另有「在另一台设备上登录」（没有邀请时才给；装到主屏幕以后它排第一、是主按钮，登录方式退成次要）：`POST /auth/device {name: platform.name, session: true}`，页面显示码和 `verify_url`，可以分享（系统的分享面板）或复制链接，在已登录的设备上打开 `#device-<码>` 允许；页面按 `interval` 轮询 `/auth/device/token`，允许后回来的是这个浏览器自己的会话 cookie（[tasks/team.md](../tasks/team.md)「身份与加入」），读 `/session` 进应用；拒绝、过期可以重来，断网和限流时接着等。终端授权页对 `session` 的码写「允许这台设备登录网页」，说明它以你的身份登录 30 天；「我」页的浏览器会话把这种会话写成「<设备名>，由另一台设备允许登录」。iOS 主屏幕 PWA 的 cookie 和 Safari 分开，会话能不能落进 PWA 要等验证 ⑩（设计稿 §10 第 12 项）在真机上确认。`/client` 接受 header 里的 token 或这个 cookie；WebSocket 握手校验 Origin（同源）。会话被吊销或用户被停用，连接立即断开，页面回到登录页。
- 响应头：CSP `default-src 'self'`（不允许内联脚本和 `style` 属性，宽度等动态样式经 CSSOM 设置；另写明 `manifest-src 'self'`、`worker-src 'self'`）、`frame-ancestors 'none'`、`nosniff`、`no-referrer`、`Cache-Control: no-cache`。
- 装到主屏幕（PWA）：
  - `/manifest.webmanifest`：`name` tend，`display: standalone`，`start_url: /?source=pwa`，`scope: /`，`theme_color` 和 `background_color` 取默认皮肤（`tend`）浅色的底色（访客自己的皮肤只在他的浏览器里，server 不知道）；图标 `icon-192.png`、`icon-512.png` 和 maskable 的 `icon-maskable-512.png`，另有 iOS 用的 `icon-180.png`（`apple-touch-icon`）。图标由 `pwa.go` 按默认皮肤的强调色画出页头的 `>_`，第一次请求时生成，仓库里没有图片文件；maskable 的记号在中心 40% 半径的安全区里。`<meta name="theme-color">` 由 `pages/boot.js` 跟着这位访客的皮肤和主题取 `--bg`。
  - `/sw.js`（根作用域）：`web/sw.js` 加上 server 写进去的一行 `BOOT`：构建号和要留的文件（`/` 和 `web/` 里的 `.js`、`.mjs`、`.css`）。安装时把它们存进缓存 `tend-<构建号>`；访问页面（`/` 带任意查询）和这些文件先从缓存取，缓存里没有就问 server；其余（`/api`、`/client`、`/auth`、皮肤、图标、manifest）一律直接去 server，不做离线模式。接管时删掉别的 `tend-*` 缓存。新构建的 `sw.js` 字节不同，浏览器装上以后等着，页面发 `skip` 才接管。
    - 推送（[tasks/team.md](../tasks/team.md)「人在任务里」的实现）：`push` 每一条都显示一条通知（浏览器会收回不显示通知的 worker 的推送权限），读不懂的负载显示一条只写「等你处理」的 `tend` 通知。有任务的：标题是任务，`n > 1` 时加「· n 项等你」，正文按 `kind` 写（权限请求写 `what`：「要你允许：<命令>」），`tag` 是任务 id、`renotify`，按钮是「查看」，负载带 `reject` 令牌时再加「拒绝」，`data` 带 `task`、`item`、`link` 和令牌（`act`）。隐藏内容的：标题 `tend`，正文「n 项等你」，`tag` `tend`，`data.count` 标明它是计数；隐藏内容的任务完成 `tag` 是 `tend-done`；看得到内容的任务完成正文「任务完成了」。用词跟着浏览器的语言（`navigator.language`，worker 读不到页面选的语言），`sw.js` 自带中英两份，每种待处理项都有。`notificationclick` 关掉通知；点通知本身或「查看」：已经开着这个页面的窗口就把 `{open: link}` 发给它并聚焦，否则 `openWindow('/' + link)`（隐藏内容的 `link` 是空的，打开首页）；点「拒绝」不开页面，带同源 cookie 和 `X-Tend` `POST /api/act {token}`，再在同一个 `tag` 上换一条不响的结果通知：204「已拒绝」，409 带 `by`「已被 <名字> 处理」、不带「已经不等你了」，401「请打开 tend 重新登录后处理」，其余（过期、令牌不对、没网）「没能拒绝，打开 tend 处理」；结果通知点开去 `link`。`pushsubscriptionchange` 用原来的公钥重新订阅，`PUT /api/push/device` 登记新的（名字空着，页面下次打开续期时补上）。
  - `/`：`index.html` 里的 `<meta name="tend-build">` 由 server 填上构建号，页面据此和 hello 的比（`wire.js`）。
  - `core/platform.js`：页面跑在哪，一个接口：`kind` 是 `browser` 或 `pwa`（`display-mode: standalone` 或 iOS 的 `navigator.standalone`），`os`（`ios` / `android` / `other`，iPad 装成 Mac 的也认），`name`（给设备码登录的设备名：iPhone、iPad、Android、Mac、Windows、Linux），`secure`（`isSecureContext`）；`start()` 只在安全上下文、并且有构建号时注册 service worker；`refresh()` 载入新构建：有等着的新 service worker 就让它接管（`skip`），接管后（`controllerchange`）再重载，没有就先 `update()`，仍没有才直接重载，因为只重载的话拿到的还是旧缓存。`outdated` 横幅的「刷新」走它，不自动刷新（没发出去的字只在内存里）。`push`：浏览器的 Web Push，没有 service worker、`PushManager` 或通知时是 null；`ask()` 向浏览器申请通知（要在点按的处理里、任何 await 之前调），`current()` / `subscribe(key)` / `unsubscribe()` 是这个浏览器的订阅，`clear(keep)` 关掉 worker 显示过、`keep` 不留的通知。`onOpen(fn)`：页面开着时点了通知，worker 发来的链接交给 `fn`，`boot.js` 交给 `router.open(link)`。
  - `core/push.js`：这位登录者在这个浏览器上的推送：`state()`（`none` 这个浏览器没有、`denied` 浏览器拒绝了通知、`on`、`off`）；`on()` 先申请、再用 `GET /api/push/key` 的公钥订阅，`PUT /api/push/device {subscription, name: platform.name}` 登记；`off()` 退订并 `DELETE /api/push/device {endpoint}`；`renew()` 在登录后（`Root`）每次打开页面时把订阅再登记一次（server 90 天没续期的设备就删），订阅用的不是 server 现在的公钥时先退订、重新订阅；退出登录前先 `off()`；`clear(inbox)` 在 inbox 每次变化时（`App`）关掉已经不在「等你」里的通知（按待处理项的 id；隐藏内容的计数在「等你」空了时关；任务完成和拒绝的结果不动；第一次推送之前不动）。

- 没有轮询：页面上的一切随 `state.watch`、`machines.watch`、`inbox.watch` 和 `run.output.watch` 的推送变化，首页的数每次重画时取当前时间。页面不调用 `setInterval`（`TestThePageRunsNoInterval`）；剩下的计时器都只响一次：防抖、重连的退避、调用超时、提示的停留、`g` 开头的两键序列、设备码登录的下一次轮询。
- 页面：首页、任务（列表、看板、树、详情、对话、改动）、运行、机器、Agent、团队、我，以及登录、终端授权、邀请。替别人登记一台机器用 `tend-server token add --node <机器> --owner <用户>`。

### 结构

- **内嵌的第三方文件**：`web/vendor/`，放 npm 上的 `preact.mjs`、`hooks.mjs`、`htm.mjs`、`signals-core.mjs`，许可证在 `LICENSES/`。只给测试用的 `preact-render-to-string` 和 preact 的 `test-utils` 放在 `webtest/vendor/`，不进二进制。
  - `tools/vendorweb` 按各自的 `manifest.json` 下载 tarball，核对 npm 的 `integrity`，取出文件和 LICENSE，把上游和写出后的 sha256 记回 manifest。
  - 只允许一种改写：manifest 里某个文件的 `imports` 声明「裸模块名 → 相对路径」，比如 `hooks.mjs` 的 `preact` → `./preact.mjs`。render-to-string 和 test-utils 指向 `../../web/vendor/preact.mjs`，这样它和组件用的是同一个 preact 实例，hooks 才挂得上。
  - 文件里出现没有声明的裸 import，或者声明的改写一处都没命中，工具就报错。
  - 版本升级：改 manifest 里的 `version`、清掉 `integrity`，再运行 `go run ./tools/vendorweb`。
- **分层**：`vendor` ← `core` ← `ui` ← `pages`，每层只 import 自己和下面的层，`ui` 不直接 import `core/wire.js`；入口 `main.js` 不受限制。所有 import 都是相对路径，不用 import map。`web/package.json` 的 `{"type":"module"}` 让 node 把这些 `.js` 当 ES 模块跑。
- **`core/`**（不碰 DOM，全部在 node 里测）：
  - `wire.js`：`/client` 上的 wire 帧，按行拆帧。
    - 连上后先发 `hello{proto: 2, role: "client"}`，形状就是 `remote.HelloParams`。回来的 `proto` 不是 2，或者 hello 的 `build` 和页面自己的不一样（`index.html` 的 `tend-build`，没有时是第一次 hello 的；server 换了一套页面文件，见 [wire.md](wire.md)「握手与版本」），状态记为 `outdated`，不再重连（页面要重新加载）；回来的 `methods` 决定哪些 `.watch` 可以开，没列出的方法在本地直接结束，报 `unsupported`。
    - `call`：30 s 超时后发 `cancel`，报 `timeout`；连接中的调用等 hello 完成再发，离线时立即报 `offline`，断线时报 `closed`。server 发来的 `ping` 照答，别的请求回 `unknown_method`。
    - `watch`：发 `req` 之前先登记接收者。推送按 `id` 交给这个流，第一条是 `open{cursor, mode}`；同一个 `id` 的 `res` 是最后一帧：`result` 表示正常结束，`lagged` 按游标立即重开，其它错误码就是流的终点。`cancel` 只发一次，之后到达的帧一律丢掉。
    - 断线后 1 s 起翻倍、最长 30 s、带抖动地重连（成功 hello 后回到 1 s），然后每个流用它的主人给的参数（带游标）重开。
    - `setVisible(false)` 结束可暂停的流（输出），`state.watch` 保留；`setVisible(true)` 按游标重开它们，离线时立即重连。连接状态放在一个 signal 里：`idle` / `connecting` / `open` / `offline` / `outdated` / `closed`。
  - `proto.js`：由 `tools/protogen` 生成，不手改：`PROTO`、`frame`、`code`、`push`、`mode`、`methods`、`kind`、`family`、`tools`（工具名 → family）、`density`（[output.md](output.md)「密度」）、`workflows`（内置工作流的名字，新建任务的工作流选择器把它们和项目自己的放在一起）。`core`、`ui`、`pages` 比较错误码、帧类型和密度都用它。结构测试检查页面 `call` / `watch` / `has` / `send` 的每个方法名都在 `coord.Methods` 里，协调器还没有的几个列在 `comingMethods`，各自标着补它的卡。
  - `fold.js`：把推送的信封折进状态，逐条照搬 `task.State.Apply`；`fold_test.go` 用 Go 生成的信封、`foldfuzz_test.go` 用随机日志和它对照。`parts` 表列出每种事件改动状态里的哪几张表。
  - `store.js`：页面的状态。
    - `state.watch` 的推送：
      - `open{mode: snapshot}` 或 `reset` 开始一份新快照；
      - `snapshot{part, items}` 的 `part` 是 `task.State` 那几张表的 JSON 名（`tasks`、`runs`、`projects`、`shares`、`drains`、`agent_defs`），`items` 是 id → 对象，同一张表可以分几批，累加起来；
      - `live{seq}` 结束快照，整份换上；
      - 之后是 `journal{信封}`，seq 不大于已应用的就跳过。
      - `open{mode: resume}` 表示接着已应用的 seq 继续。`affordances` 这个 part 和之后的 `affordances` 推送按人计算，不折叠，放进 `affordances` 这个 signal：`{runs: {运行: [操作]}, tasks: {任务: {actions, route}}}`。推送按键合并，值为 `null` 的删掉；新快照开始时先暂存，`live` 时换上。`route` 是这个任务的消息此刻会去哪：`{to: run | reply | workpad, run?, stage?, version?, why?}`；运行的操作里有 `steer`、`interrupt`、`allow_run`。
    - 状态原地折叠，每张表一个版本号 signal，一帧最多加一次（页面上用 `requestAnimationFrame`）。
    - 折叠遇到自己没有的对象，或者不认识的 part：结束这个流，不带 `after_seq` 重开。重开时的参数：已经 live 过就带 `after_seq`；`no_briefs` 时任务不带任务书，由 `brief(id)` 用 `task.get` 按需取回并填进状态；`briefOf(id)` 同步地说有没有：有就是任务书，开着 `no_briefs` 但还没取回是 `undefined`，没开 `no_briefs` 而任务没有任务书是空字符串。
    - `machines.watch` 推 `machines{items}`，`inbox.watch` 推 `inbox{items}`，都是整份替换。
    - `output(run)` 按运行引用计数：第一个持有者开 `run.output.watch`（可暂停，续传带 `from`），最后一个释放时 `cancel`。`run.output{events, cursor}` 里带 `key` 的事件替换前一条同 `key` 的（按事件本身找，前面补进的页不影响），没有 `text` 也没有 `title` 的临时事件删掉同 `key` 的临时事件，`output.js` 也不画这样的临时事件；`open{mode: gap}` 在事件里插一条 `gap{from, to}`。
    - `more()` 取最早一个事件之前的一页：`run.output.page{before, file, n: 200}`，一个都没有时 `before: -1`，按 id 去重；翻到这一代日志的开头而有 `prev` 时，下一页从上一代的末尾取。`head` 这个 signal 说前面还有什么：`{more}`、`{start}`（运行的开头）、`{gone}`（`gone` / `not_found`，已清理），取页时带 `loading`，失败带 `failed`，`stale` 时从最后一页重新找。`output(run, {watch: false})` 只翻页不开流；server 没有 `run.output.watch`（`unsupported`）时也改成 `more()` 取最后一页。往前取的页加起来超过 `OUTPUT_BYTES`（32 MiB）时，`trim()` 丢掉最早的几页。`raw(run)` 取最后一页的原始行。
  - `keys.js`：作用域栈，顺序是 `modal` → `drawer` → `list` → `page` → `global`，同一层里后推入的先查；`blocks` 的作用域挡住下面各层，`when` 为假的绑定让给下一层。
    - 键名的写法和操作表一致：`n`、`Shift+D`、`Mod+K`（⌘ 或 Ctrl）、`g h`（先按 g，1.2 s 内按 h）、`Esc`、`Space`；`Alt` 组合不认。
    - 正在组字的按键不处理。输入框里只有 `Esc` 和 `Mod+Enter` 会交给作用域。`；` `，` `？` `、` 当作 `;` `,` `?` `/`。
    - 同一个作用域里一个键只能绑一次，一个键也不能同时是另一个序列的开头。
    - `active()` 列出此刻按下会生效的绑定，每个键一次，按查找顺序；`changed` 这个 signal 在推入、弹出作用域时加一，键栏据此重画。
  - `layout.js`：宽度不到 `PHONE_BELOW`（720 px）是手机形态，否则是电脑形态，放在 `form` 这个 signal 里，由入口按 media query 设置；电脑上宽度不到 `NAV_OPEN_FROM`（1200 px）时侧栏默认收起。`createNav` 管侧栏开合：用户选过一次就记在浏览器里（`tend-nav`），之后不再随宽度变；存储不可用时照常工作，只是不记。`mac` 决定 `Mod` 显示成 ⌘ 还是 Ctrl。
  - `toasts.js`：底部的提示。带撤销的留 `UNDO_WAIT`（6 s），普通的 4 s；`undo()` 撤销最新一条可撤销的。提示不盖住底部的控件：电脑上在右下，下面有输入框时在输入框之上；手机上在底栏和新建按钮之上，全屏页上在它自己底部的控件（页脚、输入框）之上；这两种高度由 `ui/toast.js` 在提示出现时量出。底部弹层打开时提示移到顶部的遮罩上。
  - `router.js`：地址 ↔ `{page, task?, run?, view?, wait?, event?, auth?}`。页面有 `home`、`tasks`（`view` 取 `list` / `board` / `tree`）、`runs`、`machines`、`agents`、`team`、`me`。以前的 `inbox` → 首页，`settings` → 我，`projects` → 团队；`#task-<id>` 打开任务页并选中它；`#task-<id>/r-<运行>/e-<事件>`（`link()` 生成）还打开那次运行的对话、滚到那一步，`event` 只用一次，生成地址时不带；`run` 写在查询里（`?page=tasks&task=…&run=…`，或 `?page=runs&run=…` 打开那次运行自己的页）；`#wait-<任务>`（推送的链接）和 `?wait=<任务>` 是首页上那个任务的落地页；页面一打开就在 `#wait-` 上时，历史改成首页之上压一条 `?wait=`，页面开着时点通知（`open(link)`）也先压首页（已经在首页列表上就不压），所以从落地页返回总是回到「等你」；`back(to)`：这一条是页面自己压的（`history.state.back`）就 `history.back()`，否则把地址换成 `to`。`#device-`、`#invite-`、`#signin-<结果>[?参数]` 解析成 `auth`，生成地址时不带它们。
  - `i18n.js`：每个模块用 `register(模块, {键: [zh, en]})` 注册自己的词表。两个模块用了同一个键、缺一种语言、两种语言的 `%s` / `%d` 顺序不同，都会报错。
  - `actions.js`：页面唯一的操作表。每个操作有 id、键、作用域层级、分组；`bar` 的进键栏，`palette: false` 的不进命令面板（移动、数字、面板自己）。键位就是设计稿 §6.6 的键表：`g h/t/b/r/m/a/p/s` 去各页，`Mod+K` 命令面板，`?` 快捷键，`/` 搜索，`n` 新建，`[` 侧栏，`Mod+Z` 撤销，`Shift+T/L/M` 主题 / 语言 / 密度（全局）；`v` `d` `e` `x` `Shift+D`（页面）；`j` `k`（别名方向键）`Space` `Enter` `1`–`9`（列表）；`End` `Home` `Shift+O` `Mod+F`（输出）。一个键只属于一个操作。
    - 组件不直接写键：用 `useActions(层级, {id: {run, when?, label?}})` 绑定自己能做的操作，键从表里来；`label` 让页面换一个更贴切的说法（首页的数字叫「作答」，`d` 叫「重试」）。哪些绑定生效（`when`）随页面变了，作用域就重新推入，键栏跟着变。
    - 命令面板列出此刻生效的操作（`runnable(keys.active())`），按中文名、英文名、id、键都能搜到，开头匹配的排前面；快捷键页按分组列出整张表。
  - `commands.js`：页面的写操作。发出时按 key 记为 pending；可撤销的写（标记完成）在列表里先藏起来，应答之后等那张列表下一次变化再放出来，免得闪回；没收到应答（`unsure`：`timeout` / `offline` / `closed`，页面的出错提示也用它）记为 `unknown`，`retry` 用同一个 command id 重发，coordinator 的回执保证不做两次。可撤销的写由调用方先用 `newID` 取好 command id，撤销发 `task.undo{id, command}` 点名它。状态本身只来自 journal 的折叠。
  - `http.js`：普通 HTTP：`/session`（未登录是 null）、`/login`（表单提交 token）、`/logout`、`/auth/logins`、`/auth/invite`、`/auth/device`（这个浏览器从另一台设备登录）、`/api/device`、`/api/push/key` 和 `/api/push/device`（推送）、`/api/users`（页面上的人按名字显示，项目成员变了重读；`local` 显示为服务器管理员），以及各页用的 `/api/*` 和皮肤的预设 `/theme/presets.json`。写请求带 `X-Tend`，网络不通报 `offline`。
  - `prefs.js`：语言（中文 / English / 跟随浏览器，跟随时不存）、主题（跟随系统 / 浅色 / 深色）、密度（紧凑 / 标准 / 宽松）和皮肤（预设、自己的强调色、高对比度，合成样式表的名字 `skin`，换了 `boot.js` 立刻换样式表），存在 `tend-lang`、`tend-theme`、`tend-look`；浏览器通知开不开记在 `tend-notify`（默认开，还要浏览器允许）；输出的密度（简洁 / 标准 / 详细，默认标准）记在 `tend-output-density`；改动的 diff 合在一栏还是并排（默认合在一栏）记在 `tend-diff-view`，忽略空白（默认不忽略）记在 `tend-diff-space`；存储不可用时用默认值。
  - `notices.js`：页面在后台时要弹的浏览器通知：inbox 里等的东西（每个待处理项按 id 和版本，没有就是任务和原因）有了新的那几条。store 的 inbox 在第一次推送之前是 `noInbox`，第一次推送只记下、不弹，已经在等的不算新。
  - `format.js`：数字的写法（`312k` tok、`$3.18`、`1h 04m`、`14:32`）。token 数是 input + cache_write + output，不含读缓存。
  - `select.js`：页面上的数，全是纯函数，输入是 store 的状态、机器、inbox 和当前时间。首页每个数的来源见下面「首页」。
  - `tasks.js`：任务页的纯函数：处境归到哪一列、筛选、列表 / 看板 / 树的行、看板上一次拖放对应的写、目录候选、默认的机器和 agent、派发前的判断、拆解草稿的编辑和检查、一个任务此刻能做哪些操作。规则见下面「任务页」。
  - `output.js`：一段对话的事件怎么画，纯函数，见下面「对话与输出」。`items` 和 Go 的 `output.Items` 同一个规则：结果并进它的调用，同一个 `parent` 下连着的读和搜并成一步，临时事件不算。`fold.js` 的 `conversation(state, run)` 是这次运行所在的对话：同一任务里顺着 `parent` 回到同一个起点的运行，按 `seq` 排。
  - `follow.js`：长对话怎么滚，纯函数，见下面「对话与输出」。
  - `changes.js`：一次运行的改动：`run.changes` 按页取（第二页起带第一页的 `snapshot`，节点据此认出工作区变没变）、工作区在两页之间变了从头再取（最多 `RESTARTS` 次）、已结束运行的列表和取过的 diff 页留着不再取（最多 `KEPT_PAGES` 页，先取的先丢）；文件按目录排、筛选、哪些先折起；`run.diff` 按页取（每页 `HUNKS` 处，忽略空白时带 `ignore_space`，和不带的分开留），几页接成一份（一页从一处中间接着时接在上一处后面）；diff 行带新旧行号（`rowsOf`），并排时删的和紧跟着的加的配成一行（`sides`）；选行（`pick`）和把选中的行写成引用（`quote`、`withQuote`）。见下面「改动」。
  - `drafts.js`：没发出去的字，按任务记：给 agent 的消息（对话的输入框）和退回意见（验收对话框）。页面开着时一直留着，关了页签或对话框再打开还在；引用改动的行写进其中一份，并让那个框拿到焦点。
- **`ui/`**（共用组件）：每个组件从一开始就有电脑和手机两种形态，读 `form` 决定画哪一种，页面不用分两份写。样式在 `css/base.css`（尺寸变量、字体、外框）和 `css/components.css`，颜色只用皮肤 token；手机形态的样式挂在外框的 `data-form="phone"` 下，密度挂在 `:root[data-density]` 上。
  - `Shell`：电脑上是 56 px 顶栏（品牌、搜索框和 `Mod+K`、在跑 / 排队 / 离线计数、当前用户）、左侧栏（七个页面，`[` 或底部按钮开合，收起时只剩图标，等你的数目变成角标；下方是新建任务）和 32 px 键栏；手机上是 52 px 顶栏（server 名可点开切换）和底部四个标签：等你、任务、运行、我。机器页归在「运行」标签下，Agent 和团队归在「我」下。连接断开时顶部出横幅，可立即重连；server 已升级时提示刷新。它也提供按键的上下文。
  - 键栏只列此刻生效、操作表里标了 `bar` 的操作；同一说明的键合成一项（`j k 上下一条`），连续的数字写成 `1–9`。手机上不画键帽。
  - `Button`（primary / quiet / danger，`on` 是按下的开关）、`Chip`、`Segmented`、`Check`（勾选框，旁边写说明）、`Tabs`（方向键移动，只有选中的一项在 Tab 顺序里）、`Status`（形状加颜色区分状态，屏幕阅读器读状态名）、`Panel`、`Stat`。手机上按钮高 44 px，筛选项横向滚动。
  - `Table`：列表都用它。电脑上是表格，表头可排序（升、降、取消）；手机上每行变成一张卡片，列的 `mobile` 决定它在卡片的哪个位置。选中按 id 记，排序和推送之后不丢。`j` / `k` / 方向键移动，`Enter` 打开，`Space` 展开，`1`–`9` 选择。超过 `VIRTUAL_ABOVE`（200）行只画可见的一段，选中项移出视野时滚过去。
  - `ExpandItem`：原地展开的一条（等你处理的事）。收起时显示状态、问题、等了多久和快捷操作，标题一行放不下时截断，悬停看全文；展开后标题和说明换行显示全文。手机上只留第一个操作，展开 / 收起写成文字；`page` 时这一行不展开、点了去它自己的页，右边是「›」。
  - `Modal`：电脑上是居中的对话框；手机上从底部升起（sheet），长表单（`full`）占满整屏并带返回。它推一个 `blocks` 的 `modal` 作用域：`Esc` 关闭，`Mod+Enter` 执行主操作，页面的键被挡住；焦点移进来、`Tab` 在里面循环，关闭后回到打开前的元素。`Drawer` 在电脑上是右侧面板，手机上占满整屏。整屏的页头有 `extra` 一格，任务页在这里放上一个 / 下一个。
  - `Toasts`：`Mod+Z` 或按钮撤销最新一条。
  - 手机上左右切换用上一个 / 下一个按钮，不用滑动手势。
  - `Menu`：按钮下弹出的一小列选项，打开时占住按键（方向键移动、`Esc` 关闭，焦点回到按钮）。顶栏的用户菜单放语言、主题、密度和退出登录。
  - `TextInput` / `TextArea`：上面是标签，下面是说明或错误。
  - `Markdown`：任务书这类文字。认标题（画成 h3–h6）、段落、有序 / 无序列表、引用、围栏代码，行内的代码、粗体、斜体、链接和裸 URL（下划线只在词外才算强调，`snake_case` 和 id 原样显示，和 GitHub 一致）；全部生成节点，不用 innerHTML，其余原样当文字。链接只放行 http(s) 和本站的路径、查询、片段（`//` 开头的不算），别的链接连同括号当文字显示。
  - `Picker`：从一列里挑一个或几个（`multi`）：父任务、前置任务、机器、agent。电脑上在按钮下弹出，手机上占满整屏；输入即搜（按标签、值、说明和 `words`），方向键移动，`Enter` 选中。每项可带状态、说明和提示。
  - `TreeList`：缩进的树（最多画三级缩进），`Space` 或行首按钮展开 / 收起，键和选中同 `Table`；手机上点一下直接打开。
  - `Board`：看板，按列排卡片，键按列的顺序走。拖动时每列标出能不能放（`canDrop`），放在不能放的列上由 `onRefused` 说明原因。只在电脑上用。
  - 图表（`charts.js`）：`Spark`（数字下面的小折线，可画成阶梯）、`Bars`（按天并排、每天按部分堆叠，部分按顺序取色）、`Timeline`（每台机器一条泳道，运行按重叠排成几行，颜色是运行的状态；右端是现在）、`Meter`（一条按部分分色的横条）。尺寸用 style 对象设置，走 CSSOM，不产生 style 属性。
  - `Palette` / `Help`：命令面板和快捷键页，手机上占满整屏。键栏右端提示 `⌘K 全部命令 · ? 快捷键`。
  - `Output`（时间线）、`AnswerForm`（原地作答）、`Composer`（输入框）：见下面「对话与输出」。`Output` 的 `bare` 不画工具栏、不占按键（运行页的预览用它）；`Diff` 画时间线里的 diff。
- **`pages/`**：
  - `boot.js`：页面启动。读偏好并写到 `<html>`（`lang`、`data-theme`、`data-density`，皮肤样式表的地址），按 media query 设形态，建按键、路由、HTTP；问 `/session`：没登录画登录页，`#device-<码>` 画终端登录确认，否则连 `/client`、开 store 的三个 watch，画应用。登录结果、邀请这类一次性片段用过就从地址里去掉；页面隐藏时交给 `wire.setVisible`。socket、fetch、存储和时钟都可以从参数传入，预览和测试靠它换成假的。入口 `main.js` 只调用它。
  - `auth.js`：登录页（各登录方式的按钮，邀请时写明谁邀请、什么身份、加入哪个项目、何时失效，下面是 token 登录）；登录被拒（`not_admitted` / `disabled`）时说明是哪个账号、为什么，可以换账号或复制账号信息；终端登录确认（核对设备码，显示设备名、来源地址、请求时间，允许或拒绝；码已用过或过期时说明）。
  - `app.js`：登录后的页面：外框、全局操作（去各页、命令面板、快捷键、主题、语言、密度）、命令面板里的任务 / 运行 / 机器搜索（运行打开它自己的页）；`n` 和 `/` 在任何页都转到任务页，打开新建表单或搜索框。
  - `home.js`：首页，见下。
  - `conversation.js`：一个任务的对话，见下面「对话与输出」。
  - `runs.js`：运行页和一次运行自己的页，见下面「运行页」。`changes.js`：改动页签，见下面「改动」。
  - `me.js`：我页，见下面「我页」；`notify.js`：电脑上按 `notices.js` 弹浏览器通知（标题是任务，正文是原因，`tag` 是任务 id），点一下回到页面、打开那个任务。
  - `agents.js`：Agent 页，见下面「Agent 页」；它的推导（来源、编译后的样子、能跑的机器、谁在用、能做什么、新建 / 导入 / 复制的起始文本）在 `core/agents.js`，除了读两份列表的 `createAgentDefs` 都是纯函数。
  - `machines.js`：机器页，见下面「机器页」；`team.js`：团队页，见下面「团队页」；`project.js`：团队页里项目的抽屉（成员、设置、工单同步）。这些页的推导（分组、摘要、提示、排队、泳道、每个人在哪些项目、项目的数、交接会做什么、审计的分类、项目设置的草稿和只发改了的字段、检出目录的检查结果、工作流定义的名字、工单绑定的状态和 issue 链接）在 `core/team.js`（纯函数）。`ui/secret.js` 的 `Secret` 显示服务器只给一次的值（节点 token、命令），带复制；剪贴板被拒时选中文字让人手动复制。
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
  - 手机上首页只有等你和在跑两块，上下排，图表不画：
    - 等你：页头写条数和「同类里等得久的在前」；下面一行横向滚动的筛选（全部、我负责、我验收、我派发的，都带数，选的记在 `tend-home-as`）；排序和电脑上一样。每条是一张卡片的一行：任务标题，下面是它问了什么或怎么了，右边等了多久和「›」。每行只有一个快捷操作：权限是允许，待验收是完成（可撤销），出错是重试（先确认），其他是打开；提问没有快捷操作。点一行进它的落地页（`?wait=<任务>`），不在原地展开。
    - 落地页（通知的「查看」和点通知也到这里）：占满屏幕，不带顶栏和标签栏；页头「‹ 等你」（返回）、按类写的标题（等你批准 / 回答 / 验收 / 处理）和「第几条 / 共几条」；下面是状态、任务标题和它问了什么，再往下是作答的内容（见下一条），最后一行「处理完自动跳到下一条 · 还剩 N 条 · 返回回到「等你」」（最后一条写「这是最后一条」）。这一条从列表里走了（答了、别人处理了、结束了），就换成现在排在它那个位置上的一条（替换地址，返回仍回到列表）；一条都不剩时回到列表，提示「都处理完了」。打开时它已经不在「等你」里：写「已经不等你了」，给「下一条」（还有别的时）和「打开任务」。按筛选找不到这一条时用全部。电脑上打开 `?wait=` 在列表里选中并展开那一条，地址换回首页。
    - 作答的内容：编号 · agent @ 机器 · 为什么是你；详情（出错时是 `detail` 和 check 的 `tail`）；它刚做了的三步，待验收的换成它改了什么（`run.changes` 的合计和前三个文件，server 有这个方法时）；作答表单（允许一次、这次运行里都允许各带一句说明，拒绝的理由框下写理由会转给 agent）或回复框；完成、重试按钮；一行去向（「回给 X：它在这一轮里接着做」「作为回复发给 X：同一个会话里接着做」，待验收且 check 过了写「check 通过：<命令>」）和「打开任务 ›」。
    - 在跑：一行「在跑 N · 排队 M ›」（点它进运行页），下面只列在跑的，每行状态、标题、时长，点一行打开任务；停止和花费在运行页。
  - 首页的数不靠定时器刷新：每次重画时取当前时间。
- **任务页**：
  - 处境（`fold.js` 的 `situation`）归成五列：等你、在跑、排队、未开始、已结束（完成和取消）。筛选：我相关的（负责或验收）、项目、列、搜索（标题、id、标签）；前三项记在浏览器里（`tend-task-filter`），搜索不记。选中的任务写在地址里（`task`），电脑上换选中用 replace，手机上打开一个任务是 push。
  - 列表：按列的顺序，同列里最近更新的在前；每行是处境、标题（带子任务进度、花费）、原因、谁在哪跑、更新时间。手机上按列分节，每节一张卡片表。
  - 看板（只在电脑上）：五列卡片。拖放只有四种对应写操作：待办且没有未结束运行的拖到已结束是完成（带工作流的不行）；拖到未开始是「先不开始」（待办且没有未结束运行的，或已结束的）；从未开始拖到排队或在跑是开始（`task.start`）；从已结束拖到等你是重新打开。除开始以外都走 `task.set_status`，由 `task.undo` 撤销。别的组合不能放，放下时提示原因。
  - 树：父子关系缩进，兄弟按 `after` 再按创建时间排；搜到的任务连同它的上级一起显示，收起的节点记在页面里。
  - 详情：电脑上列表和树是右边的分栏，看板是右侧抽屉；手机上占满整屏，页头有上一个 / 下一个（按当前视图的顺序），不用滑动手势。内容：处境和工作流阶段；按钮只有观看者的 `affordances` 里这个任务的 `actions`（`core/tasks.js` 的 `actionsOf`），另加纯客户端的「照这个再建一个」，最后一次运行的操作有 `continue` 且任务没有工作流时加「退回意见」；主操作按处境选（重新打开、开始、停止、审拆解、验收通过、采用新版需求、合并、完成、派发），处境要的那个没给就没有主操作，不拿别的顶上；编辑和派发在电脑上直接显示，其余在「更多」里；断线时（`wire.status` 不是 `open`）按钮、「更多」和它们的键一律变灰；需求有新版、来源已关闭、有拆解草稿时各一条提示；负责人、验收人、在哪跑（项目默认的注明）、目录、分支、全部运行的花费、issue 链接；任务书（`Markdown`，按 `briefOf` 按需取回）、验收标准、工作流各阶段、父任务和前置任务、子任务、运行记录（状态、agent @ 机器、阶段、`doing` / `note` / `last`、结论、时长、用量）、工作记录。电脑上有运行的任务，详情分「概览 / 输出 / 改动」三个页签，默认是输出，选过的记在浏览器里（`tend-task-pane`）；概览以外的页签里头部收成一行（处境、标题、页签、主操作、「更多」、关闭，编号和项目在标题的提示里），任务详情这一栏从页顶起、列表的页头只在左栏，800 px 高时时间线留有约 520 px；概览里的运行记录不开 `run.output.watch`，点一次运行就在输出里打开它所在的对话（地址带 `run`）。手机上点运行，再叠一层整屏的运行页（见下面「运行页」）。
  - 写操作都经 `commands.js`，按任务记 pending，操作中按钮变灰；完成、先不开始、重新打开和调整位置可以撤销（`task.undo`，任务逐项回到那次写之前的样子）；停止和取消先确认。进行中的运行失联（`unknown`）而它的 affordances 里有 `abandon` 时，「更多」里有「放弃这次运行」（先确认，说明那台机器上的进程可能还在跑，发 `run.abandon{id}`）。失败时提示错误码，没收到应答时说明结果不明。
  - 新建（`n`）：标题、任务书（写 / 预览两个标签）、验收标准、项目、机器、agent、目录、工作流、父任务、前置任务。目录候选先是项目在这台机器上的检出目录，再是最近用过的目录；手输的目录只在 server 支持 `project.dirs` 时去那台机器上检查（停手 400 ms 后），不支持时不检查。没写完的新建表单记在浏览器里（`tend-task-draft`），发出后清掉。三种建法：先不开始、建好后拆解（`task.plan`）、建好就开始（`task.start`，主操作，`Mod+Enter`）。编辑只发改过的字段；子任务带上父任务的项目和目录；复制带上原任务除标题和工作流以外的字段。
  - 派发（`d`）：选机器和 agent（手机上是一列可点的行），先按页面自己的规则判断（已有未结束的运行、状态不对、没选、没有目录、agent 限定了机器、CLI 没装或没登录挡住；机器离线、满载只提示）。目录和协调器取法一致（`core/tasks.js` 的 `runDir`）：任务自己的目录，没有就用项目在所选机器上的第一个检出目录，并注明在哪个目录跑；两者都没有才算没有目录，没被挡住时再问 `run.preview`，显示 CLI 版本、挡住的原因和提示。确认后 `run.dispatch`；也可以「先不开始」。
  - 审拆解：左边是草稿里的任务（按父子缩进），右边编辑选中的一条（标题、key、父任务、前置、大小、说明）；手机上编辑是再叠一层整屏。上面是拆解 agent 的问题，可以直接回复（`run.continue`）。提交前按 coordinator 的规则检查（最多 50 个、key 的写法、重复、父任务和前置存在、最多三层、不依赖自己或上级、没有环），有错不能保存。改过先保存（`task.plan_save`，带 `expected_rev`），没改过就是应用（`task.plan_apply`）；也可以丢弃草稿。
  - 验收：「验收通过」（`pass`）先确认再发 `task.gate{pass: true}`；「打回重做」（`rework`）写明原因再发 `task.gate`，原因必填；「退回意见」把原因作为 `run.continue` 发给最后一次运行。手机上按钮在上面，原因在下面。
- **对话与输出**：
  - 对话是一串运行：最新的那次开 `run.output.watch`，更早的只在往上翻到时取它的最后一页，接着往前翻。每次运行之间一条分隔线（第几次运行、续接、在哪台机器）。
  - 事件除了 [output.md](output.md) 的种类，还认节点和协调器加上的：`you{input, text, by, mode, at}`（送到 agent 的一条消息，`input` 是它的发送 id）、`resolved{request, by, decision, at}`、`interrupt{id, n, by, at}`、`mark{event: "hook", name, phase}`、`gap`。运行的 `sends` 里还没被 `you` 事件认领、排队中或没送到的消息画在最后，没送到的可以重发。`after` / `interrupt` 的消息由协调器留到运行结束，再用它们续接出下一次运行（`takes` 是它们的 id，任务书是它们的原文用空行连起来）：那次运行的第一条提示按 `takes` 拆回每条一行 `you`，写发送人，不当作任务书或无名的提示。
  - 一步一行：命令写命令本身和状态、用时、行数；读和搜并成一行（「读了 2 个文件 · 搜了 1 次」）；改动写文件和 `+N −M`；计划写更新了第几步，最新的计划钉在时间线上方。说的话、你的消息、问题和出错总是展开；第一次运行的第一条消息是任务书，只露前三行。
  - 三种密度按 `proto.js` 的 `density` 表画（[output.md](output.md)「密度」）：简洁把一轮里对话以外的步骤并成一行小结（出错的照样单独一行）；标准每步一行，agent 超过 30 行的话折起；详细把思考、命令、改动、读和搜、计划都展开，输出只露头尾各 10 行，diff 超过 200 行截断。任何密度下，出错的步骤、还在跑的命令（露最后 3 行）、没答的问题自己展开；出错的输出露最后 8 行，点开看全部。临时事件按它最终会成为的那一步画，同样守密度和筛选，排在最后：正在写的消息是斜体的正文；正在写的思考是思考那一行，写「思考中…」和第一行；codex 还在跑的命令是一条 `$ 命令 ● 运行中` 的命令，带到这时为止的字节数，露最后 3 行，简洁档也显示。
  - 最新一轮以外的轮次折成一行：第几轮、工作了多久、几步、改了几个文件、最后一句话，点开展开。筛选：全部、只看对话、只看命令、只看改动、出错的；「下一处错误」按顺序跳。
  - 跟随（`follow.js`）：在底部时新内容把视图带到底部（增长不到一屏时平滑，超过一屏或手指按着时直接跳），程序自己的滚动不算离开。滚轮向上、手指向下拖、`↑` / `PageUp` / `Home`、点滚动条、跳到某一步都算离开：视图停住，底部的按钮数离开后新来的步骤（临时事件、还没送到的消息、标题行不算），有等你的（没答的问题、出错）时换警示色并能直接跳过去，运行在离开后结束时写「运行结束了 · 看结果」；新内容前面画一条「新的」线，滚过去就消失。滚动停下（`SETTLE_MS` 100 ms，触屏松手后 `TOUCH_MS` 150 ms）时离底部不到 `NEAR`（48 px）就恢复跟随。
  - 视图上方的缓冲：离开底部并且还在滚时，锚点（视野里第一行）以上的行保持画出时的样子，从锚点往下用最新的；滚动停下后一次换上，并按锚点的新位置补偿滚动距离，视野里的那一行不动。展开、收起一步时以它为锚点。超过 `KEEP_ROWS`（1500）行时，离视野 `SCREENS`（5）屏以外的每 `PAGE`（200）行换成等高的占位，有焦点、选中文字、正在写回答的那页不换。分页从顶上的「更早」行之后算起，上次画出时每页的第一行这次仍开一页，往前补进的页和后面长出的行不挪动已有的页和行，没提交的选项和输入跟着留下；离顶部 `PREFETCH_SCREENS`（1.5）屏以内时往前取一页。
  - 键（操作表，时间线有焦点时生效，手机上总是）：`j` / `k` 选一步，`Space` / `Enter` 展开收起，`Shift+O` 展开或收起这一轮的每一步，`End` 回到底部并跟随，`Home` 到开头，`Mod+F` 查找。
  - 查找：在已加载的事件里找，150 ms 去抖，只折叠 ASCII 大小写；查的是文字、标题、输出的头尾、diff 预览和问题，截掉的中段不算。命中项所在的轮次、子 agent 临时展开，`Enter` / `Shift+Enter` 上下跳；「在更早的内容里找」先让节点找（`run.output.find`，从已加载的最早事件往前，再依次找对话里更早的每次运行；节点在哪停就从哪接着问，最多 8 轮）：没有就直接说「更早的内容里也没有」，不取页；有就一页页往前取到它为止（最多 200 页）。server 没有这个方法（`unsupported`）时照旧往前取 8 页，没找到再问要不要接着找。
  - 位置：每段对话在这个标签页的 sessionStorage 里记着离开时的锚点和是否在跟随（`tend-out-<第一次运行>`），回来时从那里开始。深链 `#task-<id>/r-<运行>/e-<事件>` 打开时跳到那一步（组里的事件跳到它所在的组）并闪一下；选中一步后「复制这一步的链接」。
  - 作答（`AnswerForm`，时间线的问题行和首页的等你条目共用）：权限请求是允许、这次运行里都允许（请求上有 `allow_run`、这次运行的 `affordances` 里也有 `allow_run` 时，发 `decision: "allow_run"`）和带理由的拒绝（手机上两种允许各带一句它管多久，拒绝下面写理由会转给 agent）；问题可以一次有几个，每个从选项里点（`multi` 的可多选）或自己写，自己写的优先，全部答完才能一起发：`run.answer{answers: {问题: 选项用「, 」连起来或自己写的话}}`，也可以不回答（`allow: false`）。只有一个单选问题时，选项在首页上带数字键。已处理的问题写谁、怎么处理的、什么时候。回答回来 `request_gone`（别人先答了，`detail` 是谁；空的是请求已经不在了）时不弹失败，表单换成「已经被 X 答了」（问的是什么仍在它所在的提问行或等你条目上），作答的按钮、输入框和数字键都收起；先答的那个回答随 `state.watch` 推到，不另外取。
  - 输入框（`Composer`）：发出之前写明这条消息会去哪，按 `route`：`run` 时可以插话（`steer`，运行的操作里有才出现）、这一轮结束后再说（`after`）、打断并改说法（`interrupt`，先确认）；`reply` 是在那台机器上接着这个会话再跑一次；`workpad` 是记下来交给下一阶段。`Mod+Enter` 发送 `task.message{id, text, mode?, expect: route}`；去向变了（`route_changed`）时不发，提示看新的去向再发，写的字留着；`cannot_send` 说原因。草稿按任务记在 `core/drafts.js`，框随字数长高到 8 行，引用写进来时拿到焦点、光标在末尾。电脑上三种发法和「打断这一轮」都和输入框同一行。「打断这一轮」先确认，发 `run.interrupt{run, turn}`。
  - 手机上时间线占满整屏，工具在上面两行：筛选横向滚动；密度、查找、跟随 / 暂停，下一处错误、原始行和复制链接收在「更多」里。输入框在下面：发法一行，输入框和发送一行，去向和「打断这一轮」一行。键盘弹出时跟随中的视图留在底部。
- **运行页**：
  - 列表是全部运行，排得晚的在前；筛选按状态（全部、未结束、失败、已结束，各带数目）和机器，记在浏览器里（`tend-runs-filter`）。列：状态、运行、任务、阶段、agent @ 机器、开始、耗时、用量。
  - 电脑上选中的一条在右边预览：状态、概况，和它的输出（简洁密度、`Output` 的 `bare`）；「打开运行」「在任务里打开」。`Enter` 打开运行自己的页（`?page=runs&run=<id>`），`x` 停止（先确认，发 `run.stop{id}`）。
  - 手机上是卡片，上面一行「N 台在线 · M 台离线」（退役的不算，点它进机器页）；打开一条占满整屏，页头有上一个 / 下一个（按列表的顺序）。
  - 运行自己的页：输出（这次运行所在的对话）、改动、概况三个页签；电脑上头部一行放状态、标题、页签、停止（还没结束时）、放弃（失联时，同任务页）、在任务里打开和关闭，手机上页签在上、按钮在下。概况写任务、状态、在哪跑、阶段、目录、分支、时间、用量、节点报的 `doing`、结论、退出码、会话，和运行的 `caps` 里它能做的事（插话、打断、整次运行都允许、运行中作答、接着会话再跑、在终端接管）。
- **机器页**（`g m`）：
  - 页头一行摘要：几台（退役的不算）、几台在线、在线机器的运行位在用 / 总数、排队数；「全部检查」（有在线的机器时：`machine.check{}`，检查中按钮不可点，回来后提示检查了几台，探不了的每台一条提示）和「添加机器」。
  - 机器按「我的机器 / 分享给我的（点名分享，或分享给我在的项目）/ 其他人的」分组，没有主人的算我的。每台一张卡片：状态、怎么连上的（`via`：本机、ssh、连入服务器）、运行位条和排队数、系统、tend 版本、装了的 agent CLI，和最要紧的一条提示：退役（主人已停用）、连不上的原因、哪个 CLI 没登录（`agents.<名>.auth` 是 `missing`）、它的 tend 缺哪些节点 feature（`missing`，附 `tend hosts install` 更新）。
  - 选中的机器（`j` / `k` / 方向键，或点卡片）：下面是它今天的泳道（每个运行位一行，同首页的 `Timeline`）；右栏写主人、连接（离线时带原因，ssh 主机还带下次重试的时间；连入服务器的节点离线时写明它自己重连，最多隔 60 秒，页面没有「立即重连」：协调器连不到它）、主机名、系统、tend、运行位、排队的运行（任务和为什么在等）、提示、每个 agent CLI（已装已登录 / 已装没登录 / 已装 / 没装和版本；标题后写多久前检查的（`checked_at`，每次重画时算），在线的机器旁边有「检查」：`machine.check{machine}`，探不了时提示原因）、分享给谁（人和项目，能否批准权限请求），主人或管理员看到「改分享」和「停止接新运行」（`machine.drain`，不用确认；停着时换成「恢复接新运行」，卡片和右栏都提示「停止接新运行 · 谁 几点起」，排队的运行写「等机器恢复接新运行」，手机也看得到这条提示）；它的节点 token（`GET /api/machines`：绑定的主机、创建和最近使用的时间），「换机」和「吊销」都先确认，退役机器的 token 只能吊销；名字对不上任何机器的 token 列在「还没接入的机器」里。`Enter` 或「看这台的运行」把运行页的机器筛选（`tend-runs-filter`）设成它，转到运行页。
  - 改分享：先写信任声明（agent 以主人的账号跑，读主人 home 下的文件、用主人的 CLI 额度，提交的 committer 是主人），再选人、项目和权限请求（只能派发 / 也能批准，附说明），保存发 `machine.share`（整份替换）。
  - 添加机器：填机器名（`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`），`POST /api/machines {name}` 回 `{id, token, command}`；对话框用 `Secret` 显示 token 和命令，说明把 token 存进 `~/.config/tend/node-token` 再在那台机器上运行命令；关掉后 token 不再显示。新机器的主人是添加的人。服务器的错误码经 `apiText` 变成一句话（`api.<code>`，没有的写码本身）。
  - 手机上只看：分组的卡片列表，点一台占满整屏看它的事实（不含 token，不能检查）；添加、分享和 token 写明在电脑上管理。
- **Agent 页**（`g a`）：
  - 读 `agentdef.list`（看得到的定义：自己管的和能用的；能读的带全文、警告和启动参数）和 `agent.list`（能选的 agent：config.json 的档案和能用的定义编译后的样子），打开时、状态里的定义变了（`agent_defs` 的版本）时都重读；机器来自 `machines.watch`。
  - 页头：一句说明（派活时选的是这里的名字）、「导入 Claude Code subagent」「新建 Agent」；按来源筛选：全部、我的、项目（归项目所有）、分享给我（别人的、我能用）、其他人的（管理员能管、没分享给自己的）、档案（config.json 里、没有同名定义的），数目为零的不画。
  - 列表按名字排：名字和用途、角色、provider · 模型 · effort（能用的取编译后的值，比如从档案起步的定义）、来源、能跑的机器、检查（能读的：✓ 没有警告，! 有警告）。能跑的机器是我能派发的机器（自己的、点名分享或分享给我在的项目的，管理员是全部；退役的不算）里，没报告这个 CLI 没装或没登录、也没被定义限定在别的机器上的；离线的也算（运行等它连上）。页脚写明档案只读、同名时定义优先。
  - 右栏（`j` / `k` 或点一行选中，`Enter` 编辑自己管的）：名字、来源、第几版和改的时间、用途；角色、provider · 模型、权限、不许用的工具、用在哪里（哪些项目的默认 agent 或哪个角色、几个没完成的任务）；每台能派发的机器能不能跑（能跑、离线、没登录、没装、节点没报告），限定在一台机器上时写明；检查：`defs.Check` 的警告（译成一句话）、codex 带 mcp 或 hooks 时派发会提示 `def_pending`、`output` / `budget` 只保存不生效、哪台机器的 CLI 没登录或没装。三个页签：定义（全文；档案写成 config.json 里的样子；不能读的写明主人没让你看）、启动参数（`launch`，写明在任务目录里启动、说明书放进任务书、在项目 context 之后；不能读的和档案写明没有）、分享（归哪个项目、分享给谁、能否看说明书；分享给我的写谁分享、我能不能看；管理员不能用没分享给自己的定义）。操作：编辑、改分享、删除（自己管的），复制成我的定义（能读但不管的，和档案：从 `profile: <档案>` 起步），导出 .md（能读的）。
  - 编辑、新建、导入、复制进同一个 Markdown 编辑器（`Mod+Enter` 保存，发 `agentdef.save {text}`；新的可选归属：我，或我负责的项目（管理员是全部项目），发 `owner: "project:<id>"`）。编辑器按 front matter 的 `name` 写明保存会怎样：改了名就存成新的、原来的还在；同名的定义已经有了就覆盖它（不是自己管的不让存）；和档案同名时定义优先；没写 `name` 不让存。协调器拒绝时（`bad_request` 带原因）写在编辑器里；存好了提示，有警告时带条数。「检查」发 `agentdef.check`（参数同保存），不保存、不关编辑器，结果写在编辑框下面：会被拒绝就列出每个问题，否则「检查通过」加上译好的警告；改了文本或归属就清掉。
  - 新建先填名字（`^[a-z0-9][a-z0-9_.-]{0,63}$`，已有的不行）、类型（claude / codex，写明装在我能用的哪些机器上）、模型（手填：协调器不知道各台机器的 CLI 认哪些模型；claude 另给 opus / sonnet / haiku / fable 别名；留空用 CLI 的默认）、角色，再进编辑器写说明书。
  - 导入 Claude Code subagent：粘贴，或从文件读进编辑器；保存时照 `defs.Import` 补全：没写 provider 或 profile 的按 claude，`model: inherit` 去掉。
  - 改分享：所有人，或点名的人（主人除外）和项目；能否看说明书；保存发 `agentdef.share`（整份替换）。删除先确认，写明已经在跑或排队的运行不受影响（它们带着派发时的定义）、还有哪些项目和任务用着它；发 `agentdef.remove`。两者都让协调器从快照重来（`reset`），页面随之重读两份列表。
  - 手机上只看：来源筛选和卡片列表，点一个占满整屏看它的事实和三个页签；新建、编辑、导入和分享写明在电脑上管理。
- **团队页**（`g p`）：
  - 页头一行：几人在用、几人已停用、你是管理员还是成员；管理员有「新建项目」（名称、负责人，`project.create`，建好打开它的抽屉）和「邀请」（身份，可选加入的项目和在项目里的角色，`POST /api/invites`；对话框用 `Secret` 显示链接一次，写明到期时间）。
  - 成员（`GET /api/users`，服务器管理员 `local` 不列）：在用的按名字排，停用的在后；每人写名字、身份、参与的项目（负责人 / 参与者 / 只读，来自状态里的项目）、名下的机器（退役的不算）。管理员另看到邮箱、登录方式和最近活动（他的凭据最近一次被用），和除自己以外每人的「⋯」菜单：设为管理员 / 成员、停用（先确认，写明会话立刻断开、工作不动）、启用、交接并停用。
  - 交接并停用：选交给谁（默认自己），列出这次会做的事：他负责的项目转给对方，离开参与的项目，没结束的任务（他负责或验收的）交给各自项目的负责人（不在项目里的交给对方），他的 agent 定义交给对方，他的机器不再对别人开放、别人排在那里的运行取消，最后停用他、吊销他的全部凭据；确认发 `POST /api/users/offboard {user, to}`。
  - 项目：看得到的项目，每个写负责人、任务数和未结束数、参与和只读的人数，绑了工单仓库的再写仓库和同步状态（同步中 / 已停 / 限流中）。`j` / `k` 移动，`Enter` 或点击打开右侧抽屉：负责人和成员；项目负责人和管理员在这里加成员（没在项目里的在用的人，参与者 / 只读）、改角色、移出（先确认），都是 `project.member`。
  - 项目负责人和管理员在电脑上的抽屉另有「设置」和「工单同步」两个标签：
    - 设置：名称、负责人、项目说明（每次运行的任务书开头带上）；仓库（名称、远端、基准分支、每个任务用自己的分支和 worktree、每台机器上检出在哪个目录，可加减）；默认机器、默认工作流（内置的和项目自己的）、实现 / 评审 / 测试 / 拆解各用哪个 agent（`agent.list`）；四个钩子各一行，按空格分参数。「保存设置」只发改了的字段（`project.edit`），没改就说没有改动。「检查目录」对每个检出目录在那台机器上查它的上一级（`project.dirs`），写明是 git 仓库、在但不是 git 仓库、不在，或这台机器不让运行去那里；server 没有 `project.dirs` 时不画这个按钮。
    - 自定义工作流：列出项目自己的，可编辑、删除（先确认，写明用着它的任务在下一个阶段开始时会失败）、新建（从模板开始，名字取定义开头的 `name`，没有就不保存）；每次保存发 `project.edit {id, workflows}`。
    - 工单同步（`/api/trackers*`，见 [trackers.md](../tasks/trackers.md)）：没绑定时是「绑定仓库」：选 GitHub / Gitea / GitLab（GitHub、GitLab 预填公网地址）、地址、仓库、机器人账号的 token（只存在 server 上）和同步设置；绑好后对话框写明按种类怎么配 webhook，用 `Secret` 显示 webhook 地址和只给一次的密钥，server 没有 `public_url` 时写明只靠轮询。绑定后显示仓库、机器人账号、状态（同步中、token 被拒已停、限流到何时）、需求数和出错数、上次成功和上次扫描、最后的错误、webhook 地址；操作是同步设置、换 token、重新同步、解绑（先确认，已建的任务留着）。说明文字按设置写完成后关单还是打哪个标签。同步记录列每个跟着的 issue：链接、对应任务、评论写于何时或还没写、已关闭（只打标签时写已打标签）、待重读、属于哪条的子工单、PR 链接、最后的错误；有任务的可「预览评论」，看现在写会是什么样（按工单系统显示的样子，HTML 注释和隐藏标记不显示，`core/team.js` 的 `shownComment`）。
  - 管理员另有三块：没用过的邀请（`GET /api/invites`：编号、身份、加入哪个项目、谁在何时发出、还剩多久，「作废」发 `DELETE /api/invites`）；谁能直接登录进来（`GET /api/admits`：邮箱域名 / 邮箱 / 账号、身份；「加规则」和每条的去掉，写明先认已关联的身份、再认邀请、最后才看规则、只认已验证的邮箱）；审计（`GET /api/audit` 最近 200 条，筛选全部 / 登录 / 凭据 / 被拒，被拒的标红）。这些 `/api` 只给管理员，成员的团队页只有成员和项目两块。
  - 手机上只看：成员卡片（身份和项目）、项目列表，点项目看成员（不能改，没有设置和工单同步）；写明成员、邀请和准入在电脑上管理。
- **我页**（`g s`）：
  - 页头：名字、邮箱或用户名和身份，「退出登录」。
  - 左栏：外观（主题、皮肤的预设、强调色 `#rrggbb` 留空用皮肤自带的、对比度、密度和它的行高、语言），只存在这个浏览器里，改了立刻生效；通知：浏览器通知开 / 关（第一次打开时向浏览器申请，写明浏览器的态度：允许、会问、拒绝了、不是 HTTPS、不支持；后三种不给开关），推送到这台设备（`core/push.js` 的开 / 关；不是 HTTPS、iPhone 的 Safari 里还没装到主屏幕、浏览器没有推送、浏览器拒绝了通知时写明原因、不给开关；server 没有推送密钥时开的那一下提示 `push_key`），个人 webhook（`GET/POST /api/me/webhook`，保存只在改了时可按，留空就是去掉）。
  - 右栏：登录方式（`GET /api/identities` 的已关联账号，`/auth/logins` 里还没关联的带「关联」，去 `/auth/<名>/start?link=1`；出发前在 sessionStorage 记下时间（`tend-linking`），10 分钟内回到页面时打开我页并提示关联上了、账号已属于别人或登录没完成）；token（`GET /api/tokens` 里 `kind: token` 的：名字、建立和最近使用的时间；新建发 `POST /api/tokens {name}`，用 `Secret` 显示一次并写明怎么让 tend 用它；吊销先确认，写明用它登录的网页会话一起失效，发 `DELETE /api/tokens {id}`）；浏览器会话（`kind: web` 的：这个浏览器排最前、标「就是这个」、不给退出；用 token 登录的写那个 token 的名字；其余每条「退出」和「退出其他全部」都先确认，逐条 `DELETE /api/tokens`）。
  - 这台设备（`platform.js`）：地址不是 HTTPS 时两种形态都有「不是 HTTPS」一块，写明装不到主屏幕、收不到推送，和换成 HTTPS 的两种办法（tailnet 里 tailscale serve 或 tailscale cert，公网 `--tls-cert` / `--tls-key`，见 [deployment.md](deployment.md)「常驻部署的做法」）。手机浏览器里（没装到主屏幕时）写怎么装：iOS 是 Safari 的三步（分享、添加到主屏幕、从主屏幕打开再登录），Android 一行（浏览器菜单的「安装应用」或「添加到主屏幕」）；装好的页面和电脑上的 HTTPS 页面不写。
  - 手机上只有账号卡片、进团队页和 Agent 页的两行（这两页在手机上只看）、「通知」一块里的推送到这台设备、这台设备的一块和退出登录。
- **改动**（`pages/changes.js`，任务详情和运行页共用）：
  - 一个任务有几次运行时先选看哪一次，默认最新的。头部写文件数和 `+a −d`、筛选（全部、只看 agent 用工具改的、生成的文件，各带数目）；还在跑的运行写「截至 hh:mm」和刷新，状态变了也重读。
  - 文件按目录分组，根目录在前；每个文件一行：`+ ~ − →`（新增、修改、删除、改名，改名写 `旧 → 新`）、路径、生成的标记、`+a −d`。二进制写大小（有 `old_bytes` 时写 `旧 → 新`），不能展开；改动很大的和生成的先折起。
  - 电脑上打开时自动展开前 `AUTO_OPEN`（8）个没折起的文件，手机上都不展开。展开一个文件按页取 diff（`run.diff{run, path, snapshot, hunk, line?, n: 10, context?}`），每行带旧、新行号；不止一处时底下写「显示了 x / y 处」，还有的点「显示后面 n 处」取下一页（一处太大被截在中间时是「这一处没显示完，接着显示」，从 `next.line` 接着取）。大文件不做虚拟滚动：已画的每处用 `content-visibility: auto` 跳过屏幕外的排版。
  - 修改和改名的文件有「多看上下文」：每次多 20 行，从第一处重新取，写明现在上下文几行。
  - 电脑上页签够宽（至少 `SPLIT_WIDTH`，880px）时可以切换合在一栏 / 并排（记在 `tend-diff-view`）；并排时左边是改之前、右边是改之后，删的和紧跟着的加的配成一行，两栏各自横向滚动。手机上和窄的时候总是合在一栏。
  - 选行：点行号选一行，再点另一行选到那一行（`Shift` 点总是扩展），再点同一行取消。选中的行底下贴着一条：选了几行、「发给 agent」、验收时（任务的操作里有 `rework` 或 `sendBack`）另有「写进退回意见」、取消。引用写成 `path:41-43`（新行号；只选了删的行时写「path:12（改之前）」）加一段 `diff` 代码块（围栏比行里最长的反引号还长），最多 `QUOTE_LINES`（200）行、`QUOTE_BYTES`（16 KiB），超出的截掉，写「另有 n 行没有引用」。「发给 agent」把引用接在给 agent 的草稿后面，切到输出页签、输入框拿到焦点，照输入框的去向发（`task.message`，没有别的发法）；「写进退回意见」接在退回意见的草稿后面并打开验收对话框，对话框关了再开草稿还在，验收发出后清掉。运行自己的页只有「发给 agent」。
  - 「忽略空白」开关在头部（两种形态都有），记在 `tend-diff-space`（每个看的人自己的）。打开时展开的文件都从第一处重取（`run.diff{…, ignore_space: true}`，由节点按 `git diff -w` 算，页面不自己过滤），`+a −d` 和列表照旧；只改了空白的文件写「只有空白改动，忽略空白时不显示」。取到的页按取时开没开分开放，换了以后旧的那种不显示也不接着翻。运行所在机器的节点不支持（`unsupported`）时这次运行的开关关掉、不能点，写「这台机器上的 tend 太旧，不能忽略空白」，文件照常取；看的人的选择不变，别的运行照用。
  - 说明：不在 git 里（`git: false`）时只包含 agent 用工具改的；`hidden` 是这段时间目录里别的只有机器主人能看的文件数；清理过的（`gone`）只剩说明，排队中的写运行开始后才有；server 没有 `run.changes` 时说这台 server 还不能看改动。还在跑的运行的第一处遇到 `snapshot_changed` 时重读列表。
- **测试**（`webtest/`，不在 `web/` 下）：
  - Go 用 `runModule` 在 node 里跑 `*_test.js`，每个 JS 用例是一个子测试。
  - 假 server（`fake.js`）在进程内模拟 WebSocket 和时钟，回放 `webtest/frames/*.jsonl`：一行一个动作。`c` 是客户端应该发出的帧，`s` 是 server 发的帧，`raw` 是原样发出的一段文字，另外还有 `connect` / `refuse` / `drop` / `dialing` / `wait_ms` / `step` / `note`。
  - 第 3 期 wire 流和 `state.watch` 的 Go 测试也读这批文件：取 `c` 当输入，拿 `s` 对照输出，其余的行属于客户端，跳过。
  - store 回放 `state-snapshot.jsonl` 得到的状态，和 Go 按同样规则折叠同一文件的结果对照；快照里的对象严格按 `task.State` 的类型解码，多出的字段算失败。
  - 组件（`ui_test.js`）：用 render-to-string 把一套样例按两种形态、两种语言各画一遍，检查每个 class 在 CSS 里有规则、没有漏译的键、两种形态各有自己的结构；交互在 `dom.js` 的假文档里用 `act` 驱动：列表的键、排序、1000 行的窗口，对话框的按键和焦点，提示的撤销和时限，侧栏开合，手机标签栏，方向键。
  - 结构测试：vendor 的校验和，改写只出现在声明过的地方；import 只用相对路径并且合乎分层；每个帧文件都被某个测试回放；皮肤 token 递归检查（跳过 `vendor/`）。
  - 首页的帧：`home-state.jsonl` 是一天的运行加前六天、三台机器（一台离线）、五条 inbox，现在是 2026-09-30T14:32Z（测试用 UTC）；`home-commands.jsonl` 接在它后面，是完成和撤销、按数字作答、重试、停止，各带 command id；`output-page.jsonl` 是展开一条时取的输出；`command-unknown.jsonl` 是没收到应答的写用同一个 command id 重发。Go 把 store 折叠 `home-state` 的结果和 `task.State` 对照，并把帧里的写请求参数、应答和 `machines` / `inbox` 的每一项严格按 coordinator 的类型解码（`task.TaskStatus`、`coord.Dispatch`、`task.RunRef`、`coord.Answer`、`coord.OutputPageParams`、`coord.Continue`；`task.Task`、`task.Run`、`coord.OutputPage`；`coord.Machine`、`coord.InboxItem`）。
  - `select_test.js`：数字写法；首页每个数对帧文件算出的值；空的一天；HTTP 请求的形状；操作表和 §6.6 的键表一致、每个操作有键和两种语言的名字、两种名字都搜得到；写操作的 pending、隐藏、未知和重发；偏好的读写。
  - 任务页的帧：`tasks-state.jsonl` 是一个项目、十一个任务（需求有新版、完成、在跑、等人工验收、未开始的子任务、拆解草稿、没目录、已取消、等派发、合并冲突、排队）和它们的运行、三台机器、inbox；`tasks-create`（新建并开始）、`tasks-dispatch`（预览后派发）、`tasks-plan`（删一条、保存、应用）、`tasks-acts`（验收退回和通过、采用新版需求、合并、调整位置和撤销）、`tasks-board`（拖到已结束、撤销、被拒的拖放）、`tasks-offline`（断线）接在它后面。`tasks-state` 和 `output-state` 的 `affordances` 是协调器对 u_b（p1 的参与者，机器是 u_a 的、共享给 p1）在这份状态上算出来的，`internal/coord` 的 `TestTheWebFramesOfferWhatTheCoordinatorOffers` 对照。Go 另外严格解码其中的 `coord.TaskCreate`、`coord.TaskRef`、`task.TaskMove`、`coord.PlanSave`、`coord.PlanApply`、`coord.TaskGate`、`task.SourceAck`、`run.preview` 的 `coord.Dispatch` 和 `coord.Preview`、`coord.Agents`。
  - `tasks_test.js`：`core/tasks.js` 对 `tasks-state` 的结果：看板的列、每种拖放、树和收起、筛选 / 进度 / 花费、目录候选和默认值、派发前的判断、拆解草稿的编辑和每种错误、每种处境的操作。`taskpages_test.js`：任务页（列表、看板、树，带选中的任务）和每个表单按两种形态、两种语言画一遍，检查 class 和漏译；按上面五个帧文件走一遍；手机上的上一个 / 下一个和验收的按钮位置。
  - 对话的帧：`output-state.jsonl` 是一个任务的两次运行（第二次续接第一次的会话，一次问两个问题）、它的 `affordances` 和 inbox；`output-conv.jsonl` 接在它后面，是打开对话后 `run.output.watch` 的推送、往前一页和上一次运行的最后一页；`output-send.jsonl` 是插话、去向变了被拒、原地回答两个问题、打断、一条命令在这次运行里都允许（请求带 `allow_run`，回答带 `decision`）；`output-carry.jsonl` 接在 `output-send` 后面，是一条「这一轮结束后再说」、运行结束、协调器用它续接出的下一次运行和它的输出；`output-answer.jsonl` 是首页上一起回答两个问题；`output-answer-gone.jsonl`（首页）和 `output-gone.jsonl`（接在 `output-conv` 后面，任务页）是回答时别人已经先答了（`request_gone`）。`output_test.js`：三种密度的行、折叠和自动展开、查找和筛选、跟随的每种情形、锚点补偿、分页和占位、往前补页时写了一半的回答留着、深链、组件按两种形态两种语言画一遍，并按这几个帧文件走一遍。Go 严格解码其中 `task.message`、`run.interrupt`、`run.answer` 的参数和应答，以及 `affordances` 的 part 和推送（`coord.Affordances`）。Go 另拿 `internal/output` 的 testdata 和几种它没有的形状，分别交给 `output.Items` 和 JS 的 `items`，对照结果。
  - 改动的帧：`changes-list.jsonl` 接在 `output-state` 后面，是一次已结束运行的两页文件（生成的、改名、二进制、改动很大）、一个文件 diff 的两页（十二处先取十处、再取后两处）、一处太大截在中间再接着取、多看上下文从第一处重取、忽略空白再取一次；`changes-live.jsonl` 是还在跑的运行第二页遇到 `snapshot_changed` 从头再取、刷新、第一处也遇到它；`changes-gone.jsonl` 接在 `home-state` 后面，是不在 git 里、有 `hidden`、清理过的列表和第一处。`home-state`、`output-state` 和 `tasks-state` 的 hello 里带 `run.changes`、`run.diff`，Go 按 `node` 的参数和结果严格解码它们。`changes_test.js`：目录顺序、筛选、折起、按页取和重取、留着不再取、diff 行和行号、几页接成一份、并排配对、选行、引用的格式和截断、忽略空白的请求和分开留、只改了空白、各种说明，改动页按两种形态两种语言、合在一栏和并排、选了行、忽略空白和节点太旧画一遍。`runs_test.js`：筛选和机器数、运行页和运行自己的页按两种形态两种语言画一遍，驱动筛选（记住）、移动预览、打开、停止（确认，`Esc` 不发）、手机上的下一个和页签，任务详情的改动页签（自动展开、展开折起的、筛选、刷新、换一次运行、下一页、多看上下文、选行发给 agent 后输入框拿到焦点、写进退回意见后关了对话框再开还在、忽略空白从第一处重取并记住、节点太旧时开关关掉并照常取），手机上运行页的改动发给 agent。
  - `me_test.js`：我页按两种形态两种语言画一遍（在假文档里，读完 `/api` 之后查 class 和漏译）；外观的每一项生效并记住；浏览器通知的申请、只对页面在后台时新出现的条目弹、点开任务、关掉；浏览器拒绝、不是 HTTPS、不支持时的说明；推送到这台设备在两种形态下的开关、拒绝和缺密钥、四种不给开关的说明，inbox 变化时清通知；webhook 的保存和去掉；token 的新建（只显示一次）和吊销（确认，`Esc` 不发）；会话的退出和退出其他全部；关联账号前记下、回来后（`Root`）打开我页并提示，没记或记得太久的照旧丢掉片段；手机上的团队和 Agent 入口、退出登录；这台设备一块按 iOS / Android 浏览器、装好的页面、不是 HTTPS 和电脑各画一遍。
  - Agent 页的帧：`agents-state.jsonl` 是 Bo（u_b，成员）看到的：两个自己的定义（一个从档案 quick 起步、分享给 Cy）、Docs（他负责）和 Shop（他参与）各一个、Cy 分享给他并让他看的一个、Ann 只分享给他用的一个（不在状态里）、配置的档案，三台他能派发的机器（一台离线、一台的 codex 没登录），和打开页面时读的两份列表；`agents-edit.jsonl` 接在它后面，是一次被拒的保存和改好再存、从第一步新建一个给 Docs、从文件导入一个 Claude Code subagent、复制 Cy 的，每次存好后 journal 推 `agentdef_saved`、页面重读；`agents-share.jsonl` 是改分享和删除，两次都 `reset` 之后重读。`internal/coord` 的 `TestTheWebAgentFramesAreTheCoordinators` 在一个协调器上（另加 Bo 读不到的两个定义）逐条执行帧里的请求，对照应答、journal 推送、快照和 `live` 的 seq；Go 另外严格解码 `coord.AgentDefSave`、`task.AgentDefShare`、`task.AgentDefRef` 和 `coord.AgentDefList`、`coord.AgentDefView`。`agents_test.js`：来源、编译后的样子、能跑的机器、谁在用、能做什么和起始文本（纯函数，含管理员的情形）；页面按两种形态两种语言画一遍，每个 agent 的三个页签和新建、导入对话框都查 class 和漏译；列表和右栏的内容、`j` / `k` / `Enter`；按上面两个帧文件走一遍；编辑器对改名、覆盖、和档案同名、没名字的说明；导出；手机上只看。
  - `pages_test.js`：首页（在应用里）和登录、邀请、登录被拒、终端确认各页按两种形态、两种语言画一遍，检查 class 有规则、没有漏译；按 `home-commands` 用键盘走完完成 → 撤销 → 作答 → 重试（确认）→ 停止（确认，`Esc` 不发）；展开一条看它刚做的三步；手机首页的标题和原因、每条的快捷操作、只列在跑的、点一行进落地页、落地页的标题 / 位置 / 归属和去向、允许和拒绝的说明、答完换成原位置上的下一条（替换地址，假 history 记着每一条）、返回列表、从别的页打开的通知站在列表上、打开时已经不等了、待验收的改动、最后一条答完回到列表、按「我派发的」筛选并记住、一键允许；电脑上 `?wait=` 选中展开；`doing` → `note` → `last` 的先后；命令面板按中文名找操作、按标题找任务，快捷键页；token 登录（错的、对的）和终端的允许、已过期。
- **预览**：`go run ./tools/webpreview` 在 127.0.0.1:18765 用工作区里的文件起一个只给看的页面：`web/` 的文件、`webtest/preview/` 的预览页、帧文件、皮肤和它们的预设，响应头和 server 一样（`server.SecureHeaders`，CSP 不变）。预览页用假 socket 按方法回放上面的帧文件、用假 fetch 回答登录和终端确认，时钟固定在帧文件的时刻；默认的帧另带 `changes-gone`；`?frames=tasks` 换成任务页的帧加 `changes-list`，`?frames=output` 换成对话的帧加 `changes-list`（打开 `?page=tasks&task=t1`），`?frames=carry` 是这段对话续接出第三次运行，`?frames=gone` 是它的问题被别人先答了（`output-send`、`output-carry` 推在前面文件开的流上的内容接在那个流后面），`?frames=agents` 是 Agent 页的（打开 `?page=agents`），`?as=signedout` 看登录页，`#device-<码>`、`#invite-<码>` 看另两页。它不连任何 coordinator。
