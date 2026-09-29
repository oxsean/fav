# 其它机器

在任何一台机器上看、读、恢复其它机器上的 Claude Code / Codex 会话：远端合同（协议、标识、路径映射、配置、连接与失败）和聚合（列表、筛选、右栏、恢复、缓存）。迁移、记忆和环境诊断见 [migration.md](migration.md)。

实现：`internal/remote`（`proto.go` 方法与类型、`local.go` 应答端、`client.go` ssh 客户端、`hosts.go` 机器与缓存、`source.go` 记录来源），`internal/pathmap`，`internal/paths`（`SocketRoom`），`cmd/tend`（`hosts.go`、`rpc.go`）；帧与连接在 `internal/wire`。

## 范围

不做云同步；单用户多机经自己的 SSH 做只读聚合，迁移由用户手动发起（见 [overview.md](overview.md)「暂不包含」）。具体到多机：

- 只走用户自己的 SSH：不经过任何云，不开端口，不留常驻服务。
- 平时只**远程读**，内容留在原机器；本机只缓存列表字段（见「缓存与隐私」）。
- 只有用户**主动迁移**时，才在机器之间复制会话文件（见 [migration.md](migration.md)「迁移」）。
- 环境诊断只报告差异，tend 不替用户安装或同步任何东西来对齐环境。
- 不做：把各机器的会话 rsync 到本机做镜像；Codex 会话的完整迁移（列为验证项，见 [migration.md](migration.md)「Codex：不做完整迁移，列为验证项」）。

## 架构

每台机器各跑各的 tend。本机经 SSH 去问远端的 tend，远端读它自己的索引和文件，把结果返回；本机只做聚合和展示。

四类机器（Mac、Linux、Windows、WSL）**两端都支持**：都能跑 TUI 去看别的机器（发起端），也都能被别的机器查询（远端）。

## 远端协议

- **单一入口，命令行上不带业务参数**：
  - 长连接：`tend rpc --stdio`（与 `tend node --stdio` 相同），按行收发请求和应答。TUI 打开期间，每台机器保持一条这样的连接；TUI 退出时连接一起关闭。
  - 一次性调用：`tend rpc`，stdin 写一行请求后关闭，读到应答后进程退出。
  - 两种用法共用同一份帧格式。命令行上没有业务参数，四种 shell（POSIX、cmd、PowerShell、WSL）的引号问题和 Windows 上的中文代码页问题就都不存在。
  - 长连接不依赖 ControlMaster；Windows 自带的 OpenSSH 不支持 ControlMaster，Windows 作为发起端时也一样快。
- **帧**：一行一个 JSON，帧形状、错误和保活见 [wire.md](../runs/wire.md)「帧」「`Conn`」。和会话读取相关的约定：
  - 错误只返回稳定的错误码，不返回本地化文本，因为远端的界面语言可能不同；调用方的语言随 `hello` 传过去，检查项文本按它返回。
  - 能区分 ssh 自身的退出码 255（连接失败）和 tend 返回的错误。
  - 时间一律用 RFC3339 格式并带时区偏移，到本机后转成本机时区；路径一律按字符串传递，不经过任何一端的 `filepath` 处理。
- **握手**：`hello` 先行，返回协议号、tend 版本、操作系统、架构、端点 id、主机名、WSL 发行版、home、路径分隔符、Claude / Codex 的配置目录、支持的方法列表（见 [wire.md](../runs/wire.md)「握手与版本」）。协议号不兼容时，本机提示「远端 tend 需要更新」，不强行读取。
- **方法**：
  - 已有：`hello`、`list`（整份返回）、`messages`、`text`（全文，按偏移读）、`steps`、`pulse`、`checks`、`live`、`echo`（中文往返自检）。
  - 未实现：`list` 的 since 游标增量、`grep`（跨机器搜消息）、`memory.ls`、`env`；写 `put`（字段补丁，带预期版本号和请求 id）；迁移 `import.*`（见 [migration.md](migration.md)「Claude 完整迁移」）。
  - 新方法按 `hello.methods` 协商；新方法要在 `methods` 里登记名字、在 `local.go` 里有处理函数，读 transcript 的还要在 `local` 和 `far` 两个 `Source` 上各有一个方法。
- **预算与部分结果**（未实现）：每个读请求都带时间预算。远端索引或全文库没准备好时，先返回已有数据和进度，并标记为部分结果，不在前台等它补建完；请求可以取消。现在只有调用方超时：等不到就放弃这一次调用。
- **协议数据结构单独定义**：不直接把存储里的 `Rec` 拿来当协议。`Session` 是字段白名单（标题、摘要、标签、路径、时间、轮数等，不含消息）；存储里带 `json:"-"` 的字段（轮数、最后活动时间、改过的文件等）在 `Session` 里都有。列表或筛选要用的新字段加进 `SessionOf` 和 `Session.Rec`（时间转成本机时区）。

