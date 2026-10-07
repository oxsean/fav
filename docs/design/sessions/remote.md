# 其它机器

在任何一台机器上看、读、恢复其它机器上的 Claude Code / Codex 会话：远端合同（协议、标识、路径映射、配置、连接与失败）和聚合（列表、筛选、右栏、恢复、缓存）。迁移、记忆和环境诊断见 [migration.md](migration.md)。

实现：`internal/remote`（`proto.go` 方法与类型、`local.go` 应答端、`client.go` ssh 客户端、`transport.go` 两种传输、`hosts.go` 机器与缓存、`trash.go` 远端删除与还原、`source.go` 记录来源，`handoff.go` / `memory.go` / `env.go` / `migrate.go` 交接、记忆、环境和迁移的应答），`internal/pathmap`，`internal/paths`（`SocketRoom`），`cmd/tend`（`hosts.go`、`rpc.go`）；帧与连接在 `internal/wire`。

## 范围

不做云同步；单用户多机经自己的 SSH 做只读聚合，迁移由用户手动发起（见 [overview.md](overview.md)「暂不包含」）。具体到多机：

- 只走用户自己的 SSH：不经过任何云，不开端口，不留常驻服务。
- 平时只**远程读**，内容留在原机器；本机只缓存列表字段（见「缓存与隐私」）。
- 只有用户**主动迁移**时，才在机器之间复制会话文件（见 [migration.md](migration.md)「迁移」）。
- 环境诊断只报告差异，tend 不替用户安装或同步任何东西来对齐环境。
- 不做：把各机器的会话 rsync 到本机做镜像；Codex 会话的完整迁移（列为验证项，见 [migration.md](migration.md)「Codex：不做完整迁移，列为验证项」）。

## 架构

每台机器各跑各的 tend。本机去问远端的 tend，远端读它自己的索引和文件，把结果返回；本机只做聚合和展示。

四类机器（Mac、Linux、Windows、WSL）**两端都支持**：都能跑 TUI 去看别的机器（发起端），也都能被别的机器查询（远端）。

### 两种传输

`Hosts` 管缓存、文件身份和 `Source`，经一个传输（`Transport`）去问别的机器。传输负责四件事：给出机器名单、执行一次调用（含握手 `hello`）、给出缓存目标、说明列表能不能写盘。`Source`、翻页、文件身份、`stale` 与传输无关。

| | ssh（`NewHosts`） | `node.call`（`NewNodeCall` + `NewHostsOver`） |
|---|---|---|
| 机器名单 | `config.hosts` | 调用方给的 `[]Machine`（`SetMachines`，随时可换），不在名单上的机器回 `not_found` |
| 一次调用 | 每台机器一条 `tend rpc --stdio` 长连接，拨号后先 `hello` | 经协调器的 `node.call{machine, method, params}` 转给那台机器的节点；每台机器第一次调用前发一次 `hello`，遇到 `offline` / `closed` 后重发。`hello` 不带 `lang`（节点按它设整个进程的语言），检查项文本用节点自己的语言 |
| 缓存目标 | ssh 别名 + tend argv | server 地址 + 机器名 |
| 写盘 | 都写 | 只有 `Mine`（看的人是主人）的机器写；其余只放内存 |

- `node.call` 传输不依赖 `internal/coord`（`coord` 依赖 `remote`）：它接一个执行 `node.call` 的函数（`NodeCaller`），由客户端用自己连协调器的连接组装；那条连接归调用方，`Hosts.Close` 不关它。
- 经 `node.call` 读的机器没有 ssh 配置：`Hosts.Host` 和 `ResumeCommand` 对它们返回 false，怎么恢复由客户端定。
- 协调器转发哪些方法见 [wire.md](../runs/wire.md)（`node.call` 只转发会话读取方法），谁能读见 [team.md](../tasks/team.md)。节点断开后协调器回断开时的错误码（多为 `closed`），从没连上过的回 `offline`；两种都按取不到处理，用缓存。
- 用哪种由 `cmd/tend` 的 `newHosts` 按配置定：没有 `coordinator.url`（单机）用 ssh；有（server 模式）时 TUI、`tend sessions`、fzf、`tend show` / `preview` 都用 `node.call`，任何一步都不退回 ssh。`tend hosts` 只管 ssh 主机。

