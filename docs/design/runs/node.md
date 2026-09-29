# 节点

一台机器上的 run：run 目录、监督进程 `tend _run`、观察、会话绑定、双向流。实现：`internal/node`（run 目录、`run.*` 方法、监督进程、快照）、`internal/proc`（分离启动、进程树、存活检查，按 OS）。

## run 目录

`<home>/node/runs/<run_id>/`：

| 文件 | 写的人 | 内容 |
|---|---|---|
| （目录本身） | `run.start` / `run.stop` | 先在 `runs/.new-<run_id>-<随机>` 里写好 `spec.json`、`prompt.md`（墓碑还有 `claim` 和 `state.json`），再改名到位：出现在 `runs/` 里的目录总是完整的。改名成功 = 拿到启动权；已存在 → 回现状，绝不再启动。`run.stop` 遇到没有的目录也这样发布一个墓碑 |
| `claim` | 监督进程 / 节点 | `O_EXCL` 创建：谁先建谁决定这个 run 的第一个 state（监督进程写 starting；节点写 not_launched 或墓碑的 stopped） |
| `spec.json`（0600） | `run.start` | 冻结的启动参数：argv、env、目录、provider、预设会话 id、runner、stdin 文件、`coordinator` |
| `prompt.md`（0600） | `run.start` | 任务书；background 方式在末尾加运行约定：没人看、要人拍板就停下并让最后一条消息以 `ASK:` 开头，可以用 `tend run note` / `tend run ask`（写成 tend 的绝对路径） |
| `lock` | 监督进程 | 终生持有；`filelock.Held` = 监督进程活着 |
| `state.json` | 监督进程或拿到 `claim` 的节点（`fileio.WriteAtomic`） | `{rev, state, pid, pid_start, pane, session, exit_code, reason, detail, attention, ask, note, last, usage, stream, requests, sends, started_at, ended_at}`；`pid_start` 是 agent 进程的启动时间（`proc.StartTime`：macOS sysctl、Linux `/proc/<pid>/stat` 第 22 项、Windows `GetProcessTimes`），和 pid 一起认出同一个进程 |
| `stop` | `run.stop` | 存在 = 请求停止 |
| `output.log` | 监督进程 | background 方式的 stdout / stderr 和 hook 的输出，16 MB 轮转为 `.1`，最多两代 |
| `marks.jsonl` | 监督进程 | 只追加 `{type:"tend", event, id?, n?, phase?, name?, code?, file, off, at}`，`file` 是 `output.log` 这一代的 `fileio.ID`、`off` 是写这一行时它的位置。`event`：`roll`（日志从这一代的 `off` 接着写，每代打开时一条）、`start`（agent 启动前）、`exit{code}`（agent 退出、输出读完之后；这两条只在 background 方式）、`hook{name, phase begin\|end, code}`（setup、before_run、check、cleanup）、`turn{n}`（开始第 n 轮的那一行的开头）和 `turn{n, phase:"end"}`（带这一轮 result 的那一行的末尾）、`input{id}`（agent 把 send `id` 交回来的那一行的开头，见「双向流」）、`resolved{id}`（请求 `id` 被答掉：claude 是回答写进 stdin 的那一刻，codex 是 `serverRequest/resolved` 那一行的开头）、`interrupt{id, n}`（打断第 n 轮的请求发出的那一刻）、`diff{id, files, add, del}`（codex 第 `id` 轮的 diff 写进了 `diffs/`，在 `turn/completed` 那一行的开头）。只用来在输出里定位和翻页：run 目录 agent 写得了，谁问谁答以 journal 为准 |
| `settings.json` / `mcp.json`（0600） | `run.start` | agent 定义的 claude hooks 和 MCP 服务器（feature `files`） |
| `reports.jsonl` | agent（`tend run ask\|note\|verdict\|plan`、`note --pr`） | 只追加 `{at, kind ask\|note\|verdict\|pr\|plan, text, verdict}`；监督进程每秒读新增的整行，verdict（pass / rework / blocked）进 `state.json` 的 `verdict` |
| `answers.jsonl` | `run.answer` | 只追加 `{request, allow, decision?, message, answers}`；监督进程每 250 ms 读新增的整行 |
| `inbox.jsonl` | `run.send` | 只追加 `{id, text, state, at}`；监督进程每 250 ms 读新增的整行 |
| `interrupts.jsonl` | `run.interrupt` | 只追加 `{id, turn, at}`；监督进程每 250 ms 读新增的整行 |
| `blobs/<sha256>` | 监督进程 | 瘦身移出日志的字段原文，按内容寻址，同一份内容只存一次（见「瘦身」） |
| `blobs.gone` | 节点 | 存在 = 节点为守住总上限删掉了 `blobs/`，要用 blob 的读取回 `gone` |
| `partial.json` | 监督进程 | `{items: [{key, kind say\|think, src, parent?, text}]}`：agent 正在写的消息，每 100 ms 至多整份替换一次（`fileio.WriteAtomic`），没有正在写的就删掉（见「逐字输出」） |
| `diffs/turn-<id>.patch` | 监督进程 | codex 一轮的最后一份 `turn/diff/updated`，`turn/completed` 时写一次 |
| `touched.jsonl` | 监督进程 | 只追加 `{path, via edit\|cmd, base?, new?, lost?}`：agent 用工具改过的文件和命令里提到的路径，每个路径每种方式一条（见「改动」） |
| `trees.json` | 监督进程 | `{git, git_dir?, prefix?, base?, end?}`：运行开始和结束时工作区的树（见「改动」） |
| `base.index` / `end.index` / `live.index` | 监督进程 / 节点 | 取树用的临时索引 |
| `changes.json` | 监督进程 | 运行结束时的全部改动，`run.changes` / `run.diff` 从这里读 |