## 标识

- **机器标识**：一个稳定的端点 id（`hello` 的 `endpoint`：机器 + 操作系统 + WSL 发行版 + 配置目录），能区分 Windows 和 WSL 的不同发行版，以及同一台机器上不同的配置目录。显示名只用来展示。
- **会话引用**：一律写成「机器 + provider + session id」。完整迁移默认复制，复制后同一个 session id 会同时出现在两台机器上。fzf 的行 key、`pick()` 和命令行参数都支持 `host:sid` 的写法。
- 会话 id 只允许 `[A-Za-z0-9._-]`。
- 记录上 `Rec.Host` 标记远端行。记录另加来源机器、迁移去向两个字段（未实现，见 [migration.md](migration.md)「Claude 完整迁移」）。

## 路径映射

- 单独一个模块 `internal/pathmap`：调用时明确传入源端和目标端（各自的操作系统、home、主机名、是否 WSL，取自 `hello`），处理分隔符、盘符、大小写、UNC、空格和中文，用纯字符串实现，不管 tend 自己跑在哪个系统上。某个路径在目标端没有对应（不在源端 home 下又不是共享盘、相对路径、UNC、`..`、目标端存不下的名字）时明确返回失败。
- home 不设默认值，一律取远端 `hello` 返回的值：不同机器的用户名和 home 各不相同（有的机器上是 root）。
- Windows 和同一台机器上的 WSL 视为近邻：它们共用同一块磁盘，`C:\…` 就是 `/mnt/c/…`。两者之间迁移时不比对代码，同一个项目也不重复计数。
- 四类系统两两组合都写表驱动测试。

## 配置

`~/.agent/tend/config.json` 里的 `hosts`（见 [cli-and-config.md](cli-and-config.md)「配置」）：

- `name`：卡片上和 `host:<名字>` 里用的名字。
- `ssh`：`~/.ssh/config` 里的别名；空 = 本机子进程（换一套配置目录，测试用）。
- `tend`：在那边运行 tend 的参数数组，一个参数一个元素，不写成一整条 shell 命令；默认 `tend`。
- `shell`：远端登录 shell 的引号规则，`posix` / `cmd` / `powershell`；空时按 argv 猜：盘符路径、`.exe/.cmd/.bat`、`wsl` → cmd。

示例（别名、WSL 发行版名和路径以实际为准）：

```json
"hosts": [
  {"name": "mac",   "ssh": "<mac 别名>"},
  {"name": "linux", "ssh": "<linux 别名>"},
  {"name": "win",   "ssh": "<windows 别名>", "tend": ["C:/Users/<user>/.local/bin/tend.exe"]},
  {"name": "wsl",   "ssh": "<windows 别名>", "tend": ["wsl", "-d", "Ubuntu", "-e", "/home/<user>/.local/bin/tend"]},
  {"name": "box",   "ssh": "<linux 别名>",   "tend": ["docker", "exec", "-i", "<容器>", "/usr/local/bin/tend"]}
]
```

## 连接与失败

- 传输：`ssh -T -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=15 -o ServerAliveCountMax=3 -o Compression=yes <别名> <tend argv> rpc --stdio`，约 45 秒判定断线。
- 连接复用：非 Windows 发起端（Mac、Linux）用 ControlMaster 复用连接（`ControlPersist=60`），socket 放在只有自己能访问的 `hosts/ssh/%C`；目录长度加上 ssh 追加的 58 字节放不进 104 字节的 socket 路径上限时不复用（`paths.SocketRoom`）。
- 失败分类：ssh 退出码 255 按 stderr 分成 `offline` / `auth` / `hostkey`；协议号不兼容另算。远端找不到 tend（`no_tend`）要求进程非 0 退出，且 stderr 匹配某个 shell 自己的报错格式：sh / bash / dash / ksh 的 `not found` / `No such file or directory`、`zsh:N: no such file or directory:`、fish、cmd 的 `is not recognized as`、WSL 的 `execvpe(...) failed`。锚定这些格式是为了不把 `tend: open …` 这类 tend 自己的错误误判。连接关闭的原因按进程退出分类，不看写入时撞到的 broken pipe。
- 超时：等应答超时只放弃这一次调用，连接保留，迟到的应答按 id 跳过；请求发不出去、`Close`、远端进程退出时才关闭连接，下次调用重连；网络断开由 ssh 保活探测发现。
- 关闭：先杀进程，回收最多等 1 秒（另有 `WaitDelay` 兜底）。
- WSL 的 UTF-16 警告和非 JSON 行被跳过。
- 机器连不上时显示缓存并标注「离线 · 5 分钟前」，不弹错误打断用户。
- 同一台机器同时最多一个请求，失败后退避重试（见「TUI」）。

