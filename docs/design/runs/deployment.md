# 两种部署与安全

模式一（持锁的 TUI / CLI 或 `tend service` 当协调器，经 ssh 连节点）和模式二（`tend-server` 常驻，节点和客户端用 WebSocket 连它），以及两种模式的安全边界。实现：`internal/coord`（协调器、socket）、`internal/remote`（ssh）、`internal/dial`（模式二的节点和客户端）、`internal/server` 与 `cmd/tend-server`（模式二的 server）、`internal/node`（节点本机约束）。

## 模式一

- 协调器：持锁的 TUI / CLI，或 `tend service`（前台常驻；用户自己决定要不要放进 launchd / systemd）。
- 机器：本机（进程内节点）+ `config.hosts` 每一台（`ssh … node --stdio`）。
- TUI 和 CLI 读远端会话直接 ssh（有 ControlMaster 时和协调器共用 TCP 连接）；这只在模式一。模式二读会话见下面「客户端与接手」。

## 模式二

### server

- `tend-server --listen <addr> [--tls-cert f --tls-key f]`：独立程序，`tend` 不含 server 代码；日志存在 SQLite 的 `coord/tend.db`（`import` / `export` / `backup` / `db check` 见 [tasks/storage.md](../tasks/storage.md)「存储」）。它是同一个协调器（常驻持锁），加 HTTP：`/node`、`/client`（WebSocket），`/healthz`；Web UI 的 `/`（静态页）、`/sw.js`、`/manifest.webmanifest` 和图标（装到主屏幕，见 [clients.md](clients.md)「Web UI（模式二）」）、`/login`、`/logout`、`/session`、`/auth/logins`、`/auth/<provider>/start|callback`、`/api/*`（推送设备的 `PUT` / `DELETE /api/push/device` 和通知按钮的 `POST /api/act` 见 [tasks/team.md](../tasks/team.md)「人在任务里」的实现；其余见 [clients.md](clients.md)「Web UI（模式二）」；身份与权限见 [tasks/team.md](../tasks/team.md)「团队与权限」）。
- `--listen` 只允许回环或 tailnet 地址（100.64.0.0/10、fd7a:115c:a1e0::/48）；其它地址必须同时给 TLS，或显式 `--plain`（容器里、前面有转发时）。

### 凭据与身份

- 凭据：`tend-server token add --node <名> | --client <名> [--owner <用户>]` 打印一次，存在 `coord/tend.db` 的 `credentials` 表（只存 sha256，节点名在未吊销的凭据里唯一）；`token rm` 吊销并断开对应连接。成员也能在网页上「添加机器」、生成个人 token。旧版 server 留下的 `server/tokens.json` 启动时导入，原文件改名留底。
- 角色限制方法：node token 只能被调用 `run.*` 和会话读取、只能推 `node.changed`；client token 只能调客户端方法。
- 同一节点 token 重复连接：后连的赢，旧连接关掉。
- 节点身份：节点第一次用到时生成 `<home>/node/id`（`n_` + 16 位十六进制），握手时放在 `hello.node_id`。节点 token 第一次被带 id 的节点用时绑定这个 id（凭据记 `node_id`、`host`）；不带 id 的节点一律拒绝；之后别的 id 拿它连 → `unauthorized "node identity"`，server 在 stderr 记一行。换机器用 `tend-server token rebind <名>` 解绑，下一个连上的节点重新绑定；`token list` 多一列 MACHINE。
- 改机器主人：`tend-server token owner <机器> <用户>` 把这台机器的节点 token 改给另一个人（`store.Team.SetOwner`），token、绑定的节点身份都不变，不用 `rebind`；记审计 `machine`（`owner <机器> <旧主人> <新主人>`）。没有这台机器的节点 token、没有这个用户、用户已停用都拒绝。server 下次重读凭据（3 s 内）后，协调器的机器主人就是新主人，`Reaffirm` 把它推给客户端。用 `token add --node` 不带 `--owner` 登记的机器和从 `tokens.json` 迁来的机器都在 `local` 名下，会话除了 `local` 谁都读不了（管理员也不行），要先这样改给真正的主人。
- server 自己的密钥：Web Push（VAPID）的 P-256 密钥对在第一次启动时生成，私钥用 `seal.go` 封存进库的 `secrets` 表（[tasks/storage.md](../tasks/storage.md)「表」），以后启动读出来用；`GET /api/push/key`（登录后）回 `{key}`，是 base64url 的未压缩公钥，浏览器订阅推送时要它。库里的密钥用当前的 `server.key` 解不开时照原样留着、不重新生成（订阅都绑在原来的公钥上），server 在 stderr 记一行，没有推送地照常运行，`/api/push/key` 回 503 `push_key`。每条推送按 RFC 8291 为那个浏览器单独加密（aes128gcm、一个记录，每条新的发送方密钥和 salt，正文最多 3993 字节），用这对密钥按 RFC 8292 签 VAPID（ES256 的 JWT，`aud` 是推送服务的 origin，12 小时有效；`sub` 是 `server.public_url`，它不是 https 时用项目地址 `https://github.com/oxsean/fav`），都只用标准库（`webpush.go`），对照 RFC 8291 附录 A 的中间值测试。推送按钮的令牌由另一把 32 字节的密钥 `act` 签（HMAC-SHA256），同样封存在 `secrets` 里、重启后照用，签过的令牌在重启后仍然有效；解不开时推送不带按钮，`/api/act` 回 503 `act_key`（令牌和 `/api/act` 见 [tasks/team.md](../tasks/team.md)「人在任务里」的实现）。
- 吊销：连接和推送设备记下它们用的凭据 id；server 每 3 s 从数据库重读有效凭据和用户，凭据已吊销、过期或用户已停用的连接关掉，推送设备去掉（[tasks/team.md](../tasks/team.md)「人在任务里」的实现）。

