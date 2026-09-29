# Project

项目：关联的仓库和它们在各台机器上的路径、需求来源、项目说明、默认 workflow 与各角色的 agent、生命周期 hooks，以及派发时怎么从这些默认值里取任务的目录、agent 和机器。实现：`internal/task`（项目事件的折叠）、`internal/coord`（`project.*` 方法、派发）。

## 格式

```jsonc
{
  "id": "p_shop",                    // 短 id，看板和 CLI 里用
  "name": "Shop",
  "repos": [{
    "name": "app",
    "remote": "git@git.example.com:team/app.git",  // 跨机器接力的共享 remote，见 execution.md「跨机器接力」
    "base": "main",                            // 新任务分支从这里切
    "dirs": {"local": "~/work/app", "linux": "/srv/app", "win": "D:\\work\\app"}
  }],
  "links": [{"kind": "tracker", "url": "https://git.example.com/team/app/issues"}, {"kind": "doc", "url": "…"}],
  "context": "context.md",           // 项目说明，注入这个项目下每个 run 的任务书（放 run 目录，不写仓库）
  "defaults": {
    "workflow": "feature",
    "roles": {"planner": "planner-opus", "implement": "dev-claude", "review": "reviewer-codex", "test": "qa-linux"},
    "machine": "local"
  },
  "hooks": {"setup": ["mise", "install"], "check": ["mise", "run", "gate"]},   // 见 workflows.md「hooks」
  "fetch": ["gh", "issue", "view", "{ref}", "--json", "title,body,comments"]  // 需求抓取命令，见 planning.md「需求」
}
```

项目的字段还有 `owner`（项目负责人）和 `workflows`（名称 → Markdown 的自定义 workflow，见 [workflows.md](workflows.md)「实现」）；仓库行的 `worktrees: true` 打开每任务一个分支的工作方式（见 [execution.md](execution.md)「实现」）。工单绑定不放在 `repos` 里，而在 server 的数据库（见 [trackers.md](trackers.md)「绑定」）。项目、成员和机器分享都是事件（`project_created`、`project_edited`、`member_set`、`machine_shared`），由 `task.State` 折叠，两种模式相同。

## 规则

- **仓库按机器映射路径**：沿用 `internal/pathmap` 和 run 的 `from` 规则。没写路径的机器不能跑这个项目的任务，`run.preview` 会列出原因。
- **进度** = 这个项目下叶子任务的 done / 总数（canceled 算完成，Multica 同样处理）。
- 任务的 `dir` 仍然可以单独写，写了就覆盖项目仓库的路径：不属于任何项目的临时任务照这种方式用。
- 改项目设置的是项目负责人和管理员（`project.edit`）。
- 四个 hook（`setup`、`before_run`、`check`、`cleanup`）都会执行（见 [workflows.md](workflows.md)「hooks」和 [execution.md](execution.md)「实现」），网页的项目设置每个 hook 各有一个输入框；`fetch` 只保存和编辑，按它抓取需求未实现。

## 派发时的默认值

- **目录**：任务没写 `dir` 就用第一个仓库在目标机器上的路径。
- **agent**：任务没写 agent 就用 `defaults.agent`，再用 `roles.implement`。走 workflow 的任务按阶段选 agent（见 [workflows.md](workflows.md)「实现」）。
- **机器**：按派发参数 > agent 档案的 `machine` > 任务的 `machine` > 项目的 `defaults.machine` > agent 定义的 `machines.prefer` 取第一个；模式二一个都没有时回 `bad machine`。
  - 档案的 `machine` 是这个档案唯一能跑的机器（定义的 `machines.require` 只有一项时编译成它），派发或 `run.continue` 到别的机器回 `bad "agent X runs on M"`。
- **任务书**：`context` 放在最前面（`# <项目名>` 加正文），后面是 agent 定义的说明书，再后面是任务自己的任务书，用 `---` 隔开。
