# 任务与编排：总览

项目、agent 定义、workflow、需求拆解、执行隔离、团队与工单这一层的目标、非目标、对象、待核实的外部行为和已定决策。实现：`internal/task`、`internal/coord`、`internal/defs`、`internal/workflow`、`internal/node`、`internal/server`、`internal/store`、`internal/tracker`、`internal/auth`、`internal/dial`、`internal/ui/tui`。

## 一句话

在「协调器 + 节点 + agent 档案 + run」之上加一层：project 关联仓库和需求；agent 定义说明「谁、用什么模型、带什么 skills 和 hooks」；一条原始需求由 planner 拆成任务树，你确认后进 task 系统；每个任务按 workflow 的阶段（开发 → 评审 → 测试 → 验收）由不同 agent、不同机器接力做完；首页和看板让你一眼看到哪里要你。

## 前提

1. **借 Multica 的数据面和 UI 做法，不借它的编排方式。** Multica 的数据面很全：项目资源、父子与 stage、看板五种视图、agent 定义、skills 下发。但它**没有工作流引擎**，源码原话是 "stages are agent-driven"：
   - 状态由 agent 通过 CLI 自己写；
   - 依赖表从第一版起就没有代码读写；
   - 评审没有实体；
   - stage 屏障和 prompt 对不上，不开 PR 的多阶段流程每步都要人点 done。

   tend 的编排用确定性协调器，这是 Symphony 那一路。

   在 Multica 上实跑一条三阶段需求，暴露出四个问题：
   - 4 次评审有 3 次因为「每个 agent 一条分支」被判失败；
   - 一条运行中的评论排成并发 run，让评审评了旧提交；
   - 之后 issue 卡在评审中，没有任何机制发现，只能人手转派；
   - 阶段之间的代码由 agent 手工合并。

   这四个问题在 tend 里都由结构保证不会发生：见 [workflows.md](workflows.md)「协调器规则」「消息与插话」和 [execution.md](execution.md)。
2. **团队一起用，研发流程和 Gitea / GitLab / GitHub 的工单打通。** 团队（邀请制、项目级权限、agent 和机器可共享）和工单同步只做到够用：
   - 单团队；
   - 不开放注册；
   - 权限只有两层；
   - 工单同步以 issue 为源、tend 回写进度，不做字段级双向同步。
3. **执行器与人工节点**：
   - 做 Codex 执行器：codex 走 app-server，能远程作答、能插话；
   - 做人工审批节点：业内唯一普遍的人工节点就是批计划、批合并；
   - 执行默认在后台：后台双向流 run 能远程作答，Herdr tab 作为可选。

## 目标与非目标

### 要跑通的流程

1. 建一个 project：关联一个或多个仓库（每台机器上的路径）、需求来源（链接）、默认 workflow 和各角色用的 agent。
2. 定义 agent：类型（provider）、角色、模型与推理强度、权限、skills、工具白名单或黑名单、MCP、hooks、偏好机器、说明书。
3. 贴一段原始需求，或给一个 issue 链接 → planner 拆成任务树草稿（标题、任务书、验收标准、依赖、workflow、建议 agent）→ 你在 TUI 或网页里改 → 确认入库，任务停在「未开始」（`backlog`）。
4. 启动一棵子树：依赖满足的叶子任务自动派发，每个任务按 workflow 的阶段接力。
   - 评审或测试不过，就带着意见打回开发；超过轮数就停下等你。
   - 人工关口（批计划、验收）进「等你」，走现有的 inbox、通知、作答通道。
5. 首页综合看板、项目看板、任务树视图、任务详情：看到每个任务在哪个阶段、哪个 agent 在哪台机器跑、花了多少、哪里等你。
6. 团队：管理员加人，或按邮箱、域名白名单加入，用 GitHub 或任一 OIDC 账号（GitLab、Gitea 等）登录；项目按成员控制谁能参与、谁能看；agent 和机器默认只有主人能用，可以分享给指定的人或项目（见 [team.md](team.md)）。server 部署在 tailnet 里还是公网上由管理员选，两种都支持。
7. 工单：项目绑定 Gitea / GitLab / GitHub 仓库，打了标签的 issue 自动成为需求，tend 在 issue 上维护一条进度评论，验收后关单（见 [trackers.md](trackers.md)）。