### 节点

- 节点：`tend node --connect <url> --token-file f`，前台常驻，断线 1 s 起翻倍、最多 60 s 重连。
- `tend node install-service` 把它装成登录服务：macOS 写 `~/Library/LaunchAgents/dev.tend.node.plist`（RunAtLoad、KeepAlive、ThrottleInterval 10）并 `launchctl bootstrap gui/<uid>`；Linux 写 `~/.config/systemd/user/tend-node.service`（`Restart=always`，`systemctl --user enable --now`，未登录时也要跑需 `loginctl enable-linger`）；Windows 写环境文件 `<home>/node/service-env.json`（安装时的环境变量，JSON 对象）和任务定义 `<home>/node/service.xml`（UTF-16），先 `schtasks /End`（停掉正在跑的节点）、再 `/Create /F /TN tend-node /XML` 注册、再 `/Run`：动作直接运行 `tend.exe node --connect … --token-file … --env <环境文件> --log <home>/node/service.log`，没有 `cmd.exe` 外壳，所以 `/End` 结束的就是节点本身（它起的 `_run` 监督进程本来就脱离节点，照常留下）；任务定义带不了环境变量也重定向不了输出，所以节点自己 `--env` 最先设好环境、`--log` 把打印的一切（连同退出时的错误和崩溃栈）追加进日志。当前用户登录时触发、用交互令牌；另有一个时间触发器，起点是安装时刻（本地时间，不带时区），每分钟重复、不设期限，靠它兜底：配合 `IgnoreNew`，节点在跑时什么也不做，退出了（被杀、`/End`、出错、token 被拒）一分钟内再起，立即失败的也只是每分钟一次。登录和开机触发器不重复（它们的重复要等触发那一刻才开始，装完不重启、不登录就永远不来），只管登录或开机时马上起。不限运行时长，错过时补跑，不因电池停，优先级 5。`--at-boot`（只用于 Windows，别的系统拒绝）改成 S4U（不存密码）、再加开机触发，没人登录也跑；代价是 S4U 进程解不开用户的 DPAPI 密钥，节点和它跑的 run 用不了 Windows 凭据管理器（Git Credential Manager 等），agent CLI 的凭据文件照常可用，装完打印这条提示；注册被拒（要管理员终端）时报错并提示去掉 `--at-boot`，不改装别的形态，`/Create /F` 原地替换，所以原来的任务还在，先前被 `/End` 停掉的节点由它的时间触发器再起。服务带上安装时的 `PATH` 和 `TEND_HOME`（登录服务的 PATH 是裸的，找不到 claude / codex），日志进 `<home>/node/service.log`；重装先卸掉旧的（Windows 先 `/End` 再 `/Create /F` 覆盖），`uninstall-service` 停掉并删除任务、删它写下的文件（日志留着），两种形态一样。Mac 上要跑 claude 的节点必须这样装：ssh 会话读不到登录钥匙串（见 [overview.md](overview.md)「已核实 / 待核实」）。
- 节点本机约束（`config.node`，两种模式都生效）：
  - `allow_dirs`（模式二必填，目录做真实路径判断）。
  - `allow_bypass`（默认 false）为 false 时：`command` 档案只按节点自己同名定义跑（执行字段一致才放行：provider、command、args、stdin、model、permission，nil 与空相同，忽略 name / machine）；claude / codex 的 `args` 只接受节点自己同名档案里的，permission 只放行 claude `default` `manual` `acceptEdits` `plan` `dontAsk`（按 claude 2.1.282；`auto` `bypassPermissions` 不放行）、codex `read-only` `workspace-write`；最终命令行再按已知绕过参数兜底（含 `-sVALUE`、`--settings`、`--allowedTools`、codex `-p`）。
  - `allow_profiles`（可选白名单：只跑这些名字，且用节点自己的定义，忽略协调器发来的内容）。
  - `allow_hooks`（默认 false）：接受带项目 hook（`check`、`setup`、`cleanup`）或 agent 定义 hooks 的 run，hook 以节点用户身份执行。
  - `mcp`：agent 定义可以按名字引用的 MCP 服务器（claude 的 `mcpServers` 条目），值只在节点上。
