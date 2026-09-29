# 团队与权限

模式二（`tend-server`）里的多人：部署方式、登录与加入、会话与凭据、两层权限和三种归属、怎么做到不能绕过、agent 与机器的共享、任务里的负责人与验收人、通知、审计与隐私、成员离开。实现：`internal/coord`（`access.go`：`Principal`、方法授权表、可见性、`sees`；`team.go`；`inbox.go`）、`internal/auth`（GitHub OAuth、OIDC）、`internal/server`（`/auth/*`、`/api/*`、`Notifier`、`Directory`）、`internal/store`（`Team`）、`internal/node`（`share_sessions`、派发人写进 git 作者）、`cmd/tend-server`（`admin`、`token`）、`cmd/tend`（`tend login`）。

## 范围与部署

- 只在模式二里有多人。模式一是你笔记本上的单人协调器：所有调用都以本机主人身份进来。
- 一个实例就是一个团队，不做多工作区、多组织。
- **网络是部署选择，不是设计限制**。管理员二选一：
  - **tailnet**：`--listen` 用 tailnet 地址，可以不配 TLS。
  - **公网**：`--listen` 用公网地址，必须配 TLS（`--tls-cert/--tls-key`，或者前面放一个终止 TLS 的反代，再加 `--plain`）。公网部署另外要求：
    - cookie 带 `Secure`；
    - 登录和 OAuth 回调做限流；
    - 网页上改状态的请求校验 CSRF；
    - webhook 验签。

  两种部署下的功能相同。区别只在 webhook 能不能送进来（见 [trackers.md](trackers.md)）。
- **server 主机的 OS 用户 = 实例管理员**。`tend-server admin …`、`tend-server token …` 在 server 主机上直接读写 `coord/tend.db` 的团队表，可以和运行中的 server 并行；server 每 3 秒重读用户和凭据，改动随之生效。没有管理用的 socket。

## 身份与加入

照 Multica 的简单做法。

- **登录**：provider 可插拔，有两个：
  - `github`：GitHub OAuth App；
  - `oidc`：通用 OIDC，只靠配置接入：`issuer`、`client_id`、`client_secret`、显示名。GitLab、Gitea、Keycloak、Google 都走它，接新的一家不改代码。

  一个实例可以配置多个 provider（`config.json` 的 `server.logins`，secret 放在 `client_secret_file`）。OAuth 流程带 `state` 和 PKCE S256，code 在服务端换取。邮箱验证码不做。
  - 进行中的登录存在内存里（10 分钟），浏览器另带一个只发往 `/auth/` 的 `tend_flow` cookie（SameSite=Lax）；回调要求 state、cookie、provider 三者一致。
  - `email_verified` 取自 userinfo；userinfo 不给时（Gitea 就不给），取 token 端点一并返回的 ID token。ID token 是后端通道直接拿到的，按 OIDC Core 3.1.3.7 不验签，只要求 `iss`、`aud`（本 client）、`sub` 都对得上，邮箱也一致。
  - 回调结束于一个 meta refresh 的小页面，这样 SameSite=Strict 的会话 cookie 在下一次请求里就会带上。结果写在地址的 `#signin-<结果>` 里：`state`、`provider`、`not_admitted`、`disabled`、`linked`、`internal`。
- **身份的键**是 `(provider, issuer 或 base_url, subject)`，subject 是数字或不可变 id。
  - 不按邮箱、用户名自动合并账号。
  - 要关联第二个账号，由用户登录后主动「关联」，两个账号都要证明控制权。
- **不开放注册**（Multica 的 `ALLOW_SIGNUP=false` 加 `ALLOWED_EMAILS` / `ALLOWED_EMAIL_DOMAINS` 加邀请）。能进来的只有三种人：
  - 管理员直接加的人（邮箱，或某个 provider 的用户名）；
  - 在邮箱白名单里的；
  - 在域名白名单里的。

  白名单只认 provider 标明「已验证」的邮箱。第一个管理员用 `tend-server admin add <email> --role admin` 在服务器上建。兜底方式是管理员生成的一次性邀请链接（`<server>/#invite-<secret>`，72 小时，用过即废），网页的管理页和 `tend-server admin invite` 都能生成。
  - **判定顺序**：已关联的身份 → 邀请 → 邮箱、域名、`provider:username` 规则。一个已验证邮箱若已属于某个用户，不会自动关联，要那个用户登录后在账号页「关联」（`/auth/<provider>/start?link=1`）。
  - 内置用户 `local`：server 主机本身，管理员；从 `tokens.json` 迁进来的客户端 token 和 `tend-server` 命令行都以它的身份。
  - 登录和回调按 IP 限流：每分钟补 20 次，突发 20 次。