### 不做

- 多工作区 / 多组织、开放注册、细粒度 RBAC、评论控制面、squad / leader agent 路由；
- 自定义状态目录、自定义属性；
- 与工单系统的字段级双向同步（tend 里改标题不写回 issue）；Jira、Linear 只留适配接口；
- 自动合并、自动推送到主分支；
- 周期任务 / autopilot（用 cron 调 `tend run start`）；
- tend 自己的 skill 市场或 MCP 下发服务。

## 对象总览

```
User ──< Member（项目成员：参与 | 只读）>── Project ──< Repo（每台机器一个路径；可绑 Gitea / GitLab / GitHub 仓库）
                                              │  └─ links、context、defaults{workflow, roles→agent, machine}、hooks
                                              │
                                              └──< Task（树：parent；先后：after；owner、approver）
                                                     │  kind: requirement | work    ← 需求就是 kind=requirement 的根任务，可关联一个 issue
                                                     │  workflow, stage, loops, branch
                                                     ├──< Comment（人与人的讨论；未实现）
                                                     └──< Run + stage、role、verdict、deliverable、dispatcher
                                                                 └─ 用 AgentDef 编译出的档案，在某台 Machine 上启动

AgentDef（owner + 分享范围；Markdown + frontmatter 可导入导出）
Machine（owner + 可派发范围；节点侧 allow_* 仍是最后边界）
Workflow（阶段表 + 各阶段任务书模板）
```

| 对象 | 存在哪 | 谁写 |
|---|---|---|
| 模式二的协调器状态（用户、成员、项目、定义、任务、run、评论、工单关联） | `tend-server` 的 SQLite（见 [storage.md](storage.md)）：事件、收据，加团队实体表 | 协调器，唯一写者 |
| 模式一的协调器状态 | `coord/events.jsonl`；定义是 `<home>/defs/` 下的文件 | 协调器，唯一写者 |
| 定义的文件形式 | `tend agent export/import`，Markdown + frontmatter；`tend project export/import` 未实现 | 你：`$EDITOR` 改完再导入；可 `import` 引用 `~/.claude/agents/*.md` |
| run 目录、会话索引、收藏 | 节点上的文件 | 节点 |

- 定义不写进用户仓库。
- 模式二的定义存在数据库里，因为多人时要记录归属和分享范围、网页要能编辑、改动要能审计。文件只是导入导出格式，方便用 `$EDITOR` 改、放进 git，也用来把模式一的定义搬进 server。
- **run 在派发时冻结编译结果**：和冻结 `profile` 一样，改定义不影响已派发的 run。

## 待核实

| 项 | 影响 |
|---|---|
| claude `--json-schema` 能否和 `--output-format stream-json` 同时用 | verdict 走结构化还是自报（[workflows.md](workflows.md)「结构化结果」）；不行就用自报 |
| codex app-server 怎么注入 hooks | codex 的 hooks（[agent-definitions.md](agent-definitions.md)「编译」） |
| Windows 上 `git worktree` 与长路径 | [execution.md](execution.md)「分支属于任务，不属于 agent」 |
| 分离 HEAD 的只读 worktree 在 claude / codex 下的表现（会不会提示建分支） | [execution.md](execution.md)「分支属于任务，不属于 agent」 |
| 合并策略：merge 还是 rebase，提交信息谁来写 | [execution.md](execution.md)「集成分支」 |
| Gitea 加标签发的是不是 `issue_label` 事件，指派发的是不是 `issue_assign`；只订阅 `issues` 会不会漏掉；`[webhook] ALLOWED_HOST_LIST` 是否允许发往 tailnet 地址；API 权限和证书 | [trackers.md](trackers.md) |
| GitHub OAuth App 接不接受非 localhost 的 http 回调（tailnet 部署） | [team.md](team.md)「身份与加入」 |
| GitLab 作为 OIDC provider：discovery 地址，`email_verified` 在 userinfo 还是 ID token 里 | [team.md](team.md)「身份与加入」 |
| SQLite（modernc）在 macOS 宿主和 Linux 容器 named volume 上的 WAL、锁、checkpoint、在线备份 | [storage.md](storage.md) |