### server 模式

- 连接：TUI 启动就在后台拨 server，任务页和读会话共用这一条；断开后立刻重拨一次，再失败按 30 秒起、翻倍、封顶 5 分钟重试。命令行在第一次要读别的机器时拨一次（5 秒为限）。
- 机器名单：`machine.list` 里 `sessions` 为真的机器（看的人能读它的会话），去掉本机（`node_id` 等于本机 `<数据目录>/node/id` 的那台）；主人是看的人（`hello.caller`）的算「自己的」，可以写盘。名单变了随时换。还没连上时先用盘上缓存目标是「这个 server + 机器名」的那几台（都是自己的）。
- 连不上 server：自己的机器显示缓存，标「离线 · n 分钟前」；顶栏不再一台台列，合成一句「连不上 server：原因 · 别的机器显示缓存」（项目页写「项目分组暂时按目录」）；别人共享的机器不显示；本机会话照常。命令行只在 stderr 打这一句。
- 旧 server（机器名单里没有 `node_id`）：认不出本机，本机会话照样本地读，不归项目，底栏提示一次；它也不给 `sessions`，所以别的机器一台都不列。
- 别人共享的机器（`sessions` 为真、主人不是看的人）只读：卡片、右栏状态行在 `@机器` 后写「只读 · <主人>」，右栏的「恢复目标」和恢复前的检查换成「只读」和「<主人> 共享给你的会话：不能在这里恢复，也不能建成任务」；Enter（和 Space）打开对话，底栏写「Enter 看对话」；`r` 和命令面板的「建成任务」闪这一句，不开对话框；写记录的键见「远端行的写入」。主人是 `Machine.owner` 经协调器 `people.names` 换成的名字（每条连接缓存，没缓存的 id 一次批量问；连接换了或状态流推 `reset` 时清空；旧 server 没有这个方法，照旧写 id）。
- server 给出完整名单（有 `hello.caller`、不是旧 server）时，盘上缓存目标是这个 server、而名单里已不是自己的机器（换了主人、改成别人共享给你、或不在名单里了），整个缓存目录删掉（`NodeCall.Forget`）；连不上 server、只剩自己的机器时不删。

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
  - 已有：`hello`、`list`（整份返回）、`query`（筛选、排序、分页后的一页，见「列表与写入」）、`put`（写一条记录，同上）、`trash` / `restore`（删除到那台机器的回收站、还原，同上）、`grep`（搜消息，`{q, all, limit, budget_ms, projects, also}` → `{hits, building, busy, too_long, fixes}`）、`hits`（一个会话里的命中，`{provider, session_id, q, limit}` → `{hits, total}`）、`messages`（带 `find` 时每条消息带高亮的 `spans`）、`text`（全文，按偏移读）、`steps`、`pulse`、`checks`、`live`、`echo`（中文往返自检）。搜消息这三处的做法见 [index-and-search.md](index-and-search.md)「搜消息」的「节点」。
  - 交接、记忆、环境和迁移（做法见 [migration.md](migration.md)）：名字、参数和回答的类型在 `proto.go`，`local.go` 把它们分给 `handoff.go`、`memory.go`、`env.go`、`migrate.go`；都在 `methods` 里；对端没有某个方法时照「tend 旧」处理（迁移看目标机器有没有 `import.begin`，`tend show` 看有没有 `copies`）。
    - 交接：`handoff.facts{provider, session_id}` → `capture.HandoffFacts`（源机器：交接包的各段事实，不是成稿：标题、目录、分支、最近活动、transcript 路径、摘要、最近 5 条要求、最后一条回复、改过的文件、`git{status, remote}`，各段上限同本机交接）、`handoff.put{ref, text, dir, provider, from}` → `{id, path}`（目标机器：把成稿写成那台的 `handoff/<id>.md`，同目录 `<id>.json` 记下目录、provider、来源；`provider` 是新会话用的 CLI，`ref` 是源机器上的会话；`text` 为空、`dir` 不是这台的绝对路径、`provider` 不是 claude / codex 回 `bad_request`）。做法见 [resume.md](resume.md)「交接到另一台机器」。
    - 记忆：`memory.ls{dirs, global}` → `{sets}`、`memory.read{file}` → `{text, at, sha}`、`memory.trash{file}` → `{entry}`、`memory.restore{entry}` → `{file}`、`memory.put{dir, kind, name, text, line, expect}` → `{file, incoming, lines, bytes, over}`。
    - 环境：`env{dir, ref?}` → 这台机器的指纹（`envcheck.Print`）、`env.file{kind, name, dir}` → `{text}`。
    - 迁移，源机器：`export.plan{provider, session_id, migration?, to?}` → `{manifest, live, why, cwd, repo, git}`、`export.read{…, migration, file, off, n, id}` → `{data, eof}`、`export.done{…, migration, state, move, committed}` → `{trashed}`、`copies{provider, session_id}` → `{copies}`；目标机器：`import.begin{migration, from, provider, session_id, title, cwd, dir, pairs, manifest}` → `{staged, committed, source, clash}`、`import.chunk{migration, file, off, data}` → `{off}`、`import.commit{migration, note}` → `{row, files, source, unmapped}`、`import.abort{migration}` → `{}`。`data` 是 base64，`export.read` 的 `n` 不超过 4 MiB，`id` 是规划时的文件身份，对不上回 `stale`；`import.begin` 的 `clash` 是 `forward` / `diverged` / `exists` 或空，`committed` 是已提交时的那一行，`source` 是那次提交带过去的源主 transcript（大小、sha），驱动把它作为 `export.done` 的 `committed` 交回源机器；`copies` 每条带 `changed`（这一边的主 transcript 自迁移以后变没变，不知道时不带）和 `moved`（原件已进回收站）。驱动函数在 `migrate.go`，只经 `Peer` 调两端（`StartMigration`、`Migration.Run`、`Abandon`、`CheckCopies`），本机是进程内的 `Peer`。
    - `from`、`to` 和关系里的 `peer` 是另一台机器（`PeerRef{name, endpoint, node_id, end}`）：`name` 只用来显示，按 `endpoint` 认机器，`end` 是路径映射要的那台的系统和 home。
    - 协调器只把它们转给机器主人（[team.md](../tasks/team.md) 第 6 条），节点按 `share_sessions` 再判一次（[node.md](../runs/node.md)「会话的可见范围」）。
  - 未实现：`list` 的 since 游标增量。
  - 新方法按 `hello.methods` 协商；新方法要在 `methods` 里登记名字、在 `local.go` 里有处理函数，读 transcript 的还要在 `local` 和 `far` 两个 `Source` 上各有一个方法。