- **会话**：
  - 网页的 cookie 里只存服务端会话 id（HttpOnly、SameSite=Strict、公网下 Secure），可以轮换，也可以单独吊销。用户能看到自己登录过的设备。
  - 浏览器会话本身就是一条凭据（`kind: web`，30 天），cookie `tend_session`。用 token 登录网页时，另建一条名为 `token:<id>` 的网页会话，随那个 token 一起失效。
  - TUI 和 CLI 用个人 token，在网页账号页生成，或 `tend login [地址]` 走浏览器授权拿到：
    - `POST /auth/device`（限流，不需要会话）用客户端名字换一个 `device_code`（CLI 轮询用）、一个 `user_code`（人读的 `XXXX-XXXX`，字母表去掉 `0/O/1/I`）、`verify_url`（`<地址>/#device-<user_code>`）、轮询间隔和有效期；待确认的设备码只存在内存里，重启即丢，同时最多 100 个。
    - `POST /auth/device/token` 用 `device_code` 换状态：`pending`；`denied`（读一次即失效）；`expired`；或恰好一次的 `{status: ok, token, user}`，之后这个码就没了。
    - 网页的 `#device-<user_code>`（登录后）打开终端授权页：`GET /api/device?code=` 取码、客户端名字、来源地址、时间；`POST /api/device {code, allow}` 批准即铸一个个人 token（名字 `login:<客户端名字>`），拒绝只记录，两者都写审计（`device.allow` / `device.deny`）。
    - `tend login` 只用 `net/http`：打印 `verify_url` 和 `user_code`，按 `interval` 轮询，成功后把 token 写到 `<tend 数据目录>/coordinator.token`（0600）、把 `config.json` 的 `coordinator.url/token_file` 指过去；Ctrl+C 或过期都给出明确提示。
  - 终端授权页在登录后才打开：登录只清掉它自己留下的 `#signin-…`，`#device-` 和 `#task-` 链接保留；完成或出错后有「回到 tend」。
  - 邀请可以带一个项目和在项目里的身份（`participant` / `reader`）：`POST /api/invites {role, project, access}`、`tend-server admin invite --project p --access reader`。登录页的邀请卡片写明「登录后加入项目「x」」。对方经这条邀请**新建**账号时，server 以发出邀请的人的身份执行 `project.member`（他要仍能管理那个项目），记审计 `member`；失败只记 `invite.project_failed`，不挡登录。
  - 邀请可以列出和作废：`GET /api/invites`（管理员）列未用且未过期的，按 hash 的前 12 位称呼（不暴露 secret）；`DELETE /api/invites {id}` 作废，记审计 `invite.revoke`。管理页的成员行给出登录方式、所在项目、名下机器、最近活动（他凭据最近一次使用的时间）。
  - 会话、个人 token、节点 token 在同一张 `credentials` 表，`kind` 区分（`web | token | node`），只存 sha256。
  - 网页的 `/api/*`（用户、准入规则、邀请、token、机器、登录账号、审计）凡是改状态的请求，都要带 `X-Tend: 1` 头且 `Origin` 同源，别的站点既发不出这个头，也读不到结果。
- **机器身份**：节点 token 有主人，绑定一个稳定的 `machine_id`（`node.ID`）；token 只是可以轮换的凭据。
  - 成员在网页上「添加机器」：生成一个归本人所有的节点 token，只显示一次，并给出 `tend node install-service --connect …` 命令。
  - 模式二拒绝不报 node ID 的节点。节点 token 第一次连接时绑定 node ID，之后别的机器拿它连接会被拒；换机器用「换机」（`rebind`），下次连接的机器成为新主机。
  - `tokens.json` 在 `tend-server` 启动时迁进数据库（主人是 `local`），原文件改名留底。
