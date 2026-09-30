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
  - 每个账号记下关联的时间和最近一次用它登录的时间（`identities.last_login`，0013；关联本身不算登录）。「我」页可以解除关联：`DELETE /api/identities {provider, issuer, subject}`，只能解除自己的（否则 404），最后一个不给解（409 `last`），记审计 `unlink`。解除后再用它登录按「判定顺序」当新人处理，工单指派人也不再对应到他；已登录的会话不受影响。
- **不开放注册**（Multica 的 `ALLOW_SIGNUP=false` 加 `ALLOWED_EMAILS` / `ALLOWED_EMAIL_DOMAINS` 加邀请）。能进来的只有三种人：
  - 管理员直接加的人（邮箱，或某个 provider 的用户名）；
  - 在邮箱白名单里的；
  - 在域名白名单里的。

  白名单只认 provider 标明「已验证」的邮箱。第一个管理员用 `tend-server admin add <email> --role admin` 在服务器上建。兜底方式是管理员生成的一次性邀请链接（`<server>/#invite-<secret>`，72 小时，用过即废），网页的管理页和 `tend-server admin invite` 都能生成。
  - **判定顺序**：已关联的身份 → 邀请 → 邮箱、域名、`provider:username` 规则。一个已验证邮箱若已属于某个用户，不会自动关联，要那个用户登录后在「我」页「关联」（`/auth/<provider>/start?link=1`）。
  - 内置用户 `local`：server 主机本身，管理员；从 `tokens.json` 迁进来的客户端 token 和 `tend-server` 命令行都以它的身份。
  - 登录和回调按 IP 限流：每分钟补 20 次，突发 20 次。
  - 请求的来源地址：对端是 loopback 时当作本机上的反向代理（tailscale serve），取 `X-Forwarded-For` 的最后一个地址；别的对端带来的这个头不认，用 `RemoteAddr`。限流、设备码的按地址上限和审计的 IP 都用它。
- **会话**：
  - 网页的 cookie 里只存服务端会话 id（HttpOnly、SameSite=Strict、公网下 Secure），可以轮换，也可以单独吊销。用户能看到自己登录过的设备。
  - 浏览器会话本身就是一条凭据（`kind: web`，30 天），cookie `tend_session`。用 token 登录网页时，另建一条名为 `token:<id>` 的网页会话，随那个 token 一起失效。
  - 每条凭据记下最近一次使用的时间和来源地址（`last_used`、`last_ip`，0012；`/client`、`/node` 连上时写），浏览器会话另记登录时的 User-Agent（`agent`，最多 256 字节），「我」页凭它写出是哪个浏览器、哪台设备。
  - TUI 和 CLI 用个人 token，在网页的「我」页生成，或 `tend login [地址]` 走浏览器授权拿到：
    - `POST /auth/device`（限流，不需要会话）用客户端名字换一个 `device_code`（CLI 轮询用）、一个 `user_code`（人读的 `XXXX-XXXX`，字母表去掉 `0/O/1/I`）、`verify_url`（`<地址>/#device-<user_code>`）、轮询间隔和有效期；待确认的设备码只存在内存里，重启即丢，同时最多 100 个，同一个来源地址（和限流一样，见上面「来源地址」）最多 5 个，超出回 429 `busy`。
    - `POST /auth/device/token` 用 `device_code` 换状态：`pending`；`denied`（读一次即失效）；`expired`；或恰好一次的 `{status: ok, token, user}`，之后这个码就没了。
    - 网页的 `#device-<user_code>`（登录后）打开终端授权页：`GET /api/device?code=` 取码、客户端名字、来源地址、时间；`POST /api/device {code, allow}` 批准即铸一个个人 token（名字 `login:<客户端名字>`），拒绝只记录，两者都写审计（`device.allow` / `device.deny`）。
    - `tend login` 只用 `net/http`：打印 `verify_url` 和 `user_code`，按 `interval` 轮询，成功后把 token 写到 `<tend 数据目录>/coordinator.token`（0600）、把 `config.json` 的 `coordinator.url/token_file` 指过去；Ctrl+C 或过期都给出明确提示。
  - 网页也能用设备码登录（手机、主屏幕上的 PWA：它的 cookie 和浏览器分开，在独立窗口里走 OAuth，回调设置的会话可能落不进去）：页面发 `POST /auth/device {name, session: true}`，在别的已登录的设备上批准；批准不铸 token，只记下批准人，页面的轮询拿到的是一条批准人的网页会话（`Set-Cookie` 设 `tend_session`，名字 `device:<客户端名字>`，30 天），回 `{status: ok, user}`，不带 token，只给一次；轮询时批准人已被停用就回 `denied`。
    - 会话只给这台 server 自己的页面：带 `session` 的开始请求和它的轮询都要 `X-Tend: 1` 且 `Origin` 同源，否则 403 `csrf`，被拒的轮询不作废这个码。别的站点既不能替浏览器发起，也不能让浏览器收下一条别人的会话（登录 CSRF）。
    - 模式在开始时定下：不带 `session` 的码（`tend login`）无论怎么轮询都只给 token。`GET /api/device` 对会话码多回 `session: true`，终端授权页据此写明「那台设备会以你的身份登录网页」；审计 `device.allow` 的 detail 带 `session`，落会话时另记 `login`。
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

