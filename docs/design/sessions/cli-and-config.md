# CLI 与配置

会话这一半的全部子命令、输出与确认的约定，以及设置面板的配置项和环境变量。实现：`cmd/tend`（子命令、`newFlags`）、`internal/tend/config.go`（`config.json`）、`internal/ui/tui`（设置面板）。

## CLI 设计

```text
tend                              默认 UI（TEND_UI=fzf|tui）
tend tui [--no-mouse] | tend fzf
tend add [--supersede <id>] [--session-id <id>] [--provider <p>]
                                       从 stdin 读 /tend 的 JSON；会话目录属于某个项目时 project 写成项目名（「项目表」）
tend list [表达式] [--json|--line] [--limit N]
tend sessions [表达式] [--json] [--limit N]   本机全部会话，收藏的和没收藏的都在
tend show <id> [--json]
tend preview <id> [--width N]
tend grep <关键词 + 筛选> [--json] [--limit N]   搜消息（index-and-search.md「搜消息（> 前缀）」）；前面的 > 可省；先同步正文库，≥20 个文件时 stderr 报进度；
                                       不认识的 -x 当排除词不当参数（queryDashes，fzf-list 同样），只有 -h / --help 例外
tend open <id>            TUI 直接停在这个会话上、右栏聚焦；id 也能是 skip 的 transcript（SDK 拉起、已退出的 agent），Model.extra 让它出现在会话页
tend edit <id>
tend status <id> todo|doing|done
tend done|archive|unarchive <id>
tend favorite|unfavorite <id>
tend pin|unpin <id>
tend rm <id> [-y]                      挪进回收站；正在跑的拒绝（别名 delete）
                                       所有 <id> 都先按 record id 找，找不到当 session id（前缀也行，多于一条命中报歧义）
                                       到收藏、索引、在跑的会话里找，最后按前缀认索引里 skip 的、没说过话的文件和续接链的旧 id；
                                       没收藏的会话第一次改状态 / 编辑时建记录（不算收藏）。
tend trash [--json] [--restore <id|sid>] [--purge [--all] [-y]]
                                       --purge 只清过期；--purge --all 清空是唯一不可逆的删除，先问 y/N
tend mv <旧目录> <新目录> [-y]          移动项目（和 TUI 的 M 同一段代码）；有会话在跑就拒绝（别名 move）
tend resume <id> [--dry-run] [--no-herdr] [--app|--terminal] [--workspace 名字] [--fork]
tend rpc [--stdio]                     远端应答（remote.md「远端协议」），给 ssh 调用
tend hosts [check [名字…] [--timeout d]]
tend hosts add <名字> <ssh 别名> [--tend 路径] [--wsl 发行版 | --docker 容器] [--shell] [--no-check]
tend hosts rm <名字…> · tend hosts clear [名字…]
tend hosts install <机器> [--os --arch] [--src 目录] [--dry-run] [--server] [--build-there]
tend handoff <id> [--to claude|codex] [--dry-run] [--no-herdr] [--workspace 名字]
                                       写交接包（resume.md「分叉与交接」）；没有 --to 时把包打到 stdout
tend today | tend week [查询] [--json]  日报：today 从今天 0 点、week 从本周一 0 点起动过的会话（last:，默认 status:all turns:1，查询可再收窄），
                                       按项目分组（组多的在前）：每条会话的来源、总轮数（索引没有按天的轮数）、最近活动；组里 AI 改得最多的 5 个
                                       文件；项目目录（主仓库优先）里这段时间的提交（git log --since --no-merges，显示 3 条标题）；
                                       最后列出 ≥20 轮还没收藏的会话，提示 tend open 后 /tend。
tend fix [目录] [查询…] [编号|会话id…|all] [--to 目录] [-y]
tend clean [目录] [查询…] [编号|会话id…|all] [-y]
                                       位置参数怎么分：带路径分隔符的是目录；整数和 all 是选择；只有十六进制且正好命中一条的
                                       是会话 id 前缀；其余按查询语法（index-and-search.md「查询语法」）筛（provider: project: status: after: before: last:
                                       turns: #标签 关键词），没写 status: 就不分状态、没写 turns: 就不嫌短。
                                       同一张「恢复不了的会话」表（索引 + 收藏夹，cwd 在目录下；不给目录 = 全部；
                                       按目录、来源、会话 id 排，编号稳定）：目录不在 / 记录文件不在，在跑的标出来跳过。
                                       不带选择只列（默认 dry run）；带编号 / 会话 id 前缀 / 记录 id / all 再确认一次才动（-y 跳过确认）；
                                       数字在表的范围内才算编号，超出的当 id 找（记录 id 是十六进制，可能全是数字）。
                                       fix 只对「目录不在」的：去向 = FindMissing 猜的唯一候选（删掉的 worktree 就是主仓库；否则现存 cwd 的父目录、旧目录的
                                       兄弟目录里找同名，有 remote 的要求 origin 一致），不唯一就要 --to：点名选的没给 --to 整批不动并报错，all 则跳过它们、修其余的并列出跳过的；逐条 PlanMove+Only 移，
                                       原件进回收站。clean = tend rm 的批量版：文件进回收站、收藏记录打墓碑，可还原。
tend doctor [--compact]                 顺手清掉回收站里过期的；列出同一张表并指向 fix / clean；
                                       空闲的 agent：在跑但记录文件 4 小时没写的，写出在哪（Herdr tab / 后台 / 终端），指向 Agents 页 Z；
                                       大文件：会话文件合计 ≥100 MB、30 天没动、没收藏、没 pin、不在跑的，按大小排前 10 个，写 tend rm 命令和总大小
tend shell-init [zsh|bash] [--key K] [--ui fzf|tui]
                                       输出把 K（默认 ctrl-g；ctrl-x / alt-x 自动翻成各 shell 记法，别的原样透传）绑到 tend fzf（默认）或 tend tui 的 shell 代码（zle widget / bind -x），eval 进 rc 文件
tend install-hook | tend uninstall-hook  可选的 Claude hook（tui.md「跑着的会话（Herdr）」里的「需要你」）；幂等，改前留 .bak-<时间>
tend hook-event                         Claude hook 调用的入口（stdin 是 hook 的 JSON），不给人用
tend install-skill [--from <dir>]
tend uninstall-skill                    只删 ~/.claude、~/.codex 下的 tend 软链，真目录不碰
```