## 记录来源接口

读会话内容（右栏消息、全文、步骤、pulse、恢复前检查）一律经 `hosts.Source(r)`：本地会话直接读本地文件，远端会话走协议的 `messages` / `text` / `steps` / `pulse` / `checks`。右栏和检查项都只通过这个接口读取。

- 远端每页带上 transcript 的文件身份（`fileio.ID`：Unix 用 dev + inode，Windows 用卷序列号 + 文件索引）。之后翻页、读全文、读步骤都带上它；文件被改写过（比如那边跑了 `tend mv`）就返回 `stale`，TUI 丢掉旧页，从文件末尾重读。
- 远端 transcript 读不到时返回 `not_found`，不当作空文件。
- 读取失败（`capture.Page.Err`）不当作读到文件头：翻页失败保留游标；首次读取失败的，在主机恢复应答后重读。读取失败的 probe 不自动补页，主机恢复应答后重建。

## 聚合

### 功能

| 功能 | 做法 |
|---|---|
| 列表 | 整份拉取，不做增量游标（since 游标和在远端执行过滤：未实现）。卡片多一个「机器」标记；筛选 `host:` / `machine:`：不写 = 只看本机，`all` = 所有机器，其余是机器名（CLI 遇到没配置的名字会提示） |
| 右栏对话 | 经记录来源接口按页读取，每次取 40 句；按偏移读取的游标带上文件身份，文件变了就重新定位 |
| 搜消息 | 未实现。打字时只搜本机；按 Enter 提交后，才并行去问各台机器。远端返回「是否全中、机内排名、命中数、命中偏移」，本机按档位和名次交错合并（各机器的分数按各自语料算，不能直接比）；能取消，也能只显示已返回的部分。现在远端行上 `\` 和 `→` 不查本机正文库，只在右栏已加载的消息里一处处跳 |
| Agents | 各机器在跑的会话合在一起，标出机器。远端 Agents 卡片只显示远端自己的运行状态，不查本机按 session id 记的 pulse 和关注状态 |
| 收藏 / 状态 / 编辑 | 远端行只读（见「远端行只读」）。计划：写到会话所在的那台机器，走 `put`：字段补丁 + 预期版本号（对不上就拒绝，本机刷新后重试）+ 请求 id（重试不会重复执行）；Windows 上的记录文件锁已是进程间锁（`LockFileEx`） |
| 恢复 | 见「恢复」 |
| 项目对应关系 | 未实现。用户确认过一次的「这台机器的哪个目录对应那台机器的哪个目录」，记下来以后直接用。首次猜测时先读 `~/.claude.json` 的 `githubRepoPaths` 和 Codex 的 `git_origin_url`，读不到再扫描目录；同一个 remote 对应多个目录时让用户选 |

### 恢复

- `ssh -t <别名> <tend argv> resume --terminal --no-herdr <sid>`：远端 tend 自己处理目录、转义和 attach（后台会话用 `claude attach`），Windows 和 WSL 的命令差异也由远端处理。
- 在 Herdr 里时开本 workspace 的新 tab 跑这条命令，否则在当前终端。
- docker / podman 的 `exec` 在恢复时自动加 `-t`。

### 远端行只读

收藏、状态、标签、归档、删除、搬目录、在本机打开都拒绝，并提示到那台机器上做；拦截不看焦点，删除和搬目录的入口再各拦一次。`Store` 也拒绝写入 `Rec.Host` 非空的记录。

### TUI

- 启动时先显示各机器缓存，每台后台拉一次；之后只对筛选里看得到的机器每 30 秒拉一次（同一台同时最多一个请求，失败后间隔翻倍、封顶 5 分钟）；live 查询失败时保留上次结果。
- 行按「机器 + 会话」键复用，原地更新，光标、pin、probe 不丢。
- 机器筹码 `m` 选本机 / 全部 / 某台（带条数或离线）；筛选包含的机器连不上时头部显示「<机器> 离线 · 5 分钟前」。
- 顶部 tab 计数只算本机。

### fzf

fzf 每次击键都会 reload，所以 `fzf-list` 直接用 30 秒内的列表和 live 缓存；30 秒内失败过的主机也不重拨。

### 缓存与隐私

- 每台机器的列表缓存在 `~/.agent/tend/hosts/<名字>-<哈希>/sessions.json`，带拉取时间，文件权限 0600；缓存里记下 ssh 别名和 tend 命令，配置换了目标就不用旧缓存。取不到时用缓存并标离线。
- 缓存按字段白名单存：只有卡片字段（标题、摘要、回顾；这些由对话内容生成，所以其实带有对话摘录）。**不缓存对话正文，搜索片段也不写盘。**
- 远端内容永远不进本机的全文库 `text/`。
- `tend hosts clear` 清缓存；`tend hosts rm` 连缓存目录一起删。按机器配置 `cache: meta | none`：未实现。

## CLI：`tend hosts`

`tend sessions|list host:…` 合并远端行；`<id>` 可写成 `机器:sid`。机器管理都在 `tend hosts` 下：

- `tend hosts`：配置、各自应答过的版本、缓存时间。
- `tend hosts add <名字> <ssh 别名> [--tend 路径] [--wsl 发行版 | --docker 容器 [--docker-cmd]] [--shell]`：名字只能用字母、数字和 `._-`，不能叫 `all` 或 `local`，大小写不同也算重名。`--wsl` / `--docker` 必须配 Linux 绝对路径，据此拼出 tend argv（`wsl -d <发行版> -e <路径>`、`<docker-cmd> exec -i <容器> <路径>`）；写入配置后默认跑一次 check。
- `tend hosts rm <名字…>`：连缓存目录一起删。
- `tend hosts clear [名字…]`：清缓存。
- `tend hosts check`：见下。
- `tend hosts install <机器>`：见下。

### `tend hosts check`

对每台机器做同一组探测：连接、握手（版本和协议号）、系统、端点、PATH 里有没有 `claude` / `codex`、中文参数能否原样往返、列表和读一大页消息各要多久。结果输出成一张矩阵。

它既用来排查问题，也是验收：四类机器分别当发起端和远端，都要通过。

### `tend hosts install <机器>`

分发 tend 本身：只在用户手动运行时推，tend 不会自动推送；这不属于「对齐 AI 环境」。协议或应答端改了之后，每台配置的机器都要重新 install。

- 默认按对方系统交叉编译当前源码，传过去。
- 安装位置从 tend argv 读出，只认四种：绝对路径的二进制、裸 `tend`（装到 `~/.local/bin`）、`wsl … -e <路径>`、`docker|podman|nerdctl exec … <容器> <路径>`，其它一律拒绝；除裸的 `tend` 外，相对路径一律拒绝。
- 直接安装：先建目录，scp 到 `.new`，再改名换上。Windows 先把旧文件挪成 `.old`：若 `.old` 还被上一版的进程占着（比如一直开着的 `tend rpc`），就挪成 `.<编号>.old`；不再被占用的 `.old` 每次安装时顺手删掉。
- WSL：scp 到 Windows 登录目录，再 `wsl … -e sh -c` 在里面 cp 成 `.new` 并改名（wsl 从 ssh 的登录目录启动）。
- 容器：scp 到宿主登录目录，`docker exec mkdir`、`docker cp` 成 `.new`、`docker exec mv`。
- 临时文件最后删掉。最后打招呼，核对版本、OS、架构，与刚构建的不一致就报错。
- `--build-there`：不传二进制，让那台机器自己编译（链路传不动十几 MB 时用）。
  - 要求源码目录没有未提交的改动、HEAD 在某个远端分支上（当前分支的 upstream 优先），否则报错。
  - 生成一份几 KB 的脚本 scp 到登录目录，在 tend 运行的地方执行（登录机器 `sh`，Windows `powershell -File`，WSL `wsl … -e sh`，容器先 `docker cp` 到 `/tmp` 再 `docker exec … sh`），跑完删掉。
  - 脚本在 `~/.cache/tend-src`（Windows `%LOCALAPPDATA%\tend-src`）里 `git init`，先 `fetch --depth 1` 只拉这个提交（多数服务端按提交号给），不行再 fetch 整个分支，然后检出这个提交。
  - 有 mise 用 `mise exec -- go`，否则用 PATH 里的 go；`CGO_ENABLED=0`、`-trimpath -ldflags "-s -w -X main.version=<git describe>"` 编成 `.new` 再改名换上（Windows 换法同上）；`--server` 时同样编 tend-server。
  - `--proxy <url>` 只给这次编译的 git 和 go 设 `HTTPS_PROXY` / `HTTP_PROXY`（Windows 上的 git、go 不读系统代理）。
  - PowerShell 脚本里的值一律写成单引号字面量（赋值语句里裸词会被当成命令）。
  - 之后照常打招呼核对版本和 OS（架构以那台机器自己的为准）。

## 未实现

记忆、项目对应关系、远端写（`put`）、跨机器搜消息、迁移、环境诊断、增量列表、读请求的时间预算与部分结果、`cache: meta | none`。迁移、记忆和环境诊断的设计见 [migration.md](migration.md)。