项目、成员、机器分享和停止接新运行是事件（`project_created`、`project_edited`、`member_set`、`machine_shared`、`machine_drained`），由 `task.State` 折叠，两种模式相同。任务记下创建人 `Task.Owner`；不属于任何项目的任务只有它的创建人和管理员看得到。机器页列出每台机器的主人和分享对象，主人和管理员在那里分享，也在那里让它停止接新运行（`machine.drain`）。

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
   - 每种事件由 `sees` 按类型判定：任务和 run 的事件（含 `plan_drafted`、`plan_applied`、`task_linked`）跟随任务的读权限，项目和成员事件跟随项目成员身份，`machine_shared` 跟随机器的可见性（见「共享：agent 和机器默认私有」），`agentdef_saved` / `agentdef_shared` / `agentdef_transferred` 跟随定义的可读性；`agentdef_removed` 和没有规则的事件类型只推给管理员，所以新事件类型要同时在 `sees` 里补一条规则。
   - 推送里的 `command` 只留 `id` 和 `method`，结果和 `digest` 只回给调用者本人。
   - 历史补发和实时推送用同一条规则。
   - 事件属于哪个项目，由协调器在推送时按当前状态解析（run → task → project），不靠事件自带；对象从不删除，所以总能解析。
   - 推送里的命令名只发给调用者本人，以及看得到其中某个事件的人。
   - `member_set`、`project_edited`、`machine_shared`、`agentdef_shared`、`agentdef_removed`、`agentdef_transferred`、改了项目或负责人的 `task_edited` 不单独推：`state.watch` 在同一个流里推 `reset`，接着推这个人的新快照，丢掉不再可见的数据；续传的那一段里有这些事件时也改给快照（见 [runs/coordinator.md](../runs/coordinator.md)「订阅」）。网页和 TUI 平时自己折叠信封（网页用 `fold.js`，和 Go 的折叠用同一批 Go 生成的信封对照测试），只在 seq 断档或遇到不认识的对象时整量重读。
   - 连接上的身份在连上时定下；server 的 `sweep`（每几秒）发现凭据的主人被停用、或管理员身份被授予或撤销时断开这条连接，客户端按新身份重连。
5. **收据按 `(principal, command_id)` 存**。重放之前先检查当前的读权限；有权就返回第一次的结果。
6. **原生会话的两条旁路收紧**。
   - `node.call`（读会话列表、对话、全文，以及建项目时列目录的 `node.dirs`）只给机器主人和管理员。
   - `run.continue{session, …}`（续任意会话）只给机器主人。
   - 项目成员新建任务时查机器上的目录用 `project.dirs{project, machine, path?}`：要是这个项目的参与者（管理员也算），并且这台机器是他的或分享给了他（`canUse`；不带项目时只看分享给本人的）。协调器转问节点的 `node.dirs`，回 `{path, exists, outside?, parent?, dirs}`：没有这个目录是 `exists: false`，不在节点允许的目录里再加 `outside: true`；不带 `path` 列出节点允许的根目录。看不到的项目或机器回 `not_found`，只读成员和没拿到分享的回 `unauthorized`。
   - 项目成员用两个方法：`run.messages{run, before, n}` 和 `run.continue{run}`。服务端根据 run 找到机器、provider 和会话，再按项目权限判断。客户端声明的项目不作数。
