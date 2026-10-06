# tend

[English](README.md)

**管好你所有的 Claude Code / Codex 会话：收藏、检索、一键恢复；再把任务派给任意一台机器上的 agent 去跑。** 本机所有 AI 编码会话一个列表，值得留的一句 `/tend` 收进来，
一周后凭「做过什么」两秒找回，在正确的目录和 Herdr workspace 里接着聊。
任务写一次，派给本机或另一台机器上的 agent，看它的输出，跑完后接手它的会话。

```
 * tend                      [ 收藏 58 ]   会话 505   项目 57   Agents 3
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
 │ ✓ tend 索引增量扫描                   │ │ 恢复目标                                  │
 │   Claude  ·  tend  ·  31 轮     21:38 │ │ Herdr webapp  ->  新 tab  ->  Codex CLI   │
 │   #index #perf                       │ │ + 已识别会话来源                          │
 ╰──────────────────────────────────────╯ │ + 项目目录存在                            │
 ╭──────────────────────────────────────╮ │ + 会话记录文件可用                        │
 │ ✓ shell-init：Ctrl+G 弹出 tend fzf    │ │ ! 当前分支 main，收藏时是 feat/oauth      │
 │   Codex  ·  tend  ·  6 轮       20:11 │ │                                           │
 │   #shell #zsh                        │ │ ── 对话 · 第 1-40/128 句 · J/K 翻 · \ 找  │
 ╰──────────────────────────────────────╯ │ 你  ·  17:01  ~ 42                        │
                                          │   跳转之后 state 还是丢                   │
                                          │ AI  ·  17:02  ~ 1.2k                      │
                                          │   中间件在每个请求上都重写 cookie，包括   │
                                          │   /callback ...                           │
                                          ╰───────────────────────────────────────────╯
 Enter 操作  │ / 搜索  > 搜消息 │ f/* 取消收藏                 Tab 视图  ? 帮助
```

## 它解决什么问题

每天开十几个 Claude Code / Codex 会话，一周之后：

| 之前 | 用 tend 之后 |
|---|---|
| 自动标题是「继续」「ok」「帮我看下」，不知道哪条是哪条 | `/tend` 时 AI 还记得整段对话，标题、摘要、标签一次写好，一周后仍认得出 |
| 只记得「让它排查过 OAuth」，不记得日期、项目、用的哪个工具 | 关键词直接搜索引里的提示语和摘要，`#标签` `project:` `last:7d` 层层收窄 |
| Claude 在 `~/.claude/projects`、Codex 在 `~/.codex/sessions`，两边各自的历史列表都只看得到本目录 | 全机所有会话一张表，按时间或按项目看，不分工具 |
| 找到了还要 `cd` 到对的目录、想起是 `claude --resume` 还是 `codex resume`、再切到 Herdr 那个 workspace | `Enter`：目录、命令、workspace 全替你选好；已经在跑的直接切过去 |
| 项目目录一挪，之前的会话全部恢复不了；Claude 30 天悄悄清 transcript | `M` 把会话跟着目录一起搬；`!` 标出坏掉的，`tend fix` / `tend clean` 一键修或清；`tend pin` 保住重要的 |
| 开了六个 agent 在跑，得一个个 tab 翻 | Agents 页：谁在等你、谁在干活、谁空了多久，3 秒一刷 |

tend 不是新的聊天客户端，不替代 Claude / Codex，也不上传任何东西：它是一份**本地的、可 grep 的个人会话索引**，
外加一个「按下去就在对的地方」的恢复按钮。

## 能做什么

