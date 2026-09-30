# Workflow 与阶段

workflow 的定义、协调器推进任务的确定性规则与不变式、任务的处境、结构化结果与 workpad、三类 hooks、消息与插话、人工闸门和预算。实现：`internal/workflow`（定义、内置 workflow、阶段任务书、workpad）、`internal/task`（`flow.go`：阶段、处境推导）、`internal/coord`（`workflow.go`、`tree.go`：`flow()` 推进、`task.gate`、`task.message`）。

## 定义

```markdown
---
name: feature
stages:
  - {name: plan,      role: planner,   gate: human, output: plan-md}    # 可选：先出实现计划，你批
  - {name: implement, role: implement}
  - {name: review,    role: review,    output: verdict, on_rework: implement}
  - {name: test,      role: test,      output: verdict, on_rework: implement, machine: linux}
  - {name: accept,    gate: human}                                       # 你验收：放行 / 打回
max_loops: 2
---
## implement
{{task.brief}}
验收标准：{{task.acceptance}}
{{#rework}}上一轮评审意见：{{rework.notes}}{{/rework}}
## review
读 `git diff {{task.base}}...{{task.branch}}` 和 workpad，按验收标准逐条给结论……
```

- 内置 `feature`（implement → review → accept）、`fix`（implement → test → accept）、`docs`（implement → accept）三个。
- `plan` 阶段和 `output: plan-md` 未实现：拆解走 `task.plan`（见 [planning.md](planning.md)「拆解」）。
- 阶段只能串行。并行来自兄弟任务，不在阶段内部。

## 协调器规则

确定性，对 journal 折叠后派发。

**不变式**：每个未完成的任务在任意时刻恰好处于下面一种状态，并且带有原因。

- **运行中**：有一个未结束的 run；
- **排队**：可以派发，但在等依赖、机器、slot 或目录，原因写明等什么；
- **等你**：原因写明是哪一种（见 [board.md](board.md)「列」）。

没有第四种。协调器每次折叠后都检查这条；单元测试用随机事件序列验证它。理由：Multica 的 issue 卡在评审中，却没有任何状态指出来。

| 情况 | 动作 |
|---|---|
| 任务 `todo`，`after` 全部完成，没有未结束的 run | 排当前阶段的 run：用阶段的角色找 AgentDef，按 `machines` 和项目仓库映射选机器，受 slots 和目录互斥约束 |
| 阶段 run 结束，退出码 0，没有 attention，verdict 为 pass 或该阶段不要求 verdict | 推进到下一阶段（`task_staged`），排下一阶段 |
| verdict = rework | `loops+1`，回到 `on_rework` 阶段。开发阶段**续接它上一次的原生会话**（`run.continue`，任务书是评审意见加 workpad 里积攒的消息）；评审和测试阶段每次都是新会话，验证者和实现者分开 |
| 评审或测试的 verdict 针对的提交已经不是分支头 | verdict 作废（记为 `stale`），重新排这个阶段；不会拿旧提交的结论去推进或打回（见 [execution.md](execution.md)「分支属于任务，不属于 agent」） |
| `loops` 超过 `max_loops`、verdict = blocked、run 失败、预算用完 | 任务进「等你」，通知一次 |
| `gate: human` 的阶段 | 不派 run，任务进「等你」：`attention: approval`。验收人「放行」就进下一阶段；项目参与者都能「打回」，带着意见回 `on_rework` 阶段，默认回前面最近一个开发阶段（不是闸门、不出 verdict 的阶段；见 [team.md](team.md)「人在任务里」）。走 workflow 的任务，只有合法的放行才能让它完成，`task.set_status` 不能跳过阶段 |
| run 在跑时有 attention（权限、提问） | 照常作答，不影响阶段 |
| 子任务完成 | 节点把子任务分支合进父任务的集成分支（见 [execution.md](execution.md)「集成分支」）；冲突则子任务进「等你」 |
| 父任务的子任务全部完成 | 父任务走自己的 workflow（在集成分支上验收），不自动完成；父任务在此之前不派任何 run |
| 取消任务 | 取消它的排队 run，停止正在跑的；子任务一并取消（先确认） |
| 其余任何情况 | 按不变式归入「等你」并写明原因 |

**状态只由协调器写。** agent 只报告结果：verdict、提问、卡住。这是和 Multica 最大的不同。

**推进阶段不花 LLM。** 在 Multica 里，每次阶段切换都要唤醒父 issue 的 agent 跑一轮（一条 issue 上跑了 4 次）；tend 由规则直接推进。

### 处境与开始（实现）