### 已核实

- claude 2.1.283：`--plugin-dir`、`--plugin-url`（仅本次会话）、`--agents <json-or-file>`、`--agent`、`--settings`（能带 `hooks`、`skillOverrides`）、`--mcp-config`、`--json-schema`。
- Gitea 1.24 作 OIDC：discovery 在 `<root>/.well-known/openid-configuration`，issuer 带结尾斜杠；userinfo 没有 `email_verified`，ID token 里有（等于账号已激活）；修改 OAuth 应用会重新生成 client secret。
- 一个 run 的 `run_observed` 条数和步数相当（[storage.md](storage.md)「表」）。
- codex 0.155.1：`thread/start` 有 `developerInstructions` `baseInstructions` `config` `model` `sandbox` `approvalPolicy`；`turn/start` 有 `outputSchema` `effort`；有 `skills/extraRoots/set`、`skills/list`、`hooks/list`；`exec` 有 `--output-schema`。

## 已定决策

1. 状态只由协调器写，agent 只报告 verdict、提问、卡住。
2. 需求 = `kind: requirement` 的根任务 + 链接 + 冻结快照。
3. 定义（project、agent、workflow）不进用户仓库；可以 `import` Claude Code 的 agent 定义。
4. 打回时开发阶段续接原会话，评审和测试每次新会话。
5. 跨机器接力要求项目配置共享 git remote（例如团队的 Gitea）。
6. 取消任务时一并取消排队的 run 和子任务，要先确认。
7. TUI 命令面板键 `:`，撤销 Web `⌘Z` / TUI `u`（输入法替代键按 `keys_test.go` 的规则补）。
8. 看板拖放：Web 支持，但只允许等价于命令的移动；TUI 用键盘和点击。
9. 皮肤内部用「底色、强调色、对比度」三个输入生成，界面上先只露预设、强调色、对比度两档。
10. 父任务用集成分支：子任务完成后由节点合进去，冲突就停下等你；合进 main 由你来。
11. 评审和测试阶段在只读的分离 worktree 上跑，改动一律丢弃。
12. 阶段之间发的消息默认留给下一轮开发，不插给正在跑的评审者。
13. 团队协作做到够用：单团队、不开放注册、两层权限。
14. 管理员加人，或按邮箱、域名白名单加入。
15. 工单以 issue 为源导入需求，tend 维护一条进度评论，验收后关单；不做字段级双向同步。
16. agent 定义和机器默认只有主人能用，可以分享给指定成员、项目或所有人。
17. `tend-server` 的协调器用 SQLite（纯 Go），定义存数据库，文件只作导入导出；模式一保留 JSONL 和定义文件；节点不变。
18. 拆成 `tend` 和 `tend-server` 两个程序，同一仓库、同一 module；客户端拨号拆到 `internal/dial`。拆分的理由是依赖隔离和部署边界，不是体积。
19. 网络是部署选择：tailnet 和公网两种部署都支持，由管理员选；公网部署必须配 TLS，并带上 [team.md](team.md)「范围与部署」里的防护。Tailscale 身份不做。
20. 登录的 provider 可插拔：GitHub 和通用 OIDC（GitLab、Gitea 等只靠配置接入）；邮箱验证码不做；身份以 `(provider, issuer, subject)` 为键。
21. 工单接 Gitea、GitLab、GitHub 三家；轮询是底线，webhook 用来加速；所有同步由一个 worker 负责。
22. 批准权限请求默认只有机器主人和 run 的发起人，分享机器时可以放宽到被分享者；提问类项目参与者都能答；人工关口只有验收人能放行。
23. 调用者身份、方法授权表、单一可见性过滤、按人的收据、收紧 `node.call` 和 `run.continue{session}` 是一切团队功能的前提，先于它们就位。
24. 已有方法的语义变化靠 `hello.features` 协商，不降级执行。
25. backlog 不在不变式里（没开始），其余未完成任务恰好是 running / queued / waiting 之一；依赖满足 = 上游 done，有分支的任务合进父任务的集成分支之后才写 done，所以 done 就意味着已合入。
