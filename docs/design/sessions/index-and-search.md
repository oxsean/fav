# 索引与查找

本机全部会话的增量索引、查找流程、最新动态与对话的分页读取、查询语法、搜消息（正文库、匹配与 BM25 排序）。实现：`internal/index`（索引、`Rows.List`）、`internal/tend`（`Parse`、`Query`）、`internal/fulltext`（搜消息）、`internal/capture`（`Messages`、`ReadPulse`）、TUI 侧的 `internal/ui/tui/msgsearch.go`。

## 查找

1. 用户运行 `tend` 或 `tend fzf`。
2. **默认展示未归档的收藏**，按**会话创建时间**倒序（`session_started_at`，老记录退到 `favorited_at`）——
   收藏常在会话开始很久之后，按收藏时间排会和实际做事的先后对不上。时间一律显示完整年月日时分。归档的通过 `status:archived` 查看。
3. 用户输入关键词或 `#tag`，或通过筛选器限定项目、时间、状态、Provider。
4. 光标移动时右侧实时显示预览。
5. 记得说过的话、不记得标题：搜索框以 `>` 开头搜消息正文（见下文「搜消息（`>` 前缀）」），结果按相关度列会话，右栏落在得分最高的那条（卡片片段那条），`n`/`N` 跳转，关键词高亮。

## 全部会话与索引

tend 管的是本机**全部**会话，收藏只是其中打了 ★ 的一部分。
`internal/index` 扫 Claude `~/.claude/projects/*/*.jsonl` 和 Codex `sessions/YYYY/MM/DD/rollout-*.jsonl` 及 `archived_sessions/rollout-*.jsonl`（`capture.CodexRollouts`；Codex 已归档的照常进索引，`Rec.CodexArchived` 不落盘，卡片元信息多「Codex 已归档」，恢复检查提示先 `codex unarchive <id>`；归档 / 取消归档挪走文件后，`Attach` 发现收藏记录的 `transcript_path` 不在了就改指索引里的新路径），
每个文件记：session id、cwd、分支、开始时间、人说话的轮数、工具自己起的标题、第一条提示语、全部提示语（封顶 8KB，只用来搜）、最新的回顾（Claude 的 `system/away_summary`，去掉结尾的「(disable recaps in /config)」；Codex 最后一个 `task_complete` 的 `last_agent_message` 第一段；封顶 300 字）。索引行的扫描逻辑一变就升 `scanVer`（`internal/index/index.go`），版本不同的行整份重扫。

- **摘要**：收藏的用 `/tend` 写的摘要；没收藏的有回顾就用回顾（标「Claude 自动回顾」/「Codex 最后一轮的总结」），没有才用第一条提示语。回顾也进搜索。
- **增量**：transcript 是 append-only 的，索引记「已读到的偏移」，下次只补读新增部分。缓存在
  `~/.agent/tend/sessions.jsonl`（一行一个文件，后写覆盖；旧行的字节数超过现行各行时整个重写，否则在跑的会话反复追加大行会让它膨胀）。首次全量建索引读几个 GB 要几秒，在 TUI 后台跑；
  之后每 10 秒对照一次磁盘，稳态只是一千多次 stat。文件被清理了条目就丢。
- **标题**：收藏的 title > Claude `/rename` 写的 `custom-title` > Claude 自动的 `ai-title` / Codex `session_index.jsonl` 的 `thread_name`
  > 第一条 ≥12 字的提示语（「继续」「ok」不配当标题）。
- **不列**：Claude `entrypoint != cli` 的 -p/SDK 会话；Codex 由 `Claude Code` / `codex_exec` / `multica-agent-sdk` 发起的、以及带 `parent_thread_id` 的子代理线程（`capture.CodexOneOff`）；
  工具自己的临时目录里跑的会话（系统临时目录下、路径里有 `claude-…` 一段：Claude Code 的 scratchpad、评审用的 `claude-review-…`；`index.AgentScratch`，收藏过的照列）；这些都在 `status:agent` 里；
  一句话没说过的。注入的内容（`<` 开头、`isMeta`、「This session is being continued」等）不算人说的话，
  `<pasted_content>` 壳里的算。