- `User { id u_…, email, name, avatar, logins[{provider, issuer, subject, username}], tracker_accounts[{kind, base_url, username}], role: admin|member, disabled }`。
  - 停用用户会立即断开他的连接，他的会话和 token 一律失效。
  - `tracker_accounts` 在登录账号和工单账号不是同一家时，由用户手动绑定，用来对应指派人（见 [trackers.md](trackers.md)）；未实现。

## 权限：两层，外加三种归属

| 层 | 角色 | 能做什么 |
|---|---|---|
| 实例 | 管理员 | 加人、停用、建项目、管机器和工单凭据；看得到所有项目，并在每个项目里算「参与」（这也是一条信任声明：管理员能看到别人 run 的输出）。机器不例外：往别人的机器派发，同样要主人分享 |
| 实例 | 成员 | 只看得到自己加入的项目；可以建自己的 agent 定义，可以登记自己的机器 |
| 项目 | 参与 | 建任务、拆解、开始、派发、回答提问、打回、发消息、评论；放行只限验收人（见「人在任务里」）；批准权限请求见「共享：agent 和机器默认私有」 |
| 项目 | 只读 | 看任务、run 输出、评论、用量；不能改，也不能作答 |

归属不是角色，但各自带有独占的动作：

| 归属 | 独占动作 |
|---|---|
| 项目负责人（project.owner） | 管本项目的成员和工单绑定 |
| 机器主人（machine.owner） | 看这台机器上的原生会话；批准权限请求（可以委托，见「共享：agent 和机器默认私有」）；分享和收回这台机器 |
| 定义主人（agentdef.owner） | 编辑、分享、收回这个定义 |

**不在项目里的人看不到这个项目的任何东西**：任务、run、输出、会话、用量、工单关联，也不会知道这些对象是否存在（问起来一律 `not_found`）。

项目、成员和机器分享是事件（`project_created`、`project_edited`、`member_set`、`machine_shared`），由 `task.State` 折叠，两种模式相同。任务记下创建人 `Task.Owner`；不属于任何项目的任务只有它的创建人和管理员看得到。机器页列出每台机器的主人和分享对象，主人和管理员在那里分享。

**怎么做到不能绕过**：

1. **调用者进协调器**。每条连接绑定一个 `Principal{user, admin}`，用 `Coord.HandlerFor(p)`。
   - 模式一和本机 socket 固定是本机主人 `coord.Owner`（user `local`，admin）。
   - 收据按 `(user, command_id)` 存，不同调用者的同一个 command id 互不相干。
   - 模式二的连接缺身份时一律拒绝。
   - 防止 server 的数据目录被当成模式一 home 的检查只在 `tend-server` 一侧：`coord/events.jsonl` 有事件而 `tend.db` 不存在时拒绝启动（见 [storage.md](storage.md)「迁移」）。`tend` 不拦：server 停着时 `TEND_HOME=<server 数据目录> tend …` 会在那里起一个 JSONL 协调器，以本机主人身份写 `events.jsonl`，有了 `tend.db` 的 server 会忽略这些写入。