- 一台机器要当节点就跑 `tend node --connect`；TUI 不兼任节点（没有 `client+node` 双重身份）。

### 客户端与接手

- 客户端：`config.coordinator = {url, token_file}` 设了就连 server。模式二的 TUI 机器列表只用 `machine.list`，派发必须指定机器（没有 `local`）。
- 读会话：TUI 启动就在后台拨 server（只拨号，没有副作用），任务页和会话列表共用这条连接；别的机器的会话一律经 `node.call` 读，连不上 server 也不改走 ssh。能读哪些机器、本机怎么认、缓存和连不上时显示什么见 [sessions/remote.md](../sessions/remote.md)「server 模式」。
- 接手：自己的机器也配了同名的 `hosts`，且 ssh `hello` 的 `node_id` 和 server 名单里那台对上 → 在当前终端恢复；否则把在那台机器上执行的恢复命令给出来复制，引号按那台机器的 OS 加（`machine.list` 报 windows 用 PowerShell，否则 POSIX）。别人共享的机器不接手。

### 常驻部署的做法

- 直接装在一台机器上（不进容器，省掉转发）：macOS 上用 LaunchAgent `dev.tend.server` 跑 `tend-server --listen <tailnet 地址>:<端口>`，由 `tend hosts install <host> --server` 装；数据目录单独一个（`~/.agent/tend-server`），不和那台机器自己的节点混用；那台机器的节点用 `tend node install-service` 连它，`allow_dirs` 只开一个工作目录。
- 在容器里跑时 server 的 home 挂 volume：容器内 `--listen 0.0.0.0:<端口> --plain`，宿主上的转发只绑 tailnet 地址（`scripts/tend-e2e-server.sh` 就这样部署）；转发器从容器网络连进来，`server.trusted_proxies` 写上这个网段（docker 默认 `172.16.0.0/12`），限流和设备码才按真实来源地址算（要转发器写 `X-Forwarded-For`，见 [tasks/team.md](../tasks/team.md)「团队与权限」的「来源地址」）。
- 手机装到主屏幕和推送都要 HTTPS 地址（浏览器只在安全上下文里给 service worker 和推送；`localhost` 也算）。tailnet 里用 tailscale serve 把 server 放到 `*.ts.net` 的 HTTPS 地址上，或用 tailscale cert 取这个名字的证书交给 `--tls-cert` / `--tls-key`；公网部署直接 `--tls-cert` / `--tls-key`。不是 HTTPS 时网页照常能用，「我」页写明装不了、收不到推送和这两种办法（见 [clients.md](clients.md)「我页」）。

## 安全

- `spec.json`、`prompt.md`、token 文件、`coord/tend.db` 一律 0600。
- 任务书正文不进 argv / ps。
- 模式二信任边界按人划分：个人 token 只有它主人的权限；只有主人是 `local` 的客户端 token（`tend-server token add --client` 不带 `--owner`）和 `tend-server` 命令行算内置管理员 `local`，拿到这种 token = 能在 `allow_dirs` 里用允许的档案跑任意任务书、读 `local` 名下节点的会话；读一台机器的会话（`node.call`、`sessions.query`、`sessions.grep`）限机器主人和他放进会话可见范围的人，改它的会话记录（`node.call put`）只限机器主人，管理员都不例外（[tasks/team.md](../tasks/team.md)「共享：agent 和机器默认私有」）；连 server 的节点默认 `share_sessions: runs`。部署在 tailnet 或公网都行，公网必须 TLS；`--plain` 时只让绑定 tailnet 地址的转发器连它。
- token 哈希按常量时间比较。
- server 替成员发出的请求（webhook、Web Push、工单）只连公网地址，内网网段要管理员写进 `server.egress_allow`（[tasks/team.md](../tasks/team.md)「人在任务里」的实现，「出网」）。tailnet 的地址（`100.64.0.0/10`、`fd7a:115c:a1e0::/48`）也不连：tailnet 上的工单系统和 webhook 要把它们的地址或这两段写进 `server.egress_allow`（如 `["100.101.8.10/32"]`），写整段就是让每个成员都能让 server 连 tailnet 上任何一台机器。
- 升级到带 `__Host-` 前缀的会话 cookie 的版本后，https 下旧的 `tend_session` 不再认：每个浏览器（包括主屏幕上的 PWA）要重新登录一次，推送设备在重新登录后打开页面时按新会话重新登记。
- macOS 没有父进程死亡信号：监督进程被杀后 agent 可能还在跑（Linux 的 Pdeathsig、Windows 的 Job 会带走它），所以 unknown run 的会话守卫看 agent pid 是否还活着。
- 诊断日志（`service.log`、server 日志）不记 token 和任务书；权威日志 `events.jsonl` 存任务书（0600）。
- 节点 token 绑定到第一台用它的机器（见上文「凭据与身份」）：token 泄露后换一台机器也连不上，除非有人先 `rebind`。
- 监督进程死后的停止只结束 pid 和启动时间都对得上的进程，不会误杀 pid 被复用的别的进程。
