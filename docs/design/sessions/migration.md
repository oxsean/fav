# 迁移、记忆与环境诊断

把会话或记忆从一台机器搬到另一台：交接式迁移、Claude 完整迁移、记忆的管理与迁移、迁移前的环境诊断，以及这部分的已定决策和待核实项。远端合同和只读聚合见 [remote.md](remote.md)。

实现：本机交接包在 `internal/capture`（`handoff.go`）和 `cmd/tend`（`tend handoff`）；交接到另一台机器在 `internal/remote`（`handoff.go`、`peer.go`）和 `cmd/tend`（`handoffhost.go`）；路径映射在 `internal/pathmap`；搬项目目录的文件枚举在 `internal/index`（`move.go`），`cwd` 的改写在 `internal/index`（`rewrite.go` 的 `RewriteFile` / `RewriteCwd`，`tend mv` 和完整迁移共用）；Claude 完整迁移的清单、记录、暂存、提交、迁移说明在 `internal/migrate`，节点方法 `export.*`、`import.*`、`copies` 和驱动函数 `StartMigration`、`Migration.Run`、`Abandon`、`CheckCopies` 在 `internal/remote`（`migrate.go`），会话在不在跑的三态在 `internal/capture`（`LiveState`），命令是 `tend migrate`，首次恢复带说明在 `tend resume`；记忆的查看、删除、孤儿扫描、对比和按条写入在 `internal/memory`，节点方法 `memory.ls` / `memory.read` / `memory.trash` / `memory.restore` / `memory.put` 和两台机器之间的对比、复制（`CompareMemories`、`CopyMemory`）在 `internal/remote`（`memory.go`），命令是 `tend memory`（含 `diff`、`cp`）和 `tend doctor` 的记忆一节；环境诊断的核心维度见「迁移前的环境诊断」。**未实现**：交接包里的记忆条目、环境诊断的其余维度和界面、TUI 的迁移对话框和关系标记、Web 上的迁移。

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
- 已有：交接到另一台机器 `tend handoff <id> --host <机器>`，定目录、成稿和开法见 [resume.md](resume.md)「交接到另一台机器」；成稿里的「环境差异」段见「迁移前的环境诊断」的「展示」。
- 未实现：交接包里和这个项目有关的记忆条目（见「记忆的管理与迁移」）。

### Claude 完整迁移

`tend migrate <id|机器:sid> --to <机器> [--dir 目录] [--move] [--dry-run]`，`--abandon` 放弃未完成的。只迁 Claude 会话；源和目标都要是调用者自己的机器（被共享来的机器拒绝，同交接）。两种模式走同一条路：驱动函数只经 `remote.Peer` 调两端的节点方法，本机是进程内的 `Peer`，模式一经 ssh，模式二经 server 的 `node.call`；数据经发起端中转，节点之间不互连。

事务分在两端，发起端不留状态：源机器 S 上是 `<数据目录>/migrations.jsonl` 里的 pending 行，目标机器 T 上是暂存目录 `<数据目录>/import/<迁移 id>/`。

| 步 | 调用 | 做什么 |
|---|---|---|
| 1 | S `handoff.facts`、`export.plan{ref}`、`copies`；T `copies` | 会话的事实（定目录用）、清单、在不在跑、git；和 T 之间上一次迁移的关系（见「复制关系」）。在跑、不能确定、两边一致、T 上接着做过、已分叉都拒绝；S 上有发往 T 的 pending 迁移就沿用它的 id，接着做 |
| 2 | T `node.repos`；S、T `env` | 定目录（同交接，`handoffDir`）；环境诊断，有阻断就停（`--dry-run` 只做到这里，有阻断时退出码非 0） |
| 3 | S `export.plan{ref, m, to}` | S 记意图：pending 行，带主 transcript 的大小和 sha |
| 4 | T `import.begin` | 查同 id 冲突；建暂存或接着用，回答各文件已暂存的字节数；已提交过就直接回那一行 |
| 5 | S `export.read`，T `import.chunk` | 每块 1 MiB，从已暂存处续传 |
| 6 | S `export.plan{ref, m}` | 再核一遍：文件身份、大小、sha、在不在跑 |
| 7 | T `import.commit{m, note}` | 改写、放到位、建索引、写说明、记 done（提交标记） |
| 8 | S `export.done{m, done, move?, committed}` | S 按 T 提交的那份主 transcript 记 done；选了移走就把原件放进 S 的回收站 |