- `run.list{coordinator, runs?}` 回这个协调器派发的 run 的快照（给了 `runs` 就只回这些）；读快照只读不写。快照在 `state.json` 之上补一条：`!Held` 且 state 不是终态 → `unknown{reason: supervisor_gone}`（没人持锁时再读一次 state，防监督进程刚写完就走）；没有 `state.json`、`!Held`、且创建超过 30 s（`spec.created`）→ 报 `failed{reason: not_launched}`，结束时间是创建后 30 s，不落盘。迟到的监督进程发现创建已超过 30 s，自己写下 `failed{not_launched}` 退出，不起 agent（agent 只在第一个 state 写成之后才启动）。`state.json` 读失败重试 5 次、间隔 20 ms。
- 准入：`run.start` 在 `node/admit.lock` 下检查并发布：同一真实目录已有未结束的 run → `conflict "dir_busy <run>"`；未结束的 run 数已到 `node.slots`（0 = 不限）→ `conflict "slots n/m"`。节点预检已判定跑不了（blocker）的 run 不占名额，直接发布成 failed。
- `run.list` 每 10 分钟顺带清掉超过 1 小时的 `.new-*` 残留。
- 监督进程取 `lock` 失败会在 1 s 内重试（`Held` 的探测会短暂持锁）。
- `run.start` 先查约束再看目录是否存在（`allow_dirs` 外的路径一律 unauthorized，不泄露是否存在；约束见 [deployment.md](deployment.md)「模式二」）。
- 终态或 unknown 的 run 被协调器 ack 后保留 7 天再删目录；删之前把它的会话登记进 `<home>/node/sessions.jsonl`（只追加），会话视图仍认它是 run 的会话。两种情况不删：state 不是终态且 agent 进程还活着（会话守卫还要它）；登记会话失败（下次再试）。

## 监督进程 `tend _run <dir>`

1. 拿 `lock`；已有 `state.json` 或拿不到 `claim` 就退出。已有 `stop` → 直接写 `stopped{asked}`，不起 agent。写 `state=starting`，写不成就退出（不起 agent）。
2. 启动 agent，写 `state=running` + pid（Herdr 方式另写 pane）。Windows 上挂起启动，加入 Job 后再恢复。
3. 等 agent（进程本身）退出；期间每秒看 `stop`：存在就结束 agent 的进程组（unix）/ Job（Windows），10 s 后强杀。agent 退出后它留下的子进程还占着输出：2 s 后结束整棵树。
4. 写终态（失败重试 10 s）：有 `stop` → `stopped`（清掉 attention 和 ask）；否则 `exited{code}`，并按输出定结论：最后一条消息有以 `ASK:` 开头的行 → `attention=asked`、`ask` 是这行起的内容；claude `result.permission_denials` 非空 → `attention=permission`、`reason=permission_denied`、`detail` 列工具；非 0 退出或有错误事件 → `agent.Classify` 从错误文本（没有就用 `output.log` 末尾 4 KB）分出 `auth` `quota` `rate_limit` `overloaded` `context_overflow` `network` `session_missing`，`detail` 是最后一条能分类成同一原因的原话（stdout 和 stderr 在 `output.log` 里交错，末行常常不是原因），分不出原因时取最后一行非空文字。
5. 放锁、退出。