2. **方法授权表**：`coord.Methods` 里的每个方法都有一条规则，没有规则的默认拒绝。`coord` 的测试断言每个方法都有规则，做法和 `keys_test.go` 一样。
3. **可见性过滤只有一个入口**：`visibleState(principal, state)`。`state.get`、`task.get`、`state.watch`、`run.tail`、`run.output.page`、`run.output.watch`（开流时判一次，可见范围变了再判）、`run.messages`、`machine.list`、`machines.watch`、`inbox.watch`、`agent.list` 都走它。
4. **订阅保持 seq 连续**。
   - 仍然一个 `seq` 一个信封。
   - 信封里的事件按人过滤成子集，子集为空也照发，客户端只推进 `seq`。
   - 每种事件由 `sees` 按类型判定：任务和 run 的事件（含 `plan_drafted`、`plan_applied`、`task_linked`）跟随任务的读权限，项目和成员事件跟随项目成员身份，`machine_shared` 跟随机器的可见性（见「共享：agent 和机器默认私有」），`agentdef_saved` / `agentdef_shared` 跟随定义的可读性；`agentdef_removed` 和没有规则的事件类型只推给管理员，所以新事件类型要同时在 `sees` 里补一条规则。
   - 推送里的 `command` 只留 `id` 和 `method`，结果和 `digest` 只回给调用者本人。
   - 历史补发和实时推送用同一条规则。
   - 事件属于哪个项目，由协调器在推送时按当前状态解析（run → task → project），不靠事件自带；对象从不删除，所以总能解析。
   - 推送里的命令名只发给调用者本人，以及看得到其中某个事件的人。
   - `member_set`、`project_edited`、`machine_shared`、`agentdef_shared`、`agentdef_removed`、改了项目或负责人的 `task_edited` 不单独推：`state.watch` 在同一个流里推 `reset`，接着推这个人的新快照，丢掉不再可见的数据；续传的那一段里有这些事件时也改给快照（见 [runs/coordinator.md](../runs/coordinator.md)「订阅」）。网页和 TUI 平时自己折叠信封（网页用 `fold.js`，和 Go 的折叠用同一批 Go 生成的信封对照测试），只在 seq 断档或遇到不认识的对象时整量重读。
   - 连接上的身份在连上时定下；server 的 `sweep`（每几秒）发现凭据的主人被停用、或管理员身份被授予或撤销时断开这条连接，客户端按新身份重连。
5. **收据按 `(principal, command_id)` 存**。重放之前先检查当前的读权限；有权就返回第一次的结果。
6. **原生会话的两条旁路收紧**。
   - `node.call`（读会话列表、对话、全文，以及建项目时列目录的 `node.dirs`）只给机器主人和管理员。
   - `run.continue{session, …}`（续任意会话）只给机器主人。
   - 项目成员用两个方法：`run.messages{run, before, n}` 和 `run.continue{run}`。服务端根据 run 找到机器、provider 和会话，再按项目权限判断。客户端声明的项目不作数。
7. **节点侧的纵深防御**：节点配置 `share_sessions: runs | all | none`，模式二默认 `runs`。节点只回答 run 目录里或 `node/sessions.jsonl` 登记过的会话。这样即使 server 被攻破，别人的原生会话也读不到。
8. **没有项目也没有创建人的任务**只有管理员看得到，不当作公共数据。

## 共享：agent 和机器默认私有

- **agent 定义**：
  - 主人可用。可以分享给指定成员、指定项目，或所有人。这和 Multica 的 `permission_mode: private | public_to` 是同一思路；管理员也不能越权使用别人没分享的 agent。
  - **可用不等于可读**：分享只给使用权。要让别人查看、复制定义内容，另外勾选「可查看」。
  - run 的客户端视图只含 agent id、版本和执行摘要（provider、模型、强度）。冻结下来的完整编译结果留在协调器内部。
  - 定义也可以**归项目所有**：项目负责人管理，项目默认角色引用它，不会因为某人离开而失效。
  - 格式与方法见 [agent-definitions.md](agent-definitions.md)「存放、归属与分享」。
- **机器**：这是 tend 特有的问题。agent 在机器上跑，用的是**机器主人**的 claude / codex 登录、git 身份和文件系统，所以机器也有主人，也有「谁能往这里派发」：
  - 默认只有主人；
  - 可以对指定项目或指定成员开放；
  - 节点侧的 `allow_dirs`、`allow_profiles`、`allow_bypass` 仍然是最后一道边界；
  - 节点可以按项目设置目录白名单：`projects.<id>.dirs`；
  - 能**看见**机器的是管理员、主人、分享名单里的人和被分享项目的成员（`canSee`）。其他人在 `machine.list` 和状态里看不到它，对它 `machine.share` 回 `not_found`，不暴露它存在。看得见不等于能用：派发要主人或分享（`canUse`），否则回 `unauthorized`，`run.preview` 写明原因，管理员也一样。