- **清单**（`migrate.Plan`）：续接链上所有会话（`index.SessionIDs`）的文件（`index.SessionFilesOf`），只取 Claude home 下三个目录：`projects/<编码>/<id>.jsonl` 和 `<id>/…`、`file-history/<id>/…`、`todos/<id>…`。路径相对 Claude home、`/` 分隔；`.jsonl` 要改写 `cwd`（transcript 和附属目录里的），其余原样复制。Claude home 下其它一级目录里以会话 id 开头的东西（如 `session-env/`，可能存着环境变量）只列成「没带过去」，不复制。符号链接、非普通文件、越界路径、总量超过 2 GiB 都拒绝。每个文件记下大小、sha256、行数、文件身份（`fileio.ID`）和修改时间，sha 按「身份 + 大小 + 修改时间」在进程内缓存。链上的旧 id（清单的 `aliases`）和会话 id、迁移 id 一样只能是安全的文件名（`capture.SafeID`，和交接包 id 同一条规则），不重复、不等于会话 id；S 规划时、T 在 `import.begin` 都核，不合格回 `bad_request`。
- **传输**：驱动只读到清单记的大小；`export.read` 每次都核文件身份，变了回 `stale`；`import.chunk` 收到超出清单大小的数据也回 `stale`；`import.chunk` 只收「已暂存长度 == off」的块，重发的块按已收处理；一个文件收齐就核 sha，不对就丢掉重传。
- **暂存**：`import/<m>/` 下是 `state.json`（迁移的参数和清单）和 `files/<path>`，0600 / 0700。清单换了（第 6 步的重试），sha 没变的文件不重传；空文件不用传。每次 `import.begin` 顺带删掉 7 天没动、也没开始提交的暂存。
- **改写**（T，提交时）：`index.RewriteFile`（逐行交给 `RewriteCwd`）把每个 `"cwd":"…"` 值 JSON 解码、映射、再编码回去，`file://` 前缀保留，行数不变，其余字节不动（正文里的路径是历史，不改）。映射先按目录对，依次试、第一个对上的算：会话目录 → 目标目录，再是会话所属项目在两台机器上的各仓库目录（`projects.Snapshot.DirPairs`，和交接定目录同一个方法；重复的只留一对），用 `pathmap.Rebase`，分隔符按目标系统；再按 home 映射（`pathmap.Map`）；都对不上就不改，记进迁移说明的「保留原路径」一节。改写后核行数，再保留原来的修改时间（索引按它排序）。
- **放到位**：先把改写好的文件都写进 `out/`，写 `placing.json`（这次要放哪些文件、各自的 sha），再逐个 `fileio.Move` 到正式位置（跨文件系统时整份复制，保留权限和修改时间）：`projects/` 下按 T 的规则换成 `ClaudeProjectName(目标目录)`。中途失败就把放好的撤回，状态仍是暂存。都放好后 `RescanSave` 刷新这几个文件的索引，写说明，再追加 T 的 done 行（role `from`）：这一行就是提交标记，之后删暂存。再次提交时，已在位、sha 对得上的文件算放过，所以 `import.commit` 可以重复调用。目标目录必须存在（Claude 在 cwd 里恢复），不存在时 `import.begin` 回 `not_found`。
- **断线与续传**：TUI 退出、ssh 断线、server 重启都不丢已传的部分。再跑一次 `tend migrate` 就从 S 的 pending 行接着做。第 7 步做完、第 8 步没做：`import.begin` 回答已提交，直接补 `export.done`。T 的 done 行记下它提交的那份源主 transcript 的大小和 sha（`source`），`import.begin`（已提交时）和 `import.commit` 都回答它，S 记 done 时按它记，不按自己最后一次规划：S 在补记之前又接着做了（或 T 放到一半崩溃、续做时把那次的旧快照放完），S 记的仍是 T 真正拿到的那份，下一次 `copies` 判出「这台接着做了」，再迁一次就快进过去。这种时候（驱动的 `Behind`）`--move` 不动原件，CLI 提示再跑一次 `tend migrate`。
- **源会话在迁移中途变了**：第 1、3、6 步都要求 S 上「没在跑」（`capture.LiveState` 三态：没有 `sessions/` 目录、读不出、Herdr 出错都算「不能确定」，照样拒绝）。传输中 `stale`，或第 6 步的清单和第 3 步的不同，就用新清单自动重试一次；还不行就停下，pending 留着。第 6 步发现会话又在跑了，停下并说明原件没动。T 按清单核 sha，所以 T 上不会出现半新半旧的一份。
- **同 id 冲突和回迁**（T 判）：T 上已经有这个 provider 加 sid 的 transcript 时——

