# 需求与拆解

需求怎么进来、planner 怎么把它拆成任务树、草稿怎么由人改和确认，以及任务的字段。实现：`internal/task`（`plan.go`：计划格式与草稿；任务字段与事件）、`internal/workflow`（`PlanBrief`）、`internal/coord`（`task.plan` / `plan_save` / `plan_apply`）、`internal/node`（feature `plan`）、`cmd/tend`（`tend run plan`、`tend task plan|draft`）。

## 需求

- **需求就是 `kind: requirement` 的根任务**，不单独建对象。它的 `source` 可以有两种：
  - 链接：GitHub、Gitea、Linear 等；
  - 贴进来的一段文字或一个文件。
- 需求被抓取时，冻结一份快照：`{ref, fetched_at, digest, text}`（参考 Multica 的 `issue_source_context`）。之后 issue 再变，快照更新并标「需求有变化」，界面列出改了什么。
- 绑定了 Gitea / GitLab / GitHub 的项目，由工单同步自动导入和回写（见 [trackers.md](trackers.md)）；没绑定的，仍可贴一段文字或一个链接手动建需求。

## 拆解

LLM 起草，人确认。

1. `tend task plan <需求任务>`，或在界面上点「拆解」：用 planner 角色的定义跑一个 run。
   - 输入：需求快照、项目 context、仓库现状；
   - 输出按 schema 校验的计划：
   ```jsonc
   {"tasks": [{"key": "api", "title": "…", "brief": "…", "acceptance": ["…"],
               "after": ["model"], "workflow": "feature", "role_hint": "implement",
               "machine_hint": "linux", "size": "S|M|L"}],
    "questions": ["…"]}          // 有疑问就先问：你回答后它续同一会话重出
   ```
2. **草稿树**在 TUI 或网页里显示成可编辑的树：改标题和任务书、调整父子和依赖、删、加。拖动调整、合并两条和给某个叶子「再拆一层」（最多两层）未实现，现在按条用表单或 `$EDITOR` 编辑。没确认之前不产生任务。
3. **确认**：`plan_applied` 事件一次性建出子树，全部是 `backlog`，建出来就是可以直接派发的正式任务。
4. **启动**：选中根或任意子树点「开始」，子树里的 `backlog` 全部改成 `todo`，然后按依赖分批派发。不存另一个标志，`backlog` 本身就是人工关口（Multica 的做法）。

## 实现

- **起草**：`task.plan{id, agent?, machine?}`（网页「拆解」、`tend task plan`）排一个拆解 run：agent 默认项目 `roles.planner`，在任务的目录或仓库 checkout 里跑，不开 worktree（拆解要能在同一会话里续：claude 按目录找会话，副本每次路径不同），档案按只读收紧（同评审）。
  - 任务书由 `workflow.PlanBrief` 写：怎么拆（小、能并行就并行、用 after 排序、每条要能直接动手、可检查的验收标准、拿不准的放 questions）、可选的 workflow 名单、任务本身（需求就是 issue 快照）和它的验收标准，已有草稿时附上让它改。
  - 节点在任务书末尾附 `tend run plan - <<'PLAN'` 的约定（从标准输入交，只读沙箱里写不了文件）和 schema（feature `plan`）。
  - backlog 里的任务也能拆；已经有子任务的不能再拆。
- **计划格式**（`task.Plan`）：`{tasks: [{key, title, brief, acceptance, after, parent, workflow, role_hint, machine_hint, size}], questions}`。`tend run plan` 先校验再提交，错误原样告诉 agent 让它改了重交：key 小写且唯一、after 和 parent 指向存在的 key、不成环、最多两层、不能排在自己的祖先或后代之后、最多 50 条、256 KiB、未知字段拒绝。
- **草稿**：拆解 run 带着计划结束时，折叠直接把它变成任务的 `draft{plan, run, source_rev}`，不另写事件；`plan_drafted`（`task.plan_save`，计划为空即丢弃）记录人的修改，`plan_applied` 清掉草稿。
  - 有草稿、还没子任务的任务处于 `waiting: draft`（backlog 里的除外）；拆解 run 没交计划就是 `waiting: no_plan`。
  - 计划里的 questions 在草稿上显示，回答走 `run.continue` 续拆解会话，续出来的 run 仍是拆解 run，交的新计划替换草稿。
- **确认**：`task.plan_apply{id, expected_rev}` 按「父在子前、被依赖的在前」的顺序一次建出子树（同一个信封里的 `task_created` 加 `plan_applied`）：全部 backlog，负责人和验收人随根任务，目录和机器沿用根任务，workflow 按项目解析，`role_hint` 对上项目某个角色就用那个 agent，`machine_hint` 是已知机器才用。然后点根任务的「开始」按依赖派发。
- **绑定需求版本**：草稿记下生成时 issue 的版本；issue 有未决变化、或草稿的版本不等于当前版本时拒绝 apply（`source_changed`），人看过后再保存一次（版本随之更新）才能建。
- **界面**：网页详情页的草稿树、问题和回答框、「编辑草稿」（每条一个表单：标题、属于、workflow、规模、任务书、验收标准、先完成）、「按草稿建任务」「丢弃草稿」；CLI `tend task draft <id> [--save f | --apply | --discard]`；TUI 任务对话框的「拆解」「草稿」（见 [ui.md](ui.md)「TUI 完整交互」）。

## 任务的字段

```
Task += { project?, parent?, after: [t_…], kind: requirement|work, tags: [],
          acceptance: [], owner, approver, source?,
          workflow?, flow?, stage?, loops, stage_seq, notes,
          branch?, work_on?, head?, merged?, draft?, issue?, pr? }
status: backlog | todo | done | canceled      // 只存这四个；「运行中 / 评审中 / 等你」由事实推导
```

- 协调器写的还有 `auto`、`start_seq`、`held`（见 [workflows.md](workflows.md)「处境与开始（实现）」）和 `done_at`（最近一次变成 done 的时间，完工摘要用，见 [runs/coordinator.md](../runs/coordinator.md)「通知」）。`task.create` 不写 project 时沿用父任务的。
- 各组字段的来处：workflow 一组见 [workflows.md](workflows.md)「实现」；分支一组见 [execution.md](execution.md)「实现」；`source`、`issue`、`pr` 见 [trackers.md](trackers.md)「导入与需求快照」；负责人和验收人见 [team.md](team.md)「人在任务里」。预算在 workflow 上（`flow.budget`），任务级 `budget` 未实现。
- 事件：`task_moved`（改 parent、after）、`task_restored`（撤销，见 [workflows.md](workflows.md)「撤销」）、`task_started`、`task_held`、`plan_drafted`、`plan_applied`、`task_staged`、`task_noted`、`task_sourced`、`task_source_acked`、`task_linked`；verdict 随 run 的观测回来，不单独成事件。
- 旧 journal 照样能折叠：新字段都是可选的，没有 `workflow` 的任务就是单 run 任务。
