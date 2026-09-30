# 会话：总览

会话这一半的目标、原则、范围、两种界面、目录与模块、安全与兼容、非功能指标和验收时逐条核的行为约束。实现：`cmd/tend` 与 `internal/` 下会话相关的包（模块表见下文「目录与模块」）。

tend 是 Claude Code、Codex CLI 与 Herdr 的统一 Session 收藏、检索与恢复入口。产品名和命令是 `tend`，收藏技能是 `/tend`，数据目录 `~/.agent/tend`（`$TEND_HOME` 可改）。这组文件管会话收藏、检索、恢复；任务、agent 档案、节点、协调器、两种部署见 [runs/overview.md](../runs/overview.md)。实现依赖的外部行为（Claude / Codex / Herdr 的文件与命令）见 [external-behaviour.md](external-behaviour.md)。

## 一句话目标

在当前 AI Session 上下文仍然完整时，通过 `/tend` 自动生成有意义的标题、摘要、标签及环境信息；之后通过 `tend` 的 FZF 轻量模式或原生高保真 TUI，快速找到历史 Session，并在正确的项目目录、Herdr workspace/tab 中恢复 Claude Code 或 Codex CLI。

## 背景与痛点

用户每天会创建十几个 Claude Code / Codex Session。一周后常见问题包括：

- 自动标题辨识度低，无法表达真正完成的工作。
- Session 对话很长，逐个打开确认成本高。
- 只记得"做过某件事"，不记得日期、项目、工具、目录或 Session ID。
- 重要 Session 淹没在历史记录中。
- 找到 Session 后，仍需手动找到项目目录、Herdr workspace，再执行恢复命令。
- Claude Code、Codex、Herdr 各自保存信息，缺少统一、长期、可迁移的个人索引。

本产品不是新的聊天客户端，而是 AI Coding Session 的个人索引和恢复入口。

## 产品原则

1. **Capture while fresh**：收藏时利用当前 AI 已掌握的完整上下文生成高质量语义信息。
2. **Local first**：索引和配置默认保存在本地 `~/.agent/tend/`，不上传完整会话内容。
3. **Thin skill, thick core**：Skill 只负责理解会话；数据校验、存储、搜索、状态与恢复均由 `tend` CLI Core 负责。
4. **Two UIs, one core**：FZF 和原生 TUI 只是两个前端，不复制业务逻辑。
5. **Preview before resume**：默认先预览，再恢复，避免进入错误 Session。
6. **Graceful degradation**：Herdr、fzf 或某个 Provider 不可用时，仍可浏览数据并给出可执行的降级方案。
7. **Non-destructive by default**：取消收藏、完成、归档不直接删除记录。
8. **不猜**：会话身份、路径、workspace 无法确定时报错并列出候选，不静默取"最可能的那个"。

## 范围

### 必须支持

- Claude Code 与 Codex CLI 的 Session 收藏与恢复。
- `/tend` Skill 自动生成标题、摘要、标签和项目名。
- 自动采集 cwd、Git repo/remote/branch、主机、Provider、Session ID 和 transcript 路径。
- 可选采集 Herdr workspace、tab。
- JSONL 本地存储。
- FZF 轻量交互模式与原生高保真 TUI 模式。
- 时间线与项目聚合视图。
- 关键词、标签、项目、Provider、状态、时间范围过滤。
- Session 预览、恢复、编辑、完成、取消收藏、归档。
- **transcript 失效检测**，以及 `tend pin` 用硬链保住重要会话。
- 在 Herdr 中向正确 workspace 新建 tab 并恢复。

### 暂不包含

- 托管或同步 Claude/Codex 的完整聊天内容（`tend pin` 只在本机硬链，不复制、不上传；搜消息用的正文副本只在本机 `text/`，工具输出只留开头几行，行数见设置 `tool_output_lines`）。
- 替代 Claude Code 或 Codex CLI 的聊天界面。
- 多用户协作、团队收藏、云同步。单用户多机在范围内：经自己的 SSH 只读聚合其它机器的会话已实现，见 [remote.md](remote.md)「聚合」；会话与记忆的手动迁移、迁移前的环境诊断未实现，见 [migration.md](migration.md)「迁移」「迁移前的环境诊断」。
- 自动收藏所有 Session。
- 改写 Claude/Codex 的私有存储：只在移动项目目录、迁移会话时改写 `cwd` 字段和索引行，不改消息正文。
- Herdr workspace 的自动创建；cwd 失效时用 git remote 反查候选 repo。

