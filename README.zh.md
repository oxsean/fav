# Fav Session Manager

[English](README.md)

**管好你所有的 Claude Code / Codex 会话：收藏、检索、一键恢复。** 命令行叫 `fav`。 本机所有 AI 编码会话一个列表，值得留的一句 `/fav` 收进来，
一周后凭「做过什么」两秒找回，在正确的目录和 Herdr workspace 里接着聊。

```
 * Fav Session Manager       [ 收藏 58 ]   会话 505   项目 57   Agents 3
 ╭────────────────────────────────────────────────────────────────────────────────────╮
 │ /  webapp oauth last:7d                                                            │
 ╰────────────────────────────────────────────────────────────────────────────────────╯
 ╭ # 项目 webapp ╮ ╭ # 标签 全部 ╮ ╭ > 来源 全部 ╮ ╭ + 状态 未归档 ╮ ╭ @ 时间 7 天 ╮
 收藏 4 条 · 按最后活跃排序 · o 切换      ╭───────────────────────────────────────────╮
 今天 ─────────────────────────────────── │ OAuth 回调 302 之后丢 state 排查          │
 ╭──────────────────────────────────────╮ │ * 进行中  ·  Codex CLI  ·  今天 17:03     │
 │ * OAuth 回调 302 之后丢 state 排查   │ │ ────────────────────────────────────────  │
 │   Codex  ·  webapp  ·  14 轮   17:03 │ │ 摘要                                      │
 │   #oauth #middleware                 │ │ state cookie 在 302 之前被鉴权中间件清    │
 ╰──────────────────────────────────────╯ │ 掉；修法是 /callback 上跳过重写。下一步   │
 ╭──────────────────────────────────────╮ │ 补一个回归测试。                          │
 │ * notes-api 游标分页重复返回同一条   │ │                                           │
 │   Claude  ·  notes-api  ·  9 轮15:03 │ │ # 项目      webapp                        │
 │   #pagination #cursor                │ │ @ 分支      feat/oauth                    │
 ╰──────────────────────────────────────╯ │ ~ 目录      ~/dev/webapp                  │
 昨天 ─────────────────────────────────── │ @ 最后活动  今天 17:03                    │
 ╭──────────────────────────────────────╮ │                                           │
 │ ✓ fav 索引增量扫描                   │ │ 恢复目标                                  │
 │   Claude  ·  fav  ·  31 轮     21:38 │ │ Herdr webapp  ->  新 tab  ->  Codex CLI   │
 │   #index #perf                       │ │ + 已识别会话来源                          │
 ╰──────────────────────────────────────╯ │ + 项目目录存在                            │
 ╭──────────────────────────────────────╮ │ + 会话记录文件可用                        │
 │ ✓ shell-init：Ctrl-G 弹出 fav fzf    │ │ ! 当前分支 main，收藏时是 feat/oauth      │
 │   Codex  ·  fav  ·  6 轮       20:11 │ │                                           │
 │   #shell #zsh                        │ │ ── 对话 · 第 1-40/128 句 · J/K 翻 · \ 找  │
 ╰──────────────────────────────────────╯ │ 你  ·  17:01  ~ 42                        │
                                          │   跳转之后 state 还是丢                   │
                                          │ AI  ·  17:02  ~ 1.2k                      │
                                          │   中间件在每个请求上都重写 cookie，包括   │
                                          │   /callback ...                           │
                                          ╰───────────────────────────────────────────╯
 Enter 操作 │ Space 恢复 │ f 取消收藏 │ x 完成 │ a 归档 │ e 编辑 │ M 移动 │ D 删除
```

## 它解决什么问题

每天开十几个 Claude Code / Codex 会话，一周之后：