| T 上那份是什么 | 判定 | 做法 |
|---|---|---|
| 和 S 之间最近一次迁移（T 的记录 peer 的 endpoint 是 S，role 不限：从 S 迁来的副本，或迁到 S 去的原件）之后没动过（主 transcript 的大小和 sha 等于记下的） | `forward` | 快进 / 回迁：提交时旧文件先进 T 的回收站（标题写「被 <机器> 迁来的一份替换」），可以还原；还原时先删掉替换进来的文件（每个旧文件对应新目录下清单同一路径的那份，目标目录换了也一样），替换进来的已被改过就拒绝还原 |
| 同上，但之后动过 | `diverged` | 拒绝：两边都接着做过，已分叉 |
| 没有和 S 的关系记录 | `exists` | 拒绝，不覆盖 |

  链式（A→B→C 再回 A）只认直接的那一对，其余按 `exists` 拒绝。拒绝时 S 把这次迁移记成 aborted。快进时 T 上的会话不能在跑。
- **移走**：默认复制，两边都留。`--move` 在 T 提交以后才动原件：`export.done{move}` 在 S 上 `index.TrashSession`（续接链和附属目录一起），回收站标题写「迁往 <机器>」，可以还原；会话在跑或不能确定时不移，迁移照样记 done，提示关掉以后带 `--move` 再跑一次（那时只补移走这一步）。
- **记录**：每台机器一个 `migrations.jsonl`，只追加，持 `filelock` 写，同一个迁移 id 以最后一行为准。一行是 `{migration, role: to | from, provider, session_id, peer{name, endpoint, node_id, end}, state: pending | done | aborted, at, files, size, sha, dir?, note?, noted?, moved?}`；`size` 和 `sha` 是这一边主 transcript 在迁移那一刻的值（S 记 T 提交的那份原件的，T 记改写后的）；T 的一行另有 `source{size, sha}`：它提交的那份源主 transcript。迁移 id 形如 `20261007T153000-a1b2c3d4`。
- **复制关系与分叉检测**：列表行上只显示关系：`index.Rows.Copies` 钩子（调用方接 `migrate.Marks`）把这台机器的记录挂到本机行的 `Rec.Copies` 上，节点的 `list` / `query` / `grep` 也接，`remote.SessionOf` 带出 `copies`；CLI 卡片上是 `-> 机器`（迁走的）、`<- 机器`（迁来的）、`-> 机器（迁移未完成 · 7 天）`，ASCII 字形。状态只在需要时判断（`tend show`、再迁之前）：两边各回答 `copies`，每条带 `changed`（主 transcript 的大小或 sha 和记下的不同），`CheckCopies` 得出「两边一致 / 这台接着做了 / <机器> 上接着做了 / 已分叉 / 已移走 / 未检查」。用户绕过 tend 直接在两边 `claude --resume` 也能这样发现。不定时检查。
- **迁移说明**：驱动函数用发起端的语言写：来源和时间、目录对应（改写用的全部目录对）、S 上没带过来的未提交改动和没推送的提交、没复制的 Claude home 条目、环境诊断里不对等的项、记忆没有复制（给出 `tend memory diff` 的命令）。T 提交时补上「保留原路径」一节，写成 `<数据目录>/migrations/<m>/note.md`（0600）。T 上第一次恢复这个会话时（`tend resume`，ssh 远端恢复走的也是它），第一条消息是「这个会话刚从 <机器> 迁过来，先读 <路径>」（`migrate.FirstResume`，`capture.Plan.FirstMessage` 追加到 `claude --resume <id>` 后面；聚焦已有 tab 或 attach 后台会话时不加），发出后记 `noted`。模式二没有 ssh 时给的恢复命令，在迁来的会话上是 `tend resume <id>`，同样带上说明。
- **源机器上的提醒**：会话最近一次迁移是 S 迁走的（role `to`、done），`tend resume` 在 stderr 打「已迁往 <机器>（<时间>）：两边都接着做会分叉」，不拦。
- **同一个 id 在两台机器上**：迁移以后同一个 provider 加 sid 会出现在两台机器上。协调器按「机器 + 会话」成行，会话连到哪个任务、在不在跑都按机器分开；没有 `Copies` 钩子时一行保留它带来的 `copies`。
- **测试**：`migrate` 的单元测试覆盖改写、暂存复用、续传、提交撤回和重复提交、四种同 id 判定、回迁、移走、清理、源文件变化；切点崩溃用 `TEND_CRASH_AT`（和监督进程共用一个变量与判断函数 `proc.CrashAt`）：T 上 `migrate.begun`（建好暂存）、`migrate.half`（一个文件传了一部分）、`migrate.prepared`（`out/` 和 `placing.json` 写好、还没放）、`migrate.placing`（放了第一个文件）、`migrate.placed`（都放好、没记 done），发起端 `migrate.committed`（T 已提交、S 没记 done）；每个切点在子进程里真退出，再跑一次得到和没断过一样的结果。`remote` 用两台进程内的机器测驱动函数的时序、续传、放弃、自动重试、补记 done（源在 T 提交以后又变了时按 T 的那份记、不移原件）和各种拒绝；`cmd/tend` 用两个 fixture 数据集测 CLI（复制、恢复带说明、再迁被拒、快进、`tend show` 的分叉、回迁、崩溃后续传、移走）。
- **验收**：以目标机器上原生 CLI 能恢复这个会话为准，不以 `tend show` 能读出来为准。

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