## 双模式 UI

| 模式 | 入口 | 使用场景 | 技术 |
| --- | --- | --- | --- |
| 原生 TUI | `tend` 或 `tend tui` | 日常完整体验、聚合视图、多筛选器 | Go + Bubble Tea + Lip Gloss + Bubbles |
| FZF 模式 | `tend fzf` | SSH、远程机器、低资源环境、快速检索 | Go 编排外部 `fzf` |

默认模式由环境变量 `TEND_UI=tui|fzf` 决定，缺省为 `tui`。
`fzf` 未安装时给出清晰安装提示，不影响 Core 子命令和 TUI 使用。

两个前端各自的要求见 [tui.md](tui.md) 和 [fzf.md](fzf.md)。

## 目录与模块

```text
cmd/tend/                 CLI 入口与子命令
tools/fixture/           把合成测试机器写到目录，附 tend.sh / tend.cmd 启动脚本
tools/test-host/         测试机上 `mise run test-host` 跑的：vet、test、数据集冒烟（sessions / doctor），以及测试机经 `tend rpc` 读自己数据集的 hosts 冒烟
scripts/test-hosts.sh    把工作树打包到各测试机跑 test-host，汇总每台的 RESULT，有一台不全 ok 就失败
scripts/tui-drive.py     在伪终端里对 fixture 数据集驱动 tend tui / fzf 并打印屏幕（隔离 Herdr）
internal/tend/            Rec、Store（JSONL）、Query（解析与匹配）
internal/capture/        环境采集、provider 探测、resume 命令与体检、transcript 对话读取
internal/index/          全部会话的增量索引（sessions.jsonl 缓存）；Rows.List 唯一的会话列表入口；Trash / Restore 回收站共用流程
internal/fulltext/       搜消息：正文库增量更新、关键词拆分、并行扫描 + BM25
internal/herdr/          herdr CLI 封装（全 JSON 输出）
internal/remote/         其它机器：tend rpc 协议（proto.go）、应答端（local.go）、ssh Client、Hosts 缓存、Source（本机 / 远端读 transcript 的统一入口）
internal/pathmap/        另一台机器的路径规则，不看本机系统：Drive / Abs / Base，端点之间 Map
internal/paths/          路径规则的唯一出处：比较 / 包含 / 改根、Windows 大小写与斜杠、~ 缩写与展开、临时目录、路径的 JSON 形式
internal/shell/          shell 命令行的唯一出处：POSIX / PowerShell / cmd 引号与拼行，识别用户的 shell
internal/filelock/       文件锁的唯一出处：flock / LockFileEx 的 Lock、TryLock、Held
internal/fixture/        合成测试机器（Claude / Codex transcript、收藏、项目目录，原生路径）
internal/testkit/        测试共用：JSONString、PosixOnly、Main（TestMain 把家目录、系统配置目录、三个 agent 目录指到临时根，PATH 前放必失败的 herdr / claude / codex）
internal/fileio/         文件原语：WriteAtomic / WriteFile、Lines（可续读的整行扫描，索引 / 正文库 / 全文搜索共用）、ID（文件身份：Unix dev+inode，Windows 卷序列号+文件索引）；Windows 上原子替换碰到拒绝访问或共享冲突时重试（5 ms 起翻倍、单次封顶 100 ms、共约 2 秒），因为 Go 自己的读者打开文件不带 FILE_SHARE_DELETE
internal/docscheck/      只有测试：AGENTS.md、README、SKILL.md 提到的文件、符号、环境变量、测试名、子命令必须存在
internal/platformcheck/  只有测试：在归属包之外重复写平台规则（含文件身份、盘符）就失败
internal/render/         候选行与 preview 渲染，两个 UI 共用
internal/i18n/           界面文案：语义 key，locales/en.json + zh.json，T() 查当前语言
internal/ui/fzf/         FZF 编排
internal/ui/tui/         Bubble Tea 前端
skills/tend/SKILL.md      /tend Skill 源，install-skill 软链进两边
```