- **预算与部分结果**：`grep` 带 `budget_ms`，正文库在预算内没建完就先回已搜到的和进度 `building`，节点在后台接着建。其余读请求（索引没准备好时的 `list` / `query` 等）未实现：只有调用方超时，等不到就放弃这一次调用。
- **列表与写入**（`query`、`put`、`trash`、`restore`；类型在 `proto.go`）：
  - `query{q, all, sort, limit, after, projects, also, fresh}` → `{rows, next, total, matched, running, facets, tokens, status, trash_days}`。节点按本机列表的做法回答：`tend.Parse(q)` 读成本机的查询（`host:` 是调用方用来挑机器的，节点忽略），`all` 是 `Query.All`；`projects`（看的人看得见的项目和它们在这台机器上的目录）组成 `Rows.Belong`（`task.ProjectOf`，和协调器同一条规则），`also`（会话 id → 关联任务的文字）给关键词匹配；先 `Rows.List(…, q.Scope())`，再 `index.Select` 排序、分页、计数（见 [index-and-search.md](index-and-search.md)「查询语法」）。默认值照 TUI：未归档、没收藏的至少 3 轮。`limit` 1–500，`sort` 是 `active`（默认）/ `started` / `favorited` / `turns`，别的回 `bad_request`；`after` 是上一页的 `next`（keyset 游标）。`rows` 是 `Row{Session, project_id, live, deleted_at}`。`status:trash` 列这台机器的回收站：走 `Rows.List` 的回收站分支，行和 TUI 回收站里的一样（`updated_at` 是删除时间，对话指向回收站里的那份），每行另带 `deleted_at`。`status:agent` 回空的 `rows` 和 `status`：一次性 agent 会话只在 TUI 里看。`trash_days` 是这台机器配置的天数（超过就清掉，0 不清），每个回答都带，删除确认要写它。`tokens` 是 `tend.Tokens(q)`。索引在上次刷新 5 秒内不再刷新（几千个文件就是几千次 stat），带 `fresh` 时强制刷新；收藏库有变化每次都重载。
  - `put{provider, session_id, patch, expect}` → 写后的那一行（`Row`，不带 `project_id`：调用方没给项目，补丁也改不了目录）。补丁是 `tend.Patch`（设值语义，见 [favorites.md](favorites.md)「读写语义」），经 `Store.Edit` 写：没收藏的会话写下去就有了记录，但不是收藏，和 TUI 的 `editRec` 一样。带 `expect`（编辑的人看到的 `updated_at`）而记录的不同时回 `stale`，什么都不写；会话原来没有记录时 `expect` 不比，除非这期间有了记录。记录被删回 `not_found`；空补丁、不认识的状态、空标题回 `bad_request`。只有编辑框带 `expect`；收藏、完成、归档的补丁重复执行结果一样，不需要请求 id。
  - `trash{provider, session_id}` → `{title, files}`（移进回收站的文件数）：和 TUI 的删除同一个函数 `index.TrashSession`（续接链上每个 id 的文件、钉住的副本一起移走，记录留墓碑，索引忘掉移走的文件）。在跑的会话（`capture.LocalLive`）回 `busy`，什么都不动；找不到回 `not_found`。
  - `restore{provider, session_id}` → `{title, files}`：和 TUI 的还原同一个函数 `index.RestoreSession`，之后刷新索引，下一次 `query` 就列出它。不在回收站回 `not_found`。
  - 回收站里的会话照样能读：`messages`、`text`、`steps`、`pulse`、`checks` 找不到记录时看回收站，读那里的那份；`put` 和 `hits` 不看，`put` 回 `not_found`。
  - 节点的 `share_sessions` 对这几个方法的约束见 [node.md](../runs/node.md)「会话的可见范围」。