- 监督进程死后（run 是 unknown）的 `run.stop`：写 `stop` 后拿 run 的 `lock` 收尾：agent 进程还在、且 pid 和 `pid_start` 都对得上 → 结束整棵进程树，写 `stopped{orphan_stopped}`；agent 已不在 → `stopped{supervisor_gone}`；认不出是不是同一个进程（没有 `pid_start`，或还在 starting 没有 pid）→ 不动，仍是 unknown。`proc.Alive` 不把僵尸进程算活着（容器的 init 不回收子进程，否则停止和 unknown 收尾会永远卡住）：Linux 读 `/proc/<pid>/stat`、macOS 用 sysctl，其它 unix 认不出僵尸。
- `TEND_CRASH_AT=claimed|started|running|ending` 让监督进程在对应切点立即退出（测试用：写完 claim、agent 已起但没记 pid、已记 pid、agent 已退出但没记终态）。
- agent 的环境多了 `TEND_RUN` 和 `TEND_RUN_DIR`（run 目录）。
- stdout 逐行解析（JSON 才解析）：claude `result`（最后一条消息、`is_error`、`permission_denials`），codex `thread.started`（会话 id）、`item.completed` 的 `agent_message`（最后一条消息）、`error` / `turn.failed`（错误）；claude / codex 以外的 agent，最后一行普通输出当最后一条消息。
- 运行中每秒：读 `reports.jsonl` 新增（ask → `attention=asked` + `ask`；note → `note`，并清掉 stalled）；background 方式下 stdout / stderr 超过 `spec.stall_after`（节点 `node.stall_after`，默认 15 分钟，`off` 关闭）没有字节 → `attention=stalled`，再有输出就清掉；只标记，不停。herdr 方式每 3 s 看 agent 是否在等人：Herdr 里它的 pane 是 `blocked`，或（claude）会话 transcript 末尾是没回答的 `AskUserQuestion` / `ExitPlanMode`，或最新的 Claude hook 事件是 `Notification` / `PermissionRequest` 且 transcript 之后没再长（`tend install-hook`）→ `attention=asked`（没有 `ask` 原文）；不再等时清掉，但只清自己标的，`tend run ask` 的提问留着。Herdr pane 状态和 Claude hook 这两个来源只用于 herdr 方式。
- 读写管线：读 agent stdout 和 stderr 管道的 goroutine 只读，把字节放进内存里按字节计的有界缓冲（stdout 64 MiB，stderr 16 MiB，常数在 `internal/node/limits.go`），满了才等；写盘、解析、写 `state.json`、往 stdin 发消息都在另外的 goroutine 里做。Windows 上 agent 的这两个管道由 `proc.Pipe` 建（`CreatePipe`，缓冲 8 MiB；`os.Pipe` 只有 4 KiB，而 Node 在 Windows 上同步写管道，读的一方停一下，claude 整个就停下）。stream run 的 stdin 只由一个写 goroutine 按发送顺序写，谁发都不等 agent 读（agent 可能正等着自己的输出被读），排队超过 16 MiB 的发送失败；关 stdin 时先写完已排队的。
- stdout 先组装成整行再处理：读管道的 goroutine 组装，一行最多 32 MiB（`maxLine`）；更长的行按到来的块原样写进日志，不解析，块之间不插别的内容。顺序是组装、scrub、写 `output.log`、解析。stderr 按行写：半行等它的换行，等满 1 s 或攒到 64 KiB 才整块写出。stdout、stderr 和 hook（setup、before_run、check、cleanup）的输出都经同一个 `rolling` 写，行不会拼在一起，轮转也按全部字节计；`rolling` 打开已有的 `output.log` 时从它的大小接着算。hook 的 stdout 和 stderr 合在一起按行写，半行在 hook 结束时写出。
- 逐字输出：增量不进 `output.log`，由 `partials`（`internal/node/partial.go`）拼成正在写的消息，写进 `partial.json`：
  - claude 的 `stream_event`（`--include-partial-messages`，见 [agents.md](agents.md)）：`message_start` 给出消息 id，`content_block_start` 类型是 `text` / `thinking` 的开一条，`key` 是「消息 id:块序号」，`src` 是消息 id，`text_delta` / `thinking_delta` 接上去；`input_json_delta`（工具参数的碎片）和 `signature_delta` 丢掉。按 `parent_tool_use_id` 分开记，子 agent 的消息各算各的。
  - codex 方法名以 `delta` 结尾的通知（没有 `id`）一律不进日志；`item/agentMessage/delta` 是 say，`item/reasoning/summaryTextDelta` / `textDelta` 是 think（有摘要就只显示摘要，摘要换段时插一个换行），`key` 和 `src` 都是 `itemId`；其余增量（命令输出、文件改动、计划）丢掉。
  - 一条消息的最终那一行（claude 的 `assistant`，它的 `message.id` 和第一个块的类型对上；codex 的 `item/completed`，条目 id 对上）写进日志**之后**，才把它从 `partial.json` 去掉，所以读的一方看到它没了，最终那一行已在日志里。claude 的 `result`、`message_stop` 和 codex 的 `turn/completed` 去掉剩下的（被打断、没有最终行的）；stdout 结束时删掉文件。
  - 一条的文字超过 16 KiB 只留最后 16 KiB，前面加 `…`。
- 轮次：`rolling` 每写完一行，就照 `internal/output` 的 `Parse` 数轮次（读的是 `clipLine` 送出的样子，所以和协调器翻页时读到的一样；只送开头的行不算），轮次变了就在 `marks.jsonl` 记一条 `turn`。
- 送出的行（`run.tail{clip}`）：不超过 16 KiB 的原样；更长的 JSON 行不超过 1 MiB 时解开，每个超过 16 KiB 的字符串截到 16 KiB 并以 `…` 结尾，再编回 JSON（不转义 `<>&`），带上原行长 `size`；其余的只送前 16 KiB，标 `head`。文件里是原文（瘦身移出的字段在 blob 里或 git 里，见「瘦身」）。
- 账号、套餐用量和 agent 自己配置所在的路径不写进 `output.log`：整行丢掉的有 claude 对 `initialize` 的回应（`request_id` 为 `init`，里面有 email、organization 和订阅类型）、claude 的 `rate_limit_event`、codex 的 `account/rateLimits/updated` 和 `hook/*`（hook 的 id 里也有配置文件的路径）；去掉字段再写的有 codex 回应里的 `codexHome`、`instructionSources`、`thread.path`（rollout 文件），`thread/started` 的 `thread.path`，claude `system init` 的 `memory_paths`。超过 32 MiB 的行只按开头的 32 MiB 预筛，可能要改写就整行不写；判断先按字节预筛，JSON 字符串里的引号一定带转义，只是提到这些词的文字不会误中；`output.log` 轮转改名失败（Windows 上有读者开着）就继续追加，下次再轮转。