| 之前 | 用 fav 之后 |
|---|---|
| 自动标题是「继续」「ok」「帮我看下」，不知道哪条是哪条 | `/fav` 时 AI 还记得整段对话，标题、摘要、标签一次写好，一周后仍认得出 |
| 只记得「让它排查过 OAuth」，不记得日期、项目、用的哪个工具 | 关键词直接搜索引里的提示语和摘要，`#标签` `project:` `last:7d` 层层收窄 |
| Claude 在 `~/.claude/projects`、Codex 在 `~/.codex/sessions`，两边各自的历史列表都只看得到本目录 | 全机所有会话一张表，按时间或按项目看，不分工具 |
| 找到了还要 `cd` 到对的目录、想起是 `claude --resume` 还是 `codex resume`、再切到 Herdr 那个 workspace | `Enter`：目录、命令、workspace 全替你选好；已经在跑的直接切过去 |
| 项目目录一挪，之前的会话全部恢复不了；Claude 30 天悄悄清 transcript | `M` 把会话跟着目录一起搬；`!` 标出坏掉的，`fav fix` / `fav clean` 一键修或清；`fav pin` 保住重要的 |
| 开了六个 agent 在跑，得一个个 tab 翻 | Agents 页：谁在等你、谁在干活、谁空了多久，3 秒一刷 |

fav 不是新的聊天客户端，不替代 Claude / Codex，也不上传任何东西：它是一份**本地的、可 grep 的个人会话索引**，
外加一个「按下去就在对的地方」的恢复按钮。

## 能做什么