- **协议数据结构单独定义**：不直接把存储里的 `Rec` 拿来当协议。`Session` 是字段白名单（标题、摘要、标签、路径、时间、轮数等，不含消息）；存储里带 `json:"-"` 的字段（轮数、最后活动时间、改过的文件等）在 `Session` 里都有。列表或筛选要用的新字段加进 `SessionOf` 和 `Session.Rec`（时间转成本机时区）。`Session` 有 `copies`（这个会话和别的机器之间的迁移关系，列表只显示关系，不带 `changed`）：节点的 `list` / `query` / `grep` 由 `migrate.Marks` 从这台的 `migrations.jsonl` 填进 `Rec.Copies`，`SessionOf` 带出，`Session.Rec` 带回。

## 标识

- **机器标识**：一个稳定的端点 id（`hello` 的 `endpoint`：机器 + 操作系统 + WSL 发行版 + 配置目录），能区分 Windows 和 WSL 的不同发行版，以及同一台机器上不同的配置目录。显示名只用来展示。
- **会话引用**：一律写成「机器 + provider + session id」。完整迁移默认复制，复制后同一个 session id 会同时出现在两台机器上。fzf 的行 key、`pick()` 和命令行参数都支持 `host:sid` 的写法。
- 会话 id 只允许 `[A-Za-z0-9._-]`。
- 记录上 `Rec.Host` 标记远端行。`Rec.Copies` 是这个会话的迁移关系（从哪台迁来、迁往哪台），不存进 `records.jsonl`，列表时由 `index.Rows.Copies` 钩子从这台机器的迁移记录现挂，见 [migration.md](migration.md)「Claude 完整迁移」。

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
| 列表 | TUI 和 CLI 整份拉取（`list`），不做增量游标，缓存的整份列表在本机经 `index.Select` 筛选；节点也能在那边筛选、分页（`query`，见「列表与写入」）。卡片多一个「机器」标记；筛选 `host:` / `machine:`：不写 = 只看本机，`all` = 所有机器，其余是机器名（CLI 遇到没配置的名字会提示） |
| 右栏对话 | 经记录来源接口按页读取，每次取 40 句；按偏移读取的游标带上文件身份，文件变了就重新定位 |
| 搜消息 | 节点回答 `grep`（本机排名、命中数、是否一条全中、片段和高亮、偏移，不回分数）、`hits` 和 `messages` 的 `find`（见「方法」）。Web 的会话页按 Enter 才搜，协调器 `sessions.grep` 并行问各台读得了会话的机器，按档位和名次交错合并（各机器的分数按各自语料算，不能直接比），没建完正文库的那台标进度。TUI 和 `tend grep` 也搜 `host:` 选中的机器（见下文「TUI」和 [index-and-search.md](index-and-search.md)「搜消息」）：两种传输同一段代码（`Hosts.Grep` / `Hits` / `GrepWithin` / `GrepAll`，`grep.go`），合并和协调器同一个 `Interleave`（`grepmerge.go`），给节点的项目目录也和协调器同一个 `ProjectDirsOn` |
| Agents | 各机器在跑的会话合在一起，标出机器。远端 Agents 卡片只显示远端自己的运行状态，不查本机按 session id 记的 pulse 和关注状态 |
| 收藏 / 状态 / 编辑 / 删除 | 经节点的 `put`、`trash`、`restore`（见「列表与写入」）写到会话所在的那台机器；TUI 和 CLI 的远端行见「远端行的写入」。Windows 上的记录文件锁是进程间锁（`LockFileEx`） |
| 恢复 | 见「恢复」 |
| 项目对应关系 | 项目的目录（`task.Repo.Dirs`，机器 → 那台的检出目录）就是对应关系，两种模式都存在协调器里。会话要去的机器上没有项目目录时，问那台的 `node.repos{remote}`（见 [node.md](../runs/node.md)「找检出」）：只有一个就用它，几个让用户选，没有就手填 |