runner 方式：

| 方式 | 条件 | 做法 |
|---|---|---|
| herdr | 只给 claude；节点 Herdr 可达，且恰好一个 workspace 覆盖目录（零个或多个都走 background） | 新 tab 里跑 `tend _run <dir>`；agent 交互式、留在前台进程组；tab 被关（SIGHUP）先写 `stopped{tab_closed}`；启动时 workspace 已不在 → run 失败（不改成 background，命令行不同） |
| background | 其它 | `proc.StartDetached` 启动 `tend _run <dir>`（unix `setsid`，Windows `DETACHED_PROCESS \| CREATE_NEW_PROCESS_GROUP \| CREATE_BREAKAWAY_FROM_JOB`，父进程的 Job 不许脱离（`ERROR_ACCESS_DENIED`）时去掉 `CREATE_BREAKAWAY_FROM_JOB` 再启动一次）；agent 无界面，claude / codex / fake 走双向流（见下文「双向流（stream）」），command 模板的任务书经 stdin 或 `{prompt_file}`，输出进 `output.log` |

## 瘦身

claude 的工具结果里带着整份文件，codex 反复推一轮的全部 diff；监督进程在 scrub 之后、写 `output.log` 之前把它们移出行（`internal/node/slim.go`）。只看按字节预筛出的行（`"tool_use_result"`、`"type":"tool_use"`、`"type":"fileChange"` 和几个 codex 方法）；没改的行原样写。运行开始时 `git rev-parse --is-inside-work-tree` 定这次运行在不在 git 里。

| 字段 | 在 git 里 | 不在 git 里 |
|---|---|---|
| claude `tool_use_result.originalFile` | 去掉（任何大小；改之前的内容从起点树取） | blob |
| Write 结果的 `content`：`type: create` | blob | blob |
| Write 结果的 `content`：`type: update` | 去掉 | blob |
| `tool_use_result.structuredPatch` ≥ 64 KiB | blob（数组的 JSON） | 同左 |
| Read 结果的 `file.content` | blob | 同左 |
| `tool_use` 的 `input` 里 `content`、`old_string`、`new_string`、`new_source`、`edits[]` 的新旧文字 | blob | 同左 |
| codex `fileChange` 各文件的 `diff` ≥ 64 KiB（`item/started`、`item/completed`） | blob | 同左 |

- 除了 git 里的 `originalFile`，短于 4 KiB 的字符串（`slimMin`）留在行里。
- 原处换成 `{"$blob":"<sha256>","bytes":n,"lines":k}`；去掉的换成 `{"$omit":"<字段>","bytes":n,"lines":k}`（`output.Ref`）。blob 先用 `fileio.WriteAtomic` 写完整（临时文件再改名），才写引用它的那一行；写不成就原样留在行里。
- codex：`turn/diff/updated` 不进日志，每一轮只记最后一份，`turn/completed` 时写 `diffs/turn-<id>.patch` 并记 `diff` 标记；`item/fileChange/patchUpdated` 不进日志。
- 超过 32 MiB、按块写的行不瘦身。
- 上限：
  - 每个运行 256 MiB（`maxRunBlobs`），从 `blobs/` 已有的大小算起；超了新的字段不再存，换成 `{"$omit":"<字段>","bytes":n,"lines":k,"cap":true}`，只剩统计。
  - 每个节点 2 GiB（`maxNodeBlobs`）：`run.list` 每 10 分钟最多一次 `TrimBlobs`，超了从最早创建的已结束（或 unknown）运行删起：先写 `blobs.gone`，再删 `blobs/`，删到不超为止；还在跑的运行不删。
  - `tend doctor` 报 blob 的总量、分布在几个运行里和两个上限。

## 改动

一次运行改了什么，按文件列（`internal/node/changes.go`、`trees.go`、`touched.go`）。

- **碰过的文件**：监督进程在瘦身时顺带记 `touched.jsonl`：
  - `edit`：claude 带 `filePath` 的工具结果、codex `fileChange` 的各个 `path`（和 `move_path`）；
  - `cmd`：claude `Bash` 的 `command`、codex `commandExecution` 在 `item/started` 时的 `command`（按它的 `cwd`）。按 POSIX 规则拆词，跳过程序名（开头和 `&&` `||` `|` `;` 之后的词）、`-` 开头的参数和带通配符的词，`a=b` 取 `b`，只留运行目录下的路径，一条命令最多 100 个。只按字面认，不看文件在不在，所以命令生成的文件只要命令里写了路径也认得出。
  - 不在 git 里时，每个文件第一次被改时记下改之前的内容：claude 取 `originalFile`（`type: create` 或 `null` 是新文件），codex 在 `item/started` 时读盘；存成 blob，`base` 是它的 sha256。存不下（超上限）或者没有原文（codex 只见到 `item/completed`）记 `lost`。