第三方依赖：Charm v2（`charm.land/` 下的 bubbletea / bubbles / lipgloss，另有 x/ansi / x/term）、`go-runewidth`、`atotto/clipboard`、`golang.org/x/sys`；无 CGO，单文件跨平台分发。选 Charm v2 是因为 v1 已停止发版、主流 TUI（gh、crush、glow、gum）都用 v2、v2 在 Windows 上的输入和拖选更好，代价是去符号的二进制大约多 16%（约 1MB，主要是字素表、ultraviolet 渲染器和 displaywidth），接受且不为体积再裁剪（发布已带 `-trimpath -s -w`，没有可砍的依赖）。版本只写在 `mise.toml`：`[tools]` 的 go 人人共用，`test-host` 任务的 `tools` 给测试机固定 claude / codex / fzf。

文案国际化：用户可见的字符串一律 `i18n.T("<语义 key>")`（`i18n.F` 带格式化参数，`i18n.E` 出 error）；key 小写点分、按界面区域分命名空间（`help.*`、`settings.*`、`footer.*`、`cli.*`、`flash.*`…），文案放 `internal/i18n/locales/en.json` 和 `zh.json`（embed 进二进制，扁平 key → 文案）。查当前语言，缺了退回英文，再缺原样显示 key。整句带 `%s`/`%d` 占位符，不在代码里拼片段。英文里跟在数目后面的名词或动词写成单复数两种形式 `%d {file|files}`：`i18n.F` / `E` 按紧挨在它前面的那个 `%d` 挑，是 1 用前一种，别的数用后一种；中文不写。`coverage_test` 保证每个用到的 key 两个文件都有、没有没人用的 key、两种语言占位符一致。语言在 `run()` 入口按 `config.lang` 解析一次，设置面板改了立刻生效。

### 各模块机制