### 恢复

- `ssh -t <别名> <tend argv> resume --terminal --no-herdr <sid>`：远端 tend 自己处理目录、转义和 attach（后台会话用 `claude attach`），Windows 和 WSL 的命令差异也由远端处理。
- 在 Herdr 里时开本 workspace 的新 tab 跑这条命令，否则在当前终端。
- docker / podman 的 `exec` 在恢复时自动加 `-t`。
- server 模式：
  - 自己的机器在 `config.hosts` 里有同名项时，先经 ssh `hello` 问它的 `node_id`，和 server 名单里那台的对上才走上面的 ssh 恢复（每个进程每台问一次；连不上不记结果）。
  - 没有同名项或对不上：对话框给在那台机器上执行的恢复命令（`cd <目录> && <agent> resume <id>`，引号按 server 报的那台机器的系统：Windows 用 PowerShell，其余 POSIX），主按钮复制，同网页；命令行的 `tend resume` 打印这条命令并失败退出，fzf 的复制复制它。迁到那台的会话给 `tend resume <id>`，好让首次恢复带上迁移说明（[resume.md](resume.md)「恢复流程」第 7 条）。
  - 别人共享的机器不给恢复：TUI 里 Enter 打开对话，命令行拒绝。

### 远端行的写入

- 自己的机器（单机模式下 `config.hosts` 的全部；server 模式下主人是看的人的）：收藏、状态（`x` 和 `tend status` / `done`）、归档、编辑（标题、标签、摘要，包括恢复框里改的标题）经那台机器的 `put` 写，删除和还原经它的 `trash` / `restore`，补丁和本机行同一个 `tend.Patch`。`Hosts.Put(ctx, 机器, ref, patch, expect)` 经传输的 `Call` 发出，ssh 和 `node.call` 一样；回答的行换掉缓存里那一行（没有就补上），缓存照「两种传输」的写盘规则落盘。只有编辑框带 `expect`（打开时那条记录的 `updated_at`），对不上回 `stale`；开关和撤销不带。
- `Hosts.Trash` / `Restore` / `Trashed`（`internal/remote/trash.go`，TUI 和 CLI 共用）：那台上次 `hello` 的 `methods` 里没有 `trash`（或这个方法本身）时不发请求，回 `unknown_method`。`Trash` 成功后从缓存的列表里去掉这一行；`Restore` 不动缓存，下一次拉列表带回它；`Trashed` 是 `query{q: "status:trash", all}`（翻完所有页），行的 `updated_at` 换成 `deleted_at`，另回 `trash_days`。失败的说法（`remote.Refused`）三种写法共用。
- TUI：远端写只有一条路（`farWrite`）：按键时取好参数，后台调一次 `Hosts` 的方法，后台只读值的拷贝；回答回来在主循环上应用。`put` 把新值原地拷进同一行（指针不变），再闪一句、给撤销，和本机行同一段代码，所以撤销窗口从回答到达时算起，撤销也是一次 `put`；之前这一行仍是旧值。恢复框里改了标题时先等 `put` 回答，回答到了、恢复框还开着才接着恢复；失败就留在恢复框，闪原因。
- TUI 的删除：`D` 弹和本机一样的确认框，正文写「它在 <机器> 上的文件移入那台机器的回收站；在回收站筛选里按 `D` 可还原」（键取自 `bindings`）和那台的 `trash_days`（这次进过回收站视图才知道，不知道就不写）；文件数要那台机器才知道，确认框不写，成功后的闪句也只写标题。已知在跑（那台 `live` 里有它）就直接闪「这个会话正在 <机器> 上运行」，不开确认框；那边回 `busy` 时也闪这句。成功后这一行从列表里去掉。本机的删除不给撤销，远端也不给。
- TUI 的回收站视图：`status:trash` 且 `host:` 选到的自己的机器，每次进视图后台读一次它的 `Trashed`（离开视图再进才重读，删除过的机器下次进也重读），行照本机回收站那样按 `status:all`、不嫌短筛选；读不到时闪「读不到 <机器> 的回收站：<原因>」。别人共享的、tend 旧的、连不上 server 时不读。`D` 经 `restore` 还原，成功后行从回收站里去掉，再拉一次那台的列表。
- 写不了时按键只闪一句，不开编辑框或确认框：
  - 别人共享的机器：「<主人> 共享给你的会话在这里只读：只能看对话」；
  - server 模式下连不上 server：「连不上 server，改不了别的机器上的会话：<原因>」；
  - 那台的 tend 旧（上次 `hello` 的 `methods` 里没有 `put`，删除和还原看 `trash` / `restore`，每个方法分开记；没取到过 `hello` 的，`Hosts` 不发请求，回 `unknown_method`）：「<机器> 的 tend 旧：先 `tend hosts install <机器>`」；
  - 发出去失败：`stale` 说「这条记录刚被别处改过」，`busy` 说会话在那边运行，其余写「改不了 / 删不了 / 还原不了 <机器> 上的会话：<原因>」，行不变。