7. **节点侧的纵深防御**：节点配置 `share_sessions: runs | all | none`，模式二默认 `runs`。节点只回答 run 目录里或 `node/sessions.jsonl` 登记过的会话。这样即使 server 被攻破，别人的原生会话也读不到。
8. **没有项目也没有创建人的任务**只有管理员看得到，不当作公共数据。

## 共享：agent 和机器默认私有

- **agent 定义**：
  - 主人可用。可以分享给指定成员、指定项目，或所有人。这和 Multica 的 `permission_mode: private | public_to` 是同一思路；管理员也不能越权使用别人没分享的 agent。
  - 用得到别人定义的人可以「不再使用」，不管是点名、经项目还是所有人分享到的：只对他自己生效，主人点名分享回来才恢复（[agent-definitions.md](agent-definitions.md)「存放、归属与分享」）。
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
- 机器页按「我的机器 / 分享给我的 / 其他人的」分组（模式一只有一组）；每台标出怎么连上的（`machine.list` 的 `via`：本机、ssh、连入）、agent CLI 装没装和登没登录（看得见它的人可以让它重新检查：`machine.check`）、它的 tend 缺哪些节点 feature（`missing`，缺的那些运行不会派到这里）。模式二里每个成员在机器页添加自己的机器、拿到一次性的节点 token，主人和管理员在那里换机或吊销它的 token；页面细节见 [runs/clients.md](../runs/clients.md)「机器页」。管理员在团队页改身份、停用、交接并停用、邀请、管准入规则和看审计，项目负责人和管理员在项目抽屉里管成员；见同一文件的「团队页」。
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
  - **验收人**（approver）：默认等于负责人；没指定时，放行、通知和收件箱的 `as` 都按负责人算（一条规则：`Task.Accepter`）。
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