`tend mv` 搬项目目录时，旧项目目录里 transcript 以外剩下的东西（`memory/` 等）一起搬到新目录（`internal/index/move.go` 的 `sweepProjectDir`）。下面 1–5 都已实现，只有第 4 条的 TUI 浮层未实现。

### 做法

1. **看**
   - 找 Claude 记忆目录和 Claude 同样的规则（`memory.ClaudeDir`）：`~/.claude/settings.json` 有 `autoMemoryDirectory` 就用它；没有就取 git 仓库根（`git rev-parse --git-common-dir` 的上一级，worktree 和子目录都归到主仓库，不在 git 里用目录本身），记忆在 `ClaudeProjectDir(根)/memory`。
   - 一组（`memory.Set`）就是一个记忆目录：`MEMORY.md` 的行数、字节数，超过 200 行或 25 KB 记 `over`（只对 Claude）；每条（`.md` 文件，不含 `MEMORY.md` 和以 `.` 开头的，所以 `.incoming/` 不算）的标题取 frontmatter 的 `name`，没有就取 `MEMORY.md` 那一行的链接文字，再没有就取文件名；描述取 `description`，没有就取那一行破折号后面的部分；另有修改时间、大小、原文的 `sha`、换行统一成 LF 后的 `norm`，`in_index` 说明 `MEMORY.md` 里有没有指向它的行。Claude 的组另带 `incoming`：`.incoming/` 里等着手动合并的条目，各项同上（不在索引里）；旧版 tend 不回这一项。
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
3. **对比**（`memory.Diff`，`tend memory diff <项目|目录> <机器> [--from <机器>] [--dir <那台的目录>]`）
   - 配对（`projects.MemoryPairs`，命令行和 TUI 共用）：给项目时，取它两台都有目录的仓库，一个仓库一对（项目的目录按机器名记，这台是项目表里的本机名）；给目录时，它所在的项目在那台的目录用 `pathmap.Rebase` 接过去，不在项目里就要 `--dir`，不猜。
   - 两端都经 `remote.Peer`，和交接同一条路：这台在进程内回答，别的机器模式一经 ssh、模式二经 server 的 `node.call`；两端都必须是自己的机器，`--from` 可以是另一台，两端都不必是这台。两边各 `memory.ls{[这一对的目录], global}`，比较在发起端做（`remote.CompareMemories`）；项目有几个仓库时，Codex 全局记忆里没写适用目录的那组只随第一对比一次。
   - 按类别和名字配对：Claude 的按文件名，Codex 全局的按块标题（块的 `sha` / `norm` 只算它自己那几行，不含和下一块之间的空行）。先比原始 `sha`，再比 `norm`（换行统一成 LF）；还不同、又有映射时，用 `memory.read` 读这一条两边的正文（Codex 的块从文件里切出来，同一个文件只读一次），把那边正文里的路径写成这边的再比。映射只有这一对目录和两边的 home：路径要在词边界上，是映射的目录本身或在它下面，用 `pathmap.Rebase` 接过去，到空白、引号、括号或标点为止；别的文字一律不动。
   - 分四组：只在这边、只在那边、内容不同、相同；相同里标出「只差换行或路径」（`loose`）。任一边的 `MEMORY.md` 超过加载上限时写出来。`--json` 给每一对的目录、两边的 `sets` 和这四组。