- 数据流：`internal/index` 增量扫描 transcript（`~/.claude/projects/*/*.jsonl`、`~/.codex/sessions/**/rollout-*.jsonl`、`~/.codex/archived_sessions/rollout-*.jsonl`）写进 `~/.agent/tend/sessions.jsonl`（扫描先按字节预筛 `"type":"user"`、`"role":"user"`、`"session_meta"` 这类紧凑写法，所以只认 CLI 原样写出的紧凑 JSON，冒号逗号后带空格的行当无关行跳过），`Index.Attach(store, prev)` 与收藏库合并：库里有的会话补上轮数、最后活动和提示词，没有的成为 `ID` 为空的 `tend.Rec`。每次刷新复用同一批对象，TUI 按指针记 probe、光标和 pin。
- `internal/tend`：`Rec` 三个正交状态（`favorited_at` 为空 = 未收藏；`status` todo / doing / done，默认 done；`archived_at`）。`Store` 是追加式 JSONL，同 id 以最后一行为准，`Deleted` 是墓碑；`Put` / `Compact` 持 `records.jsonl.lock`，Compact 重新加载后原子改名。`Query`：不写 `status:` = 未归档的开放记录，`active` = todo + doing，另有 `archived` / `all` / `live` / `trash` / `agent`；`All` 为假时不含未收藏会话；`Live` 回调决定 `status:live`。状态改动走 `Rec.ToggleFavorite` / `ToggleArchived` / `ToggleStatus` 和 `Store.Update`（按 id 或会话重新定位，必要时生成 id）。回收站：会话文件移到 `trash/<provider>/<sid>-<ts>/`，`trash/manifest.jsonl` 记原位置和记录；`MoveToTrash` / `RestoreTrash` / `PurgeTrash` 持 `trash/manifest.jsonl.lock`，每个会话一条，改名失败时复制。
- `internal/index/rows.go`：`Rows.List(store, idx, unfav, live, q)` 是唯一的会话列表入口（库查询、`q.All` 时加未收藏、`status:trash` 读回收站清单、`status:agent` 读 agent 运行、`status:live` 合成正在运行的会话）；合成的行跨调用复用。`trash.go`：`Trash` / `Restore` / `SessionFilesOf` / `RescanSave`，CLI 和 TUI 共用。
- `internal/index/move.go`：`PlanMove`（按索引和库找旧 cwd 下的会话，运行中的单列）→ `Apply`（原件先进回收站，`rewriteCwd` 流式改写每个 transcript 只替换 `"cwd":"…"` 字段；Claude 文件落到 `ClaudeProjectDir(newCwd)`，同名 `<sid>/` 附属目录和清空的项目目录里的 `memory/` 跟着走，库记录和 `~/.claude.json` 的 `projects` 键改根）。`FindMissing` 猜消失的 cwd 去了哪（已知父目录 / 兄弟目录下的同名目录，git remote 必须一致）。
- `internal/capture`：当前会话识别（Herdr pane → `CLAUDE_CODE_SESSION_ID` → Codex rollout 查找）；恢复命令 `CommandSpec`，发给 Herdr 的一律经 `ShellLine`（POSIX 引号），给用户看或复制的经 `Display` / `TerminalLine`（按启动 tend 的 shell 加引号）。读对话：`Messages(path, before, n)` 按 1 MB 块向前翻页（`Page{Msgs, From, Done}`），单条文字截到 16 KB，`TextFull` 按偏移重读全文，工具步骤挂在前一条消息上。`live.go`：Claude 的 `sessions/*.json`、Codex 的 flock 探测（`live_unix.go` / `live_windows.go`）和 Herdr，由 `MergeLive` 合并。
- `internal/fulltext`：`Update` 把每个 transcript 的正文和工具输入（经 `capture.Extract`）增量镜像到 `text/<sha1>.tsv`，`state.json` 记每个文件的偏移和大小，追加前先把文本文件截到记录的大小（崩溃安全），`text/.lock` 是 try-lock（`ErrBusy`）。`Search` 并行扫候选文件：CJK 二元组 + ASCII 词，一个关键词命中其 ≥60% 的词项才算，每个关键词都要在会话里出现，命中消息按 BM25 排。`ParseQuery` 认引号（原样）、`a|b`、`-x`、`who:`；`Expand` 按 `vocab.json` 补拼写；工具输出按 `config.tool_output_lines` 保留开头；`Sync` 是调用入口，索引为空时拒绝清理。`Split` 分开关键词和筛选词，`>` / `》` 前缀（`Prefixed`）让 TUI 搜索框、`tend fzf-list`、`tend grep` 切到搜消息。TUI 侧在 `msgsearch.go`（防抖、可取消、后台更新带进度）。
- `internal/ui/tui`：视图 favorites / sessions / projects / live（Agents）。`refresh()` 从 `m.list`（`index.Rows.List`）重建 `rows`，光标跟着记录指针（或分组名）并保持屏幕行；`pin` 让刚编辑的记录在光标离开前一直可见。右栏由 `probes[*Rec]` 驱动（检查项 + 分页消息，`load` 由 `loading` 防重入）；运行中的会话每 3 秒经 `refreshChat` → `stash` → `applyFresh` 重读尾部。鼠标点击区在渲染时登记（`zone.go`）。`D` 经 `index.Trash` 删除并用 `Index.Forget` 立刻去掉行；在 `status:trash` 里 `D` 经 `index.Restore` 恢复并后台 `reindex`。远端行（`remote.go`）见 [remote.md](remote.md)「TUI」。
- `internal/ui/fzf`：把 `tend fzf-list` / `fzf-tab` / `fzf-pick` 组成一个有三个 tab 的 fzf 会话；当前 tab 只存在 fzf 提示符里（`FZF_PROMPT` 传到每个子命令）。行键是记录 id，未收藏会话用 session id，`cmd/tend` 的 `pick()` 两种都认。
- `internal/render`：宽度一律用 `go-runewidth`（CJK 占 2 列）；图标默认 ASCII，Nerd Font 私有区字符需 `icons=nerd` / `TEND_ICONS=nerd` 打开。
- `internal/i18n`：见上一段；语言取 `config.lang` 或系统（`LC_ALL` / `LC_MESSAGES` / `LANG`，Windows 看界面语言），查找顺序 zh → en → key。
- `cmd/tend`：`hostrows.go` 把远端行并进 CLI 列表和 fzf，并解析 `机器:sid`；`hosts.go` 是 `tend hosts` 各子命令；`install.go` 是 `tend hosts install`（见 [remote.md](remote.md)「`tend hosts install <机器>`」）。
- 运行检测：Codex 看 `~/.codex/thread-writer-locks/*.lock` 的 flock 和 `session_meta` 首行；Claude 看 `~/.claude/sessions/<pid>.json`（`kind=bg` 用 `claude attach <jobId>` 恢复，不用 `--resume`）。