- 通知：协调器在每次提交后比较受影响任务的前后处境和待处理项（`task.State.Pending`，按 `id`、`version`、`reason` 比）：任务在等人（`dispatch` 除外）并且有了之前没有的待处理项，就产生 `task.needs_you`，`items` 带上新出现的那几项，所以权限请求 A 换成 B、或者旧的还没答又来一个同类的，都会再通知；变成完成产生 `task.done`。收件人是负责人、在 `accept` 时加上验收人、以及相关 run 的发起人，在提交那一刻按当时的状态算出。
- 投递（`tend-server` 的 `Notifier`，`notify.go`）：
  - **发件箱**：`Options.Notice` 在协调器的锁里把通知写进 `deliveries`（[storage.md](storage.md)「表」），每个收件人的个人 webhook 一行、每台推送设备一行，同一个「seq × 收件人 × 事件 × 设备」只写一次。通知在 `Notifier` 接上库之前到的（`coord.Open` 期间），先留在内存里，接上后写入。重启后没投完的行照投；投到一半被杀的会再投一次，同一个任务的通知在浏览器里互相替换（`tag`），重复无害。
  - **渠道**：`webhook`（`users.webhook`，「我」页设置，POST 一段带 `text` 的 JSON；配了 `public_url` 时带任务链接 `#task-<id>`；所有事件、立即投。「我」页可以试发：`POST /api/me/webhook/test` 不经发件箱，当场往已保存的地址发一条 `event: "test"`，回 `{ok, status?}`，`status` 是 webhook 回的 HTTP 码，别的失败一律 `unreachable`，不说是超时还是被拒，免得成了探 server 能连到哪里的工具；和登录一样按 IP 限流，记审计 `webhook.test`）和 `webpush`（`task.needs_you`，设备要时还有 `task.done`，见下面「这台设备的设置」）。每个渠道一个有界的并发（webhook 4、webpush 8），每台设备（webhook 按人）同时只有一个在途，单次 10 s 超时，一个慢的推送服务只挡它自己那台设备。
  - **升级，不抑制**：`task.needs_you` 推送那一行排在通知之后 N 秒才投：设备自己设了等待就按它（「立即」是 0），否则新出现的项里有权限请求时 30 s、其余 60 s；`task.done` 立即投；这段时间里网页（电脑上的浏览器通知、页面本身）先提示。页面没有计时器，等待全在发件箱的 `next_at` 里。
  - **每次投递和重试之前再核一次**，不成立就记 `canceled`、不发：人还在、没停用；`task.needs_you` 的任务还在他的收件箱里（`coord.Waiting`，和 `inbox.list` 同一条规则，权限请求只给能批的人），并且这条通知带来的项（`id` 和 `version`）至少还剩一个，所以 N 秒内处理掉的不推，被替换掉的也不推；别的事件要他还看得到这个任务（`coord.Sees`）；webhook 已经去掉的不投；设备行还在。
  - **结果**：2xx 成功（设备 `failures` 清零、记 `last_ok_at`）；Web Push 回 404 / 410 时删掉这台设备，它其余待投的行记 `gone`；5xx、429、408、网络错误隔 30 s、2 min、8 min 各重试一次（推送服务给的 `Retry-After` 更长就等它，最多 1 h），第 4 次仍失败记 `failed`；其余 4xx 直接 `failed`。
  - **推送的内容**在投递时按当时的状态算，RFC 8291 加密，只有那个浏览器能读：`{v:1, server, seq, event, task, item, kind, title, what?, project?, n, link, at, actions?}`。`item` / `kind` 是这条通知带来、现在还在的第一项；`n` 是这个任务现在等他的项数；`what` 只在权限请求时带（`Request.Summary`，没有就是工具名），锁屏上点开之前就看得到要批准的是什么；`link` 是 `#wait-<id>`，那个任务在「等你」里的落地页（[runs/clients.md](../runs/clients.md)「首页」），任务完成的是 `#task-<id>`；标题和 `what` 截短，正文不超过推送服务的 4 KB。请求头 `TTL` 24 h、`Urgency`（权限请求和提问 `high`，其余 `normal`）、`Topic` 是任务 id（推送服务那边同一个任务还没送到的旧消息被替换）。
- **通知上的按钮**：只有「查看」和「拒绝」（「允许」要看得到上下文、Android 锁屏上不解锁也能点，所以点开页面再做）。「查看」不经 server；「拒绝」只给权限请求，并且投递时协调器确认这个人现在能拒绝它（`coord.NoticeActs`：待处理项还在这个版本，`run.answer` 的 deny 试算通过），负载里才带 `actions: [{action: "reject", token}]`。
  - **令牌**：`a1.<claim>.<签名>`，两段 base64url；claim 是 `{u 用户, t 任务, i 待处理项, v 版本, a 动作 deny, s 通知的 seq, d 设备, x 到期}`，签名是 HMAC-SHA256，密钥是封存在 `secrets` 里的 `act`（[runs/deployment.md](../runs/deployment.md)「凭据与身份」）。每次投递按收件人和设备现签，24 h 有效（推送的 `TTL`）。
  - **`POST /api/act {token}`**：和别的 `/api/*` 一样要会话（service worker 用同源 cookie）、`X-Tend` 和同源，没有会话 401、跨站 403 `csrf`；人已停用 401（直接读库，不等目录 3 s 一次的重读）；签名不对、格式不对、令牌里的人不是登录的人 403 `token`（审计 `denied push.act`）；过期 410 `expired`。之后交给 `coord.Act`：在协调器的同一把锁里确认这一项还在、版本没变（否则 `request_gone`，被人答了带是谁），再由状态算出 `run.answer{run, request, decision: deny}`，`run.answer` 自己的规则（可写、能批）照旧，不能就 403；`command_id` 由「人、任务、项、版本、动作」算出，不含设备，两台设备各按一次、同一个令牌重放，都只执行一次，后来的拿到第一次的收据。成功 204；`request_gone` 回 409 `{error, by}`（`by` 是先处理的人的名字）；每次都记审计 `push.act`（设备、任务、项、动作、结果）。
