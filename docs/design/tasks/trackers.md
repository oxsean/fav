# 工单同步（Gitea、GitLab、GitHub）

项目绑定工单仓库后：打了标签的 issue 自动成为需求，需求快照与版本，tend 在 issue 上维护一条进度评论，验收后关单或打标签，可选子 issue 和 PR / MR；一个 worker 负责全部同步，轮询兜底、webhook 加速。只在 `tend-server` 里有，模式一不同步。实现：`internal/tracker`（`Tracker` 接口、`rest.go`、`gitea.go`、`gitlab.go`、`hook.go`、`trackertest`）、`internal/server`（`sync.go` 的 `Syncer`、`links.go`、`/api/trackers*`、`/hooks/{id}`、`seal.go`）、`internal/coord`（`task.sync`、`task.source_ack`、`task.link`）、`internal/store`（`trackers`、`tracker_issues`、`tracker_deliveries`）。

## 绑定

- 绑定记下 `kind: gitea|gitlab|github`、`base_url`、`repo`，另外记一个稳定的仓库 id（仓库改名不影响绑定）。
- **绑定放在 server 的数据库**（`trackers` 表），不放进 `Project.repos[].tracker`：凭据和同步状态本来就只在 server 上，模式一不同步。一个仓库只能绑一次（`kind, base, repo_id` 唯一）。
- **凭据**：机器人账号的 token（Gitea、GitLab）；GitHub 用机器人账号的 PAT，GitHub App 未实现。
  - token 用 AES-256-GCM 加密后存库，密钥来自 `TEND_SERVER_KEY` 或 `<home>/server.key`（0600，首次启动生成，不进备份）。
  - 绑定和换凭据时先用 token 读一次用户和仓库，记下机器人登录名和仓库 id。
  - 凭据快过期时提前进管理员的「等你」：未实现。
- 项目负责人和管理员在项目页的「工单同步」里绑定、改设置、换凭据、重新同步、解绑。设置有：
  - 导入条件：导入标签（默认 `tend`），是否也导入指派给项目成员的 issue；
  - 是否维护进度评论；
  - 回写哪些内容：默认只写总体进度和交付链接，子任务明细（「列子任务」）、内部路径、错误原文要项目明确打开，因为仓库的读者不一定都是项目成员；
  - 完成后关单还是只打标签（`on_accept`，默认关单；打标签时打 `accept_label`，默认 `tend:accepted`，关单时不打）；
  - 轮询间隔（30–3600 秒，默认 60）；
  - `sub_issues`、`pr`（见下文）。
- 绑定表单里选种类，绑好后按种类说明 webhook 怎么配。

## 同步 worker

- **一个 worker 负责所有同步**（reconciliation worker，`Syncer`）。webhook、轮询、人工「重新同步」只做一件事：把某个 issue 标成「待同步」（或清游标）。worker 按 issue 串行处理：
  1. 拉 issue 的最新内容；
  2. 更新需求快照；
  3. 算出期望的进度评论和是否该关单；
  4. 和已应用的比对，不同就写。

  这样重复和乱序只有一处要处理。
- **轮询是底线，webhook 用来加速**：
  - 两种部署都轮询：`since = 游标 − 2 分钟`，游标取见过的最大 `updated_at`；带上次的 `ETag`（tracker 给的话，`If-None-Match`）；同一 issue 的 `updated_at` 没变就不再读。分页处理完才推进游标，扫描窗口前后重叠一点，再去重。
  - webhook 送得进来就开：公网部署，或者 Gitea、GitLab 和 server 在同一个网络里。进来的请求要按种类验签（`X-Gitea-Signature`、`X-Gitlab-Token`、`X-Hub-Signature-256`），body 上限 1 MiB，按 delivery id 去重，核对绑定的仓库 id。payload 只用来触发一次重新拉取。
  - 同一个 issue 在 tend 里只对应一个任务，唯一键是 `(tracker 实例, 仓库 id, issue 号)`。
- `task.sync` 只有进程内的 `coord.System` 能调，连接永远带不上它。

## 导入与需求快照