- `Situation{kind, reason, run}` 由 `task.State.Situation` 推导，网页的 `core/fold.js` 逐条照搬：
  - `backlog`：还没开始，不算在不变式里，也不派发。
  - `running`：有未结束的 run。
  - `queued`：会自己往下走：`after`（前置任务没完成）、`children`（子任务没完成）、`drain`（run 的机器停止接新运行，见 [../runs/coordinator.md](../runs/coordinator.md)「调度与对账」；先于 `dir` 判断）、`slot`（run 在等机器接手：离线、连接中或并发槽满）、`dir`（同一台机器上另一个不在自己分支或副本上的 run 正在这个目录里跑；判断用的是 run 记下的目录，映射后不同就算作 `slot`）、`ready`（协调器马上派发）、`completing`（run 成功，协调器马上标完成）；workflow 的 `advance`、`rework`、`stale` 见下文「实现」。
  - `waiting`：要有人动手：`accept`（子任务全部完成等验收，或在人工闸门）、`dispatch`（没开始、也没人派发）、`after_canceled`、`held`（协调器派不出去，`held` 字段写原因）、`ended`（手动派发的 run 正常结束，等人标完成），以及 run 的 `asked`、`permission`、`unknown`、`failed` 等结束原因；workflow 的 `max_loops`、`blocked`、`budget`，拆解的 `draft`、`no_plan`（[planning.md](planning.md)「实现」），合并的 `merge_conflict`（[execution.md](execution.md)「实现」），需求的 `source_changed`、`source_closed`、`source_reopened`（[trackers.md](trackers.md)「导入与需求快照」）。
- **开始**：`task.start` 把任务和它整棵子树里未完成的任务标为自动（`auto`，记下 `start_seq`），backlog 的变成 todo。之后由协调器的 `flow()` 在每次提交后推进：`ready` 的以任务主人的身份派发（照常走权限和可见性检查），失败就写 `task_held`；`completing` 的写成 done。没开始的任务照旧手动派发，`waiting: dispatch` 是它们的常态，不进收件箱也不发通知。
- **依赖**：上游 `done` 才算满足；有分支的任务合进父任务的集成分支之后才写 done（见 [execution.md](execution.md)「实现」）。上游被取消，下游进 `waiting: after_canceled`，由人决定改依赖还是取消。
- **重试** = 再次 `task.start`：`start_seq` 之前的 run 算作更早的尝试，不再决定现状；编辑任务或再次开始都会清掉 `held`。
- **重开**：已结束（done 或 canceled）的任务改回 todo 或 backlog（`task.set_status`：重新打开、已结束的先不开始）是一条尝试的分界线：任务不再是自动的（`auto` 清掉），`start_seq` 记成这次改状态的 seq，`merged` 清掉。之前的 run（包括合并 run）都算更早的尝试，不再决定现状，所以协调器不会把它再标完成；改回 todo 而没有新 run 时它是 `waiting: dispatch`。走 workflow 的任务在同一条命令里回到当前阶段的 `Flow.Back`（和打回一样，`loops` 不加）。要再跑就再开始或派发。来源 issue 上 tend 自己的关单或标签跟着撤回（[trackers.md](trackers.md)「回写」）。issue 在外面被重新打开时，协调器替人做同一次重开（`waiting: source_reopened`，见 [trackers.md](trackers.md)「导入与需求快照」）。
- **撤销**不是重开：`task.undo{id, command}` 把任务的状态、`auto`、`start_seq`、`merged`、阶段（`stage`、`loops`、`stage_seq`、`stages`）和位置（`parent`、`after`、`held`）逐项放回被撤销的那条 `task.set_status` 或 `task.move` 之前的样子（`task_restored`），所以撤销完成后任务又落在原来的处境、回到收件箱，撤销重新打开后它原样是 done（workflow 任务也一样）。原位置已经放不下（原父任务结束了、有打开的 run、太深）时拒绝撤销移动。只还原那条命令改过的；协调器因它已经做了的事（派发了下游、父任务往下走）不撤回。完成排的是合并 run 时，撤销在合并还在排队时取消它（`run_canceled`，`reason: undone`，这样的 run 不算任何一次尝试），开跑了就拒绝。撤销完成后，来源 issue 上 tend 自己的关单或标签也撤回（和重开一样）。
- **父任务**在子任务完成前从不派 run。子任务全部完成后，没有 workflow 的父任务进 `waiting: accept`，由人标完成；有 workflow 的走自己的阶段。
- 树最多三层；`task.move` 改 parent 和 after，拒绝成环、跨项目和超过深度。
- 随机事件序列测试（`internal/task/tree_test.go`）在每一步检查不变式：每个未完成、非 backlog 的任务恰好落在 running / queued / waiting 之一，并带原因。

