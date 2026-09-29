# 执行与隔离

任务的分支和工作树、评审与测试的只读副本、子任务合进集成分支、交付物、跨机器接力和护栏。实现：`internal/node`（`work.go`：工作树、fetch / push、合并、hooks；`agent.Workspace`）、`internal/task`（`work.go`：分支字段、`stale`、`merge_conflict`）、`internal/coord`（排合并 run、按「目录 + 分支」判断占用）。

## 分支属于任务，不属于 agent

- 任务第一次派发时，节点在项目仓库上建 worktree。
  - 分支叫 `tend/<task>`，从父任务的集成分支切（没有父任务就从 `base` 切）。
  - 目录放在 `<仓库>-wt/<task>`；由项目指定别的位置未实现。
- 这个任务后续的所有开发阶段、所有打回轮次，都在同一个 worktree、同一条分支上。
- **评审和测试阶段不碰这个 worktree。**
  - 节点在分支当前的提交上另建一个分离 HEAD（detached）的只读 worktree，run 在那里跑；
  - 评审角色的权限另外收紧：claude 禁用编辑工具；codex 在这个一次性副本上用 `workspace-write`（要能构建和跑测试），没有副本时用 `read-only`；测试角色不收紧，靠副本丢弃兜底；
  - run 结束后，这个 worktree 里的任何改动都丢弃，并在 workpad 里注明「评审阶段改了 N 个文件，已丢弃」；
  - 被说明书要求「不改代码」的评审者仍可能改代码（Multica 里的 Reviewer 就调用了 `patch_apply`），所以只读要靠机制，不靠说明书。
- verdict 记下被评审的提交。分支之后又有新提交，这个 verdict 就作废，重新评（见 [workflows.md](workflows.md)「协调器规则」）。
- 用户自己的 checkout 不动。不做未提交改动的回放（Multica 做了）。
- 不开 worktree 的任务（`dir` 直接指定，或仓库没打开 `worktrees`）照目录规则：同目录的 run 串行。

## 集成分支

子任务怎么合到一起。

- 有子任务的任务，它的分支 `tend/<parent>` 就是**集成分支**。
- 子任务完成所有阶段（含验收）后，由节点把 `tend/<child>` 合进集成分支。合并放在所有阶段之后，而不是最后一个非人工阶段之后：少一个中间态。
  - 冲突就停下：子任务进「等你」，原因是「合并冲突」。你可以自己解决，也可以派一个开发 run 在集成分支上解决冲突。
- 依赖（`after`）只决定派发顺序。
  - 被依赖的任务先完成，并已合进集成分支；
  - 依赖它的任务在这之后才从集成分支切出来，天然带着前面的代码。
  - 所以不需要让分支层层叠加，也不需要 agent 手工合并。
- 没有依赖关系的兄弟任务并行开发，各自合进集成分支；后合的遇到冲突，就按上面的规则停下。
- 父任务的 `accept` 在集成分支上做。验收通过后，看板显示「可合并」和分支名。
- **合进 `base`（main）不自动做**：由你来，或者交给一个专门的 merge 任务。

## 交付物

run 结束时节点记录 `{dir, branch, head, commits, diffstat, warnings, pr}`。

- `pr`：agent 用 `gh pr create` 开了 PR 之后，用 `tend run note --pr <url>` 报告；从最后一条消息里识别未实现。
- 记账失败写进 `warnings`，不改 run 的结论（见 [workflows.md](workflows.md)「结构化结果」）。

## 跨机器接力

- 项目配了 `remote` 时，每个 run 开始前 fetch、结束后把分支推上去，集成分支同理。阶段换机器（比如 test 放在 Linux 机器）时，下一台机器 fetch 同一分支再建 worktree。这样成果不会只留在某个人的机器上（见 [team.md](team.md)「成员离开」）。节点之间仍然不直接通信。
- 项目没配 `remote` 时，所有阶段只能在同一台机器上跑，`run.preview` 会说明。
- 会话不跨机器、不跨 provider 接续：评审换 provider 本来就该是新会话。开发阶段打回续接时，要求和上次同一台机器、同一 provider；那台机器不在线，任务就排队，原因写「等 <机器> 上线」。

## 护栏

- 并发：沿用机器 `slots`；项目级 `max_active`（默认 3）未实现。
- 预算：AgentDef、workflow、任务三级 `budget{usd, tokens, minutes}`，超出时软停止（interrupt），任务进「等你」。现在只有 workflow 的 `budget{usd, minutes}` 生效，且只在派发前检查（见 [workflows.md](workflows.md)「实现」）；AgentDef 和任务级预算、token 上限、软停止未实现。
  - claude 有费用；codex 只有 token。没有计价的模型只显示 token，标「未计价」，不拿 0 充当费用（Multica 里 codex 的费用停在 $2.06 不再变化，疑似没计价）。
- 权限：`bypassPermissions` 仍受节点 `allow_bypass` 约束；评审和测试阶段在丢弃改动的副本上跑，评审角色另外收紧权限，拆解 run 始终 `read-only`。