`tend hosts` 各子命令与 `tend rpc` 的细节见 [remote.md](remote.md)「CLI：`tend hosts`」「远端协议」；`tend mv` / TUI `M` 的搬迁步骤和回收站见 [tui.md](tui.md)「键盘与鼠标」（「移动项目目录」「删除会话」两行）。

所有面向 UI 的子命令支持稳定 JSON 输出（list / sessions / show / trash / clean / fix 的列表都有 `--json`），ANSI 展示输出与业务数据分离。
所有 `[y/N]` 确认在 stdin 不是终端时直接报错退出，要脚本化就加 `-y`；clean / fix 有失败项时非零退出。
子命令一律用 `newFlags(name)` 建参数解析器：`-h` / `--help` 只打印全局用法里属于这个子命令的行和它的参数，退出码 0；`tend help` 打完整用法。

### 项目表

`tend add`、`tend list` / `sessions`、fzf 的各页和项目选择器、`tend report` 用的是同一个归属（`cmd/tend/projects.go`，每个命令只读一次）：

- 单机：只折叠本机协调器日志（`<数据目录>/coord/events.jsonl`）里的项目和成员事件，不拿锁；另一台机器的系统取这次连上时 `hello` 说的 `OS`，不知道时按路径写法猜。
- server（`coordinator.url`）：读 `<数据目录>/projects.json`（0600，`projects.SaveTable`），30 秒内写的才用；没有就拨号 1 秒（`hello` + `state.get` + `machine.list`），拿到就写进表。拨号失败也记在表里（`failed_at`），30 秒内不再拨；这时没有项目，全部按自动组，从不用过期的表。