- **worktree**：会话跑在 git linked worktree 里时 `Rec.Repo` = 主仓库（不落盘），未收藏会话的项目名取主仓库目录名，卡片元信息多一段 `worktree <分支>`，详情多「主仓库」一行。来源依次是：Claude `worktree-state` 行的 `originalCwd`（`File.WtRepo`，`worktreeSession` 为 null 时清空）；路径里有 `/.claude/worktrees/` 的取前面那段；目录还在时 `Rescan` 在后台问 git（`rev-parse --git-common-dir --show-toplevel`，common dir 不在自己的 top level 下就是 worktree；主仓库顺带记 origin），每个目录只问一次，存 `~/.agent/tend/worktrees.json`，worktree 删了也还认得；Codex 桌面版的 `<codex home>/worktrees/<id>/<仓库名>` 删了且没记过的，按 `session_meta.git.repository_url`（`File.Remote`）和仓库名对上记过的主仓库。删掉的 worktree：`FindMissing` 的去向就是主仓库（`Missing.Repo`），`tend fix` 标「worktree 已删」（`--json` 带 `worktree_of`），恢复框修复行的按钮写「M 移到主仓库」，检查项提示同样的去处。
- **改过的文件**：`File.Files` 记每个路径被 AI 写了几次（Claude `Edit` / `Write` / `MultiEdit` 的 `file_path`、`NotebookEdit` 的 `notebook_path`；Codex `custom_tool_call` `apply_patch` 的 `*** Add/Update/Delete File:` / `*** Move to:`，相对路径按会话 cwd 转绝对；工具临时目录里的不算，同 `AgentScratch`），每个文件最多 200 个路径，扫描时先按字节预筛（`isEdit`），同一会话多个文件相加成 `Rec.Files`（不落盘）。详情和 fzf 预览多一行「改过的文件：N 个 · 写得最多的 6 个」（目录下的写相对路径，`×n` 是次数），项目信息多「本周常改」（组里最近 7 天动过的会话，前 5 个）。
- **短会话**：默认只列 ≥3 轮（`turns:` 限定词可改，`turns:1` 全列）；收藏的不受此限。
- Codex 一个会话续接一次多一个 rollout 文件，按 session id 合并：轮数相加、最早的文件当正本。
- Claude Code 上下文用完时把对话交给后台工作进程（`claude bg-spare`，新 session id），原进程 `parkedJobId` 指向新 id、transcript 末尾写一行 `{"type":"continued-in","continuedInSessionId":…}`。索引记下 `ContinuedIn`，`Sessions()` 把链折成**最新 id** 一条（`Aliases` 存旧 id；轮数相加、transcript 用最新文件，只有最新 id 能 `--resume`）；`Attach` 按旧 id 也能找到收藏记录并把它的 `session_id` / `transcript_path` 挪到最新 id（下次写盘落地）。`ClaudeLive` 跳过带 `parkedJobId` 的进程。
- Herdr 只在窗格状态变化时重读 Claude 的 session id（Fleet 里临时打开再关掉的会话会留一个旧 id 挂几分钟），所以 `HerdrLive` 丢掉 `sessions/*.json` 里没有的 Claude id（目录不存在时不过滤）。
- 三个标签页：「收藏」= 只看库里的（默认），「会话」= 全部按日期，「项目」= 全部按项目折叠。没收藏的会话
  `f` 落库（标题沿用索引里的，resume 进去再 `/tend` 补摘要）；Enter（和 `r`）对任何会话都只打开恢复确认框，框里 Enter 才恢复。第四个标签页「Agents」见 [tui.md](tui.md)「跑着的会话（Herdr）」。