- **在 git 里**：
  - 运行开始时（工作树准备好、agent 启动之前）：`rev-parse --git-common-dir` 和 `--show-prefix` 定仓库和运行目录在里面的位置；把用户的索引（`rev-parse --git-path index`）复制成 `base.index`，用 `GIT_INDEX_FILE=base.index GIT_OPTIONAL_LOCKS=0` 在运行目录下 `git add -A -- .`、`git write-tree`，就是起点树：未跟踪的文件在里面，忽略的不在；用户的索引和工作区只读不写。挂 `refs/tend/runs/<run>/base`，写 `trees.json`。
  - 结束时（agent 退出、提交留下的改动、check 之后，settle 之前）：同样从 `base.index` 复制出 `end.index` 取终点树，挂 `…/end`，把改动写进 `changes.json`。之后工作树被续接复用、只读副本被删，都不影响结果。
  - 比较：`diff-tree -r -M --raw` 和 `--numstat`（限在 `prefix` 下），大小用 `cat-file --batch-check`，`generated` 是文件名规则（`go.sum`、各种 lock 文件、`*.min.js`、`*.pb.go`、`vendor/`、`dist/`……）加 `check-attr linguist-generated`；`big` 是改动超过 400 行或文件超过 256 KiB；`agent` 是 `touched.jsonl` 里 edit 过的，或者在某个 cmd 路径之下的。
  - 运行目录清理（`forget`）时两个 ref 一起删。
- **不在 git 里**：每个 edit 过的文件，从 `base` 到文件现在的内容，用节点里的行 diff（Myers，`lineDiff`；超过 2000 处改动的那一段整块算删掉再加上）数 `+a −d`；结束时文件现在的内容存成 blob（`end`）。命令改的文件不在里面。含 NUL 或不是 UTF-8 的文件算二进制。
- **运行中**：节点现取：git 里用 `live.index`（从 `base.index` 复制）写当前的树，不在 git 里读盘；同一个运行 2 s 内复用上一次的结果。`snapshot` 是这棵树（不在 git 里是各文件内容的哈希）。结束后只读 `changes.json`，`snapshot` 是终点树。
- **方法**（方法名进 `hello.methods`；`run.start` 的参数没变，没有节点 feature；旧节点起的运行没有 `trees.json`，回 `gone`）：
  - `run.changes{run, after?, snapshot?, all?}` → `{files: [{path, op add|modify|delete|rename, from?, add, del, bytes, old_bytes?, binary?, generated?, big?, agent?}], total: {files, add, del}, snapshot, git, hidden?, next?}`：按路径排序，`after` 之后的 500 个，`next` 是这一页最后一个路径；`path` 相对运行目录。
  - `run.diff{run, path, snapshot?, hunk?, line?, n?, context?, all?}` → `{hunks: [{at, lines}], of, next?: {hunk, line?}}`：从第 `hunk` 处（这一处从第 `line` 行）起最多 `n`（默认 20）处、256 KiB；一处放不下就在这一处里按行续；`context` 默认 3、最多 10000。git 里是 `git diff --no-ext-diff --no-textconv -M` 两棵树之间这个文件；二进制没有 hunk。
  - `run.blob{run, sha, off?, n?}` → `{text, off, size, next?}`：这个运行 `blobs/` 里的 blob，每页最多 256 KiB，只在字符边界切。
  - `snapshot` 和现在的不一样 → `snapshot_changed`；`path` 不在这个人能看到的列表里 → `not_found`；blob 已清理（`blobs.gone`）、不在 git 里的 `lost` 文件、或者没有起点 → `gone`。
  - `all`：看的人是机器主人。目录运行（没有 `Workspace`）不带 `all` 时只列 `agent` 的文件，其余只给个数 `hidden`；工作树运行全列。`all` 由协调器按看的人填，节点照信。

## 观察

- 节点连接存活期间，每 3 s 看本机未结束的 run：监督进程锁、`state.json` 的 `rev`；有变化推 `node.changed`。
- 「需要人」用 `attention`（见 [coordinator.md](coordinator.md)「状态」）表达，来源：run 自己的输出、报告、沉默时长，herdr 方式另有 Herdr 的 pane 状态、transcript 和 Claude hook 事件（见上文「监督进程 `tend _run <dir>`」）。

## 会话绑定

| provider | 方式 |
|---|---|
| claude | 启动时 `--session-id <uuid>` 预设；续跑用 `--resume <sid>`，会话 id 不变 |
| codex | background 跑 `app-server`：`thread/start` 回的 thread id 写进 `state.json`；续跑用 `thread/resume`，spec 里已带会话 id |
| fake | 预设 id，写 Claude 形状的 transcript（在节点的 `CLAUDE_CONFIG_DIR` 下，端到端一律用 fixture 启动器，不碰真实目录） |
| command | 不绑定，只有 `output.log` |