4. **按条复制**（`memory.put`，`tend memory cp <项目|目录> <机器> <名字…> [--from <机器>] [--dir …]`）
   - 先照 `diff` 比一次，再逐条（`remote.CopyMemory`）：在源机器用 `memory.read` 读正文和源 `MEMORY.md` 里指向它的那一行，`memory.put{dir, kind, name, text, line, expect}` 写到目标。名字可以不带 `.md`。
   - `memory.put`（`memory.Put`）：`dir` 是那台存在的项目目录，或记忆目录本身，记忆目录按那台自己的规则找（`autoMemoryDirectory`、git 仓库根）；`kind` 只收 `claude`，Codex 的回 `bad_request`；`line` 只能是一行，并且指向 `name`。`expect` 是调用方看到的那边这个文件的 `sha`（没有为空），和现在的不一样就回 `stale`、什么也不写：比较之后那边变了，要重新比。对上了交给 `memory.Write`：没有就写入，并把这一行加进 `MEMORY.md`（已经有指向这个文件的行就不加），`MEMORY.md` 按行取并集；内容相同不动；不同就写进 `memory/.incoming/<名字>`，**不进索引**，免得两份互相矛盾的记忆同时进入上下文，由用户来合并；`.incoming/` 里已有另一份不同的同名文件时写成 `<名字去掉 .md>-2.md`、`-3.md`…，那里的也不覆盖。回答带写到哪、是否进了 `.incoming/`、写完以后 `MEMORY.md` 的行数和字节数、是否超过 Claude 的加载上限（前 200 行或前 25KB，超出部分启动时不加载，命令行写出来）。
   - 命令行：已相同的、只差换行或路径的不复制，各说一句；Codex 的条目说只对比不复制；源上没有的名字和 `stale` 的算没复制成，退出码非 0。
   - TUI：记忆浮层的「对比…」（[tui.md](tui.md)「记忆对比」），同一组函数：`projects.MemoryPairs` 配对、`remote.CompareMemories` 比、`remote.CopyMemory` 逐条写，往这边复制时把这一对和条目反过来看（`MemoryPair.Swap`、`Entry.Swap`）。
5. **Codex 记忆**：只看、只对比，不写。需要带到另一台机器时，放进交接包，或者用户手动复制。

## 迁移前的环境诊断

只诊断，不对齐。目的：迁移之前知道「这个会话到了那台机器，AI 看到的上下文和原来差多少」。只报告差异；每项差异附一句手动处理的提示（纯文字，tend 不执行）。

实现：`internal/envcheck`（`Collect` 采集、`SeenOf` 会话所见、`Compare` 比较、`GitOf`、`FileText`）、`internal/index`（`env.go`，会话当时看到的环境）、`internal/remote`（`env.go`，`env`、`env.file` 和交接用的 `Handover.Diagnose`）、`cmd/tend`（`env.go`，`tend env`、`tend env diff`）。做了的是核心维度：CLI、代码、指令文件、会话用过的 skill 和 MCP、按路径存放的项目配置、模型提供方。未实现：「对比什么」表里其余各行、目标机器同一项目最近一个会话的记录、TUI 的报告面板、迁移对话框里的诊断。

### 数据来源

- **源端**：会话自己记录了它看到的环境。索引在增量扫描时把它叠加成 `File.Env`（字段和隐私边界见 [index-and-search.md](index-and-search.md)「全部会话与索引」），`envcheck.SeenOf` 把它转成 `Seen`，文件按 `Print` 的叫法命名。
  - Claude：每行的 `version`；`attachment` 里的 `instructions`（加载了哪些指令文件）、`skill_listing`、`invoked_skills`、`deferred_tools_delta`、`mcp_instructions_delta`、`agent_listing_delta`、`model`、`environment`（系统、shell、是否 worktree）；`Skill` 和 `mcp__…` 工具调用（用过哪些 skill 和 MCP 服务器）。
  - Codex：`session_meta` 的 `cli_version`、`model_provider`，`turn_context` 的模型和审批 / 沙箱策略。
  - 工具、子代理、skill 这几类记录的是增删事件，叠加成会话结束时的最终状态再比较。
  - 这些附件大约从 Claude Code 2.1.259 起才有，实测只有一半左右的会话带。老会话大多只能标「未知」，这是常态，不是例外。
  - Claude 记下的指令文件正文是处理过的（去掉了 frontmatter 和 HTML 注释），哈希和磁盘上的原文对不上。所以会话记下的文件只证明「当时加载了哪些」，用来标证据来源；内容比较用两边机器当前的文件。