- `tend sessions [表达式] [--json]` 是同一份数据的 CLI 出口。

## 最新动态与对话

摘要是收藏那一刻写的，会话之后可能又跑了两小时。详情和预览里加一行「最后活动」：
文件 mtime + 最后一条用户消息（只倒读文件尾 512KB，按 mtime 缓存）。

TUI 详情面板下方是「对话」：光标落到记录上先只读文件尾 1MB 里最近 40 句（滚动零成本）。
往前翻是**从文件尾向前分块读**（`capture.Messages(path, before, n)`）：每次从上一页最早那句的偏移再往前读
1MB，块边界总落在行尾所以句子不会切坏，凑够 20 句就停；快翻到已读末尾（剩 10 句）或面板没填满时提前补一页，
每页几毫秒，翻到那儿早就在了；不翻就不读，147MB 的会话看看最近几十句就走时整个文件不会被读。读到文件头
才算「全文」。每句正文最多留 16KB，字数（`Message.Chars`）解析时数好；工具往返只记文件偏移，看全文时按偏移回读；
超过 1MB 的行整行跳过。展开过的同时只留一份，换到别的记录再翻时上一份退回尾 40 句并记住偏移。
`\`（`Ctrl+S`）在本会话里找不读整个会话：输入后 Enter 走正文库（`openHits`），把本会话的命中列进左栏，
`n`/`N` 前后跳，命中高亮，Esc 清掉；查询跨记录保留，换一条记录按它重新算命中；正在跑的会话里正文库还没读到的新消息（`fulltext.Covered` 之后）用右栏已加载的消息补上（见下文「搜消息（`>` 前缀）」）。

Claude Code 默认 30 天清理 transcript（`cleanupPeriodDays`），压缩（/compact）不删文件内容。
不想丢就在 `~/.claude/settings.json` 里把它调大，或 `tend pin` 硬链。

## 查询语法

所有 UI 共用同一解析器（`internal/tend.Parse`）。

```text
#notes-api #debug websocket
project:notes-api provider:claude status:active after:2026-09-01 cursor drift
last:7d status:archived
```

语义：

- 多个标签默认为 AND；普通关键词也是 AND。
- 关键词搜 title、summary、project、work_type、branch、remote、cwd、tags，中文直接子串匹配，不分词。
- `project:x`：会话归到项目时，比项目 id 或项目名（不分大小写）；不在项目里的比自动组名（`Rec.Project`）。项目名也参与关键词匹配。
- 限定词：`project:` `provider:`（别名 `source:`）`status:` `after:`/`since:` `before:`/`until:` `last:` `turns:` `file:`。
- `file:x`：AI 在这个会话里写过路径含 `x` 的文件（不分大小写的子串，匹配 `Rec.Files` 的绝对路径）。
- 时间：`after:` / `before:` 按会话开始时间；`last:7d`、`last:2026-09-01` 按最近活动（上周开始、今天还在用的会话也算，`Rec.ActiveAt`）。时间筛选器里开放区间（今天、最近 N 天、本周、本月、今年、手输 `09-01`）写 `last:`，封闭区间（昨天、上周、上月、`a..b`）写 `after:` + `before:`，chip 上 `last:` 显示为「动过 · 自 …」。
- `turns:N`：没收藏的会话至少 N 轮才列，默认 3；收藏的不受它管。索引里的提示语也参与关键词匹配。
- `status:` 不写 = `open`（未归档，任意看板状态）；`active`（todo + doing）/ `todo` / `doing` / `done`（别名 `completed`）/ `archived`（已归档，任意看板状态）/ `live`（别名 `running`，正在跑的）/ `trash`（别名 `deleted`，回收站：库里没有这种记录，从 `trash/manifest.jsonl` 另取）/ `agent`（别名 `sdk`，SDK / `-p` / 子代理拉起的一次性会话：索引里打了 `skip`，其余任何列表都不出现，只有写了这个才列出来，`Index.AgentSessions()` 另取；标题多半没有，兜底成「目录名: 首条消息」；不受 `turns:` 下限约束；放在状态里而不是来源轮换里，因为 Claude 和 Codex 都有这类会话，放进来源就没法说「Codex 的 agent 会话」，不进默认列表是因为一天能有几十条）/ `all`。除 `archived` / `all` 外都不含已归档。未收藏的记录只在 `All` 作用域（`tend sessions`、会话 / 项目视图）里出现。
  `live` 要调用方给 `Query.Live`（谁在跑），TUI 用探测结果，CLI 问一次 Herdr；没给就一条都不匹配。
- 所有列表（TUI 各页、`tend list` / `sessions`、fzf 三页、`tend clean`、fzf 的标签 / 项目选择器）都从 `index.Rows.List` 取。匹配之前，每一行先经 `Rows.Belong` 填上 `Rec.ProjectID` / `ProjectName`（不落盘）：调用方注入这个函数（`projects.Snapshot.Belong`，内部调 `task.ProjectOf`，参数是机器名、它的系统、`Repo` 否则 `Cwd`），`internal/index` 不依赖 `internal/task`；别处列出的行（其他机器的）走 `Rows.Place`。来源：库里的记录，`All` 时加上索引里没收藏的，`live` 再加上索引还没见过的在跑会话；`trash` / `agent` 不看 `All`，各取各的来源。排序：会话列表按 `Rec.ActiveAt`（索引看到的最后活动，没有就退到 `When()`），收藏列表按 `When()`，Agents 页默认 `tend.SortByStart`（没有时间的算最新）。
- 作用域另有 `Query.All`：默认只看收藏（`tend list`、「收藏」页）；「会话」「项目」页和 `tend sessions` 置 true，库里不算收藏的记录也一起看。
- 时间接受 `2026-09-01` / `09-01`（今年）和相对量 `24h` `7d` `2w` `3m` `1y`；`after:` / `before:` 比的是会话开始时间，`last:` 比的是最近活动（见上）。
- 无法识别的限定词按普通文本处理，并记录下来供 UI 轻提示。
- 其它机器的筛选词 `host:` / `machine:` 见 [remote.md](remote.md)「功能」。

### 搜消息（`>` 前缀）

查询以 `>` 或 `》` 开头（`fulltext.Prefixed`）切到搜消息：TUI 搜索框（Agents 页除外）、`tend fzf-list`（收藏 / 会话两页）、`tend grep`。

- 拆分（`fulltext.Split`）：`tend.Parse` 认作普通词的 token 是关键词，其余（`project:` `#tag` `last:` `status:` …）组成范围查询；范围没写 `status:` 补 `status:all`、没写 `turns:` 补 `turns:0`——搜消息默认不挑状态、不嫌短。
- 正文库 `~/.agent/tend/text/`：每个 transcript 一个 `<sha1(path)[:8] hex>.tsv`，一行一条 `偏移\t角色\tunix 秒\t文本`（角色 u / a / t / s，s 是 Claude 上下文用完时写的续接摘要，记录里带 `isCompactSummary`（Codex 的上下文压缩摘要是单独一种记录，不当消息抽取，不进正文库，也就不用降权）；t 是工具调用的名字 + 输入首行，封顶 300 字节，偏移取它所属消息的；超过 256KB 的行整行不读，每条正文封顶 16KB）；存说的话、工具输入，以及每次工具输出的前 N 行（角色 o，非空行、每行封顶 300 字节、用 ` | ` 连成一条；N 由设置「工具输出」定，可选不搜 / 3 / 10 / 30 行，默认 3；`config.tool_output_lines`；state 里记着写入时的 N，变了就整体重建）。`vocab.json` 记正文库里出现过的英文词（4–40 个字母）和次数，只增不减，随正文库更新，重建时清零。`state.json` 记每个源文件读到的偏移、Codex 当前消息的归属偏移、text 文件大小、读时的源文件大小和 mtime、源文件头 4KB 和读到处之前 256 字节的指纹。收藏里 `tend pin` 过、原文件已被清掉的会话，硬链文件也建正文（`fulltext.Sources`），候选路径同样落到硬链上。
- 更新（`fulltext.Update`）：跟索引走——TUI 启动和每次索引变化后台跑一次，`tend grep` 前同步跑，`tend fzf-list` 每次按键给 300ms 预算，没建完就脱离进程组起一个隐藏的 `tend text-sync` 在后台建完（下次按键就是全量结果）。只读源文件大小变了的部分（`capture.Extract` 从上次偏移流式读，末尾半行留到下次）；源文件变小、头 / 尾指纹变了（`tend mv` 就地改写 cwd）、大小跟读时一样但 mtime 变了、或 text 文件丢了 / 比记录的短，都整份重建；单个文件读到一半预算到了就停在完整行上，下次接着读；先把 text 文件截到记录的大小再追加，崩溃后不会重复；索引里没有的源文件连同孤儿 text 文件删掉；每 2 秒存一次 state。`text/.lock` 非阻塞 flock（Windows 用 `LockFileEx`），拿不到就是 `ErrBusy`，这次不更新、照常搜。索引为空时不调（否则会删光）。
- 匹配：不分词。中文 / 日文 / 韩文连续段切相邻两字（单字段保留单字），ASCII 取 2 字符以上的词，只折叠 ASCII 大小写。一个关键词的词项命中 ≥60%（向上取整）算这条消息命中它；用引号包起来的关键词（`"…"`、`“…”`、`「…」`，里面可以有空格，没闭合就到结尾）必须原样连着出现（小写后子串），高亮也只标整段，`Split` 保留引号；`a|b` 是一个关键词的几个备选（有一个命中就算，高亮全标）；`-x` 排除：含 x 的消息不算命中（按消息算，不是整个会话）；`who:me`（用户）、`who:ai`（AI 和续接摘要）、`who:tool`（工具调用和输出）只留某一方的消息，可叠加；这些在列表解析器里都是普通词，`Split` 自然归到关键词，由 `fulltext.ParseQuery` 解释；每个关键词都得在会话的某条消息里命中，会话才进结果（按同一条消息算太严，会漏掉词分散在几条消息里的会话；按多数词算太松，一两百个会话等于没筛）。不用分词词典：jieba 词典 10–50MB，技术词和中英混写常被切错；不做语义或向量检索：本地模型几百 MB，和 tend 的体量不配。
- 排序：每个会话只留 32 条最强的命中（按命中关键词数、加权词频）参与打分，内存不随命中总数涨；BM25（k1 1.2，b 0.75，df 按本次全部候选消息算），关键词原样出现的加成 `×(1+0.5·原样数/关键词数)`；一条消息命中两个以上关键词时按词距加成 `×(1+1/(1+最短覆盖字节数/60))`（最短覆盖 = 包住每个关键词各一次的最短一段，关键词位置取它任一词项出现处，每个词项最多看 32 处；挨着接近 ×2，隔 20 个汉字左右 ×1.5，越远越趋近 ×1）；每条再乘权重：谁说的（用户 ×1.2、AI ×1、工具命令 ×0.5、续接摘要和工具输出 ×0.3）× 新旧 `(1+0.5·2^(−天数/30))`（今天 ×1.5，一个月前 ×1.25）；会话分 = 最好的一条 × 标题/摘要/标签加成 `(1+0.3·其中出现的关键词数/关键词数)` + `0.2·ln(1+命中条数)`，卡片片段和右栏落点就是这最好的一条；同分按这条的时间新的在前。`o` 在「相关度」和「最近命中」（会话里最新一处命中的时间）之间切，只在本次运行有效；查询变了的新结果到达、或按 `o` 切换时，光标和滚动回到第一条；查询没变的重搜（正文库增量、候选变化）光标跟着原来那条会话不动；有一条消息同时含全部关键词的会话排前面。片段取最好那条，从第一个命中前 24 字起截 120 字。
- 拼写纠正（`fulltext.Expand`）：不带引号、不含 `|` 的关键词里，5 个字母以上的英文词如果在词表里出现不超过 2 次，就把词表里只差一处（多一个、少一个、错一个、相邻对调）且至少出现 5 次和它 5 倍以上的词（最多 3 个，常见的在前）作为备选加进来；靠纠正命中的消息权重 ×0.7，高亮也标纠正后的词；TUI 标题、命中列表标题、`tend grep` 第一行写「也搜了 …」。纠正后词项超过 64 个就按原样搜。
- 关键词超过 64 个或不同词项超过 64 个直接不搜，TUI 标题 / `tend grep` / fzf（一条无 key 的提示行）提示删词（不静默丢词）。
- 已知取舍：命中在大会话很靠前时，右栏往回翻页会把中间的消息都载入内存。
- 搜索并行流式扫候选文件（至多 8 个 worker，每 4096 行查一次取消），不建倒排（扫一遍约 0.05 秒；数据涨到约 10 倍时再在同一份正文上加二元倒排，查询规则不变）；可取消（TUI 150ms 防抖，改一个字就取消上一次）。
- 搜消息时搜索框里的 `Enter` 只结束输入（焦点回列表，不起恢复，哪怕方向键选过），这样 `n`/`N`/`→` 马上能用；标题在输入时写「Enter 开始看命中」，离开后写「n/N 下一处 · → 全部命中」；要改关键词按 `/` 或 `>` 回框里接着改。
- TUI：左栏变成排好序的会话（标题「消息 · N 个会话 · M 处」，正文库在建时追加进度），卡片第三行是命中数 + 高亮片段；右栏不显示摘要和字段，整栏给对话；右栏落在卡片片段那条消息（`Result.Path/Off` → `showOff` 按偏移往回翻页，找不到就停在最近的更早一条），长消息的预览从关键词所在行前一行开始（`… ` 开头）。会话上按 `→` 左栏换成本会话全部命中（`fulltext.Hits`，后台流式读该会话的 text 文件，每个文件只留最新 500 条（环形缓冲），卡片那一处无论多老都保留，标题写「最新 500 处（共 N 处）」；可取消，关掉或重开就停；新的在前；每条：时间 · 谁 · 关键词前后片段两行），`↑↓`/`n`/`N`/滚轮选、右栏跟着跳，`Enter` 或双击打开所选那一处的全文并滚到关键词（右栏还在翻页就等翻到再开；右栏看不到的旧文件命中直接用正文库里的文本开），`→` 进右栏（双向联动：右栏里移到哪条消息，左边选中跟到那一处，不是命中就跟到最近的一处，`←` 回来接着走），`←`/`Esc` 回会话列表；续接链里较早文件的命中标「右栏看不到」（同一文件的硬链路径——`tend pin` 过的会话——按同一文件算）。结果集合随候选会话变化重搜；关键词变了，新结果到了再落位。右栏查找词缺省取这些关键词（`findQuery`，`\` 自己的查找词优先），高亮、`n`/`N` 与对话查找共用一套（`fulltext.MatchAny` / `Spans`）。搜消息时右栏只往回翻页到出现命中为止（`findHit`，每次 200 条），`n` 走到已加载的最后一处再往回翻，不整读大会话。`\` 同样走正文库（`openHits(q)`），不整读会话；正文库还没读到的部分（正在跑的会话最近几秒，`fulltext.Covered` 之后）用右栏已加载的消息补上。命中列表开着时右栏也不显示摘要和字段。结果按 `provider:session` 记，别的进程改了收藏库、记录对象重建后照样对得上。