- 命中条件的 issue 自动变成需求任务：`kind: requirement`，带 `source`。
- `Task.Source{kind, tracker, base, repo, repo_id, number, url, rev, digest, seen, seen_rev, pending, closed, closed_acked}`，唯一键 `(base, repo_id, number)`。
- 快照 = 标题 + 正文 + 人的评论（排除 tend 自己的进度评论），摘要是它们的 sha256。每次取到的内容有一个不可变的版本；导入时任务书就是快照。
- 拆解、任务树确认、验收都记录自己依据的版本（草稿的 `source_rev`，见 [planning.md](planning.md)「实现」）。
- issue 的正文或评论变了，就记一个新版本（`pending`），任务进 `waiting: source_changed`（「需求有变化」），由人「采用新版本」（标题和任务书换成新版本）或「维持本轮范围」（这个版本不再提起）；有未决变化时不能标完成。
- issue 在外面被关掉了，需求不自动取消，而是进 `waiting: source_closed`，原因「issue 已关闭」，由人「继续做」或取消任务；tend 自己关的（任务已完成）不算。
- 事件：`task_created`（带 source）、`task_sourced`、`task_source_acked`。
- **指派人对应**：issue 的指派人按「在这个 tracker 的地址上登录过的账号」对应到成员（`identities.issuer` 与绑定的地址一致、用户名相同、只有一个；GitHub 登录的 issuer 是 `https://github.com`，GitLab、Gitea 走 OIDC，issuer 就是它们的地址）；对应不上时归项目负责人并打 `unmapped_assignee`，之后对应上了再改回（见 [team.md](team.md)「人在任务里」）。`tracker_accounts` 显式对应表未实现。

## 回写

- 在 issue 上只维护**一条**进度评论。
- **回写用期望状态对账，不用事务内的 outbox**：worker 每 5 秒按当前状态算出每条需求期望的评论正文和是否该关单，和 `tracker_issues` 表里已应用的（`comment_id`、`body_hash`、`written`、`closed`）比对，不同就把这个 issue 标脏去处理。状态本身就在 journal 里，崩溃重启后重算即可，效果和 outbox 一样，少一套意图表。
- 评论编辑做去抖，至少间隔 30 秒；编辑返回 404 就重建。
- 评论里带隐藏标记 `<!-- tend:progress <协调器 id> <任务 id> -->`。创建之前先按「机器人作者 + 隐藏标记前缀」认领已有评论，所以「创建成功、响应丢失」不会留下两条。
- **评论内容**：英文，一行状态、叶子子任务进度、@ 负责人和验收人（按上面的指派人对应）；项目打开「列子任务」才列明细；走 workflow 的任务多一行 `Stages: …`。
- 验收通过后关单或打标签。这次关单由 tend 发出，回来时只确认同步成功，不会被当成「在外面被关掉」。
- 打标签和 GitHub、GitLab 按名字加标签一致：仓库没有这个标签就先建。Gitea 不会自己建，对不存在的标签照样回 200 却不加，所以在 Gitea 上先按名字找仓库的标签，没有就建（颜色固定），再按 id 加。加完核对应答里 issue 确实带着它（Gitea、GitHub），不带就算这条 issue 失败，一分钟后重试，不记成已完成回写。
- **自己的写不算需求变化**：识别依据是评论 id、写入版本和机器人账号，不能只靠可伪造的隐藏标记。

## 子 issue

- 默认不把子任务同步成子 issue；绑定设置 `sub_issues` 打开后，同步 worker 给每个未结束、还没有对应 issue 的子任务（任意层）开一条 issue：标题是子任务标题，正文是任务书（截到 4000 字）加「Part of #n」和隐藏标记 `<!-- tend:subtask <协调器 id> <任务 id> -->`；GitHub 再挂成 sub-issue，Gitea、GitLab 只靠这句引用。
- `tracker_issues` 里这一行记 `parent`，它永远不当需求导入（不走 `task.sync`），只回写自己的进度评论，子任务完成后按 `on_accept` 关单或打标签。
- 开单的应答丢了时，下一次先在最近一天更新过的 issue 里按标记找回，不重复开。
- 任务记 `issue`（事件 `task_linked`）。

## PR / MR

- 绑定设置 `pr` 打开后，需求的分支（`tend/<任务>`）上有推到 remote 的提交、并且任务在等验收或已完成时，同步 worker 用绑定的凭据从这个分支开 PR / MR：目标是任务工作区的 base，没有就用仓库的默认分支；正文是「For #n.」、需求的验收项、绑定打开 `detail` 时的子任务列表和来源分支；先按分支找已开的，有就沿用。
- 链接记在 `tracker_issues.pr` 和任务的 `pr`（事件 `task_linked`，内部命令 `task.link`），进度评论里多一行「Pull request: …」，网页任务详情的分支块里显示。
- 来源 issue、PR 和子 issue 链接只在网页任务详情里显示，CLI 和 TUI 的任务视图不显示（`tend run show` 只显示 run 自己用 `--pr` 报的 PR）。
- 合并仍由人做。分支不在 tracker 仓库里时，错误记在这条 issue 上，一分钟后重试，不停整个绑定。

