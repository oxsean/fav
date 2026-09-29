# FZF 模式与预览

`tend fzf` 的三页结构、候选行、键位规则、版本门槛，以及 `tend preview` 给两种界面共用的预览内容。实现：`internal/ui/fzf`（只做编排）、`internal/render`（候选行与预览）、`cmd/tend`（`fzf-list` / `fzf-tab` / `fzf-pick` / `preview` / `pick()`）。

## FZF 模式要求

- `--reverse`，查询框在顶部；左侧候选、右侧 preview，比例 45:55。
- **三个 tab 在同一个 fzf 进程里**：收藏 / 会话 / Agents，`Tab` / `Shift+Tab` 轮换（没有 `--multi`，Tab 是空的），`F1`–`F3` 直达（`Alt+1/2/3` 也绑着但不写：Herdr 占了 Alt+数字；Ctrl+数字终端发不出来；Ghostty 默认不把 Option 当 Alt，`Alt+F` / `Alt+Enter` / `Alt+D` 等要在它的配置里加 `macos-option-as-alt = true` 才有反应），初始 tab 跟 `config.default_view`（项目视图没有 fzf 版，落到收藏）。
  当前在哪个 tab 只记在 prompt 里（`收藏 > ` / `会话 > ` / `Agents > `）：fzf 把 prompt 以 `FZF_PROMPT` 传给每个子命令，
  所以候选源统一是 `tend fzf-list {q}`，它按 prompt 决定查收藏（`store.Query`）、会话（索引 + 收藏，同 `tend sessions`，每次按键重扫一遍索引约 50 ms）
  还是在跑的（`capture.LiveSessions()`，索引里没有的刚开会话按来源给的标题和目录凑一条，按开始时间排）。
  切 tab 是 `transform(tend fzf-tab <名字>)`：tend 打出 `change-prompt + change-header + change-footer + reload` 一串动作，fzf 照做。
- Agents 页 `every(3)` 触发 `tend fzf-tab tick`，只有在 Agents 页才打出 reload；`--track --id-nth 1` 让重载后光标钉在同一条上。
  版本门槛：`transform` / `FZF_PROMPT` 0.46（低于它 `tend fzf` 直接报错让升级）、`--footer` 0.63、`--id-nth` 0.71、`every` 0.73，缺哪个降级哪个（没有 timer 时 Agents 页靠 `Ctrl+L` 手动刷新，header 里说明）。
- 候选行首字段是隐藏的 key（`--delimiter=\t --with-nth=2`）：record id，没收藏的会话是 session id——`pick()` 找不到记录时按 session id 前缀到收藏、索引、在跑的会话里找，
  所以 `preview / resume / status / tend / archive / edit / rm` 对没收藏的会话都能用，改状态时才建记录（不算收藏）。
  可见部分是 图标（没收藏 `o`，收藏了按状态 `*` / `✓`，所以 Alt+F 后一眼能看出变了）· 时间（定宽 11）· 轮数（Agents 页换成 等你 / 工作中 / 空闲多久）· 标题 · 来源 · 项目 · 标签。
- 绑定命令由 fzf 起的 shell 执行，tend 按那个 shell 转义自身路径：macOS / Linux 从 0.51 起用 `--with-shell "sh -c"` 指定 sh；Windows 跟 fzf 自己的选择（`$SHELL`，没设就是 cmd），不用 `--with-shell`（指定后 fzf 会按 POSIX 转义 `{q}`）。子选择器（标签 / 项目 / 状态 / 时间）因为嵌套 fzf 需要 TTY，先 `execute(tend fzf-pick <kind> {q})` 把新查询写进 `$TEND_PICK_FILE`，再 `transform-query(tend fzf-pick read {q})` 读回，不依赖 `>` 和 `cat`，Windows 的 cmd 下也能跑；子进程继承 `TEND_SHELL`，所以复制的命令跟随用户自己的 shell。
- Preview 由 `tend preview <key>` 动态生成。
- 过滤全部交给 tend（`--disabled` + `change:reload`），保证两个 UI 共用同一个查询解析器（见 [index-and-search.md](index-and-search.md)「查询语法」）。以 `>` 开头的查询在收藏 / 会话两页搜消息，见 [index-and-search.md](index-and-search.md)「搜消息（`>` 前缀）」。
- 键位规则：**Ctrl+字母 = TUI 里同一个字母的动作**（Ctrl+X 完成 ↔ 重开、Ctrl+E 编辑、Ctrl+Y 复制，归档是 Alt+A——Ctrl+A 是 Herdr 前缀，都是 `fzf-pick toggle*` / 子命令 + reload），动作后的那次重载带 `--keep <key>`：那条即使已经不匹配（取消收藏、归档）也留在原位，直到下一次重载才消失，给人按回去的机会（对应 TUI 的 pin）；不按时间消失，因为正看着的行突然没了更糟，fzf 也做不稳定时器；
  **Alt+字母 = 筛选器**（Alt+T 标签 / Alt+P 项目 / Alt+S 状态 / Alt+D 时间，Ctrl+S 也是状态因为对得上 TUI 的 `s`）。
  例外：Alt+F 收藏 ↔ 取消（TUI 的 Ctrl+F 是翻页、fzf 的是光标右移）、Alt+Enter 在当前终端恢复（`become(tend resume --no-herdr)`，TUI 里是弹框后按 `t`）。
  不给 D 删除 / M 移动：fzf 弹不出确认框。不占 fzf 自己的 Ctrl+P（上一条）、Ctrl+U（清空输入）、Ctrl+D。
- Enter 在跑的会话上和 TUI 一样是切 tab / attach（`tend resume` 自己判断）。
- 没有项目视图；预览（`tend preview`）在卡片之后附最近 40 句对话（每句最多 6 行，读文件尾 1MB），翻页、查找、全文在 tend tui。
- Windows 上 TUI 是主界面，`tend fzf` 只是备用入口，保证能用即可、不单独投入：那里用 fzf 的多是 PowerShell 用户（PSFzf），而 fzf 执行绑定默认用 cmd。
- 其它机器的行怎么进 fzf（`fzf-list` 用 30 秒内的缓存）见 [remote.md](remote.md)「fzf」。

fzf 能力所限的两点：说明只能放 `--header` / `--footer`（候选区只能放候选项），两者都只占候选列 45% 且不折行，切 tab 时按终端宽度（子进程里读 `FZF_COLUMNS`）自己切行——
键位放 footer、说明放 header，没有 footer 就都放 header；当前查询串本身就在 prompt 行。
中文实时计数做不到——`--info` 的格式固定为 `1/67`，`--info=hidden` 后自写的 header 不会随输入更新。

## Preview 内容

- 标题与状态（待办/进行中/已完成/已归档 + 是否收藏 + 已固定）。
- Provider、收藏时间、恢复次数与最后恢复时间。
- Project、work type、Git branch、cwd、完整会话 id（`card.session`）、Herdr workspace/tab、标签。
- Summary（没收藏的会话是索引里的第一条提示语）；轮数与最后写入时间。
- `tend preview`（fzf 右栏）在卡片之后附最近 40 句对话，每句最多 6 行。
- 恢复将采取的路径。
- 逐条校验：Provider 是否在 PATH、cwd 是否存在、**会话记录文件是否仍然可用**、当前分支是否与收藏分支不同。

预览不展示敏感环境变量；对话按需加载，见 [index-and-search.md](index-and-search.md)「最新动态与对话」。