- **收藏**：会话里 `/fav`，AI 按对话语言写标题 / 摘要 / 标签，`fav` 自己采集 provider、session id、目录、git 分支、Herdr workspace。同一会话再 `/fav` 是更新。
- **全部会话**：不用收藏也都在列表里——Claude 和 Codex 的全部历史增量索引，只读文件头尾和新增部分，几百 MB 的会话也不整读。
- **搜**：同一套查询语法贯穿 TUI、fzf、命令行：关键词、`#标签`、`project:`、`provider:`、`status:`、`last:7d`、`turns:`。
- **三个搜索键**：`/` 搜会话（标题、摘要、项目、标签），`>` 搜所有会话的消息，`\`（或 `ctrl+s`）搜当前会话的消息；焦点在哪含义都一样，中文输入法把 `/` 打成 `、` 也照样是搜会话。
- **搜消息**：按 `>` 或查询以 `>` 开头，就在其余条件选出的会话里搜每条消息和命令；结果是按相关度排的会话，带命中数和片段，右栏停在卡片片段那一处，`n`/`N` 跳转；`→` 列出这个会话的全部命中和前后文，`Enter` 打开整条消息并停在关键词处。中文不用分词。
- **看**：右栏是这条会话的对话，从尾往前翻，句内可搜；不用打开就知道是不是它。
- **恢复**：Herdr 在跑 → 它的 workspace 里开新 tab；没有 → 当前终端 `exec` 接管；已经在跑 → 切 tab；后台会话 → `claude attach`。恢复前逐项校验目录、记录文件、分支。
- **整理**：待办 / 进行中 / 已完成 / 已归档四态，改标题标签，按项目分组。
- **搬家与自愈**：项目目录移动后会话一起迁（记录里的 cwd、Claude 项目目录、`~/.claude.json` 全改）；目录或记录文件没了的会话标 `!`，命令行批量修复或清理，删除进回收站可还原。
- **Agents 面板**：此刻在跑的会话，三源合并（Claude `sessions/*.json`、Codex 线程锁、Herdr），不装 Herdr 也能用。
- **三种前端**：TUI（日常）、fzf（SSH / 两秒找一条，收藏 / 会话 / Agents 三页都有）、CLI（脚本，`--json`）。中英文界面，中文输入法下所有动作都有非字母键。

## 快速开始

```bash
# macOS / Linux：从 Releases 下载最新版到 ~/.local/bin
curl -fsSL https://github.com/oxsean/fav/releases/latest/download/fav_$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz | tar xz -C ~/.local/bin fav
# 或者有 Go：
go install github.com/oxsean/fav/cmd/fav@latest

fav install-skill                            # /fav Skill 装进 ~/.claude/skills 和 ~/.codex/skills
fav                                          # 第一次启动后台建索引，几秒后「会话」页就有你所有历史
```

Windows：到 [Releases](https://github.com/oxsean/fav/releases/latest) 下 `fav_windows_amd64.zip`，解压到 PATH 里的目录。

然后：

1. 在任何 Claude Code / Codex 会话里做完一件事，输入 `/fav`。
2. 下周想接着做：`fav`，打几个词，`Enter`。

想在 shell 里一键弹出：`.zshrc` 加一行 `eval "$(fav shell-init zsh)"`（bash 用 `shell-init bash`），之后按 `Ctrl-G` 弹出 fzf 版，退出回到提示符。
换键 `--key alt-f`（或直接给 shell 自己的记法），`--ui tui` 改弹 TUI。

可选依赖：`fzf`（fzf 模式）、Herdr（恢复到 workspace、看谁在跑、切 tab）、
Nerd Font（设置里把图标从 ASCII 切成 Nerd Font，或 `FAV_ICONS=nerd`）。卸载 Skill：`fav uninstall-skill`。

## 用法

### 收藏：`/fav`

在会话里输入 `/fav`（或说「收藏这个会话」）。Skill 回顾整段对话，写标题（12–40 字，说清对象和做了什么）、
一段摘要（结论和下一步，不是流水账）、2–5 个标签，交给 `fav add` 入库。
provider、session id、目录、git、Herdr 上下文由 `fav` 自己采集，AI 不猜。

没 `/fav` 过的会话也在「会话」页，随时按 `f` 收进来，标题用第一句提示语；恢复进去再 `/fav` 补摘要。

### 找回来、恢复：`fav`（TUI）

```
fav            # 默认 TUI；FAV_UI=fzf 改默认
fav tui --no-mouse
```

四个页面，`Tab` / `1`–`4` 切：

| 页 | 看什么 |
|---|---|
| 收藏 | `/fav` 过的，按最后活跃排（`o` 切排序） |
| 会话 | 本机全部会话（短于 3 轮的默认不列，`turns:1` 全列） |
| 项目 | 按目录分组：`→` 展开、`←` 折叠、再 `→` 到右栏看项目信息（目录 / 会话数 / 来源 / 最近会话）；在哪个目录里启动 `fav`，那个项目的分组自动展开并滚到顶上 |
| Agents | 现在谁在跑：等你 / 工作中 / 空闲多久，每 3 秒刷 |

一条典型流程：`/` 搜（`webapp oauth last:7d`）或 `;` 进 chip 行按项目 / 标签 / 来源 / 状态 / 时间筛；
`↑↓` 挑到那条，右栏就是它的对话（`→` 进去逐句看，`Enter` 全文，`\` 在对话里找）；
`Enter` 弹操作框——`Enter 恢复`、`t` 强制当前终端、`y` 复制恢复命令、`i` / `c` 用 IDE / VS Code 打开目录。
在跑的会话 `Enter` 是切到那个 tab，后台会话（`claude --bg`）是 attach。`Space` 跳过操作框直接恢复。

整理：`f` / `*` 收藏、`x` 完成、`a` 归档、`e` 改标题 / 标签 / 摘要，`s` 状态筛选（未归档 / 进行中 / 已完成 / 已归档 / 全部 / 回收站）。

项目目录挪了：项目页分组标题上 `M`（会话上 `M` 只动这一条），目录选择器里 `Enter` 进子目录、再按 `M` 选定；
确认框列出从哪到哪、多少会话 / 文件、要不要重启开着的 Claude / Codex，`y` 才动。原件先进回收站，有会话在跑的项目拒绝。

删除与回收站：`D` 把会话文件挪进 `~/.agent/fav/trash/`，状态筛「回收站」能看被删的对话，`D` 还原；默认 30 天后彻底清。
目录或记录文件没了的会话标题后有暗红 `!`，操作框只给「移动」「删除」。

中文输入法开着时小写字母会被拿去组词：收藏用 `*`，`Ctrl-X` 完成 / `Ctrl-A` 归档 / `Ctrl-E` 编辑 / `Ctrl-Y` 复制 /
`Ctrl-N/P` 上下 / `Ctrl-G` 恢复；大写 `D M X` 输入法不拦；操作框里所有动作都有按钮。`?` 看全部键位。

鼠标默认开：点标签页、点 chip、点卡片、双击恢复、点输入框光标落到点的位置、滚轮、浮层按钮；按住左键拖过文字松手即复制。
`,` 打开设置：时间显示、默认页 / 排序、短会话阈值、滚轮步长、图标、鼠标、回收站保留天数、IDE、语言。

### 两秒找一条：`fav fzf`

同样三个页面（收藏 / 会话 / Agents，`Tab` / `Shift-Tab` 轮换，`F1`–`F3` 直达），没有对话预览和项目视图，适合 SSH、低资源、或者你已经知道要找什么。
输入框直接写查询语法，边打边筛，过滤仍由 `fav` 做（和 TUI 同一个解析器）；Agents 页每 3 秒自动刷新。需要 fzf 0.46+，0.73+ 才有自动刷新。

| 键 | 动作 |
|---|---|
| `Enter` / `Alt-Enter` | 恢复（Herdr 在跑就开新 tab；在跑的会话是切过去 / 接管）/ 当前终端恢复 |
| `Ctrl-X` / `Ctrl-A` / `Alt-F` | 完成 ↔ 重开 / 归档 ↔ 取消 / 收藏 ↔ 取消（都是切换，和 TUI 的 `x` `a` `f` 一样） |
| `Ctrl-E` / `Ctrl-Y` | 用 `$EDITOR` 编辑 / 复制恢复命令 |
| `Alt-T` / `Alt-P` / `Alt-S` / `Alt-D` | 标签 / 项目 / 状态 / 时间筛选器，选完写回查询（`Ctrl-S` 也是状态） |
| `Ctrl-L` / `Shift-↑↓` | 重新加载 / 翻右栏预览（最近 40 句对话在下面） |

规则：`Ctrl-字母` = TUI 里同一个字母的动作，`Alt-字母` = 筛选器。取消收藏 / 归档之后那条先留在原位，再按一次就回来了，下次打字或切页才消失。

### 脚本和维护：命令行

```bash
fav list '#notes-api last:7d' --json    # 收藏
fav sessions 'webapp oauth' --json      # 全部会话
fav show <id> --json
fav grep '滚轮 加速 project:fav'      # 搜消息：关键词 + 筛选，按相关度列会话和片段（--json、--limit）
fav open <id>                           # 直接打开这个会话的界面，右栏聚焦（列表不显示的也行）
fav resume <id> --dry-run               # 只打印要执行的命令和检查项
fav resume <id> --no-herdr              # 当前终端恢复

fav status <id> todo|doing|done  ·  fav done <id>  ·  fav archive|unarchive <id>  ·  fav fav|unfav <id>
fav edit <id>                           # $EDITOR 里改标题 / 标签 / 摘要
fav pin <id>                            # 硬链保住会话记录文件（Claude 默认 30 天清 transcript）
```

坏会话（目录挪了 / 记录文件没了）：`fix` 和 `clean` 用同一张表，默认只列，选中才动：

```bash
fav clean                               # 列出所有恢复不了的会话：编号、原因、猜到的去向
fav clean provider:codex last:30d       # 筛选和 fav list 一样；目录参数只看它下面
fav fix 1 3                             # 目录挪走的：移到猜到的去向（先打印这几条，y/N 确认）
fav fix 2 --to ~/dev/proj               # 没猜到或猜错了就指定
fav fix all                             # 有唯一去向的全修，其余列出来跳过
fav clean 01a07dcf -y                   # 按会话 id 前缀清，-y 不问；进回收站，可还原
fav trash --restore 01a07dcf
```

编号跟着筛选变，要和刚才同样的目录 / 筛选词一起用。stdin 不是终端又没 `-y` 会直接报错；有失败项非零退出。

```bash
fav mv <旧目录> <新目录>                 # 等于 TUI 的 M
fav rm <id>                              # 单条进回收站；所有 <id> 也认会话 id 前缀
fav trash [--json]                      # 看回收站；--purge 清过期的，--purge --all 清空（会确认）
fav doctor [--compact]                  # 体检：数据文件、失效会话、回收站过期；--compact 压实
```

## 查询语法

`#标签`、`project:x`、`provider:claude|codex`、`status:open|active|done|archived|trash|all|live|agent`、
`after:2026-09-01`、`before:…`、`last:7d`、`turns:3`，以及普通关键词。全部 AND，中文直接子串匹配。
以 `>`（或 `》`）开头改搜消息正文：关键词在每条消息和工具命令里找，筛选词只限定会话范围（默认 `status:all turns:0`）。每个关键词都要在会话里出现；
关键词的词项命中六成就算中（中文按相邻两字切，关键词内部不讲词序；用引号包起来——`"…"`、`“…”` 或 `「…」`——就必须原样连着出现；`a|b` 两个有一个就算，`-x` 去掉含 x 的消息，`who:me`、`who:ai` 或 `who:tool` 只看某一方说的；英文词拼错、会话里又几乎没出现过时，也会顺带搜只差一个字母的常见词，标题里写明「也搜了 …」）；BM25 排序，关键词挨得近、消息越新、是你自己说的（工具命令、工具输出和 Claude 的续接摘要权重低）、标题摘要标签里也有关键词的，都加分；有一条消息同时含全部关键词的会话排前面，`o` 切到按最近命中排。
默认看未归档的；关键词也搜索引里的用户提示语——记得「让它做过 X」就能搜到。三个前端共用同一个解析器。

## 工作原理

**索引。** `internal/index` 扫 `~/.claude/projects/*/*.jsonl` 和 `~/.codex/sessions/**/rollout-*.jsonl`，每个文件记 session id、
cwd、分支、开始时间、人说话的轮数、标题、全部提示语（封顶 8KB，只用来搜）。transcript 是 append-only 的，索引记「读到哪了」，
下次只补读新增；对话预览从文件尾向前分块读，翻到哪读到哪。`-p` / SDK 会话、Codex 子代理线程、一句话没说过的不列。

**消息正文。** `internal/fulltext` 在 `~/.agent/fav/text/` 给每个 transcript 存一份正文（TSV：偏移、角色、时间、文本），
只含说的话和工具命令，不含工具输出；跟索引一样在每次刷新后增量补读。搜索时并行流式扫候选文件，不维护倒排索引，几百 MB 不到一秒。

**收藏。** Skill 只负责理解会话，输出一段 JSON；校验、采集环境、存储、幂等（provider + session id）都在 `fav add`。
`records.jsonl` append-only，最后一行为准。

**恢复。** 先校验（provider 在 PATH、目录存在、记录文件还在、分支是否变了），再决定路由：
会话已在某个 Herdr tab → `herdr tab focus`；Claude 后台会话 → `claude attach`；Herdr 可达且能按 workspace 或目录找到位置 → 新 tab 里恢复，
TUI 不退出；否则 `cd` 到记录目录在当前终端 `exec`。路径或 workspace 对不上时报错列候选，不静默猜。

**在跑的会话。** Claude 写 `~/.claude/sessions/<pid>.json`（busy / idle），Codex 开着的线程持有 `thread-writer-locks/*.lock`，
Herdr 多知道 tab 和「等你」；三者非空字段叠加。Claude Code 上下文用完时会把对话交给一个后台工作进程、换一个 session id，原进程只当终端：
fav 把这条链合成一个会话（轮数相加、用最新的 id 恢复、收藏跟着走），停车的原进程不算在跑。

## 数据

| 文件 | 是什么 |
|---|---|
| `~/.agent/fav/records.jsonl` | 收藏，append-only 一行一条；可以 grep、手改、git 同步 |
| `~/.agent/fav/sessions.jsonl` | 会话索引缓存，只存提示语；删了下次启动重建 |
| `~/.agent/fav/text/` | `>` 搜消息用的正文副本，一个 transcript 一个文件，外加 `vocab.json`（出现过的英文词，用于纠正拼写）；删了自动重建 |
| `~/.agent/fav/trash/` | 回收站：被删的会话文件按原样挪进来，`manifest.jsonl` 记着来处 |
| `~/.agent/fav/config.json` | 设置面板写的 |

环境变量：`FAV_HOME` 改数据目录，`FAV_UI=fzf|tui` 改默认前端，`FAV_ICONS=nerd|ascii` 选图标。
界面语言默认跟系统（`LANG` 等以 zh 开头是中文，否则英文），设置里可固定。

聊天正文只在本机 `text/` 里存一份供搜索，不上传任何东西；只在你点名移动目录时改会话文件里的 cwd，改之前原件先进回收站。`fav pin` 只在本机做硬链。

## 文档与开发

```bash
go build ./... && go vet ./... && go test ./...
HERDR_LIVE=1 go test ./internal/herdr/   # 实跑 Herdr 建 tab → 执行 → 清理
```