## 结构化结果

- **verdict**：`{verdict: pass|rework|blocked, summary, findings: [{severity, file, line, text}]}`。优先用 provider 的结构化输出；拿不到就用 `tend run verdict pass|rework|blocked "…"` 自报（经 `TEND_RUN_DIR` 写进 reports，和 `tend run ask` 同一条路）。两样都没有，按 blocked 处理。
- **plan**：见 [planning.md](planning.md)「拆解」。
- **workpad**：每个任务一份，由协调器拼出来，不让 agent 去改。内容是各阶段的 summary、findings、rework 意见、分支和 diffstat、你在阶段之间发的消息，作为下一阶段任务书的一部分。它替代了评论线程（Symphony 的 workpad、Conductor 的 `.context` 同一思路）。
- **结论和记账分开**：一个 run 的结果拆成三件事，互不覆盖：
  - agent 的结论：verdict 或交付；
  - 进程的结束：退出码、原因；
  - 节点的记账：分支、提交、diffstat 是否记录成功。

  记账出错只作为警告挂在 run 上，并进入「等你」的原因，不会把一个已经给出 verdict 的评审判成失败（Multica 就把评审这样判错过）。

## hooks

分三类，各在各的位置，不混。

| 类 | 在哪跑 | 用途 | 定义在 |
|---|---|---|---|
| 1. tend 生命周期 hooks | 目标机器，工作目录里，由监督进程执行 | `setup`：worktree 建好后跑，比如装依赖；`before_run`：每次运行开工前在任务的 worktree 里跑（只读副本和合并不跑），失败则运行失败（`before_run_failed`），worktree 保留，要节点 feature `before_run`；`check`：阶段结束后跑，比如 `mise run gate`，失败等同 verdict=rework，输出进 workpad；`cleanup` | project（可被 workflow 阶段覆盖） |
| 2. provider hooks | agent 进程内 | Claude Code 的 `PreToolUse`、`Stop` 等：拦截危险命令、结束前强制跑测试 | AgentDef 的 `hooks`，编译进 `--settings` |
| 3. 事件 hooks | 协调器 | `notify_command` 扩展到任务事件：`task.needs_you`、`task.stage`、`task.done`、`task.rework` | `config.json` |

第 1 类是 tend 独有的：「测试在 Linux 机器上跑 gate，不过就自动打回」不需要 agent 配合。

第 3 类：`task.needs_you`（任务在 `waiting`、`dispatch` 除外，并且有了新的待处理项）、`task.done`、`task.stage`（进入某阶段）和 `task.rework`（被退回，`stage` 是退回到的阶段）。后两个只发给点名了它们的 `notify_command`，不推个人 webhook：它们不需要人动手。模式一的 `notify_command` 默认只听 `run.*`，任务事件要在 `notify_events` 里点名才发，环境变量多一个 `TEND_TASK`；团队模式走个人 webhook（见 [team.md](team.md)「人在任务里」）。

## 消息与插话

你随时可以给一个任务发消息。发出去的消息去哪，取决于任务当时的状态；不会另起并发 run（Multica 会把运行中的评论排成并发 run，让评审评了旧提交）：

| 任务当时 | 消息去哪 |
|---|---|
| 有 run 正在跑，收消息 | 发给它：插进当前这一轮，或者这一轮完了续接（也可以先打断） |
| run 还没开始，或在跑但不收消息 | 发不了 |
| 这个阶段上一个 run 已结束、能续会话 | 作为回复续接它 |
| 其余 | 进 workpad，留给下一个阶段 |

发送框上直接写明这条消息会去哪（例如「会插进 Dev 当前这一轮」「会留给下一轮开发」），不用一个意义不明的开关。

## 实现