- CLI：`favorite` / `unfavorite` / `archive` / `unarchive` / `status` / `done` / `edit` 和 fzf 的 `fzf-pick toggle*` 接受 `host:sid`（`remotePick`、`writeRec`）；`tend rm host:sid` 问过 y/N 后经 `Hosts.Trash`；`tend trash --restore host:sid` 在那台的 `Trashed` 里按 session id 前缀或记录 id 找，再经 `Hosts.Restore`。server 模式下先拨 server，拨不上、或机器是别人共享的就拒绝（`farWritable`）；`edit` 先从那台重读这一行，再开编辑器。`tend trash host:<机器>` / `host:all` 和 `tend sessions host:<机器> status:trash` 经 `Hosts.Trashed` 列那些机器的回收站（`hostTrash`，别人共享的机器不列）；`--purge` 只管本机。
- 搬目录、钉住、在本机打开、交接、分叉仍只在本机做：TUI 提示「其它机器上的会话在这里能收藏、改状态、归档、编辑和删除，其余到那台机器上做」，拦截不看焦点，搬目录的入口再拦一次；CLI 拒绝。`Store` 拒绝写入 `Rec.Host` 非空的记录。

### TUI

- 启动时先显示各机器缓存，每台后台拉一次；之后只对筛选里看得到的机器每 30 秒拉一次（同一台同时最多一个请求，失败后间隔翻倍、封顶 5 分钟）；live 查询失败时保留上次结果。
- 行按「机器 + 会话」键复用，原地更新，光标、pin、probe 不丢。
- 机器筹码 `m` 选本机 / 全部 / 某台（带条数或离线）；筛选包含的机器连不上时头部显示「<机器> 离线 · 5 分钟前」。server 模式下列 server 名单里能读的机器（去掉本机），副标题「server 上你能读会话的机器；本机的会话不经 server」，「本机」一项保留；别人共享的写「<机器>：只读 · <主人>」（离线时再加原因），按键提示下面一行灰字「谁能看你机器上的会话，在网页的机器页设」：会话可见范围、分享这类管理只在网页上做，TUI 不给入口。
- 顶部 tab 计数只算本机。
- 搜消息：除本机外，问筛选里看得到的机器（`host:` 选中、不是 `status:trash` / `status:agent`）各自的 `grep`，每台一条后台命令，经 `m.hosts`（ssh 或 `node.call`），每台最多等 `GrepWait`（5 秒），节点搜 `GrepBudget`（3 秒）；列表已经取不到的机器、server 连不上时的全部机器不问。每一次都要碰到每一台机器，所以输入时停顿 600ms 才发、离开搜索框马上发，本机正文库增量和索引刷新不让远端重搜。其余见 [index-and-search.md](index-and-search.md)「搜消息」。