## 实现

- **开关**：项目仓库的 `worktrees: true`（网页仓库行里写 `worktrees`）才启用；不写就在 checkout 里跑，同目录串行。它是显式开关，已有项目不会因为升级突然改成分支工作流。
- **工作区**（`agent.Workspace`，节点 feature `worktree`）：`{checkout, branch, chain, base, remote, read_only, merge, setup, cleanup}`。
  - 分支 `tend/<任务 id>`，工作树在 `<checkout>-wt/<任务 id>`；`chain` 是祖先任务的分支（外层在前），第一条从 `base`（有 remote 时优先 remote 上的 base）切。
  - 节点在监督进程里建工作树（同一 checkout 的 git 簿记由节点级锁串行），新建时跑 `setup` hook，失败就删掉工作树、run 以 `setup_failed` 失败。
  - 分支名、base、remote 都由节点校验，工作树路径由节点自己算，准入按 checkout 判断。
  - git 操作由节点在 `internal/node/work.go` 里包一层 exec 直接调 `git`，没有单独的包。
- **开发阶段**：agent 正常结束后，节点把它没提交的改动提交掉（`git add -A`，作者是派发人，警告里写明提交了几个文件），再跑 check hook，然后记录交付物；有 remote 就推分支，推失败只记警告。任务书末尾告诉 agent 在自己的分支上提交、不要切分支或推送；沙箱挡住提交时不要为提交请求批准，留着改动，停下后由节点提交：codex `workspace-write` 写不了工作树的 gitdir，它在主 checkout 的 `.git/worktrees/` 下，不在可写根里。
- **评审和测试阶段**（要 verdict 的阶段）在 `<checkout>-wt/.ro/<run>` 的分离 HEAD 副本里跑，结束后数改动文件（`discarded`）、删副本；崩溃留下的副本下次准备时清理。
  - 评审角色额外收紧：claude 禁用 Edit / Write / MultiEdit / NotebookEdit；codex 在副本上用 `workspace-write`（`read-only` 连临时目录都建不了，构建和测试跑不起来，副本反正丢弃），没有副本时用 `read-only`；接着评审的 run 换了 agent 也照此收紧。
  - 拆解 run 始终 `read-only`。测试角色不收紧（要能构建），靠副本丢弃兜底。
- **任务状态**：`Task` 有 `branch` `work_on` `head` `merged`，都由事件折叠推导：排队的写入 run 给任务和 chain 上的祖先记分支和机器；写入 run 结束时 `head` 取它留下的；合并 run 成功时子任务 `merged`、父任务 `head` 前移。
- **作废重评**：要 verdict 的阶段，run 看到的 head 和任务当前的 `head` 不同 → `queued: stale`，协调器重派这个阶段。实际触发场景是父任务评审后又有子任务合进来。
- **合并**：任务有分支、有父任务、还没合进去时，本该写 done 的地方（run 成功、最后一个阶段通过、人工放行、手动标完成）改为排一个合并 run（`stage: merge`，agent 记作 `git`，不跑 agent，以任务负责人的身份）。
  - 节点在父任务的工作树里 `git merge --no-ff`；冲突就 `merge --abort`，run 以 `merge_conflict` 结束、带冲突文件，任务进 `waiting: merge_conflict`。
  - 在父任务的工作树里自己合完提交后，`task.merge`（网页「重试合并」、`tend task merge`）再排一次，已合入就直接成功。
  - 合并成功后跑 `cleanup` hook、删掉子任务的工作树（分支保留），任务才写 done；依赖和父任务都以 done 为准，所以「先完成的已经合进集成分支」自然成立。
- **并行**：协调器按「目录 + 分支」判断占用（只读副本各自独立），同一 checkout 下的兄弟任务并行；合进同一父分支的合并 run 互相串行。
- **跨机器**：没有 remote 时，一棵树的分支都在最先建分支的机器上（`work_on`），之后的 run 自动派到那台；显式指定别的机器会被拒并说明。有 remote 时每个 run 开始前 fetch、结束后 push，别的机器可以接着做（节点测试覆盖了另一台机器评审同一提交）。
- **交付物**：`tend run note --pr <url>` 报告 PR；`tend run show` 和网页详情显示分支、head、提交数、diffstat、丢弃数、警告。没有父任务的任务完成后显示「可合并」和分支名，合进 base 仍由人来。
- **AgentDef 文件**（节点 feature `files`，只对 claude）：`hooks` 写进 run 目录的 `settings.json` 用 `--settings`，要节点 `allow_hooks`；`mcp` 按名字取节点 `node.mcp` 里的配置写进 `mcp.json` 用 `--mcp-config`，值不离开节点；`skills` 要求节点的 `~/.claude/skills/<名字>` 已装好，否则派发被拒。节点先对不含自家文件的命令行做绕过检查，再加上 `--settings` / `--mcp-config`。codex 的 hooks 和 mcp 被节点拒绝，预检提示 `def_pending`。
- 未实现：skills 同步、`output` 结构化输出、AgentDef 的 `budget`、项目 `max_active`。