- **目标端**：目标机器当前的配置（`env`）。目标机器上同一个项目最近一个会话的记录未实现。
- **每一项都注明证据的来源和时间**：`session`（会话当时看到的）、`config`（机器当前的配置）、`unknown`（未知）。读不到的维度标「未知」，不能判为一致。私有格式变化时，也便于查是哪一项读失败了。

### 采集：`env`

`envcheck.Collect(dir)` 在回答的那台机器上跑，只取名字、版本和哈希；`dir` 为空时只看机器本身（不比代码和项目配置）。

| 维度 | 取什么 | 不取什么 |
|---|---|---|
| CLI | `claude --version`、`codex --version`（各 5 秒超时，进程内缓存 1 分钟）；PATH 上有没有 | |
| 代码 | `git status --porcelain=v2 --branch`：分支、HEAD、未提交的文件数和前 30 个文件名；没推送的提交数（有上游比上游，没有上游数不在任何远端分支上的）；origin 的 URL，去掉用户名和密码，和它的 `RemoteKey`（`task.RemoteKey`，同一个仓库的 ssh、https 写法得到同一个） | 文件内容 |
| 指令文件 | `<claude>/CLAUDE.md`、`<dir>/CLAUDE.md`、`<dir>/.claude/CLAUDE.md`、`<dir>/CLAUDE.local.md`，以及它们 `@` 引用的文件（只跟一层，只认 `.md` / `.txt`，跳过代码块和行内代码）；`<codex>/AGENTS.md`、`<dir>/AGENTS.md`。每个文件：类别（`claude` / `codex` / `home` / `dir`，都不在下面的是 `path`）、相对名、`sha`（原始字节）、`norm`（`index.NormSHA`：换行统一成 LF、去掉首尾空白）；最多读 1 MiB | 正文（点开一项时才用 `env.file` 取那一个） |
| skill 和 MCP | skill：`<claude>/skills/*`、`<dir>/.claude/skills/*`、`plugins/installed_plugins.json` 里用户级或这个目录装的插件带的，记成 `插件:skill`；MCP 服务器名：`.claude.json` 的 `mcpServers` 和 `projects[dir].mcpServers` 的键、`<dir>/.mcp.json` 的键、Codex `config.toml` 的 `[mcp_servers.*]` 名 | 每个 MCP 的 `env`、`headers`、`args`、`url` |
| 按路径存放的项目配置 | 按目录找，经过软链也算（CLI 按解析后的路径记，macOS 的 `/var` 是 `/private/var`）。`.claude.json` 的 `projects[dir]`：`allowedTools` 括号前的工具名、`enabledMcpjsonServers`、`hasTrustDialogAccepted`；Codex 的 `projects."<dir>".trust_level` | `allowedTools` 的参数 |
| 模型提供方 | Claude 的 `<claude>/settings.json`、`<dir>/.claude/settings.json` 和 `settings.local.json` 里 `env` 的键名，`ANTHROPIC_BASE_URL` 只取主机名；Codex 的 `model_provider` 和 `model_providers.<名字>.base_url` 的主机名 | 任何值、密钥、token |

- JSON 按白名单流式解码，只走上表那些键，其余整段跳过；`config.toml` 按行读表头和这几个键。不打开 `.credentials.json`、Codex 的 `auth.json`、`.env`，不打日志。
- 文件在但读不了、解析不了的记进 `unknown`（`claude_json`、`mcp_json`、`claude_settings`、`plugins`、`codex_config`、`git`、`file:<类别>:<名字>`；CLI 在 PATH 上但读不到版本是 `cli:<名字>`）；文件不在不算未知。
- 协议：`env {dir, ref}`，`dir` 是绝对路径；带 `ref`（这台机器上的会话）时没给 `dir` 就用它的 cwd，回答多一个 `seen`。`env.file {kind, name, dir}` 只给 `Collect` 会列出的那些指令文件的正文，别的一律 `not_found`。
- 测试在 fixture 的 `.claude.json`、settings、`.mcp.json`、`config.toml`、凭据文件、`.env`、MCP 工具参数和 transcript 附件里埋假密钥和假邮箱，断言 `env` 的回答、`tend env` / `tend env diff` 的输出和索引缓存里都搜不到。

### 比较：`envcheck.Compare`

`Compare(src, dst Print, from, to pathmap.End) Report` 是纯函数，CLI 和交接包用它，TUI 和协调器以后也用它。