- **收藏**：会话里 `/tend`，AI 按对话语言写标题 / 摘要 / 标签，`tend` 自己采集 provider、session id、目录、git 分支、Herdr workspace。同一会话再 `/tend` 是更新。
- **全部会话**：不用收藏也都在列表里——Claude 和 Codex 的全部历史增量索引，只读文件头尾和新增部分，几百 MB 的会话也不整读。
- **搜**：同一套查询语法贯穿 TUI、fzf、命令行：关键词、`#标签`、`project:`、`provider:`、`status:`、`last:7d`、`turns:`、`file:`（AI 改过路径里含这段的文件的会话）。
- **三个搜索键**：`/` 搜会话（标题、摘要、项目、标签），`>` 搜所有会话的消息，`\`（或 `Ctrl+S`）搜当前会话的消息；焦点在哪含义都一样，中文输入法把 `/` 打成 `、` 也照样是搜会话。
- **搜消息**：按 `>` 或查询以 `>` 开头，就在其余条件选出的会话里搜每条消息和命令；结果是按相关度排的会话，带命中数和片段，右栏停在卡片片段那一处，`n`/`N` 跳转；`→` 列出这个会话的全部命中和前后文，`Enter` 打开整条消息并停在关键词处。中文不用分词。
- **看**：右栏是这条会话的对话，从尾往前翻，句内可搜；不用打开就知道是不是它。
- **恢复**：Herdr 在跑 → 它的 workspace 里开新 tab；没有 → 当前终端 `exec` 接管；已经在跑 → 切 tab；后台会话 → `claude attach`。恢复前逐项校验目录、记录文件、分支。
- **桌面 App**：恢复框里也能在 Claude 桌面版或 ChatGPT 桌面版（Codex）里打开这个会话；设置里可以让 App 成为默认，或只对在 App 里建的会话默认用 App——这些会话的卡片标着 `Claude App`、`Codex App`。需要会话的工作目录还在、记录文件在默认的 `~/.claude/projects` 或 `~/.codex/sessions` 下，并且系统把 `claude://`、`codex://` 交给对应 App 处理时才显示这个按钮（macOS、Windows，Linux 走 xdg-mime）。
- **分叉与交接**：恢复框里按 `b` 分叉（`claude --resume … --fork-session` / `codex fork`），得到一个带着同样历史的新会话，原会话不动。按 `s` 写一份交接包（摘要、最近 5 条要求、最后一条回复、改过的文件、`git status`，不含工具输出），放在 `~/.agent/tend/handoff/`，先给你看、可以编辑，再在同一目录开一个新的 Claude 或 Codex 会话，第一条消息让它先读交接包。命令行：`tend resume --fork <id>`、`tend handoff <id> [--to claude|codex]`。
- **瞄一眼 / 回一句**：在 Herdr 里跑的会话上按 `` ` ``（输入法下的 `·` 也行）看它的终端（每秒刷新；权限确认只出现在终端里，记录文件里没有），`:` 输入一句作为它的下一句话发过去，`1`–`3` 按两次回答编号选项（画面一变或过 5 秒就作废）。
- **清理空闲**：Agents 页按 `Z` 一次关掉 4 小时没写东西、也没有没看过的输出的 Herdr tab（先确认，焦点在取消）。
- **在这里开新会话**：项目页分组标题或任意会话上按 `w`（`Ctrl+W`），先列出那个目录里已经在跑的会话（↑↓ Enter 直接过去），再选 Claude 或 Codex（`1` / `2` 选中，再按一次或 Enter 才开）在那里开新会话；那里有 Herdr workspace 就开在新 tab 里。
- **整理**：待办 / 进行中 / 已完成 / 已归档四态，改标题标签，按项目分组。
- **搬家与自愈**：项目目录移动后会话一起迁（记录里的 cwd、Claude 项目目录、`~/.claude.json` 全改）；目录或记录文件没了的会话标 `!`，命令行批量修复或清理，删除进回收站可还原。
- **Agents 面板**：此刻在跑的会话，三源合并（Claude `sessions/*.json`、Codex 线程锁、Herdr），不装 Herdr 也能用。
- **三种前端**：TUI（日常）、fzf（SSH / 两秒找一条，收藏 / 会话 / Agents 三页都有）、CLI（脚本，`--json`）。中英文界面，中文输入法下所有动作都有非字母键。

## 快速开始

```bash
# macOS / Linux：从 Releases 下载最新版到 ~/.local/bin
curl -fsSL https://github.com/oxsean/fav/releases/latest/download/tend_$(uname -s | tr A-Z a-z)_$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/').tar.gz | tar xz -C ~/.local/bin tend
# 或者有 Go：
go install github.com/oxsean/fav/cmd/tend@latest

tend install-skill                            # /tend Skill 装进 ~/.claude/skills 和 ~/.codex/skills（设了 $CLAUDE_CONFIG_DIR / $CODEX_HOME 就装到那里）
tend install-hook                             # 可选：Claude Code 问你问题时告诉 tend，没有 Herdr 也能在 Agents 页标出权限确认（tend uninstall-hook 删掉）
tend                                          # 第一次启动后台建索引，几秒后「会话」页就有你所有历史
```

Windows：到 [Releases](https://github.com/oxsean/fav/releases/latest) 下 `tend_windows_amd64.zip`，解压到 PATH 里的目录。

然后：

1. 在任何 Claude Code / Codex 会话里做完一件事，输入 `/tend`。
2. 下周想接着做：`tend`，打几个词，`Enter`。

想在 shell 里一键弹出：`.zshrc` 加一行 `eval "$(tend shell-init zsh)"`（bash 用 `shell-init bash`），之后按 `Ctrl+G` 弹出 fzf 版，退出回到提示符。
换键 `--key alt-f`（或直接给 shell 自己的记法），`--ui tui` 改弹 TUI。

可选依赖：`fzf`（fzf 模式）、Herdr（恢复到 workspace、看谁在跑、切 tab）、
Nerd Font（设置里把图标从 ASCII 切成 Nerd Font，或 `TEND_ICONS=nerd`）。卸载 Skill：`tend uninstall-skill`。

## 用法

### 收藏：`/tend`

在会话里输入 `/tend`（或说「收藏这个会话」）。Skill 回顾整段对话，写标题（12–40 字，说清对象和做了什么）、
一段摘要（结论和下一步，不是流水账）、2–5 个标签，交给 `tend add` 入库。
provider、session id、目录、git、Herdr 上下文由 `tend` 自己采集，AI 不猜。

没 `/tend` 过的会话也在「会话」页，随时按 `f` 收进来，标题用第一句提示语；恢复进去再 `/tend` 补摘要。在那之前，有 Claude 自动回顾（目标 · 已做 · 下一步）或 Codex 最后一轮总结的，摘要就先用它。

### 找回来、恢复：`tend`（TUI）

```
tend            # 默认 TUI；TEND_UI=fzf 改默认
tend tui --no-mouse
```

五个页面，`Tab` / `1`–`5` 切：

| 页 | 看什么 |
|---|---|
| 收藏 | `/tend` 过的，按最后活跃排（`o` 切排序） |
| 会话 | 本机全部会话（短于 3 轮的默认不列，`turns:1` 全列） |
| 项目 | 按目录分组（git worktree 里的会话归到主仓库，卡片标 `worktree <分支>`；worktree 删了，`tend fix` 把会话移回主仓库）：`→` 展开、`←` 折叠、再 `→` 到右栏看项目信息（目录 / 会话数 / 来源 / 最近会话）；在哪个目录里启动 `tend`，那个项目的分组自动展开并滚到顶上 |
| Agents | 现在谁在跑：等你 / 工作中 / 空闲多久，本轮跑了多久、上下文用了多少、AI 最后说了什么，每 3 秒刷；等你回答、跑完了还没看的会提醒（标签页 `Agents 5 !2`），`.` 标已处理、`H` 暂缓 1 小时 |
| 任务 | 任务和它们的运行记录（见[任务](#任务把-agent-派到你的机器上)） |

一条典型流程：`/` 搜（`webapp oauth last:7d`）或 `;` 进 chip 行按项目 / 标签 / 来源 / 状态 / 时间筛；
`↑↓` 挑到那条，右栏就是它的对话（`→` 进去逐句看，`Enter` 全文，`\` 在对话里找）；
`Enter` 弹操作框——`Enter 恢复`、`t` 强制当前终端、`y` 复制恢复命令、`p` 在 Claude / ChatGPT 桌面 App 里打开，`i` / `c` / `o` 用 IDE / VS Code / Finder（资源管理器、文件管理器）打开目录——按「继续会话 / 打开项目 / 这条记录」分三行。
在跑的会话 `Enter` 是切到那个 tab，后台会话（`claude --bg`）是 attach；在别的终端里跑的不能再恢复一份（两边会同时写）。`Space` 只做一件事：切到已经在 Herdr tab 里跑的会话，其余同 `Enter`，只打开操作框——会开进程、开 tab 的动作都要在操作框里确认。操作框里和列表意思不同的键（`t` `p` `b` `s` `i` `c` `o`）按一下只把焦点移到那个按钮，再按一次或 `Enter` 才执行；所有弹窗里 `Tab` / `Shift+Tab` 只在字段和按钮之间移焦点；会话在跑时多一行「在跑」：瞄一眼 / 已处理 / 暂缓 / 关掉 tab。

整理：`f` / `*` 收藏、`x` 完成、`a` 归档、`e` 改标题 / 标签 / 摘要，`s` 状态筛选（未归档 / 进行中 / 已完成 / 已归档 / 全部 / 回收站）。

项目目录挪了：项目页分组标题上 `M`（会话上 `M` 只动这一条），目录选择器里 `Enter` / `→` 进子目录，第一行「就是这个目录」或「移动到这里」按钮选定；
确认框列出从哪到哪、多少会话 / 文件、要不要重启开着的 Claude / Codex，`y` 才动。原件先进回收站，有会话在跑的项目拒绝。

删除与回收站：`D` 确认后（`y`，焦点默认在取消）把会话文件挪进 `~/.agent/tend/trash/`，状态筛「回收站」能看被删的对话，`D` 还原；默认 30 天后彻底清。
目录或记录文件没了的会话标题后有暗红 `!`，操作框只给「移动」「删除」。

中文输入法开着时小写字母会被拿去组词：收藏用 `*`，`Ctrl+X` 完成 / `Ctrl+E` 编辑 / `Ctrl+Y` 复制 /
`Ctrl+N/P` 上下 / `Ctrl+G` 同 `Space`；大写 `D M X` 输入法不拦；操作框里所有动作都有按钮。`?` 打开帮助，三页：列表的键位、搜索语法、鼠标与输入法。

鼠标默认开：点标签页、点 chip、点卡片、双击恢复、点输入框光标落到点的位置、滚轮、浮层按钮；按住左键拖过文字松手即复制。
`,` 打开设置：时间显示、默认页 / 排序、短会话阈值、滚轮步长、图标、鼠标、回收站保留天数、IDE、语言。

<details>
<summary>完整按键与说明</summary>

TUI 里的 `?` 只列简短版；这里是逐条的完整说明，包括操作框和弹窗里的键。

**搜索**

| 键 | 作用 |
|---|---|
| `/` `、` | 搜会话：标题、摘要、项目、#标签；焦点在哪都一样 |
| `>` `》` | 搜所有会话的消息（语法见「查询语法」） |
| `Ctrl+S` `\` | 只在当前会话里搜消息，命中列在左栏 |
| `n / N` | 跳到下一处 / 上一处命中 |
| `l` `→` | 搜消息时：列出本会话的全部命中，右栏跟着跳 |
| `o` `Ctrl+O` | 切换排序；搜消息时在相关度 / 最近命中之间切换 |

**移动**

| 键 | 作用 |
|---|---|
| `j / k` `↓ / ↑` `Ctrl+N / Ctrl+P` | 上下选择；搜索框里用 Ctrl+N / Ctrl+P |
| `PgDn / PgUp` `Ctrl+F / Ctrl+B` `Ctrl+D / Ctrl+U` `g / G` `Home / End` | 翻页 / 半页 / 到顶到底；右栏对话同样 |
| `h / l` `← / →` | 左右栏切焦点；筛选行里换 chip |
| `Tab / Shift+Tab` `1` `2` `3` `4` `5` | 切标签页：收藏 / 会话 / 项目 / Agents / 任务 |

**视图与筛选**

| 键 | 作用 |
|---|---|
| `;` | 进 / 出筛选 chip 行（左右换，Enter 打开） |
| `t / p / v / d` | 标签 / 项目 / 来源 / 时间筛选 |
| `s` | 状态筛选：未归档 / 进行中 / 已完成 / 已归档 / 全部 / 回收站 |
| `Enter` | 项目视图的分组标题上：折叠 / 展开 |
| `z / - / =` `+` | 项目视图：切换全折 / 全展、全折、全展；任务视图：对运行输出里的改动行同样操作，展开的行下面列第一处（点一行只展开它） |

**打开与继续**

| 键 | 作用 |
|---|---|
| `Enter / r` | 宽屏打开操作框，窄屏进详情；在跑的会话主按钮是「切过去」或「接管」 |
| `Space` `Ctrl+G` | 切到已经在 Herdr tab 里运行的会话；其他情况同 Enter（打开操作框）；在右栏里是向下翻页 |
| `w` `Ctrl+W` | 在这个项目里开新会话（先列出那里已经在跑的） |

**这条记录**

| 键 | 作用 |
|---|---|
| `f` `*` | 收藏 / 取消收藏；取消不删记录，再按原样恢复 |
| `x` `Ctrl+X` | 标已完成；再按回到进行中 |
| `a` | 归档 / 取消归档 |
| `e` `Ctrl+E` | 编辑标题 / 标签 / 摘要 |
| `M` | 移动项目目录（见上文「项目目录挪了」） |
| `D` | 删除：进回收站；在回收站里是还原 |

**右栏**

| 键 | 作用 |
|---|---|
| `J / K` `Ctrl+J / Ctrl+K` | 不离开列表移右栏高亮句（往旧 / 往新）；焦点在右栏时上下键也是 |
| `Enter` | 打开这句的全文和之后每一步 |
| `y` `Ctrl+Y` | 复制右栏高亮的这句 |

**在跑的会话**

| 键 | 作用 |
|---|---|
| `` ` `` `·` | 瞄一眼 Herdr 里 agent 的终端，并回它一句 |
| `.` `。` | 标已处理：在跑的会话不再提醒，直到它有新输出 |
| `H` | 暂缓 1 小时再提醒 |
| `X` | 关掉它的 Herdr tab（先确认） |
| `Z` | 关掉空闲了几小时的 Herdr tab（Agents 页，先确认） |

**恢复框里**

会开进程、开 tab 或 App 的键按一下只把焦点移到那个按钮，再按一次或 Enter 才执行；和列表同义的键直接生效。

| 键 | 作用 |
|---|---|
| `r` | 在终端恢复（桌面 App 排第一时用它） |
| `t` `Ctrl+T` | 改用当前终端恢复，不进 Herdr |
| `p` | 在 Claude / ChatGPT 桌面 App 里打开 |
| `y` `Ctrl+Y` | 把 cd + 恢复命令复制到剪贴板 |
| `b` | 分叉：开一个带着这段历史的新会话 |
| `s` | 交接：写交接包，新开 Claude / Codex 会话先读它 |
| `i / c / o` | 用 IDE / VS Code / 文件管理器打开项目目录 |
| `n` | 只改标题 |
| `g` | 打开用过这个会话的 run 所属的任务（只在有时显示） |

**弹窗**

| 键 | 作用 |
|---|---|
| `Tab / Shift+Tab` `→ / ←` `l / h` | 在按钮和字段之间移焦点 |
| `Enter` | 按焦点所在的按钮；没移过焦点时按主按钮 |
| `Esc` `q` `n` | 关掉，或退回上一层；确认框里是取消 |
| `y` | 确认框里直接确认 |
| `1 / 2` | 新会话 / 交接框：选 Claude / Codex，再按一次同一个键才启动 |
| `e` `Ctrl+E` `y` `Ctrl+Y` | 交接框：编辑 / 复制交接包 |
| `1` `2` `3` | 瞄一眼：回答编号问题，同一个数字按两次才发出 |
| `:` | 瞄一眼：写一句话，Enter 作为 agent 的下一句发出 |

**其他**

| 键 | 作用 |
|---|---|
| `,` | 设置 |
| `?` | 打开帮助 |
| `Esc` | 关浮层；返回列表；清空筛选 |
| `q` `Ctrl+C` | 退出（Ctrl+C 在搜索框和浮层里也能用） |

</details>

### 两秒找一条：`tend fzf`

同样三个页面（收藏 / 会话 / Agents，`Tab` / `Shift+Tab` 轮换，`F1`–`F3` 直达），没有对话预览和项目视图，适合 SSH、低资源、或者你已经知道要找什么。
输入框直接写查询语法，边打边筛，过滤仍由 `tend` 做（和 TUI 同一个解析器）；Agents 页每 3 秒自动刷新。需要 fzf 0.46+，0.73+ 才有自动刷新。

| 键 | 动作 |
|---|---|
| `Enter` / `Alt+Enter` | 恢复（Herdr 在跑就开新 tab；在跑的会话是切过去 / 接管）/ 当前终端恢复 |
| `Ctrl+X` / `Alt+A` / `Alt+F` | 完成 ↔ 重开 / 归档 ↔ 取消 / 收藏 ↔ 取消（都是切换，和 TUI 的 `x` `a` `f` 一样） |
| `Ctrl+E` / `Ctrl+Y` | 用 `$EDITOR` 编辑 / 复制恢复命令 |
| `Alt+T` / `Alt+P` / `Alt+S` / `Alt+D` | 标签 / 项目 / 状态 / 时间筛选器，选完写回查询（`Ctrl+S` 也是状态） |
| `Ctrl+L` / `Shift+↑↓` | 重新加载 / 翻右栏预览（最近 40 句对话在下面） |

规则：`Ctrl+字母` = TUI 里同一个字母的动作，`Alt+字母` = 筛选器。取消收藏 / 归档之后那条先留在原位，再按一次就回来了，下次打字或切页才消失。

### 脚本和维护：命令行

```bash
tend list '#notes-api last:7d' --json    # 收藏
tend sessions 'webapp oauth' --json      # 全部会话
tend show <id> --json
tend grep '滚轮 加速 project:tend'      # 搜消息：关键词 + 筛选，按相关度列会话和片段（--json、--limit）
tend today / tend week [查询]             # 按项目看今天 / 本周做了什么：会话、AI 改的文件、提交；还没收藏的长会话（--json）
tend open <id>                           # 直接打开这个会话的界面，右栏聚焦（列表不显示的也行）
tend resume <id> --dry-run               # 只打印要执行的命令和检查项
tend resume <id> --no-herdr              # 当前终端恢复
tend resume <id> --workspace api         # 目录下有多个 Herdr workspace 时指定一个
tend resume <id> --app                   # 在桌面 App 里打开（Claude，Codex 用 ChatGPT）；--terminal 不管设置走终端

tend status <id> todo|doing|done  ·  tend done <id>  ·  tend archive|unarchive <id>  ·  tend favorite|unfavorite <id>
tend edit <id>                           # $EDITOR 里改标题 / 标签 / 摘要
tend pin <id>                            # 硬链保住会话记录文件（Claude 默认 30 天清 transcript）
```

坏会话（目录挪了 / 记录文件没了）：`fix` 和 `clean` 用同一张表，默认只列，选中才动：

```bash
tend clean                               # 列出所有恢复不了的会话：编号、原因、猜到的去向
tend clean provider:codex last:30d       # 筛选和 tend list 一样；目录参数只看它下面
tend fix 1 3                             # 目录挪走的：移到猜到的去向（先打印这几条，y/N 确认）
tend fix 2 --to ~/dev/proj               # 没猜到或猜错了就指定
tend fix all                             # 有唯一去向的全修，其余列出来跳过
tend clean 01a07dcf -y                   # 按会话 id 前缀清，-y 不问；进回收站，可还原
tend trash --restore 01a07dcf
```

编号跟着筛选变，要和刚才同样的目录 / 筛选词一起用。stdin 不是终端又没 `-y` 会直接报错；有失败项非零退出。

```bash
tend mv <旧目录> <新目录>                 # 等于 TUI 的 M
tend rm <id>                              # 单条进回收站；所有 <id> 也认会话 id 前缀
tend trash [--json]                      # 看回收站；--purge 清过期的，--purge --all 清空（会确认）
tend doctor [--compact]                  # 体检：数据文件、失效会话、回收站过期、空闲几小时的 agent、没人留的大文件；--compact 压实
```

### 其它机器：`hosts`

别的机器上的会话出现在同一个列表里，经 ssh 从那台机器上装的 tend 读取。每台机器要先在 `~/.ssh/config` 里有一个用密钥登录（不弹密码）的别名，然后：

```bash
tend hosts add mba mba --tend /Users/me/.local/bin/tend                          # 名字、ssh 别名、那边 tend 的绝对路径
tend hosts add win win-pc --tend 'C:\Users\me\.local\bin\tend.exe'
tend hosts add wsl win-pc --wsl Debian --tend /home/me/.local/bin/tend            # 那台 Windows 里的一个 WSL 发行版
tend hosts add box nas --docker dev --tend /usr/local/bin/tend                    # 那边的一个容器（--docker-cmd podman 或完整路径）
tend hosts install mba [--dry-run]       # 用当前源码按那边的系统编译 tend 并装过去（WSL、容器里也行），最后核对版本
tend hosts install win --build-there     # 让那台机器自己编译已推送的提交（那边要有 git，以及 go 或 mise），链路传不动二进制时用
tend hosts                               # 机器列表、各自应答的 tend 版本、列表上次什么时候取的
tend hosts check [名字…]                 # 连接、版本、系统、claude/codex 是否在 PATH、中文往返、列表和读消息耗时
tend hosts rm <名字…> · tend hosts clear [名字…]   # 删掉机器 / 清掉缓存的列表
```

`add` 会把机器写进 `~/.agent/tend/config.json` 的 `hosts` 并检查一遍（`--no-check` 跳过）；机器上还没有 tend 时，第一次 `install` 要带 `--os` 和 `--arch`。`--tend` 要写绝对路径：ssh 在那边起的 shell 常常不把 `~/.local/bin` 放进 PATH。远端 shell 按 `--tend` 猜（Windows 路径或 `wsl` 用 cmd），也可用 `--shell posix|cmd|powershell` 指定。

```bash
tend sessions host:all                   # 所有机器；host:mba 只看它；不写 host: 只看本机
tend show mba:<id> · tend resume mba:<id> # 远端会话：预览和检查来自那台机器，恢复执行 `ssh -t mba tend resume …`
```

TUI 里用机器筹码（`m`）选本机、全部或某一台；远端行带 `@名字`，预览和 Agents 从那台机器读，恢复在新的 Herdr tab 或当前终端里开 `ssh -t`。远端会话只读：收藏、打标签、归档、删除、搬目录都到它自己的机器上做。筛选里看得到的机器每 30 秒在后台取一次列表（失败后间隔翻倍，最长 5 分钟），缓存在 `~/.agent/tend/hosts/`；连不上时显示缓存的行和“离线 · 多久前”。

## 任务：把 agent 派到你的机器上

任务是一件写下来的事：标题、任务书（要 agent 做什么）、目录，以及默认的机器和 agent 档案。一次运行（run）是做它的一次尝试：
在某台机器上带着任务书启动一个 agent，一直跟到它结束。一个任务同时最多一个 run 在跑；每个 run 留着输出和它创建的会话，
可以看、可以恢复，也可以换台机器再跑一次。

```bash
tend task add "修掉不稳定的分页测试" --dir ~/dev/webapp --brief-file brief.md --agent claude
tend run start <task> --machine mba --wait   # 排到 mba 上，一直跟到结束
tend run list · tend run show <run> · tend run logs <run> -f · tend run stop <run> · tend run abandon <run>
tend run continue <run> "用 main 分支"   # 回复一个在等你的 run，在它自己的会话里接着跑
tend run answer <run> --allow | --deny | --answer pg   # 回答运行中的 run 在等的权限或提问
tend run send <run> "顺便更新 changelog"              # 给运行中的 run 发一条消息，在它当前这一轮里送到
tend task list [--all] · tend task show <id> · tend task edit <id> --title … · tend task done|reopen|cancel <id>
tend agent list · tend machine list [--connect]
tend inbox                                   # 所有机器上需要你的 run，等得最久的在前
tend journal verify · tend journal repair    # 检查协调器的任务日志；repair 截掉残缺或损坏的最后一行
```

TUI 里第 `5` 页是任务：`w` 新建，`e` 编辑，`x` 标完成 / 重新打开，`Enter` 打开任务对话框（选机器和档案跑起来、停止、放弃、接手），
`X` 停止当前 run，`b` 暂停或恢复任务树下的派发，`Space` 到会话页看这次 run 的对话。右栏是任务书、最近几次 run 和最近一次 run 的输出。
接手就是打开这次 run 会话的恢复框；run 还在驱动它时，恢复框会提示先停掉 run。
反过来，某个 run 用过的会话在卡片和右栏带上它的任务标签（id、标题、阶段），恢复框里 `g` 打开那个任务，`/` 按任务的标题或 id 也能搜到这个会话；
TUI 连上协调器（打开过任务页）之后才有标签。Web 的会话页也显示这个标签，点它打开任务。
`o` 轮换任务的排列：首页（等你的在上，在跑的在下；终端高于 24 行时底部再加机器和 7 天用量各一行）、列表、树（子任务在父任务下面）、
看板（按处境分列，`h` / `l` 在列间移动）。`:` 打开命令面板，用中文或英文说明都能找到任意列表操作；设置（`,`）里可以选皮肤、强调色（`#rrggbb`）
和标准或高对比度，和网页用同一个生成器。
任务对话框里的「拆解」让项目的 planner 起草子任务；草稿就在那里评审（子任务按父子排列，列出 planner 的问题），可以按草稿建任务、
在 `$EDITOR` 里改（和 `tend task draft --save` 读的同一种 JSON）或丢弃。「项目设置」把任务所属的项目当 JSON 编辑，运行对话框里可以编辑选中
agent 的定义或新建一个（Markdown）；协调器没收下的内容会留着，下次编辑接着改。
`Shift+↓` / `Shift+↑` 区间多选任务，`x` 或一个运行对话框作用于全部；「盯在旁边」把一个 run 的输出固定在详情下方，看别的任务时也在。
`u`（`Ctrl+Z`）在 6 秒内撤销刚才的收藏、归档、完成或重开；终端标题显示等你的数量。

**任务树。** 任务可以挂在另一个任务下面（`--parent`，最多三层），也可以排在别的任务之后（`--after t1,t2`）；`--backlog` 先放进待办，
开始之前不动它。`tend task start <id>` 开始一个任务和它下面的全部任务：前置任务完成后立刻派发，run 成功就标完成；父任务自己从不跑，
子任务都完成后等人验收。每个未完成的任务都写明现在的处境：运行中、排队（在等什么：前置任务、子任务、机器、同一目录里的另一个运行）或等人（为什么）。
派不出去的任务写明原因并停下，再开始一次就是重试。`tend task move <id> --parent … --after …` 调整位置。
`tend task pause <id>` 暂停一棵任务树（需求，或带子任务的任务）：下面在跑的照常跑完，不再派新的（就绪的任务、workflow 的下一阶段、排队的运行都不启动），
它的任务处境是「已暂停派发」；`tend task resume <id>` 恢复。暂停记在日志里，协调器重启后照样生效。

**档案。** 内置 `claude`（无界面的 `claude -p`，stream-json 双向收发；目录在某个 Herdr workspace 里时，改在新 Herdr tab 里开交互式
Claude）、`codex`（`codex app-server`）、`fake`（测试用）。更多的写进 `config.json`：

```json
{"agents": [
  {"name": "opus", "provider": "claude", "model": "opus", "permission": "acceptEdits"},
  {"name": "lint", "provider": "command", "command": ["my-agent", "--prompt-file", "{prompt_file}", "--dir", "{dir}"], "machine": "mba"}
]}
```

`command` 可以跑任意命令行：`{prompt_file}`、`{model}`、`{dir}` 会被填上，任务书从不出现在命令行上（`"stdin": true` 改成从标准输入给）；
`machine` 表示这个档案只在那台机器上跑。`machines.<名>.slots`（默认 2）限制一台机器同时跑几个 run；同一目录的 run 排队等前一个结束。

**agent 定义。** 定义是一个带 YAML frontmatter 的 Markdown 文件，格式照 Claude Code 的 subagent：

```markdown
---
name: careful
description: 慢一点，仔细一点
role: implement
profile: quick          # 或 provider: claude / codex，model: …
effort: high
permission: acceptEdits
tools: {deny: [WebFetch]}
machines: {prefer: [mba]}
---
所有东西读两遍。停下之前跑一遍测试。
```

`tend agent import careful.md` 保存它（里面写 `import: ~/.claude/agents/foo.md` 可以复用 Claude Code 的 subagent），之后按名字像档案一样用：
effort 和禁用的工具进 agent 的命令行，正文放在任务书前面。`tend agent defs | export | check | rm | share` 管理它们。没有 server 时它们是
`~/.agent/tend/defs/agents/` 里的文件；有 server 时定义归主人（或某个项目），分享给人、项目或所有人之后别人才能用（`--view` 让他们也能看正文）。`tend agent leave <名>` 不再使用一个不归你管的定义（点名、经项目或所有人分享到的都行），`tend agent transfer <名> --project <id>` 把你的定义转给你负责的项目。
对 claude：`hooks` 写进这次运行的设置（节点要打开 `node.allow_hooks`），`mcp` 按名字引用节点自己 `node.mcp` 里的服务器（值不离开那台机器），`skills` 要求那台机器已装好；`output`、`budget` 会保存但还不生效。

**工作流。** 任务可以分阶段走，而不是只跑一个 run：`tend task add … --workflow feature`（或者用项目的默认工作流）。内置
`feature`（实现 → 评审 → 验收）、`fix`（实现 → 测试 → 验收）、`docs`（实现 → 验收）。每个阶段按角色（`implement`、`review`、`test`）
用项目配置的 agent；评审或测试的 run 最后用 `tend run verdict pass|rework|blocked "…"` 给结论，要返工就退回实现阶段，实现者在自己原来的
会话里接着改，任务书是评审意见。run 自己的汇报（`tend run note|ask|verdict|plan`）不用等批准，codex 在 workspace-write 沙箱里也能写自己的 run 目录。写了 `check: true` 的阶段在 agent 结束后在那台机器上跑项目的 `hooks.check`（比如 `mise run gate`），
失败就算返工；节点要打开 `node.allow_hooks` 才接这种 run。退回超过 `max_loops` 次、结论是 blocked、或预算用完，任务停下等人。最后一关是
人工验收：验收人放行（`tend task gate <id> --pass`），任务相关的人都能带着意见打回（`--rework "…"`）。`tend task message <id> "…"`
会进正在运行的 run、接着这个阶段上一个 run 的会话，否则记进任务的工作记录，每个阶段的任务书都会带上它。项目可以用 Markdown 定义自己的工作流：
frontmatter 写阶段，每个 `## <阶段名>` 小节是那个阶段的任务书模板（`{{task.brief}}`、`{{task.acceptance}}`、
`{{#rework}}…{{rework.notes}}…{{/rework}}`、`{{workpad}}`）。任务拿到工作流时就定下来，之后改定义不影响它。

**拆解。** `tend task plan <id>`（或点「拆解」）让项目的拆解 agent 读任务（需求就是 issue 的正文）和仓库，用 `tend run plan` 交回子任务：
标题、任务书、验收标准、谁先谁后，最多两层，还有要你决定的问题。这时什么都还没建：计划是一份草稿，你可以改、回答问题（拆解 agent 在同一会话里
重出）或丢弃；「按草稿建任务」（`tend task draft <id> --apply`）把它们放进这个任务下的待办，开始这个任务就按依赖派发。草稿绑定生成它时的
issue 版本。

**分支。** 项目的仓库标上 `worktrees` 后，每个任务在自己的分支 `tend/<任务>` 上、在 checkout 旁边的工作区（`<checkout>-wt/<任务>`）里做，
你自己的 checkout 不动。工作区建好后跑一次项目的 `hooks.setup`，每次运行开工前在里面跑 `hooks.before_run`（失败则这次运行失败）。agent 没提交的改动由 tend 替它提交；评审和测试在分支的只读副本里跑，结束后
丢弃（run 会写明它在副本里改了几个文件）；codex 评审可以写这个副本，好构建和跑测试，claude 评审不给编辑工具。子任务从父任务的分支切出来，可以并行；每个子任务完成前先合进父任务的分支，所以排在它后面的任务
一开始就带着它的代码。合并冲突时 tend 撤销合并并停下等你：在父任务的工作区里自己合好并提交，再 `tend task merge <id>`（或点「重试合并」）。
顶层任务完成后显示「可合并」，合进 `main` 由你来。配了 `remote` 时每个 run 结束都推分支、开始前先拉，阶段可以换机器；没配时一棵树留在最先
开工的那台机器上。`tend run note --pr <url>` 记下 PR。

**跑之前。** `tend run start` 先说明 run 会怎么跑：那台机器上 agent 命令行的版本、是否登录，以及要不要等机器、槽位或目录。
命令行没装或没登录时 run 会直接失败，所以不派发（`--force` 强制派发；节点那边也会以同样的原因拒绝）。TUI 和网页的派发框显示同样的内容；
`tend machine list` 多了 AGENTS 一列。

**run 需要你的时候。** 后台的 claude 和 codex run 会一直保持对话：权限请求（claude 配 `"permission": "default"`、codex 的审批）
或它提的问题（claude 的 AskUserQuestion、codex 的 user input）会等着，直到你用 `tend run answer`、TUI 的任务对话框或网页回答；
`tend run send` 在它干活时给它发消息（claude 在当前这一轮读到，codex 会调整正在跑的这一轮）。run 会显示它最新说的话和到目前的花费
（tokens，claude 另有估算的费用）。后台 agent 也会被告知怎么以提问结束：最后一条消息以 `ASK:` 开头，或者执行 `tend run ask "…"`
（`tend run note "…"` 报告进展；两者都经 `TEND_RUN_DIR` 写进 run 的状态）。权限模式没问就拒掉的工具会列出来。
这样结束的 run 显示为「在问你」或「等你批准」；`tend run continue <run> "…"`（TUI 和网页里的
**回复**）带着你的回答，在同一个会话里起一个新的后台 run。`tend run continue --session <id> "…"` 对任意已索引的会话也一样。
失败的 run 会说明原因：`cli_missing`、`auth_missing`、`auth`、`quota`、`rate_limit`、`overloaded`、`context_overflow`、`network`、
`session_missing`、`permission_denied`，附上命令行的原话和下一步（`tend run show`）。15 分钟没有输出的 run 标为「疑似卡住」
（`node.stall_after`，`"off"` 关闭），只标记，不会停掉；运行中的 run 显示最后一次输出的时间（到分钟）。在 Herdr tab 里跑的 run：
transcript 停在一个提问上标成「在问你」；（装了 `tend install-hook` 时）Claude 最新的 hook 事件是权限提示，标成「等你批准」并写出工具；
别的提示或 Herdr 显示它的 pane 卡住，标成「等你处理」；transcript 一直不长、又不在等人、这一轮也没结束，同样标「疑似卡住」。`tend inbox`、TUI 任务列表顶部和网页的
**待处理**计数把所有需要你的 run 放在一起，等得最久的在前。

想收到通知就配一个命令：`"notify_command": ["my-notifier"]` 会在 run 需要人的时候运行，标准输入是一个 JSON 对象（`event` 为
`run.waiting`、`run.asked`、`run.permission`、`run.failed` 或 `run.stalled`，还有 run、task、title、machine、agent、state、reason、detail、ask）；
`notify_events` 可以只选其中几种。任务事件 `task.needs_you`、`task.done` 和 `task.tree_done` 只有在 `notify_events` 里点名才发。
`task.tree_done` 是「完工」：一棵任务树的根变成完成（根自己的 `task.done` 照发），JSON 带 `summary`：完成的叶子数、取消的、运行次数、
第一个 run 开始的时间、完工的时间和子任务合进去的分支。TUI 在底栏、网页用一条提示告诉你，根的详情里一直留着这份摘要。

**谁在协调。** 同一时刻只有一个进程记任务日志、派发 run：谁拿到 `~/.agent/tend/coord/` 里的锁就是谁——打开任务页的 TUI、
执行期间的 `tend task|run …` 命令，或你常驻的 `tend service`。其它进程经本机 socket 找它。run 不依赖它：
每个 run 在自己的机器上有一个监督进程，记下 run 怎么结束；下一个协调器读到后补上记录。

**机器，模式一（ssh）。** 本机加上配置里的每台主机（上面的 `tend hosts add`）。协调器用 `ssh <别名> tend node --stdio` 连过去；
连接断了 run 照常跑。

**机器，模式二（server）。** 常驻的 `tend-server` 把任务日志存在 SQLite 数据库（`coord/tend.db`）里，各台机器连上来。它是单独的程序，每次发布和 `tend` 一起出，也可以用 `tend hosts install <机器> --server` 装：

```bash
# server 上（回环或 tailnet 地址；其它地址要 --tls-cert/--tls-key，或在转发之后用 --plain）
tend-server token add --node mba          # token 只打印这一次，只存它的哈希
tend-server token add --client laptop
tend-server --listen 100.101.8.10:7788

# 每台跑 agent 的机器：config.json 里要有 "node": {"allow_dirs": ["~/dev"]}
tend node --connect ws://100.101.8.10:7788 --token-file ~/.config/tend/node-token
# 或者让它登录即启动：LaunchAgent（macOS）、systemd --user（Linux）、计划任务（Windows）
tend node install-service --connect ws://100.101.8.10:7788 --token-file ~/.config/tend/node-token   # 先用 --print 看一眼

# 客户端：通过浏览器登录（设备码，在打印出的地址上核对后允许）
tend login http://100.101.8.10:7788   # 自动写好 coordinator.token 和 config.json 的 coordinator
tend task list   # 命令行和 TUI 的任务页都改为和 server 说话
```

节点只在 `allow_dirs` 里跑。没设 `node.allow_bypass` 时：`command` 档案只按节点自己 `config.json` 里的定义运行；claude / codex 的
`args` 只接受节点自己档案里的；权限模式只放行 `default` / `manual` / `acceptEdits` / `plan` / `dontAsk`（claude；不含 `auto`）和 `read-only` / `workspace-write`（codex）；
带已知绕过参数的命令行拒绝。设了 `node.allow_profiles` 时只跑这些名字，且都按节点自己的定义（要在节点上定义好，带上需要的权限）。
`tend-server token rm <名>` 吊销 token 并断开它的连接。节点 token 绑定第一台用它连上的机器，换机器会被拒绝，要先 `tend-server token rebind <名>`。

**成员。** server 给一个团队用。成员用 GitHub，或 server 的 `config.json` 里列出的任意 OIDC 服务（Gitea、GitLab、Keycloak、Google）登录：

```json
{"server": {"public_url": "https://tend.example", "logins": [
  {"name": "gitea", "kind": "oidc", "display": "Gitea", "issuer": "https://git.example", "client_id": "…", "client_secret_file": "/etc/tend/gitea-secret"},
  {"name": "github", "kind": "github", "client_id": "…", "client_secret_file": "/etc/tend/github-secret"}]}}
```

回调地址是 `<public_url>/auth/<name>/callback`。不开放注册：`tend-server admin add ann@corp.example --role admin`
建第一个管理员；之后由管理员在团队页加准入规则，或用 `tend-server admin add <邮箱 | 域名 | provider:用户名>`（已验证的邮箱、
某个域名下任何已验证的邮箱、某个账号），也可以发一次性邀请链接（`tend-server admin invite` 或团队页，3 天有效），邀请还可以让对方加入某个项目，身份是参与或只读（`--project`、`--access`）。
`tend-server admin disable <用户>` 让这个人在所有地方立即下线。

任务归属于项目。管理员建项目，项目负责人把成员加为「参与」（建任务、派发、回答、发消息）或「只读」（只能看）。
不在项目里的人看不到这个项目的任何东西，连它是否存在都不知道；不属于任何项目的任务只归创建人。机器也默认私有：
谁添加的机器就归谁（目前由服务器管理员给它发节点 token：`tend-server token add --node <名字> --owner <用户>`），分享给别人或项目之前，只有主人能往它派发。
机器上的 run 用的是主人的 claude / codex 登录、git 身份和文件，所以要分享的机器请专门准备（单独的系统用户或容器），
不要分享自己的笔记本。run 的权限请求由机器主人和派发人批准；分享时可以让被分享的人也能批。

项目负责人还设定项目的任务怎么跑：放在每份任务书前面的项目说明、仓库和它在每台机器上的路径（没写目录的任务就用它）、
默认 agent 和机器，以及 hooks。任务有负责人和验收人；任务停下来等人时，会通知它的负责人、要验收时的验收人，以及相关 run 的派发人：
出现在网页的**等你**列表里，网页开着时弹浏览器通知，也会发到个人 webhook（账号页设置；POST 一段带 `text` 字段的 JSON，
适配 ntfy、Slack 等，配了 `public_url` 时带任务链接）。server 只往公网地址发；团队内网或 tailnet（`100.64.0.0/10`、`fd7a:115c:a1e0::/48`）里的 webhook 或工单系统，要把它的网段写进 `server.egress_allow`（`["10.0.0.0/8"]`）。管理员在团队页用**交接并停用**把离开的成员的项目、任务和定义交给别人，并让其凭据全部失效；其机器标为已退役，上面没结束的 run 进管理员的**等你**。

**工单。** 项目可以跟一个 Gitea、GitHub 或 GitLab 仓库同步（项目页的**工单同步**）：填地址（GitHub 填 `https://github.com`）、
仓库（`owner/name`，GitLab 可带子组）和机器人账号的 token
（加密保存在 server 上，密钥在 `server.key` 或 `TEND_SERVER_KEY`）。打了标签（默认 `tend`）的 issue，或可选地指派给项目成员的 issue，
会成为项目的需求；指派人用这个工单系统登录过 tend 的，就归他负责。tend 在每个 issue 上只维护一条进度评论，需求完成后关单；需求重开或撤销完成时，tend 重新打开自己关的单（在外面关的不动）。
issue 改了，需求会停下来等人选「采用新版本」或「维持本轮范围」；issue 在外面被关掉，由人决定是否继续。server 默认每 60 秒轮询一次；
在仓库里加一个指向 `<public_url>/hooks/<id>` 的 webhook（密钥在绑定时只显示一次；GitHub 的 Content type 选
`application/json`，GitLab 把密钥填作 Secret token）会更快。token 被拒时绑定停下并通知项目负责人和管理员，限流时暂停。项目负责人和管理员在每个需求上能看到它的 issue 同步得怎样：上次同步的时间，是否待同步或同步失败，以及错误和下次重试的时间。

**Web UI。** server 在自己的地址上还提供一个网页（`http://100.101.8.10:7788/`）。用登录服务或 token 登录；
浏览器会话保持 30 天，退出登录或吊销即失效。网页列出任务和它们的 run；新建、编辑、派发任务；派发前预检；
跟看 run 的输出和对话；显示 run 为什么结束、问了什么并接受回复；停止或放弃 run；把任务标为完成、重开或取消；
显示任务树并开始它们；列出等你处理的事；编辑和分享 agent 定义与项目设置；查看机器（状态、运行位、agent CLI、排队和当天的运行）、它们的主人和分享对象；机器的主人和管理员能读它自己的 Claude / Codex 会话（筛选、往前翻对话、复制恢复命令；节点要在 `node` 里写 `"share_sessions": "all"` 才共享全部会话）；添加机器并一次性给出节点 token，换机或吊销节点 token；管理项目和成员；在账号页为 CLI 和 TUI 生成个人 token，
或者在终端授权页确认 `tend login` 发来的一次登录（设备码、客户端名字、来源地址和时间、允许或拒绝）；管理员还能管理用户、
准入规则、邀请和审计日志。它实时跟随任务日志，断线后自动重连。**首页**最上面是等你的事，问题和工具请求就在那里回答、批准或拒绝；下面是在跑的 run 和机器、
每个项目的进度（点计数进看板）、7 天的 token 和花费、最近的会话。任务可以看列表，也可以看按处境分列的看板，按状态、机器、项目、阶段、运行状态筛选；
视图、筛选、选中的任务和标签页都在地址里，刷新或把链接发给别人看到的是同一个画面。`⌘K`（`Ctrl+K`）打开命令面板，用中文或英文名都能找到任意操作或任务；每个操作也都有键
（`?` 列出全部：`n` 新建任务、`g h` 首页、`g b` 看板、`d` 派发、`p` 暂停或恢复任务树的派发……）。**等你**页写明每条为什么是你（你负责、你验收或你派发），
按类型和身份筛选，并就地作答：`1` 允许、`2` 拒绝（可附理由）、数字选第几个选项，或者自己写。**运行**页（`g r`）按状态和机器列出全部 run，
选中一条预览它最近的输出。任务详情写出 workflow 预算和各 run 已花的对比；agent 定义页写出它被哪些项目和任务用着、运行时启动的命令，
可以导入导出 Markdown；项目设置能逐台检查仓库目录；工单绑定的**同步记录**列出它跟着的 issue，并预览进度评论；
团队页列出待接受的邀请可作废，交接前先列出每样东西交给谁。**设置**里可以选主题（浅色、深色或跟随系统）、皮肤（tend、森林、
余烬、石墨，也可以换强调色）、标准或高对比度、三档密度，存在浏览器里；皮肤由 server 生成（`/theme/<name>.css`），不管强调色怎么选，
文字对比度都不低于 4.5:1（高对比度 7:1）。
手机上用 HTTPS 地址打开时，网页可以装到主屏幕（账号页写明怎么装：Safari 里「分享」再「添加到主屏幕」，Android 浏览器菜单里的「安装应用」）；
装好的页面从另一台设备登录：在已登录的地方打开它给的链接并允许。账号页的「推送到这台设备」打开后，有事等你、网页上一会儿
没人处理（权限请求 30 秒，其余 60 秒），就推到这台手机或电脑，网页关着也收得到；服务器重启后没推完的照推。每人最多十台这样的设备，推送地址要在浏览器厂商自己的推送服务上（`server.push_services` 可以另列）。地址不是 HTTPS 时账号页写明装不了、收不到推送：
用 tailscale serve 把 server 放到 HTTPS 地址上，或用 `--tls-cert/--tls-key` 给它证书。

**server 的数据库。** `tend-server import` 把模式一的日志（`coord/events.jsonl`）搬进数据库，要先停掉 server；
这份日志里有事件而数据库还不存在时，server 拒绝启动。server 运行时也可以用：`tend-server db check` 检查数据库，
`tend-server export -o f` 把它写成 `tend journal verify` 能读的日志，`tend-server backup [目录]` 把它连同 `coord/id`
和配置复制到一个新目录。成员、他们的登录账号、凭据（只存哈希）和审计日志都在同一个数据库里。

client token 或已登录的浏览器可以在分享给这个用户的每台机器上运行 agent（受各节点的限制）：server 只部署在 tailnet 内，
或放在 TLS 后面（`--tls-cert/--tls-key`，或在终止 TLS 的反代后面用 `--plain`）。
不在 server 这台机器上的代理（比如容器前面的转发）写进 `server.trusted_proxies`（`["172.16.0.0/12"]`），登录限流才按每个人自己的地址算。

## 查询语法

`#标签`、`project:x`、`provider:claude|codex`、`host:all|local|<名字>`（其它机器，见上）、`status:open|active|done|archived|trash|all|live|agent`、
`after:2026-09-01`、`before:…`（按会话开始时间）、`last:7d` / `last:2026-09-01`（按最近活动：上周开始、今天还在用的也算）、`turns:3`、`file:internal/index`（AI 写过路径含这段的文件），以及普通关键词。筛选行的时间框里还能手输 `09-01`、`09-01..09-15`、`..09-15`、`7d`。全部 AND，中文直接子串匹配。
以 `>`（或 `》`）开头改搜消息正文：关键词在每条消息和工具命令里找，筛选词只限定会话范围（默认 `status:all turns:0`）。每个关键词都要在会话里出现；
关键词的词项命中六成就算中（中文按相邻两字切，关键词内部不讲词序；用引号包起来——`"…"`、`“…”` 或 `「…」`——就必须原样连着出现；`a|b` 两个有一个就算，`-x` 去掉含 x 的消息，`who:me`、`who:ai` 或 `who:tool` 只看某一方说的；英文词拼错、会话里又几乎没出现过时，也会顺带搜只差一个字母的常见词，标题里写明「也搜了 …」）；BM25 排序，关键词挨得近、消息越新、是你自己说的（工具命令、工具输出和 Claude 的续接摘要权重低）、标题摘要标签里也有关键词的，都加分；有一条消息同时含全部关键词的会话排前面，`o` 切到按最近命中排。
默认看未归档的；关键词也搜索引里的用户提示语——记得「让它做过 X」就能搜到。三个前端共用同一个解析器。这份语法在 TUI 里也有（`?` → 搜索语法），搜索框只输入 `>` 时列表区也会显示，`tend grep --help` 同样会列出。

## 工作原理

**索引。** `internal/index` 扫 `~/.claude/projects/*/*.jsonl` 和 `~/.codex/sessions/**/rollout-*.jsonl`，每个文件记 session id、
cwd、分支、开始时间、人说话的轮数、标题、全部提示语（封顶 8KB，只用来搜）。transcript 是 append-only 的，索引记「读到哪了」，
下次只补读新增；对话预览从文件尾向前分块读，翻到哪读到哪。`-p` / SDK 会话、Codex 子代理线程、一句话没说过的不列。

**消息正文。** `internal/fulltext` 在 `~/.agent/tend/text/` 给每个 transcript 存一份正文（TSV：偏移、角色、时间、文本），
只含说的话和工具命令，不含工具输出；跟索引一样在每次刷新后增量补读。搜索时并行流式扫候选文件，不维护倒排索引，几百 MB 不到一秒。

**收藏。** Skill 只负责理解会话，输出一段 JSON；校验、采集环境、存储、幂等（provider + session id）都在 `tend add`。
`records.jsonl` append-only，最后一行为准。

**恢复。** 先校验（provider 在 PATH、目录存在、记录文件还在、分支是否变了），再决定路由：
会话已在某个 Herdr tab → `herdr tab focus`；Claude 后台会话 → `claude attach`；Herdr 可达且能按 workspace 或目录找到位置 → 新 tab 里恢复，
TUI 不退出；否则 `cd` 到记录目录在当前终端 `exec`。路径或 workspace 对不上时报错列候选，不静默猜。

**在跑的会话。** Claude 写 `~/.claude/sessions/<pid>.json`（busy / idle），Codex 开着的线程持有 `thread-writer-locks/*.lock`，
Herdr 多知道 tab 和「等你」；三者非空字段叠加。Claude Code 上下文用完时会把对话交给一个后台工作进程、换一个 session id，原进程只当终端：
tend 把这条链合成一个会话（轮数相加、用最新的 id 恢复、收藏跟着走），停车的原进程不算在跑。

## 数据

| 文件 | 是什么 |
|---|---|
| `~/.agent/tend/records.jsonl` | 收藏，append-only 一行一条；可以 grep、手改、git 同步 |
| `~/.agent/tend/sessions.jsonl` | 会话索引缓存，只存提示语；删了下次启动重建 |
| `~/.agent/tend/text/` | `>` 搜消息用的正文副本，一个 transcript 一个文件，外加 `vocab.json`（出现过的英文词，用于纠正拼写）；删了自动重建 |
| `~/.agent/tend/trash/` | 回收站：被删的会话文件按原样挪进来，`manifest.jsonl` 记着来处 |
| `~/.agent/tend/config.json` | 设置面板写的 |
| `~/.agent/tend/coord/` | 协调器的任务日志（`events.jsonl`，每次变化一行，带校验和） |
| `~/.agent/tend/node/runs/` | 本机每个 run 一个目录：冻结的命令、状态、输出日志；结束一周后删除 |
| `<server home>/coord/tend.db` | 只在 `tend-server`：任务日志、成员、登录账号、凭据哈希和审计日志（SQLite） |
| `~/.agent/tend/hosts/` | 从每台其它机器最近取到的列表（只有列表字段：标题、摘要、标签、路径；没有消息）、ssh 连接复用的 socket |

环境变量：`TEND_HOME`（或 `TEND_HOME`）改数据目录，`TEND_UI=fzf|tui` 改默认前端，`TEND_ICONS=nerd|ascii` 选图标，`TEND_TRACE=1` 把按键、滚轮和后台事件的时间线记到 `~/.agent/tend/trace.log`（报告界面卡顿时用）。
界面语言默认跟系统（`LANG` 等以 zh 开头是中文，否则英文），设置里可固定。

聊天正文只在本机 `text/` 里存一份供搜索，不上传任何东西；只在你点名移动目录时改会话文件里的 cwd，改之前原件先进回收站。`tend pin` 只在本机做硬链。

## 文档与开发

```bash
go build ./... && go vet ./... && go test ./...
HERDR_LIVE=1 go test ./internal/herdr/   # 实跑 Herdr 建 tab → 执行 → 清理
go run ./tools/fixture -o ~/tend-demo     # 造一台合成机器（Claude + Codex 会话、收藏）；~/tend-demo/tend.sh tui 在它上面跑 tend
scripts/test-hosts.sh ssh:host wsl:host:Debian win:host docker:host:ctr   # 同步工作区，在各目标机本地编译并测试
```