fzf 的项目选择器先列项目（`<id>  # <名字>  (N)`，选了写 `project:<id>`），再列自动组；卡片和行里的项目写项目名（`Rec.Group()`）。`--json` 输出里的 `project` 仍是记录自己的字段。

## 配置

TUI 里 `,`（中文 `，` 也认）打开设置面板，↑↓（`Tab` / `Shift+Tab`）选项、←→/Enter/Space 换值，改了立刻生效并写进
`~/.agent/tend/config.json`；环境变量和命令行参数优先于它。

| 设置 | 键 | 选项 | 默认 |
| --- | --- | --- | --- |
| 时间显示 | `relative_time` | 相对（今天 `16:53`、`昨天 16:53`、7 天内 `周三 16:53`、今年 `09-12`、更早 `2025-12-01`）/ 绝对 | 相对；详情面板里的时间一律完整 |
| 默认标签页 | `default_view` | favorites / sessions / projects / live | favorites |
| 默认排序 | `sort` | active / started / favorited / turns | active |
| 语言 | `lang` | 空（跟系统：`LC_ALL` / `LC_MESSAGES` / `LANG` 以 zh 开头是中文，别的是英文；都没设时 Windows 看用户界面语言，其他平台英文）/ zh / en | 空 |
| 最少轮数 | `min_turns` | 1 / 2 / 3 / 5 / 8 | 3 |
| 滚轮步长 | `wheel_step` | 每 1 / 2 / 3 / 5 个事件一步 | 3 |
| 滚动加速 | `wheel_speed` | 关 / 标准 / 快（甩动时一帧滚几步） | 标准 |
| 图标 | `icons` | ascii / nerd | ascii |
| IDE | `ide` | 自由输入：app 名 / 命令 / 路径（面板里 Enter 进编辑，Enter 存，Esc 放弃） | 空 = Rebased（mac `open -a Rebased`，win `rebased64.exe`） |
| 鼠标 | `mouse` | 开 / 关 | 开 |
| 回收站 | `trash_days` | 7 / 30 / 90 天 / 永久（0） | 30 |
| 工具输出 | `tool_output_lines` | 不搜（0）/ 3 / 10 / 30 行：每次工具输出进正文库的行数，改了后台整体重建正文库 | 3 |
| 恢复方式 | `resume_in` | terminal（终端）/ app（桌面 App）/ origin（跟来源走：App 里建的会话用 App） | terminal |
| 需要你时 | `notify` | off（只在底栏提示）/ bell（再响铃） | off |
| 其它机器 | `hosts` | 面板里没有，用 `tend hosts add / rm` 管理（见 [remote.md](remote.md)「配置」） | 空 |
| Agents 页顺序 | `live_sort` | started / group / active；面板里没有，Agents 页按 `o` 轮 | started |
| 项目页分组顺序 | `project_sort` | active / count / name；面板里没有，项目页按 `o` 轮 | active |

环境变量：

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `TEND_UI` | `tui` | 默认前端，`tui` 或 `fzf` |
| `TEND_HOME` | `~/.agent/tend` | 数据目录 |
| `TEND_ICONS` | 空 | `nerd` / `ascii`，优先于设置面板 |

界面图标默认纯 ASCII（`* ✓ ! # -> v >` 等；✓ U+2713 是 Neutral 宽度，不像 ★ 那样在 CJK 终端里会变 2 格）；`config.icons` 对所有子命令生效（`tend list` / `preview` / fzf 里的行都按它画），`TEND_ICONS` 环境变量优先；设置里切 Nerd Font 用 Font Awesome 区（U+F000–F2E0）与
Powerline 区（U+E0A0）。默认不开 NF 是因为没打补丁的终端会拿 2 格宽的字形顶替私有区字符，整行错位，而程序
认不出终端用的什么字体。不用 ★☆→▾▸ 这类 East Asian Ambiguous 字符。

另外尊重 `NO_COLOR`、`CODEX_HOME`、fzf 注入的 `FZF_PREVIEW_COLUMNS`。