- 索引对「run 目录里登记过的会话 id」不置 Skip（`claude -p` 的 `sdk-cli`、`codex exec` 平时会被跳过）；这些会话在会话视图里带 run 标记。
- 运行守卫：`PlanResume` 除了本机 live 表，还看这个会话有没有未结束的 run（没有终态，且监督进程或 agent 进程还活着）；有就不给「直接恢复」。

## 双向流（stream）

- background 方式下 claude、codex、fake 的 run 是 stream run（`spec.stream`）：监督进程持有 agent 的 stdin（管道），`prompt.md` 不作 stdin 文件。协议按 provider 分：
  - claude：`-p --input-format stream-json --output-format stream-json --verbose --permission-prompt-tool stdio --replay-user-messages`（不禁 `AskUserQuestion`）。启动后发 `initialize` 控制请求，再把 `prompt.md` 作为第一条 user 消息；每条 user 消息带 `uuid`，由 run id 和 send id（任务书是 `brief`）经 SHA-256 定出，同一个 id 总是同一个 uuid；`control_request{can_use_tool}` 成为请求（`AskUserQuestion` 是 question，其余是 permission，摘要取 `command` / `file_path` / `url` 等字段），回答写 `control_response`（允许带 `updatedInput`，问题的回答放进 `updatedInput.answers`；拒绝带 `message`）；其它控制请求回 error。消息是 user 消息，claude 在当前这一轮读到。
  - codex：`codex app-server -c approval_policy=on-request [-c model=… -c sandbox_mode=…]`（值不加引号：`codex.cmd` 拒绝引号），JSON-RPC 2.0。`initialize` → `initialized` → `thread/start{cwd, approvalPolicy}`（续跑用 `thread/resume{threadId}`），thread id 即会话 → `turn/start` 带任务书。`turn/start`、`turn/steer` 都带 `clientUserMessageId`：send id，任务书是 `brief`。`item/commandExecution/requestApproval`、`item/fileChange/requestApproval`、`item/permissions/requestApproval` 成为 permission，`item/tool/requestUserInput` 成为 question，其它服务端请求回 error；回答是 `decision accept|decline`、授予所请求的权限、或按问题 id 给 `answers`。消息在一轮进行中发 `turn/steer{expectedTurnId}`，太晚（报错）或在两轮之间就 `turn/start`。用量取 `thread/tokenUsage/updated` 的 total，最后一句话和最终消息取 `item/completed` 的 agentMessage。
- 请求进 `state.requests`，同时标 attention：有问题 → `asked`（`ask` 是第一个问题），否则 `permission`；请求都答完就清掉自己标的。等请求时不算沉默（回答或送出消息后沉默计时重置）。
- 一轮结束（claude `result`、codex `turn/completed`）后：从没送过消息就立即关 stdin，agent 随之退出；送过消息则等 2 s，期间没有新输出、也没有新消息才关（新消息会开下一轮）。
- 停止：先中断当前这一轮（claude `interrupt` 控制请求，每次一个新的 `request_id` `stop_<hex>`；codex `turn/interrupt`）并关 stdin，3 s 后还没退出再结束进程树，之后照常 10 s 强杀。
- 打断 `run.interrupt{run, turn, id?}`：节点把 `{id, turn}` 追加进 `interrupts.jsonl`（没给 `id` 就生成 `int_<hex>`，回 `{id, …快照}`；同一个 id 已在文件里 → 直接回）。监督进程读到时，只有 `turn` 就是 `rolling` 数到的当前这一轮、这一轮还没收尾、stdin 还开着，才发打断（claude `interrupt` 控制请求，`request_id` 就是这个 id；codex `turn/interrupt`），记 `interrupt{id, n}` 标记，再关 stdin；否则什么都不做，重试和迟到的打断不会打到后来的一轮。之后和停止一样：agent 收尾退出，3 s 后还没退出再结束进程树；run 记为 `stopped{interrupted}`，不跑 check，也不提交留下的改动。节点：不是 stream run → `conflict cannot_interrupt`；run 已结束 → 直接回快照（它的轮次都已结束）。
- 消息从 `inbox.jsonl` 取出时在 `state.sends` 里是 queued，写 goroutine 真的写进管道后才改成 sent（同时算作开了下一轮、沉默计时重置），写失败（agent 已退出）改成 failed；一轮结束后还有消息在排队时不关 stdin。
- agent 已接收（`seen`）：claude 回放 `isReplay:true` 的 user 消息，按 `uuid` 对上 send；codex 的 userMessage `item/started` 按 `item.clientId` 对上。第一次对上时 send 改成 `seen`，在这一行的开头记 `input{id}` 标记；再出现就忽略（claude 连发几条时各回放一次，最后一条的回放里是拼起来的全文，所以文字以 journal 为准）。回放行照旧进 `output.log`；协调器按它的 `uuid` / `clientId` 对上 journal 的 send，把它显示成 `you`（见 [output.md](output.md)「标记和 journal」），`input` 标记只表示位置和 `seen`。对不上的（进程被杀、崩溃）停在 sent。一轮结束时还有 sent 的消息，stdin 最多多开 30 s 等它（claude 在纯文字回复中收到的消息要等 `result` 之后另起一轮）。
- codex 的 `serverRequest/resolved{requestId}` 记 `resolved{rpc-<id>}`；这个请求还在等回答（codex 自己撤回了它）就从 `state.requests` 里去掉。
- 结束时：关 stdin，等排队的写完，最多 2 s，还没写进去的（agent 留下的子进程拿着 stdin 不读）记 failed；还没送出的消息记 failed，没回答的请求清掉；用户拒绝过的工具不算「等你批准」（只有权限模式没问就拒的才算）。
- 节点 feature：`input_marks`（`input`、`resolved` 标记和 `seen`）、`interrupt`（`run.interrupt`，方法名同时进 `hello.methods`）、`answer_scope`（`run.answer` 的 `decision`）。
- 回答的 `decision`：`allow`（这一次）、`allow_run`（这次运行里这条命令都允许）、`deny`；`allow` 跟着 `decision` 定，不认识 `decision` 的一端照 `allow` 读，`allow_run` 在那里就是允许一次；别的值 → `bad_request decision`。`allow_run` 只对 `requests[].allow_run` 的请求生效，其余照允许一次：
  - claude：请求的 `permission_suggestions` 里有 `addRules`（`behavior: allow`）才算。回答的 `updatedPermissions` 只有一条 `{type: addRules, rules, behavior: allow, destination: session}`，`rules` 只取 `toolName`、`ruleContent`（Bash 是这一条命令）；`destination` 写死 `session`，建议里的 `localSettings` 会把规则写进仓库的 `.claude/settings.local.json`；`setMode`、`addDirectories` 一律不转发。规则只活在这个进程里，续接以后就没了。
  - codex：`item/commandExecution/requestApproval`、`item/fileChange/requestApproval` 回 `acceptForSession`（不用长期写进 execpolicy 的 `acceptWithExecpolicyAmendment`）；`item/permissions/requestApproval` 仍只授予这一轮。