## 三家的差别

都在 `internal/tracker` 里消化，worker 不分种类。`rest.go` 是共用的 REST 层；`gitea.go` 同时服务 Gitea 和 GitHub，两家只差 API 位置、token 写法、页大小和打标签；`gitlab.go`；`hook.go` 按种类验签、取 delivery id、取仓库和 issue 号；`trackertest` 是测试用的假服务器，能说三家的方言（包括 Gitea 对不存在的标签回 200 不加），能注入限流、拒绝凭据、响应丢失、加标签被悄悄丢掉。

| | Gitea | GitHub | GitLab |
|---|---|---|---|
| API | `<base>/api/v1` | `api.github.com`（`github.com`），其它地址 `<base>/api/v3` | `<base>/api/v4`，项目按 URL 编码的路径寻址，可带子组 |
| token | `Authorization: token …` | `Authorization: Bearer …` + `X-GitHub-Api-Version` | `PRIVATE-TOKEN` |
| 列表 | `since`、`limit=50`、`type=issues` | `since`、`per_page=100`，去掉 `pull_request` | `updated_after`、`per_page=100`、`scope=all`，MR 本来就分开 |
| 评论 | issue comments；编辑按评论 id | 同 Gitea | notes，跳过 `system` 的；编辑要带 issue 号 |
| 关单 / 标签 | `PATCH state=closed` / 找或建仓库标签（`GET`、`POST labels`）后按 id `POST issues/{n}/labels` | `PATCH state=closed` / 按名字 `POST labels`（自动建） | `PUT state_event=close` / `PUT add_labels` |
| webhook | `X-Gitea-Signature`（HMAC）、`X-Gitea-Delivery` | `X-Hub-Signature-256: sha256=…`、`X-GitHub-Delivery`；Content type 要选 `application/json` | `X-Gitlab-Token`（原样比对）、`X-Gitlab-Event-UUID`；`object_kind` 为 `issue` 或 `note` 时才指向 issue |
| 限流 | `Retry-After` | `X-RateLimit-Reset` / `X-RateLimit-Remaining` | `RateLimit-Reset` / `RateLimit-Remaining` |

## 限流与失败

- 429，或带 `Retry-After` / `X-RateLimit-Remaining: 0`（GitLab 是 `RateLimit-Remaining`）的 403 → 这个绑定暂停到指定时间（按 `Retry-After` 或 reset 时间退避）。按凭据统一节流未实现。
- 401 / 403 → 停止（`stopped: auth`），通知项目负责人和管理员（server 自己的 `tracker.stopped` 通知，走个人 webhook），换凭据后继续。
- 单个 issue 的其它错误记在它那一行，一分钟后重试。
- 同步状态（游标、最后错误、webhook 投递记录、每个 issue 已应用的回写）存表。

## 同步状态的界面

- 项目页显示每个绑定的状态、上次成功时间、需求数和出错数。
- 绑定行的「同步记录」（`GET /api/trackers/issues?id=`，项目负责人和管理员）列出这个绑定跟着的每个 issue：对应的任务（点了打开）、是不是子工单、评论写于何时、已关闭、待重读、PR 链接、最后的错误；「预览评论」（`GET /api/trackers/preview?id=&number=`）给出现在写的话进度评论会是什么样。
- 任务级状态：`tracker_issues.synced` 记最后一次把 issue 和任务对齐的时间；`GET /api/trackers/tasks?project=`（项目负责人、管理员）给每个有 issue 的任务一个状态：
  - `sync_failed`：绑定停了、这条上次失败、或绑定上一轮就失败了（连不上、服务端出错），带错误，`next` 是这条的重试或绑定限流结束的时间；
  - `sync_pending`：绑定在限流，或这条待重读待回写；
  - 其余 `ok`。

  Web 任务详情的来源 / 子 issue 一栏显示它，最多半分钟取一次。

## 不做

- 字段级双向同步；
- Jira、Linear（`internal/tracker` 的接口留好）。