- **定义**：`internal/workflow` 读 Markdown + YAML frontmatter（`name`、`stages`、`max_loops`，默认 2、`budget{usd, minutes}`），每个 `## <阶段名>` 小节是该阶段的任务书模板；阶段字段 `name` `role`（planner / implement / review / test）`agent` `gate: human` `output: verdict` `on_rework` `machine` `check`。内置 `feature` `fix` `docs` 嵌在二进制里；项目的 `workflows`（名称 → Markdown）同名时覆盖内置。阶段没写模板时按角色用内置的默认任务书（implement、review、test、planner）。
- **冻结**：任务拿到 workflow 时（`task.create` 的 `workflow`，缺省取项目 `defaults.workflow`，`none` 表示不用；`task.edit` 可换，有 run 在跑时拒绝）把整份定义解析后存进 `Task.Flow`，之后改项目里的定义不影响已有任务。换 workflow 从第一阶段、第 0 轮重来。
- **状态**：`Task` 有 `workflow` `flow` `stage` `loops` `stage_seq` `notes`；`Run` 有 `stage` `judge`（要 verdict）`check`（hook 的 argv）`verdict` `checked`。事件 `task_staged{id, stage, loops, back}`、`task_noted{id, note}`（都会让 `rev` 加一）。
- **推导**：已开始的 workflow 任务在没被别的原因挡住时，看当前阶段：闸门 → `waiting: accept`；本阶段（`stage_seq` 之后）还没 run → 预算用完就 `waiting: budget`，否则 `queued: ready`；最新 run 正常结束 → 按 verdict：pass（或不要求 verdict 的阶段）→ `queued: advance`，rework → `queued: rework`，但 `loops` 已到 `max_loops` 就 `waiting: max_loops`；要求 verdict 却没给、或给了 blocked → `waiting: blocked`；check 失败等同 rework；verdict 针对的 head 不是任务当前的 head → `queued: stale`。run 失败、提问等照旧。`fold.js` 逐条照搬，随机不变式测试覆盖了这些原因。
- **推进**：`flow()` 每次提交后写 `advance`（下一阶段，最后一阶段之后写 done）和 `rework`（先记一条 `rework` 笔记写明原因：verdict 摘要或失败的 check 输出，再回 `on_rework`，`loops+1`）。派发时按阶段选 agent（阶段的 `agent` → 项目 `roles[role]` → 任务的 agent），任务书由模板填出（`{{task.brief}}` `{{task.title}}` `{{task.acceptance}}` `{{#acceptance}}…{{/acceptance}}` `{{#rework}}…{{/rework}}` `{{rework.notes}}` `{{loops}}` `{{stage}}` `{{workpad}}`），没写 `{{workpad}}` 就把 workpad 接在后面。被退回的开发阶段在同一台机器、同一 provider 上续接上一次的会话，任务书只有退回原因和 workpad；评审和测试每次都是新会话。
- **verdict**：自报：要 verdict 的 run 任务书末尾会告诉 agent 用 `tend run verdict pass|rework|blocked "摘要"`，写进 `reports.jsonl`，节点的观测带回协调器。provider 结构化输出（见 [overview.md](overview.md)「待核实」）和 `findings` 未实现。作废重评见 [execution.md](execution.md)「实现」。
- **check hook**：项目 `hooks.check`，阶段写了 `check: true` 才跑；节点监督进程在 agent 退出码 0 之后在工作目录里跑它（30 分钟上限），输出接进 `output.log`，结果 `{argv, exit, tail}` 随观测回来。节点要 `node.allow_hooks`（或 `allow_bypass`）才接受带 hook 的 run；节点 feature `verdict`、`check` 由协商决定，旧节点会被拒绝派发。
- **人工闸门**：`task.gate{id, pass, notes, expected_rev}`。放行只有验收人（没有就是负责人）或管理员可以（模式一不限）；任何能写任务的人都能打回，意见记成 `gate` 笔记。`expected_rev` 不等于当前 `rev` 回 conflict。走 workflow 的任务 `task.set_status done` 一律拒绝，只能由最后一个阶段完成；需求有未决变化时放行最后一关也被拒。
- **消息**：`task.message{id, text, mode, expect}` 按上文「消息与插话」路由（规则是 `task.Route`，见 [coordinator.md](../runs/coordinator.md)「能做什么、等谁」），回答 `{to: run|reply|workpad, run}`；进 workpad 的记成 `message` 笔记。
- **workpad**：`workflow.Workpad` 按时间列出各阶段 run 的 verdict 或结束方式、失败的 check（输出尾部）、留言和闸门决定，上限 8 KiB（保留最新的）；`rework` 笔记不重复列出（它就是那个 run 的结论）。
- **预算**：workflow 的 `budget` 只在派发前检查（已结束的 run 的费用估计和时长），不中途停 run。
- **界面**：网页任务表单选 workflow，详情页显示阶段条、第几轮、workpad、放行 / 退回、写明去向的留言框，走 workflow 的任务不显示「标记完成」；项目设置里选默认 workflow、编辑自定义 workflow。CLI：`tend task add --workflow`、`tend task gate <id> --pass | --rework "…"`、`tend task message <id> "…" [--mode steer|after|interrupt]`、`tend run verdict`。进度评论多一行 `Stages: implement → **review** → accept (round 2)`。
