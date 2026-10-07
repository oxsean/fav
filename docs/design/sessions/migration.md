# 迁移、记忆与环境诊断

把会话或记忆从一台机器搬到另一台：交接式迁移、Claude 完整迁移、记忆的管理与迁移、迁移前的环境诊断，以及这部分的已定决策和待核实项。远端合同和只读聚合见 [remote.md](remote.md)。

实现：本机交接包在 `internal/capture`（`handoff.go`）和 `cmd/tend`（`tend handoff`）；路径映射在 `internal/pathmap`；搬项目目录的文件枚举和改写在 `internal/index`（`move.go`）；记忆的查看、删除和孤儿扫描在 `internal/memory`，节点方法 `memory.ls` / `memory.read` / `memory.trash` / `memory.restore` 在 `internal/remote`（`memory.go`），命令是 `tend memory` 和 `tend doctor` 的记忆一节。本文其余部分**未实现**：跨机器交接、完整迁移（`import.*`）、记忆的对比和同步（`memory.put`、`memory diff`）、环境诊断（`env`、`env diff`），代码里都还没有。

## 迁移

### 两种迁移

| | 交接式迁移 | Claude 完整迁移 |
|---|---|---|
| 做法 | 写交接包，传到目标机器，在对应目录开一个新会话，交接包作为首条消息 | 把会话文件原样复制过去，改写路径，在目标机器上 `--resume` 接着做 |
| 保留什么 | 结论和状态，不含完整历史 | 完整历史 |
| 依赖私有格式 | 几乎不依赖 | 强依赖 |
| Codex | 支持 | 不做（见「Codex：不做完整迁移，列为验证项」） |

### 交接式迁移

- 已有：本机交接 `tend handoff <id> [--to claude|codex]`，交接包的内容、弹窗和去向见 [resume.md](resume.md)「分叉与交接」。
- 未实现：`tend handoff <id> --host <机器>`：写交接包 → 传到目标机器 → 在目标机器对应的目录（按项目对应关系找，见 [remote.md](remote.md)「功能」）开新会话。
- 未实现：交接包里再加两样：和这个项目有关的记忆条目（见「记忆的管理与迁移」）、迁移前的环境差异摘要（见「迁移前的环境诊断」，诊断做好之后加上）。

### Claude 完整迁移

未实现。复制走一套导入事务，不复用 `internal/index/move.go` 的移动流程（那是「移走」，而且只按本机系统处理路径）；只复用它的文件枚举和纯改写函数。

1. **检查**：
   - 两边都查「在跑 / 没在跑 / 未知」，未知时拒绝迁移。探测失败不能当成「没在跑」；现有的 `LiveSessions` 把读不到的来源当成空，导入不能照搬这种处理。
   - 目标机器上有对应的仓库（按项目对应关系找）。
   - 显示分支、提交、未提交改动和没推送提交的差异。
   - 环境诊断有阻断项时，不允许迁移。
2. **清单**：按当前 Claude 版本探测会话有哪些文件：`<sid>.jsonl`、`<sid>/` 子目录、`file-history/<sid>/`，以及当前版本存在的其他文件。`todos/` 这类目录不一定存在，清单不写死。每个文件记下 sha256 和行数。
3. **传输**：经 `import.begin` / `import.chunk` 流式传到目标机器，先放在暂存区，不直接落到正式位置。
4. **校验与改写**：目标机器核对哈希，按路径映射（见 [remote.md](remote.md)「路径映射」）改写 `cwd` 字段，再核对行数。拒绝越界路径和符号链接；目标机器已有同一个 id 时不覆盖。同一个迁移重复导入能识别出来。
5. **提交**：放进目标机器的 `ClaudeProjectDir(新 cwd)`，建索引，写提交标记；然后源机器标记「已迁往 <机器>」。任何一步失败，源机器上的原件都不动。
6. **验收**：以目标机器上原生 CLI 能恢复这个会话为准，不以 `tend show` 能读出来为准。
7. **原件**：默认两边都保留。在源机器上恢复前，提醒「已迁走，两边都继续会分叉」。迁移时也可以选「移走」，原件进回收站，可以还原。
8. **复制关系与分叉检测**：记录新增两个字段：来源机器、迁移去向；两边都记下 `migrated{peer, at, size, sha}`。之后聚合视图里显示「两边一致 / 已分叉 / 未检查」，用户绕过 tend 直接在两边分别 `claude --resume`，也能被发现。回迁时，如果对方的文件是本机文件的前缀，就快进覆盖；否则拒绝。
9. **迁移说明**：在目标机器第一次恢复时，自动附一条说明作为首条消息（`claude --resume <id> "<说明>"`），内容包括路径映射表、没带过来的未提交改动、诊断里缺的东西。避免 AI 按历史里的旧路径去读文件；tend 不改写历史正文里的路径。

