# 两种部署与安全

模式一（持锁的 TUI / CLI 或 `tend service` 当协调器，经 ssh 连节点）和模式二（`tend-server` 常驻，节点和客户端用 WebSocket 连它），以及两种模式的安全边界。实现：`internal/coord`（协调器、socket）、`internal/remote`（ssh）、`internal/dial`（模式二的节点和客户端）、`internal/server` 与 `cmd/tend-server`（模式二的 server）、`internal/node`（节点本机约束）。

## 模式一

- 协调器：持锁的 TUI / CLI，或 `tend service`（前台常驻；用户自己决定要不要放进 launchd / systemd）。
- 机器：本机（进程内节点）+ `config.hosts` 每一台（`ssh … node --stdio`）。
- TUI 读远端会话仍直接 ssh（有 ControlMaster 时和协调器共用 TCP 连接）。

## 模式二

### server

- `tend-server --listen <addr> [--tls-cert f --tls-key f]`：独立程序，`tend` 不含 server 代码；日志存在 SQLite 的 `coord/tend.db`（`import` / `export` / `backup` / `db check` 见 [tasks/storage.md](../tasks/storage.md)「存储」）。它是同一个协调器（常驻持锁），加 HTTP：`/node`、`/client`（WebSocket），`/healthz`；Web UI 的 `/`（静态页）、`/sw.js`、`/manifest.webmanifest` 和图标（装到主屏幕，见 [clients.md](clients.md)「Web UI（模式二）」）、`/login`、`/logout`、`/session`、`/auth/logins`、`/auth/<provider>/start|callback`、`/api/*`（推送设备的 `PUT` / `DELETE /api/push/device` 和通知按钮的 `POST /api/act` 见 [tasks/team.md](../tasks/team.md)「人在任务里」的实现；其余见 [clients.md](clients.md)「Web UI（模式二）」；身份与权限见 [tasks/team.md](../tasks/team.md)「团队与权限」）。
- `--listen` 只允许回环或 tailnet 地址（100.64.0.0/10、fd7a:115c:a1e0::/48）；其它地址必须同时给 TLS，或显式 `--plain`（容器里、前面有转发时）。

### 凭据与身份

- 凭据：`tend-server token add --node <名> | --client <名> [--owner <用户>]` 打印一次，存在 `coord/tend.db` 的 `credentials` 表（只存 sha256，节点名在未吊销的凭据里唯一）；`token rm` 吊销并断开对应连接。成员也能在网页上「添加机器」、生成个人 token。旧版 server 留下的 `server/tokens.json` 启动时导入，原文件改名留底。
- 角色限制方法：node token 只能被调用 `run.*` 和会话读取、只能推 `node.changed`；client token 只能调客户端方法。
- 同一节点 token 重复连接：后连的赢，旧连接关掉。
- 节点身份：节点第一次用到时生成 `<home>/node/id`（`n_` + 16 位十六进制），握手时放在 `hello.node_id`。节点 token 第一次被带 id 的节点用时绑定这个 id（凭据记 `node_id`、`host`）；不带 id 的节点一律拒绝；之后别的 id 拿它连 → `unauthorized "node identity"`，server 在 stderr 记一行。换机器用 `tend-server token rebind <名>` 解绑，下一个连上的节点重新绑定；`token list` 多一列 MACHINE。
- server 自己的密钥：Web Push（VAPID）的 P-256 密钥对在第一次启动时生成，私钥用 `seal.go` 封存进库的 `secrets` 表（[tasks/storage.md](../tasks/storage.md)「表」），以后启动读出来用；`GET /api/push/key`（登录后）回 `{key}`，是 base64url 的未压缩公钥，浏览器订阅推送时要它。库里的密钥用当前的 `server.key` 解不开时照原样留着、不重新生成（订阅都绑在原来的公钥上），server 在 stderr 记一行，没有推送地照常运行，`/api/push/key` 回 503 `push_key`。每条推送按 RFC 8291 为那个浏览器单独加密（aes128gcm、一个记录，每条新的发送方密钥和 salt，正文最多 3993 字节），用这对密钥按 RFC 8292 签 VAPID（ES256 的 JWT，`aud` 是推送服务的 origin，12 小时有效；`sub` 是 `server.public_url`，它不是 https 时用项目地址 `https://github.com/oxsean/fav`），都只用标准库（`webpush.go`），对照 RFC 8291 附录 A 的中间值测试。推送按钮的令牌由另一把 32 字节的密钥 `act` 签（HMAC-SHA256），同样封存在 `secrets` 里、重启后照用，签过的令牌在重启后仍然有效；解不开时推送不带按钮，`/api/act` 回 503 `act_key`（令牌和 `/api/act` 见 [tasks/team.md](../tasks/team.md)「人在任务里」的实现）。
- 吊销：连接和推送设备记下它们用的凭据 id；server 每 3 s 从数据库重读有效凭据和用户，凭据已吊销、过期或用户已停用的连接关掉，推送设备去掉（[tasks/team.md](../tasks/team.md)「人在任务里」的实现）。

### 节点