### fzf

fzf 每次击键都会 reload，所以 `fzf-list` 直接用 30 秒内的列表和 live 缓存；30 秒内失败过的主机也不重拨。

server 模式下同样守 30 秒：

- 只列自己的机器（盘上缓存目标是这个 server 的），列表 30 秒内的直接用，不拨 server；
- 要拨时拨失败，就在 `hosts/server.json`（0600）记下 server 地址和失败时间（不含任何会话内容），30 秒内不再拨；
- 别人共享的机器只放内存，fzf 每次是新进程，所以不列；筛选含别的机器时列表末尾一行无键的提示「共享给你的机器在 tend tui 里看」。

### 缓存与隐私

- 每台机器的列表缓存在 `~/.agent/tend/hosts/<名字>-<哈希>/sessions.json`，在跑名单在同目录的 `live.json`，都带拉取时间，文件权限 0600。缓存里记下传输给的目标（ssh：别名 + tend 命令；`node.call`：server 地址 + 机器名），目标变了（改了配置、换了 server、在 ssh 和 server 之间切换）就不用旧缓存。取不到时用缓存并标离线。
- 传输说不能写盘的机器（经 `node.call` 读、主人不是看的人），列表、在跑名单和上次失败都只放在这个进程的内存里，`hosts/` 下不为它建任何文件；进程退出就没了。原来是自己的、现在不是的机器，TUI 或命令行收到 server 的完整名单时删掉它的缓存目录（见「server 模式」）。
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

增量列表、`grep` 以外读请求的时间预算与部分结果、`cache: meta | none`。迁移、记忆和环境诊断各自未实现的部分见 [migration.md](migration.md)。