### Codex：不做完整迁移，列为验证项

- 现在的 Codex 会话已经是 `history_mode=paginated`，历史存在 `thread_history_1.sqlite` 里；rollout 文件只是遗留副本，直接复制过去，目标机器的 Codex 未必认。
- rollout 里还有 `workspace_roots` 这类绝对路径数组，只改 `cwd` 字段覆盖不到。
- Codex 会话只走交接式迁移。
- 用临时 `CODEX_HOME` 模拟「目标机器」验证过 `codex migrate-rollouts`（Codex 0.154.0）；所测机器上约 85% 的 Codex 会话是新格式（paginated），其余是旧格式：
  - **旧格式**：把 rollout 复制到空的目标目录后，`migrate-rollouts --thread <id> --apply` 能迁进去（状态 `migrated`，新建 `thread_history_1.sqlite`，rollout 被改写成 paginated，线程登记进 `state_5.sqlite`）。原生 `codex resume` 能否接着做没有验证，因为那需要登录并实际调用模型。
  - **新格式**：rollout 仍然带着消息（抽查一个会话：rollout 有 139 条 response_item、10 个 turn_context，历史库里是 114 条、10 个 turn），但复制到目标机器后，`migrate-rollouts` 直接判为 `already_paginated` 并跳过，目标机器的历史库里没有这个会话的历史。要搬新格式会话，只能导出 Codex 私有的历史库，所以维持「不做」。
- 剩下的验证项：旧格式会话复制并 `--apply` 之后，原生 `codex resume` 能否接着做。这要在一台已登录 Codex 的测试机上跑；它只覆盖越来越少的旧会话，优先级低。
- 风险监控：tend 读取 Codex 会话依赖 rollout 文件继续被写入。`tend doctor` **不检测** `history_mode`：只看文件名和修改时间找不到稳定的信号。核实的事实（Codex 0.155.1，只看了文件名和二进制里的字符串）：`history_mode`（`legacy` / `paginated`）写在 rollout 首行 `session_meta` 里，是内容不是文件名；paginated 会话的 rollout 仍然带消息，所以这个值本身不说明 rollout 停写；「`thread-writer-locks/` 有新锁而 rollout 不变大」也不可靠，开着而空闲的线程本来就不写。tend 不带 SQLite，宁可不报。

## 记忆的管理与迁移

### 记忆在哪

| 记忆 | 位置与结构 | 特点 |
|---|---|---|
| Claude 项目记忆 | `~/.claude/projects/<编码后的项目路径>/memory/`：`MEMORY.md` 索引（一行一条）+ 每条一个带 frontmatter 的 `.md`。按官方文档，项目路径取自 **git 仓库**，同一仓库的 worktree 和子目录共用一份，不在 git 里时才按项目根目录；`settings.json` 的 `autoMemoryDirectory` 可以把它挪到别处；启动时只加载 `MEMORY.md` 的前 200 行或前 25KB（先到者为准），条目文件按需读取；官方明确写着「只存在本机」 | 实测大部分目录是空的；cwd 已不存在的几乎全是临时目录（scratchpad、claude-review、workdir）；少数反查不到来源 |
| Codex 按项目的记忆 | 旧布局 `~/.codex/memories/projects/<编码后的 cwd>/`：Codex 0.155.1 已不维护（二进制里没有这个路径），现存的只是早先导入留下的；现在外部 agent 的记忆导入到 `~/.codex/memories/extensions/external_agent_import/resources/<project-key>/`，旁边一个 `scope.json`（只核过文件名，没读内容） | tend 两处都不读（推后，见「推后」） |
| Codex 全局记忆 | `~/.codex/memories/` 下的 `MEMORY.md`（可达数百 KB）、`memory_summary.md`、`raw_memories.md`、`rollout_summaries/`、`skills/`、`extensions/`；`MEMORY.md` 一块一个 `# Task Group: <标题>`，下面是 `scope: <说明>` 和 `applies_to: cwd=<路径或一类目录>; reuse_rule=…`；目录是 Codex 自己建的 git 仓库（没有 remote），另有 `memories_1.sqlite` 记录整理任务的进度 | 由 Codex 自己在后台整理、改写 |
| Claude 全局配置 | `~/.claude/CLAUDE.md`、skills、settings | 用户自己用 git 管，不归 tend 管 |