- 节点：`tend node --connect <url> --token-file f`，前台常驻，断线 1 s 起翻倍、最多 60 s 重连。
- `tend node install-service` 把它装成登录服务：macOS 写 `~/Library/LaunchAgents/dev.tend.node.plist`（RunAtLoad、KeepAlive、ThrottleInterval 10）并 `launchctl bootstrap gui/<uid>`；Linux 写 `~/.config/systemd/user/tend-node.service`（`Restart=always`，`systemctl --user enable --now`，未登录时也要跑需 `loginctl enable-linger`）；Windows `schtasks /SC ONLOGON /TN tend-node`。服务带上安装时的 `PATH` 和 `TEND_HOME`（登录服务的 PATH 是裸的，找不到 claude / codex），日志进 `<home>/node/service.log`；重装先卸掉旧的。Mac 上要跑 claude 的节点必须这样装：ssh 会话读不到登录钥匙串（见 [overview.md](overview.md)「已核实 / 待核实」）。
- 节点本机约束（`config.node`，两种模式都生效）：
  - `allow_dirs`（模式二必填，目录做真实路径判断）。
  - `allow_bypass`（默认 false）为 false 时：`command` 档案只按节点自己同名定义跑（执行字段一致才放行：provider、command、args、stdin、model、permission，nil 与空相同，忽略 name / machine）；claude / codex 的 `args` 只接受节点自己同名档案里的，permission 只放行 claude `default` `manual` `acceptEdits` `plan` `dontAsk`（按 claude 2.1.282；`auto` `bypassPermissions` 不放行）、codex `read-only` `workspace-write`；最终命令行再按已知绕过参数兜底（含 `-sVALUE`、`--settings`、`--allowedTools`、codex `-p`）。
  - `allow_profiles`（可选白名单：只跑这些名字，且用节点自己的定义，忽略协调器发来的内容）。
  - `allow_hooks`（默认 false）：接受带项目 hook（`check`、`setup`、`cleanup`）或 agent 定义 hooks 的 run，hook 以节点用户身份执行。
  - `mcp`：agent 定义可以按名字引用的 MCP 服务器（claude 的 `mcpServers` 条目），值只在节点上。
- 一台机器要当节点就跑 `tend node --connect`；TUI 不兼任节点（没有 `client+node` 双重身份）。

### 客户端与接手

- 客户端：`config.coordinator = {url, token_file}` 设了就连 server。模式二的 TUI 机器列表只用 `machine.list`，派发必须指定机器（没有 `local`）。
- 接手：机器也配了 `hosts`（能 ssh）→ 在当前终端恢复；否则把在那台机器上执行的恢复命令复制下来并显示，引号按那台机器的 OS 加（`machine.list` 报 windows 用 PowerShell，否则 POSIX）。

### 常驻部署的做法

- 直接装在一台机器上（不进容器，省掉转发）：macOS 上用 LaunchAgent `dev.tend.server` 跑 `tend-server --listen <tailnet 地址>:<端口>`，由 `tend hosts install <host> --server` 装；数据目录单独一个（`~/.agent/tend-server`），不和那台机器自己的节点混用；那台机器的节点用 `tend node install-service` 连它，`allow_dirs` 只开一个工作目录。
- 在容器里跑时 server 的 home 挂 volume：容器内 `--listen 0.0.0.0:<端口> --plain`，宿主上的转发只绑 tailnet 地址（`scripts/tend-e2e-server.sh` 就这样部署）。
- 手机装到主屏幕和推送都要 HTTPS 地址（浏览器只在安全上下文里给 service worker 和推送；`localhost` 也算）。tailnet 里用 tailscale serve 把 server 放到 `*.ts.net` 的 HTTPS 地址上，或用 tailscale cert 取这个名字的证书交给 `--tls-cert` / `--tls-key`；公网部署直接 `--tls-cert` / `--tls-key`。不是 HTTPS 时网页照常能用，「我」页写明装不了、收不到推送和这两种办法（见 [clients.md](clients.md)「我页」）。

## 安全

- `spec.json`、`prompt.md`、token 文件、`coord/tend.db` 一律 0600。
- 任务书正文不进 argv / ps。
- 模式二信任边界按人划分：个人 token 只有它主人的权限；只有主人是 `local` 的客户端 token（`tend-server token add --client` 不带 `--owner`）和 `tend-server` 命令行算内置管理员 `local`，拿到这种 token = 能在 `allow_dirs` 里用允许的档案跑任意任务书、读所有节点的会话；`node.call` 限机器主人和管理员；连 server 的节点默认 `share_sessions: runs`。部署在 tailnet 或公网都行，公网必须 TLS；`--plain` 时只让绑定 tailnet 地址的转发器连它。
- token 哈希按常量时间比较。
- server 替成员发出的请求（webhook、Web Push、工单）只连公网地址，内网网段要管理员写进 `server.egress_allow`（[tasks/team.md](../tasks/team.md)「人在任务里」的实现，「出网」）。
- macOS 没有父进程死亡信号：监督进程被杀后 agent 可能还在跑（Linux 的 Pdeathsig、Windows 的 Job 会带走它），所以 unknown run 的会话守卫看 agent pid 是否还活着。
- 诊断日志（`service.log`、server 日志）不记 token 和任务书；权威日志 `events.jsonl` 存任务书（0600）。
- 节点 token 绑定到第一台用它的机器（见上文「凭据与身份」）：token 泄露后换一台机器也连不上，除非有人先 `rebind`。
- 监督进程死后的停止只结束 pid 和启动时间都对得上的进程，不会误杀 pid 被复用的别的进程。