- 回答写进 stdin 之后请求才从 `state.requests` 去掉；没写进去（输入已关、排队已满、写失败）就留着，标 `failed`，可以再答一次。
- 实际能力 `state.caps{steer, after, interrupt, answer_scope, questions, continue, takeover}`：agent 启动时写一次。stream run 有 `steer`、`interrupt`、`answer_scope`、`questions`，provider 能续会话时还有 `after`；`continue` 是 provider 能续会话、而且不是 herdr 方式；`takeover` 是 provider 能在终端里续（claude、codex）。协调器照搬到 `Run.caps`。
- 正在做 `state.doing`：`rolling` 数轮次时，每个工具事件（`tool`、`cmd`、`edit`、`mcp`）的 `title` 记成 `doing`，这一轮的 result 清空，run 结束也清空；和 `last` 一起每秒最多写一次 state，随 `run_observed` 进 `Run.doing`。`state.turn` 是 `rolling` 数到的轮次，随它写，进 `Run.turn`；`run.interrupt` 按它点名一轮。
- 节点 `run.answer`：请求不在 `state.requests` 里 → `conflict request_gone`；同一请求已在 `answers.jsonl` 里、而且没标 `failed` → 直接回快照。`run.send`：不是 stream run 或已不在 starting / running → `conflict cannot_send`；同 id 已有 → 直接回快照。快照的 `sends` = `state.sends` 加上 `inbox.jsonl` 里监督进程还没取的（queued；run 已结束或 unknown 时算 failed）。
- 最后一句话（`last`，500 字节内）和用量（`usage{input, cache_read, cache_write, output, cost_usd, turns}`）：claude 取 assistant 文本和每个 `result` 的 usage 累加、`total_cost_usd` 取最新；codex exec 取 `turn.completed` 的 usage；普通命令行取最后一行。每秒最多写一次 state。

## 读输出

- `run.tail{run, before, max, file?, clip?}` 从 `before`（`-1` 是末尾）往前读最多 `max`（默认 64 KiB，最多 1 MiB）。`file` 是日志的 `fileio.ID`：当前这一代，或者还留着的 `.1`（按打开的句柄核对 ID，打开前后正好轮转也认得出）；都不是回 `stale`。回 `{from, file, done, prev?, turn?}`：`done` 是读到了这一代的开头，这时 `prev` 给出 `.1` 的 ID；`turn` 是 `from` 处的轮次（`{turn, closed}`，照 `marks.jsonl` 算，标记里没有这一代就不给）。
  - 不带 `clip` 回 `text`：从第一个换行之后起的原文。
  - 带 `clip` 回 `lines[]{off, text, size?, head?}`，每行照上面「送出的行」处理。页从一行的开头切：第一行被切开时，这一行超过 1 MiB 就往前找到它的开头，把它作为 `head` 放在第一行，`from` 是它的开头；否则丢掉这一段。最后一行还没写完、又超过 16 KiB 时不送。
  - 带 `clip` 时另回 `marks`：这一代日志里偏移落在这一页的标记（页的末尾正好是文件末尾时，末尾那个偏移上的也算），每条带 `pos`，即它在 `marks.jsonl` 里的字节位置；`marks_to` 是这次读到 `marks.jsonl` 的哪里，从这一页的末尾跟随时，标记从这里接着读。