难点：项目记忆按路径存放，四类机器上同一个仓库的路径各不相同，所以记忆天然是每台机器各一份；记忆正文里也有绝对路径；Codex 的记忆由它自己维护。

`tend mv` 搬项目目录时，旧项目目录里 transcript 以外剩下的东西（`memory/` 等）一起搬到新目录（`internal/index/move.go` 的 `sweepProjectDir`）。下面 1、2 已实现，3–5 未实现。

### 做法

1. **看**
   - 找 Claude 记忆目录和 Claude 同样的规则（`memory.ClaudeDir`）：`~/.claude/settings.json` 有 `autoMemoryDirectory` 就用它；没有就取 git 仓库根（`git rev-parse --git-common-dir` 的上一级，worktree 和子目录都归到主仓库，不在 git 里用目录本身），记忆在 `ClaudeProjectDir(根)/memory`。
   - 一组（`memory.Set`）就是一个记忆目录：`MEMORY.md` 的行数、字节数，超过 200 行或 25 KB 记 `over`（只对 Claude）；每条（`.md` 文件，不含 `MEMORY.md` 和以 `.` 开头的，所以 `.incoming/` 不算）的标题取 frontmatter 的 `name`，没有就取 `MEMORY.md` 那一行的链接文字，再没有就取文件名；描述取 `description`，没有就取那一行破折号后面的部分；另有修改时间、大小、原文的 `sha`、换行统一成 LF 后的 `norm`，`in_index` 说明 `MEMORY.md` 里有没有指向它的行。
   - Codex 全局记忆按块读：`applies_to` 的 `cwd=` 是绝对路径、并且落在要看的目录下的，归这个目录（一组，`dir` 是这个目录）；`cwd=` 不是绝对路径的（「一类目录」之类的说明）单列一组「没写适用目录」（`dir` 为空），不按 cwd 过滤掉。每块的 `file` 是 `MEMORY.md`，`line` 是块开头的行号。只读。
   - 节点方法：`memory.ls{dirs, global}` 回每个目录的 Claude 组（同一个记忆目录只回一次）和（`global` 时）Codex 全局的各组；`memory.read{file}` 只读记忆根下的普通文件：Claude 的 `projects/<名字>/memory/`、`autoMemoryDirectory`、Codex 的 `memories/`，按解开链接后的真实路径判；其余路径一律回 `unauthorized`，不说存不存在。`tend memory`、`tend memory show` 在本机直接读，`host:` 经这两个方法。
2. **管**
   - `tend doctor` 扫描 `~/.claude/projects/*/memory/`（`memory.Scan`）：空目录只计数；有内容的，用索引里会话的 `Cwd`、`Repo` 和收藏记录的 `Cwd` 反查（目录名是单向编码，只能拿已知目录编码后比对）。有一个候选目录还在，或者按编码从根目录一层层能在盘上找到原目录（最多读 500 个目录），就不是孤儿。孤儿分三类：
     - 临时目录：候选是 agent 的 scratch 目录（`index.AgentScratch`），或在临时目录里（Claude home 本身也在临时目录里时不按这一条判，比如测试和 fixture）。建议 `tend memory rm <记忆目录>`。
     - 已搬走：候选带 git remote（Codex 会话记的 `repository_url`、收藏记录的 `git_remote`），本机的 `node.repos` 按 `task.RemoteKey` 只找到这个仓库的一个检出（在 `allow_dirs` 下，没有就在 home 下）。建议 `tend memory merge <记忆目录> <那个检出>`。
     - 其余：「来源未知」，不猜。
   - 删一条记忆（`memory.Trash`，`tend memory rm`、`memory.trash`）：只删 Claude 记忆目录里的条目文件，不删 `MEMORY.md`，不碰 Codex 的记忆。文件进 tend 回收站（`TrashEntry` 的 `kind: "memory"`，`line` 是从 `MEMORY.md` 去掉的那一行原文），条目 id 是它在回收站里的目录名。还原（`memory.Restore`，`tend trash --restore <id>`、`memory.restore`）把文件放回，原处已有同名文件就拒绝，那一行加回 `MEMORY.md` 末尾（已有就不加）。`tend trash` 和会话一起列出记忆条目；TUI 的回收站视图仍只列会话。
   - 整个记忆目录也能进回收站（`tend memory rm <记忆目录>`），还原同上。
   - 「并到新目录」（`memory.Merge`，`tend memory merge`，只对本机）：逐条用 `memory.Write` 写过去，不覆盖：目标没有就写入并把旧 `MEMORY.md` 里指向它的那一行加进目标的 `MEMORY.md`（目标已有这一行就不加）；内容相同的不动；同名而内容不同的写进目标的 `.incoming/`，不进 `MEMORY.md`。旧目录里只有记忆条目时，全部写完整个进回收站；有别的文件就保留旧目录，列出这些文件。