- 每一项是 `{dim, level: block | unequal | hint, name, here, there, evidence, at, what, fix}`，`fix` 是一句手动处理的提示（i18n 文本）。按级别、再按维度排，严重的在前；`Report` 带三个计数，第一行汇总「阻断 0 · 不对等 3 · 提示 5」。
- 证据：「会话当时看到的」优先，会话没记的用源机器当前的配置。
- 会话是 Claude 的只比 Claude 那一侧（skill、Claude 的 MCP、项目配置和提供方），Codex 的只比 Codex 那一侧；不针对会话时两侧都比。CLI 只比会话那个，不针对会话时比源机器上装了的。
- 指令文件按「类别 + 相对名」配对（`path` 类的先按路径映射），比 `norm`，所以 CRLF 和 home 不同不算差异；只差 `sha` 的报一条「只差换行」提示。
- 同一台机器上的 Windows 和 WSL 是近邻，同一台机器上的同一个目录就是它自己：都不比代码（[remote.md](remote.md)「路径映射」）。
- 目标机器上没有这个目录时只报那一条，目录下的指令文件和项目配置不再逐个报缺。
- 会话调用过、源机器配置里却找不到的 MCP 服务器（claude.ai 连接器或插件带的）报一条「未知」提示，不判缺失。

### 对比什么

| 维度 | 内容 | 级别 |
|---|---|---|
| CLI | Claude / Codex 的 CLI 版本 | 没装：**阻断**；更旧：**不对等**，附升级命令；读不到版本：**提示**（未知）；只有已知不兼容才阻断 |
| 代码 | 仓库在不在、origin 是不是同一个仓库（比 `RemoteKey`，没带它的旧 tend 由 URL 现算；源目录没有 origin 不比）、分支、HEAD、未提交改动、没推送的提交；未实现：AI **写过**的文件逐个比哈希（设上限） | 源目录是仓库而目标没有这个目录或不是仓库：**阻断**；源目录不是仓库而目标没有：**不对等**；其余：**不对等** |
| git 带不走的东西 | `CLAUDE.local.md` 随指令文件比，`.claude/settings.local.json` 的 `env` 键名随模型提供方比；未实现：它的其余设置、`.env`、worktree | **不对等** |
| 指令文件 | 全局 / 项目 / local 的 CLAUDE.md 及其 `@` 引用；`~/.codex/AGENTS.md`、仓库里的 AGENTS.md | 缺了、内容不同、目标多出来的：**不对等**；只差换行：**提示** |
| 记忆（未实现） | 项目记忆逐条比较；Codex 全局记忆里和本项目有关的条目；两边的 `autoMemoryDirectory` 设置 | **不对等** |
| skill、MCP；插件、子代理（未实现） | 会话结束时的最终状态 vs 目标机器的；会话用过的排在前面；插件带来的 skill 归到插件名下；claude.ai 连接器跟着账号走，账号无法安全确认时标「未知」，不去读凭据推断 | 用过的缺了：**不对等**；没用过的缺了：**提示** |
| 模型提供方 | 提供方名称、base_url 的主机名（不取密钥）；Claude 的 `env` 只看键名 | **不对等** |
| 按绝对路径存放的项目配置 | `~/.claude.json` 的 `projects[cwd]`（allowedTools、enabledMcpjsonServers、是否信任过）；Codex 的 `projects."<路径>".trust_level` | **不对等** |
| 权限与沙箱（未实现） | `permissions`、Codex 的审批 / 沙箱策略 | 目标机器放得更宽：**突出显示**；其余：**提示** |
| hook（未实现） | 配置的命令路径在目标机器上是否存在 | **提示** |
| 模型与推理强度（未实现） | model、effort | **提示** |
| 命令行工具（未实现） | 会话在 Bash 里用过的程序在目标机器上有没有、版本多少 | **提示**（排在最后做） |
| 系统（未实现） | 系统、shell、home | **提示** |
| 读不到的 | 两边各自的 `unknown` | **提示**（未知） |

### 展示