- 机器页按「我的机器 / 分享给我的 / 其他人的」分组（模式一只有一组）；每台标出怎么连上的（`machine.list` 的 `via`：本机、ssh、连入）和它的 tend 缺哪些节点 feature（`missing`，缺的那些运行不会派到这里）。
- **信任声明**：分享对话框写明「他们的 agent 以你的账号运行，能读你 home 下的文件、用你的 CLI 额度」。推荐做法是：要共享的机器用专用 OS 用户或容器跑 `tend node`，登录团队自己的 claude / codex 账号；个人机器不共享。计划：节点在 `hello` 里报告自己是不是主人的交互账号，共享派发时 `run.preview` 给出 `shared_home` 提示（未实现）。
- **派发检查**：下面四条都满足才派发。任何一条不满足，`run.preview` 都会写明原因。
  1. 发起人在这个项目里是「参与」；
  2. 这个 agent 定义对他或这个项目可用；
  3. 这台机器对他或这个项目开放；
  4. 节点接受。
- **什么时候检查**：派发时查一次；排队的 run 在写 `run_starting` 之前、resume 之前、自动推进下一阶段之前，都要重算。
  - 不满足就写 `run_canceled{reason: access_revoked}`，任务进「等你」。
  - 自动推进的 actor 记为 `system`，但检查以任务负责人的身份做，`system` 不能天然越权。
  - 已经在跑的 run 被撤权后，界面显示「待停止」，并照 abandon / ack 规则占用资源，直到确认结束。
- **`run.start` 带上 `dispatcher` 和 `project`**。需要 feature `dispatcher`（见 [protocol.md](protocol.md)「版本协商」）。节点用它们做三件事：
  - 按项目目录白名单再检查一遍；
  - 写进 `spec.json`，事后有据可查；
  - 设置 `GIT_AUTHOR_NAME/EMAIL` 为派发人，`GIT_COMMITTER` 仍是机器主人。提交尾的 `Co-authored-by: <agent 定义>` 未实现。
- **批准权限请求**（agent 要执行 Bash、写文件等）：
  - 默认只有机器主人和这个 run 的发起人能批。
  - 分享机器时可以打开「被分享者也能批」。
  - 提问类（AskUserQuestion、requestUserInput）项目里的参与者都能答。
  - `run.send`（插话）的风险和派发相同，按派发的四条检查。
  - `run_answered`、`run_sent` 事件记录 actor。
- run 记录发起人（dispatcher）和机器主人。用量按机器主人和项目分别统计。

## 人在任务里

- 任务有两个人：
  - **负责人**（owner）：默认是创建人。从 issue 导入的，按 issue 的指派人对应；对应不上（没登录过、已停用、多人指派）时，落到项目负责人，任务打 `unmapped_assignee` 徽章，用户第一次登录后再回补。
  - **验收人**（approver）：默认等于负责人。
  - 能改负责人、验收人的是任务负责人、项目负责人和管理员；验收人必须是项目参与者（只读成员不能验收），参与者不能接走别人的任务。
- **人工关口**：`gate: human` 的阶段只有验收人能放行，项目参与者都能打回（见 [workflows.md](workflows.md)「实现」）。
  - 「先把自己改成验收人再放行」要留下事件，并通知原验收人。
  - 多人会同时编辑任务、定义和关口，这些命令要带 `expected_rev`，旧页面不能覆盖新决定。现在关口（`task.gate`）和草稿（`task.plan_save`、`task.plan_apply`）带它；任务编辑和定义保存带它未实现。
- **通知**：发给发起人、负责人和验收人（在关口时）。
  - 投递记录按「事件 × 收件人」去重，投递前再核一次权限。
  - 渠道：个人收件箱；网页开着时用浏览器通知；每人可以配一个 webhook URL（ntfy、Slack、企业微信都行）；工单进度评论里 @负责人和验收人，让工单系统自己发通知（见 [trackers.md](trackers.md)）。
  - 协调器级的 `notify_command` 只在模式一里用。
- 收件箱和「等你」按人计算：只列我能处理、并且与我有关的，也就是我负责、我验收、我发起的。首页的「等你」就是这个列表。
- **评论**（未实现）：人与人的讨论，Markdown，可以 @成员。
  - 评论不触发 agent（不做评论控制面）。
  - 要让 agent 看到某条评论，就勾选「带给下一轮」，这条评论会进 workpad。