## 安全、可靠性与兼容性

- 所有外部命令使用 executable + args 数组执行，避免 shell injection；业务参数不经过 shell。必须给 shell 读的字符串（复制的恢复命令、Herdr pane 里敲的行、fzf 绑定）一律由 `internal/shell` 按那个 shell 的规则转义。
- 并发追加用文件锁串行化（`internal/filelock`：unix flock，Windows LockFileEx）。
- 单行损坏不影响整体可读性；`tend doctor` 体检，`--compact` 压实。
- `add` 幂等。
- UI 操作失败不导致界面退出或记录丢失。
- 状态改变可撤销（`unarchive` / `restore`）；墓碑不是物理删除。
- 不采集环境变量值、凭据；会话正文只在本机 `text/` 存一份供搜索（工具输出只留每次的前 `tool_output_lines` 行，默认 3，0 不存）；`tend pin` 只在本机硬链，不复制、不上传。
- macOS、Linux、Windows（含 WSL）都支持，平台差异只在 `paths` / `shell` / `filelock` 和按平台拆分的 `_windows.go` / `_unix.go` 里；`scripts/test-hosts.sh` 在五个目标上同步源码、本地编译并跑测试。路径比较只在 Windows 上忽略大小写（`paths.eq`）；macOS 默认的 APFS 虽不区分大小写，仍按区分大小写比较，是已知限制。

## 非功能指标

- 支持终端 resize；颜色不足时可读；尊重 `NO_COLOR`。
- 任何渲染行不得超过终端宽度（中英混排是主要风险来源），有回归测试守住。
- 关键流程提供单元测试；store、query、render、TUI 布局有测试覆盖。
- 不定冷启动 / 查询延迟指标；扫描与渲染守两条硬约束：不读整文件、渲染不做 I/O 之外的重活。

## 行为约束（验收时逐条核）

1. 在 Claude 和 Codex 中执行 `/tend`，均可新增或更新一条收藏。
2. 标题、摘要、标签达到约定质量，且记录文件不保存完整对话（正文副本只在 `text/`）。
3. 两个 UI 都能实时检索并实时预览。
4. `#tag` 多标签为 AND；项目、状态、Provider、时间过滤正确。
5. 可完成、取消收藏、归档、编辑，列表无需重启即可刷新。
6. Claude/Codex 至少各有一个真实 Session 完成端到端恢复。
7. Herdr 可用时完成「已有 workspace → 新 tab → cwd → resume」。
8. 路径不存在、会话文件失效、Provider 未安装、Herdr 不可用、Codex 会话歧义时，均有清晰错误和降级选项。
9. 同一收藏重复执行 `/tend` 不产生重复记录。
10. Core 测试不依赖具体 UI；两个 UI 的查询结果、排序和状态操作一致（同一个 `tend.Parse`、同一个 `index.Rows.List`、同一组 `Rec.Toggle*` 和 `Store.Update`）。
11. 宽/窄/精简三档布局可用，键盘全覆盖，任何一行都不超出终端宽度。
12. 三个视图切换时不丢失筛选条件。
13. `>` 搜消息：中文不分词也能命中、词序无关；筛选词限定范围；结果有命中数和片段；右栏停在命中处、`n`/`N` 可跳；正文库增量更新，建库期间可搜、可打断。