3. **按条复制**（未实现，随完整迁移）：列出两边的差异，用户确认后按条复制，不覆盖目标机器已有的记忆。
4. **对比与同步界面**（未实现）
   - 两台机器上的同一个项目按项目对应关系配对。
   - `memory diff <项目> <机器>`：列出只在这边、只在那边、两边内容不同的条目。比较时同时给出原始哈希和规范化之后的结果；路径只按明确的映射转换，不对全文做替换。
   - 同步：按条复制，不自动合并正文；`MEMORY.md` 按行取并集。冲突时对方那份放进 `memory/.incoming/`，**不进索引**，免得两份互相矛盾的记忆同时进入上下文，由用户来合并；合并后检查 `MEMORY.md` 是否超过 Claude 的加载上限：前 200 行或前 25KB，超出部分启动时不加载。
5. **Codex 记忆**：只看、只对比，不写。需要带到另一台机器时，放进交接包，或者用户手动复制。

## 迁移前的环境诊断

未实现。只诊断，不对齐。目的：迁移之前知道「这个会话到了那台机器，AI 看到的上下文和原来差多少」。只报告差异；每项差异附一句手动处理的提示（纯文字，tend 不执行）。

### 数据来源

- **源端**：会话自己记录了它看到的环境。
  - Claude：每行的 `version`、`gitBranch`；`attachment` 里的 `instructions`（加载了哪些指令文件，含原文，可以直接算出当时的哈希）、`skill_listing`、`invoked_skills`、`deferred_tools_delta`、`mcp_instructions_delta`、`agent_listing_delta`、`model`、`environment`（系统、shell、是否 worktree）。
  - Codex：`session_meta` 的 `cli_version`、`model_provider`，`turn_context` 的模型和审批 / 沙箱策略。
  - 工具、子代理、skill 这几类记录的是增删事件，要先叠加成会话结束时的最终状态再比较。
  - 这些附件大约从 Claude Code 2.1.259 起才有，实测只有一半左右的会话带。老会话大多只能标「未知」，这是常态，不是例外。
- **目标端**：目标机器当前的配置扫描，加上目标机器上同一个项目最近一个会话的记录，两者并列。
  - 最近那个会话的 CLI 版本和目标当前的不一致，或者超过 N 天，就只作参考。
- **每一项都注明证据的来源和时间**：「会话当时看到的」「目标机器当前的配置」「未知」。读不到的维度标「未知」，不能判为一致。私有格式变化时，也便于查是哪一项读失败了。

### 对比什么

| 维度 | 内容 | 级别 |
|---|---|---|
| CLI | Claude / Codex 的 CLI 版本 | 没装：**阻断**；更旧：**不对等**，附升级命令；只有已知不兼容才阻断 |
| 代码 | 仓库在不在、分支、HEAD、未提交改动、没推送的提交；AI **写过**的文件逐个比哈希（设上限） | 仓库不在：**阻断**；其余：**不对等** |
| git 带不走的东西 | `.claude/settings.local.json`、`CLAUDE.local.md`、`.env`、worktree | **不对等** |
| 指令文件 | 全局 / 项目 / local 的 CLAUDE.md 及其 `@` 引用；`~/.codex/AGENTS.md`、仓库里的 AGENTS.md | **不对等** |
| 记忆 | 项目记忆逐条比较；Codex 全局记忆里和本项目有关的条目；两边的 `autoMemoryDirectory` 设置 | **不对等** |
| skill、插件、子代理、MCP | 会话结束时的最终状态 vs 目标机器的；会话用过的排在前面；插件带来的 skill、子代理、MCP 归到插件名下；claude.ai 连接器跟着账号走，账号无法安全确认时标「未知」，不去读凭据推断 | 用过的缺了：**不对等**；没用过的缺了：**提示** |
| 模型提供方 | 提供方名称、base_url 的主机名（不取密钥）；Claude 的 `env` 只看键名 | **不对等** |
| 按绝对路径存放的项目配置 | `~/.claude.json` 的 `projects[cwd]`（allowedTools、enabledMcpjsonServers）；Codex 的 `projects."<路径>".trust_level` | **不对等** |
| 权限与沙箱 | `permissions`、Codex 的审批 / 沙箱策略 | 目标机器放得更宽：**突出显示**；其余：**提示** |
| hook | 配置的命令路径在目标机器上是否存在 | **提示** |
| 模型与推理强度 | model、effort | **提示** |
| 命令行工具 | 会话在 Bash 里用过的程序在目标机器上有没有、版本多少 | **提示**（排在最后做） |
| 系统 | 系统、shell、home | **提示** |