### 实现

- 通知：协调器在每次提交后比较受影响任务的前后 `Situation`，产生 `task.needs_you` / `task.done`，收件人是负责人、在 `accept` 时加上验收人、以及相关 run 的发起人。`tend-server` 把它们交给个人 webhook（`users.webhook`，账号页设置，POST 一段带 `text` 的 JSON；配了 `public_url` 时带任务链接 `#task-<id>`），`deliveries` 表按「seq × 收件人 × 事件」去重；收件人在提交那一刻按当时的状态算出，已停用的人不投。
- 收件箱：`inbox.list`（跟随用 `inbox.watch`）列出处于 `waiting`（`dispatch` 除外）、与我有关、并且我能写的任务，等得最久的排前面。网页有「等你」页和计数；页面开着且浏览器允许时，新条目弹浏览器通知。

## 审计与隐私

- 每个信封都带 actor：`user`、`node` 或 `system`（协调器规则）。任务详情里有活动流。
- **安全审计**和领域事件分开，单独一张只追加的表。记录这些：
  - 登录；
  - 被拒绝的访问；
  - token、会话、分享的变更；
  - 权限请求是谁批的。

  不记任何秘密，只记 id 和来源 IP（`login`、`login_refused`、`denied`、`token`、`machine`、`revoke`、`rebind`、`admit`、`invite`、`user`、`link`，以及 `machine.share`、`project.member`、`run.answer` 这类命令）。管理员在网页的管理页或 `tend-server admin audit` 查看。
- 事件里只写 user id，不写邮箱和姓名。
- 会话索引归机器主人。项目 run 产生的会话按项目权限可见；机器上其余的原生会话只有主人能看，不出现在别人的界面里（见「权限：两层，外加三种归属」第 6、7 条）。
- token、OAuth 凭据、工单凭据只存在 server 上，只存哈希或加密后的值，从不进事件表。加密密钥来自 `TEND_SERVER_KEY` 或者一个 0600 的文件，不放在数据库里。

## 成员离开

- **只停用，不删除**：事件里引用的 user id 保持有效。「删除」等于停用，再清空 `users` 表里的个人资料。
- **停用向导**：
  - 转移他作为项目负责人、任务负责人、验收人的身份，默认转给项目负责人；
  - 吊销他的会话、个人 token 和节点 token；
  - 他的机器标为 `retired`；
  - 他机器上没结束的 run 进管理员的「等你」（停止或放弃）；
  - 他的私有 agent 定义可以「转给项目」。
- **成果不能只留在他的机器上**：分支在每个 run 结束后推到共享 remote，不只在换机器时推（见 [execution.md](execution.md)「跨机器接力」）。

### 实现

`user.offboard`（管理员；网页管理页的「交接并停用」）：

- 他负责的项目交给指定的人（默认是操作的管理员）；他从所有项目的成员里移除；
- 他负责或验收的未完成任务：项目里的交给那个项目的负责人（交接后的），项目外的交给指定的人；
- 他的 agent 定义交给指定的人；他机器上的分享全部撤掉；
- `tend-server` 接着停用他，吊销他所有的会话、token 和节点 token，记审计 `offboard`。
- 网页的交接对话框先列出会发生什么：他负责的项目、项目外（或在这些项目里）的未完成任务、他的 agent 定义 → 接手人；其他项目里的任务 → 各项目负责人；他的机器 → 停止分享；项目成员身份移除；所有 token 和登录立即失效；并写明会进审计。
- 机器退役：节点 token 的主人被停用，这台机器就是 `retired`。不另存状态：`store.RetiredMachines` 从已停用主人的节点凭据里认出来，`Directory` 用它继续回答机器主人；机器视图 `retired: true`，网页机器卡片标「已退役」。上面还没结束的 run（`task.Open`）进每个管理员的「等你」，身份 `admin`（「我是管理员，它的机器已退役」），由管理员停止或放弃；run 结束后就不再出现。主人重新启用，或这个机器名换了别人的有效节点 token，就不再退役。