- **这台设备的设置**（`push_devices.prefs`，0011；零值是默认）：`events` 是要哪些事件（`null` 就是只要 `task.needs_you`，`[]` 什么都不要，另可加 `task.done`，负责、派发或验收的任务完成时推），`hide` 隐藏内容（负载只剩 `{v, server, seq, event, n}`，`n` 是等这个人的项一共几项，每个任务的每个待处理项算一项，不带按钮，`Topic` 固定 `tend`，所以锁屏上始终只有一条「tend：n 项等你」；隐藏内容的任务完成只剩 `{v, server, seq, event}`，`Topic` 是 `tend-done`，不顶掉那条计数），`wait` 是 `task.needs_you` 升级前的秒数（0 默认，-1 立即，最多 1 h）。在投递前读：写发件箱时按 `events` 和 `wait` 决定写不写、何时投，投递时按 `hide` 决定内容。`GET /api/push/devices` 列出自己的设备（名字、建立、续期、最近送达、失败次数、`prefs`），`POST /api/push/prefs {id, prefs}` 改、`DELETE /api/push/devices {id}` 去掉，都只能动自己的，别人的设备回 404；设备换人登录时设置跟着设备走。
- 推送设备（`push_devices`，挂在人身上，经由一条会话推送）：`PUT /api/push/device {subscription, name}` 登记或续期一个浏览器的订阅（`subscription` 是 `PushSubscription.toJSON()`，endpoint 必须是 https）；按 endpoint 认出同一个浏览器，换了人登录，设备改归新人，前一个人在它上面还没发出的推送记 `canceled`；投递前再核一次设备的主人就是收件人，不是也记 `canceled`。登记和续期时记下当时的凭据（`credential_id`，0011：浏览器会话，或 Bearer 调用的 token），设备只在它有效时推送：它被退出、吊销、过期，或它的主人被停用，`sweep`（每 3 s，退出、吊销之后立刻）就去掉这台设备和它还没发出的推送，投递前也再核一次，失效的记 `canceled`；同一个浏览器重新登录后打开页面，按新会话再登记。`DELETE /api/push/device {endpoint}` 去掉自己的。90 天没续期的设备，`Notifier` 每小时清一次。页面一侧：「我」页的「推送到这台设备」开关申请、订阅和登记，每次打开页面续期，退出登录时去掉，service worker 显示推送、点开落到任务，打开页面时关掉已经不在「等你」里的通知（见 [runs/clients.md](../runs/clients.md)「Web UI（模式二）」）。
- 收件箱：`inbox.list`（跟随用 `inbox.watch`）列出处于 `waiting`（`dispatch` 除外）、与我有关、并且我能写的任务，等得最久的排前面。网页首页有「等你」和计数；电脑上页面开着、在后台，本人在「我」页打开了浏览器通知并且浏览器允许时，等的东西有了新的条目弹浏览器通知（手机上的通知是 Web Push 的事）。

## 审计与隐私

- 每个信封都带 actor：`user`、`node` 或 `system`（协调器规则）。任务详情里有活动流。
- **安全审计**和领域事件分开，单独一张只追加的表。记录这些：
  - 登录；
  - 被拒绝的访问；
  - token、会话、分享的变更；
  - 权限请求是谁批的。

  不记任何秘密，只记 id 和来源 IP（`login`、`login_refused`、`denied`、`token`、`machine`、`revoke`、`rebind`、`admit`、`invite`、`user`、`link`、`unlink`，以及 `machine.share`、`project.member`、`run.answer` 这类命令）。管理员在网页的管理页或 `tend-server admin audit` 查看。
- 事件里只写 user id，不写邮箱和姓名。
- 会话索引归机器主人。项目 run 产生的会话按项目权限可见；机器上其余的原生会话只有主人能看，不出现在别人的界面里（见「权限：两层，外加三种归属」第 6、7 条）。
- token、OAuth 凭据、工单凭据、Web Push 的私钥只存在 server 上，只存哈希或加密后的值，从不进事件表。加密密钥来自 `TEND_SERVER_KEY` 或者一个 0600 的文件，不放在数据库里。

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