- 命令行：`tend env [--dir 目录] [--json]` 输出本机指纹，`--json` 就是 `env` 的回答；`tend env diff <机器> --session <id>`（针对一个会话）、`--dir 目录`（一个目录）、都不给（两台机器整体比较）；目标机器上的目录和交接到另一台机器一样找（`handoffDir`，见 [resume.md](resume.md)「交接到另一台机器」第 2 步，remote 先取源目录的 origin），找不到或找到几个时要 `--there` 给出；有阻断项时退出码非 0（[cli-and-config.md](cli-and-config.md)）。
- 想看某个 CLAUDE.md 具体差在哪，点开时才去两边各取那一个文件（`env.file`）做文本对比，不预先传正文。
- 交接包：`tend handoff <id> --host <机器>` 定下目录后，发起端用同一份 `Report` 写「环境差异」段（`Handover.Diagnose`：源机器经 `env` 回答会话当时看到的，目标机器回答那个目录的）：一行汇总，再列阻断和不对等的条目，只有名字和哈希，不列提示；stderr 也打汇总和阻断项。有阻断也照样交接，新会话从包里读到差异。有一边的 tend 没有 `env` 时，包里写明没有比较和原因。本机交接和同一台机器不加这一段。
- 完整迁移：`tend migrate` 在 stderr 打汇总和阻断、不对等的条目；有阻断项时不迁（`Migration.Run` 拒绝，`--dry-run` 退出码非 0）；只有不对等项时照样迁，迁移说明里写明这些差异。
- 未实现：迁移确认框和 TUI 报告面板里一行汇总、展开看明细，确认框里写明「到那边 AI 会缺什么」。

## 推后

| 什么 | 为什么 |
|---|---|
| Codex 按项目的记忆（旧布局 `memories/projects/<编码后的 cwd>/` 和 `extensions/external_agent_import/resources/<project-key>/`） | Codex 0.155.1 不再维护旧布局，只对旧数据有效；导入布局的归属写在 `scope.json` 里，要读内容才知道，等需要时再核 |
| `tend doctor` 检测 Codex `history_mode` | 只看文件名和修改时间没有稳定的信号（见「Codex：不做完整迁移」的风险监控） |
| 远端机器的孤儿扫描 | `tend doctor` 只扫本机；要看那台就在那台上跑 |
| TUI 回收站视图里的记忆条目 | 回收站视图只列会话；记忆用 `tend trash --restore` 还原 |
| 链式迁移（A→B→C 再回 A）的快进 | 只认直接的那一对，其余按 `exists` 拒绝，不会丢数据 |
| 定时检查分叉 | 检查要读文件，定时就成了轮询；只在 `tend show`、再迁之前检查 |
| 白名单外的 Claude 目录（如 `session-env/`） | 可能存着环境变量；列出来，不复制 |
| Web 上发起迁移；模式二由协调器驱动、数据只过一次 server | 要协调器替网页跑驱动函数并推送进度；两端都不是发起端时数据经 server 两次，分块、可续传 |

## 决定

1. 范围：不做云同步；单用户多机经自己的 SSH 做只读聚合，迁移由用户手动发起。
2. 机器：Mac、Linux、Windows、WSL 四类都支持，两端都做（发起端和远端）。多机功能的验收：每类系统都当两端，用 `tools/test-host` 的子进程 smoke 覆盖（两个数据集互为 `self` 和 `peer`）；发起端用 server 模式在 Mac 和 Windows 的真机上验，测试机之间不配 SSH 密钥；模式一的 ssh 发起端只验 Mac。WSL 不进这一期的验收：`pathmap` 的测试照样覆盖 WSL，以后用到时重搭 `wsl` 目标补跑 smoke。迁移还要覆盖断线、同 id 冲突、迁移中途源文件变化；Claude 完整迁移以目标机器上原生 CLI 能恢复为准，Mac→Windows、Windows→Mac 各一次。
3. 完整迁移默认复制：两边都保留；源机器上的行标 `-> <机器>`，在源机器恢复时提醒。
4. 环境诊断只报告差异，不对齐。
5. Codex 会话完整迁移不做，列为验证项。
6. 不做 rsync 镜像。
7. 做分发命令 `tend hosts install`（含 WSL 和容器）：只在用户手动执行时推送。

## 待核实

- 已核实：
  - Claude 记忆的加载上限是前 200 行或前 25KB；记忆按 git 仓库存放，可以用 `autoMemoryDirectory` 挪位置。来源：Claude Code 官方文档「How Claude remembers your project」。
  - `codex migrate-rollouts`：结果见「Codex：不做完整迁移，列为验证项」。
- 未核实：
  - Claude 完整迁移在真机上用原生 CLI 恢复：Mac→Windows、Windows→Mac 各迁一个专门建的测试会话，在目标机器上 `tend resume <id>`（即 `claude --resume`）跑一轮（决定 2）。
  - Windows 上运行 TUI 的终端能力（pty、鼠标、宽字符）是否满足 tend 的要求：需要在一台 Windows 机器上实测，也可以用 `tend hosts check` 一并测。
  - 旧格式 Codex 会话迁移后，原生 `codex resume` 能否接着做（见「Codex：不做完整迁移，列为验证项」）。