- `run.follow.watch{run, from{file, off, marks}}` 是流（见 [wire.md](wire.md)「流」），协调器的输出 hub 用它（见 [output.md](output.md)「实时」）：
  - 每 200 ms 按路径打开 `output.log`，定位到 `off`，读到末尾，关掉，不一直开着文件，Windows 上轮转不受影响；`marks.jsonl` 同样从 `marks` 这个字节位置接着读。第一条推送是 `open{cursor, mode}`；之后每条 `run.follow{file, lines[], part?, marks[], gap?, cursor}` 只含一代日志，行照 `run.tail{clip}` 截断，每行带 `at`（跟随读到它的时间），一条大约 256 KiB。
  - 只推完整的行，游标只跟着整行走。最后一行等了 1 秒还没写完（运行结束时不等），又不超过 16 KiB，就作为 `part` 推出去，不推进游标；内容没变不再推。
  - 每一拍先读 `partial.json`，再读日志；内容和上次推的不一样，就在这一拍的行之后推一条 `run.follow{partial: {items}}`，文件没了推空的 `items`。比较的是字节，不是 `fileio.ID`：整份替换后 inode 号会被复用。打不开或读到的不是 JSON（Windows 上正赶上改名）就当没变，下一拍再读。
  - 轮转：`from.file` 是 `.1` 的 ID，或者读的过程中 `fileio.ID` 变了，就把 `.1` 从游标读完，再从新文件的 0 开始；文件比游标短（同一 ID）或者两代都对不上，推 `gap{from, to}`，从新文件的 0 开始。打开时 `from.file` 已经两代都不是，第一条推 `open{mode: gap}`，从还留着的最早一代的开头读。
  - `from` 带 `file` 或 `off` 而 `marks` 为 0 时，第一次读标记只留偏移在 `from` 之后的（这一代从 `off` 起，之后各代全部）。
  - 运行结束（`Terminal` 或 `unknown`）以后再读一遍，然后回 `done`；run 目录没了回 `gone`。推送用 `PushWait`：协调器读得慢，跟随就停下等，数据在文件里，不丢。
- `run.line{run, file?, off, max?}` 从 `off`（一行的开头）读这一行，最多 `max`（默认也最多 1 MiB）字节；回 `{text, size, file}`，`size` 是整行的长度（含换行）。`run.tail` 在 `from > 0` 时会丢掉第一个换行之前的内容，取不出从某处开始的一整行，所以另有这个方法。
- `run.output.find{run, q, file?, before, limit?}` 在一次运行的输出里找文字，只找看得到的：页送出去的事件里，Web UI 查找搜的那些字段（`text`；`title`，没有时用 `tool`；`output`；`diff`；`input.command`；`ask` 的问题；`plan` 的步骤），一个事件的这些字段用换行连起来再找。长输出截掉的中间、超过 16 KiB 被截掉的字符串、移进 blob 的内容、临时事件和 `marks.jsonl` 都不找。
  - 找的范围：`file`（默认当前这一代）里 `before`（`-1` 是末尾，要是一行的开头）之前的整行；`file` 是当前这一代时接着找 `.1`。`file` 不在了回 `stale`；`q` 去掉首尾空白后为空或超过 1 KiB 回 `bad_request`。
  - 做法：逐行读，一行最多留 1 MiB；先在字节上预筛，这一行按 ASCII 折叠大小写后含 `q` 的原文、JSON 转义（不转义 `<>&`）或 `json.Marshal` 转义（`<>&` 写成 `\u003c`，节点瘦身重编的行是这样）三种形式之一才往下；命中的行照「送出的行」剪裁，用 `internal/output` 解析，再在上面的字段里核对。所以查 `tool` 不会命中 `"type":"tool_use"`，带引号、换行的文字也找得到。大小写只折叠 ASCII，按子串匹配。
  - 回 `{hits: [{id, kind, ref?, turn, at?, excerpt}], file, next?: {file, before}}`：离 `before` 最近的 `limit`（默认 50，最多 200）条，按日志先后排；同一行的命中不拆开，所以可以多出几条。`ref` 是 `tool_result` 对应的调用；`turn` 照 `marks.jsonl` 算；`excerpt` 是第一处命中前后约 160 个字符，截断处带 `…`。`next` 是接着往前找的起点，没有就是找完了；当前这一代找满了而 `.1` 还没找时是 `{当前这一代, 0}`。
  - 一个节点同时跑两个，多的排队，调用方取消就不再等；扫描中每读 1 MiB 看一次取消。两代日志最多约 2 × 16 MiB，不设扫描上限。
