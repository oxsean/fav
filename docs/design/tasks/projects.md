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
    "dirs": {"local": "/work/app", "linux": "/srv/app", "win": "D:\\work\\app"}
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
- **会话归属**：会话的目录落在某个项目某个仓库在那台机器上的路径下，就归这个项目（`task.ProjectOf`）。
  - 目录：会话在 linked worktree 里时取主仓库（`tend.Rec.Repo`、`remote.Session.Repo`），否则取 `cwd`。
  - 落在几个路径下时取最深的；同一个路径登记在两个项目里时归项目 id 排前的。
  - 包含关系按会话所在机器的系统判断（`pathmap.Under`，纯字符串，不读文件系统）：Windows 两种分隔符都认、不分大小写，根是盘符或 UNC 共享；其他系统区分大小写，WSL 的 `/mnt/c/…` 也按 POSIX 比；末尾和重复的分隔符不算，`/a/bc` 不在 `/a/b` 下；相对路径、带 `..` 的路径、另一种系统写法的路径不在任何目录下。路径登记成 `~/…` 的仓库对不上任何会话：协调器不按机器的 home 展开（server 模式的 TUI 不知道各机器的 home，展开会让规则分成两份）。
  - 参与比较的只有调用方看得见的项目；项目只决定分组，不决定谁能看会话。
- **进度** = 这个项目下叶子任务的 done / 总数（canceled 算完成，Multica 同样处理）。
- 任务的 `dir` 仍然可以单独写，写了就覆盖项目仓库的路径：不属于任何项目的临时任务照这种方式用。
- **谁建项目**：谁都能建（`project.create`），建的人默认是负责人；只有管理员能指定别人当负责人，成员填别人回 `unauthorized`。没有其他成员的项目是个人项目，加了成员就是团队项目。模式二里个人项目只有负责人看得到，管理员也看不到、不在里面算参与，问起来回 `not_found`；管理员替别人建的项目在加第一个成员之前也是这样，所以由负责人加成员（[team.md](team.md)「私人任务和个人项目」）。项目 id 是 `[a-z0-9][a-z0-9_-]{0,31}`，`none` 不能用：客户端用它指「不在任何项目里」。
- 改项目设置的是项目负责人和管理员（`project.edit`，整份替换所写的字段）；别人的个人项目管理员看不到，也就改不了。
- **一个个加、删目录**：`project.attach{project, machine, dir, remote?}`、`project.detach{project, machine, dir}`，回改完的项目。协调器在锁里从当前的 `repos` 算出新的一份，写成 `project_edited{id, repos}`，不另加事件类型；两个客户端同时加目录不会互相覆盖（`project.edit` 不带 `expected_rev`）。
  - 谁：负责人和管理员能动看得见的任何机器；参与者只能动自己名下的机器；只读成员不能。看不见项目回 `not_found`，看得见没权限回 `unauthorized`。`attach` 时看不见或没有这台机器回 `not_found`；`detach` 不要求机器还在。
  - `attach` 的 `dir` 必须是这台机器写法的绝对路径（机器的系统未知时按路径本身的写法判断），`~/…`、相对路径、带 `..` 的回 `bad_request`。这台机器在项目里已有同一个目录（按它的系统比较）时什么都不改；否则并进 `remote` 相同（按 `task.RemoteKey` 比：`git@host:a/b.git`、`ssh://git@host:22/a/b`、`https://host/a/b` 都读成 `host/a/b`，host 小写，去掉用户名、端口、末尾的 `/` 和 `.git`；读不出时按原样比）、这台机器还没有目录的第一个仓库，没有就新建一个仓库，名字取目录名，重名依次加 `-2`、`-3`，`remote` 记下传来的值。
  - `detach` 去掉那台机器上的这个目录（按它的系统比较，系统未知时按原文），仓库因此一个目录都不剩就整个去掉；项目里没有这个目录回 `not_found`。已有的 `~/…` 目录按原文去掉，再用绝对路径 `attach`。
  - `project.edit` 照旧能整份写 `repos`，不检查路径的写法（网页的项目设置会把已有的 `~` 目录原样带回去）。
- 四个 hook（`setup`、`before_run`、`check`、`cleanup`）都会执行（见 [workflows.md](workflows.md)「hooks」和 [execution.md](execution.md)「实现」），网页的项目设置每个 hook 各有一个输入框；`fetch` 只保存和编辑，按它抓取需求未实现。

## 派发时的默认值

- **目录**：任务没写 `dir` 就用第一个仓库在目标机器上的路径。
- **agent**：任务没写 agent 就用 `defaults.agent`，再用 `roles.implement`。走 workflow 的任务按阶段选 agent（见 [workflows.md](workflows.md)「实现」）。
- **机器**：按派发参数 > agent 档案的 `machine` > 任务的 `machine` > 项目的 `defaults.machine` > agent 定义的 `machines.prefer` 取第一个；模式二一个都没有时回 `bad machine`。
  - 档案的 `machine` 是这个档案唯一能跑的机器（定义的 `machines.require` 只有一项时编译成它），派发或 `run.continue` 到别的机器回 `bad "agent X runs on M"`。
- **任务书**：`context` 放在最前面（`# <项目名>` 加正文），后面是 agent 定义的说明书，再后面是任务自己的任务书，用 `---` 隔开。