计划先做核心维度：CLI、代码状态、指令文件、会话用过的 skill 和 MCP、按路径存放的项目配置、模型提供方；其余维度以后补上。

### 采集与展示

- 每台机器通过协议的 `env` 方法（命令行对应 `env --json`）输出指纹：**只有名字、版本和内容哈希**，不含文件正文、环境变量的值、MCP 的 env / headers，也不含令牌。
- 同时保留原始哈希和规范化之后的比较结果（换行统一成 LF，路径按映射转换），避免把 CRLF 或 home 不同误报成差异，也不会掩盖真实的差异。
- 想看某个 CLAUDE.md 或某条记忆具体差在哪，点开时才去取那一个文件做文本对比，不预先传正文。
- 迁移确认框里一行汇总「阻断 0 · 不对等 3 · 提示 5」，展开看明细。有阻断项时不能迁移；只有不对等项时照样可以迁，确认框里写明「到那边 AI 会缺什么」。
- 命令行：`env diff <机器> --session <id>`（针对一个会话）、`env diff <机器>`（两台机器整体比较）；有阻断项时退出码非 0。

## 推后

| 什么 | 为什么 |
|---|---|
| Codex 按项目的记忆（旧布局 `memories/projects/<编码后的 cwd>/` 和 `extensions/external_agent_import/resources/<project-key>/`） | Codex 0.155.1 不再维护旧布局，只对旧数据有效；导入布局的归属写在 `scope.json` 里，要读内容才知道，等需要时再核 |
| `tend doctor` 检测 Codex `history_mode` | 只看文件名和修改时间没有稳定的信号（见「Codex：不做完整迁移」的风险监控） |
| 远端机器的孤儿扫描 | `tend doctor` 只扫本机；要看那台就在那台上跑 |
| TUI 回收站视图里的记忆条目 | 回收站视图只列会话；记忆用 `tend trash --restore` 还原 |

## 决定

1. 范围：不做云同步；单用户多机经自己的 SSH 做只读聚合，迁移由用户手动发起。
2. 机器：Mac、Linux、Windows、WSL 四类都支持，两端都做（发起端和远端）。多机功能的验收：四类机器分别当发起端和远端，全部通过 `tend hosts check`；迁移还要覆盖断线、同 id 冲突、迁移中途源文件变化这几种情况。
3. 完整迁移默认复制：两边都保留；源机器的记录标「已迁往 <机器>」，在源机器恢复前提醒。
4. 环境诊断只报告差异，不对齐。
5. Codex 会话完整迁移不做，列为验证项。
6. 不做 rsync 镜像。
7. 做分发命令 `tend hosts install`（含 WSL 和容器）：只在用户手动执行时推送。

## 待核实

- 已核实：
  - Claude 记忆的加载上限是前 200 行或前 25KB；记忆按 git 仓库存放，可以用 `autoMemoryDirectory` 挪位置。来源：Claude Code 官方文档「How Claude remembers your project」。
  - `codex migrate-rollouts`：结果见「Codex：不做完整迁移，列为验证项」。
- 未核实：
  - 除 Mac 外的三类系统经真实 SSH 当发起端：现在只验证过 Mac 经真实 SSH 连另一台 Mac、Linux 容器（docker exec）、Windows（cmd）、WSL 四类远端；另外三类当发起端时只用本机子进程充当远端（`tools/test-host` 的 hosts smoke）。要验证需在测试机之间配 SSH 密钥。
  - Windows 上运行 TUI 的终端能力（pty、鼠标、宽字符）是否满足 tend 的要求：需要在一台 Windows 机器上实测，也可以用 `tend hosts check` 一并测。
  - 旧格式 Codex 会话迁移后，原生 `codex resume` 能否接着做（见「Codex：不做完整迁移，列为验证项」）。
