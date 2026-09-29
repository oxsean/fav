# Agent 定义

agent 定义：类型与角色、Markdown 格式、存放、归属与分享、编译成启动参数，以及 skills、MCP 和密钥怎么到目标机器。实现：`internal/defs`（`Parse`、`Format`、`Check`、`Import`、`Compile`）、`internal/coord`（`agentdef.*`）、`internal/node` 和 `internal/agent`（翻译成启动参数）、`cmd/tend`（`tend agent …`）。

## 「类型」怎么理解

- **类型 = provider**：`claude`、`codex`、`command`（pi、OpenCode、Gemini 等走 argv 模板）、`fake`。它决定能力：双向流、作答、插话、结构化输出、会话恢复（`agent.Caps`）。
- **角色 = 这个定义在 workflow 里干什么**：`planner`、`implement`、`review`、`test`、`any`。角色只是标签，workflow 按角色从项目的 `defaults.roles` 里挑定义；也可以直接指定定义名。
- 计划：tend 内置五个角色的默认说明书（embed），定义可以覆盖或追加（未实现；workflow 按角色内置了阶段任务书的默认模板，见 [workflows.md](workflows.md)「实现」）。

## 格式

向 Claude Code subagent 看齐，它是业内事实标准。

```markdown
---
name: reviewer-codex
description: 审 diff，对照验收标准，只写发现不改代码
role: review
provider: codex             # 或 profile: codex-high（引用 config.json 里现有的档案）
model: gpt-6-astra
effort: xhigh
permission: read-only       # claude: permission-mode；codex: sandbox
skills: [code-review]
tools: {deny: [Edit, Write]}
mcp: [gitea]                # 名字；配置和密钥留在节点，见下文「skills、MCP 和密钥怎么到目标机器」
hooks:                      # provider 自己的 hooks，编译进启动参数（workflows.md「hooks」第 2 类）
  Stop: [{command: "tend run check"}]
machines: {prefer: [linux], require: []}
output: verdict             # none | verdict | plan：要求结构化结果（workflows.md「结构化结果」）
budget: {usd: 3, minutes: 40}
---
你是评审者。读 workpad 和分支 diff，逐条对照验收标准……
```

- 字段只增不改；未知字段保留，并在 `tend agent check` 里给出警告。
- `import: ~/.claude/agents/foo.md`：直接复用 Claude Code 的 subagent 定义，tend 只补 `role`、`machines`、`output`。导入在客户端做（`tend agent import <file>` 读本机文件，把合成后的文本交给 `agentdef.save`），协调器从不读客户端路径。
- 解析用 `go.yaml.in/yaml/v3`（`internal/defs`），它是这部分给 `cmd/tend` 带来的唯一依赖。

## 存放、归属与分享

- **存放**：团队模式是 journal 事件 `agentdef_saved` / `agentdef_removed` / `agentdef_shared`（随 journal 存在 SQLite 里）；模式一是 `<home>/defs/agents/<name>.md`，归这台机器的用户。同名时定义优先于 `config.json` 的档案。
- **归属与分享**：主人是用户，或 `project:<id>`（项目负责人管理，项目参与者使用）。`DefShare{users, projects, all, view}`：分享给人、给项目（该项目的参与者在该项目的任务上用），或给所有人；`view` 决定被分享的人能否看说明书。
  - 管理员能看见和管理所有定义，但不能用没分享给自己的定义。
  - 别人看不见也用不了的定义，报 `not_found`；看得见但这个任务不能用的，报 `unauthorized` 并说明原因。
  - 分享的完整规则见 [team.md](team.md)「共享：agent 和机器默认私有」。
- 方法：`agentdef.list` / `get` / `save` / `remove` / `share`；CLI 是 `tend agent defs|import|export|check|rm|share`。

## 编译

一个 AgentDef 加一个 run 上下文，编译成 `LaunchSpec`。编译结果在 `run.preview` 和 run 详情里原样显示。

| 字段 | claude | codex（app-server） | command |
|---|---|---|---|
| model / effort | `--model` `--effort` | `-c model=` `-c model_reasoning_effort=` | 模板变量 |
| permission | `--permission-mode` | `-c sandbox_mode=` `approval_policy` | — |
| 说明书 | `--append-system-prompt-file <run>/agent.md` | `thread/start` 的 `developerInstructions` | 进任务书 |
| tools | `--allowedTools` / `--disallowedTools` | —（用 sandbox 近似） | — |
| skills | `--plugin-dir <run>/plugin`（见下文） | `skills/extraRoots/set` 指向 `<run>/skills` | — |
| hooks | `--settings <run>/settings.json`（`hooks` 键） | app-server 有 hooks（`hooks/list`、hook 通知），注入方式待核实 | — |
| mcp | `--mcp-config <run>/mcp.json` | `-c mcp_servers.…` | — |
| output | `--json-schema`（文档只写了配 `--output-format json`，与 stream-json 同用待核实） | `turn/start` 的 `outputSchema` | `tend run verdict` 自报 |

待核实的几项见 [overview.md](overview.md)「待核实」。

run 自己的回报（`tend run note|ask|verdict|plan`）写 run 目录，沙箱不该拦它：

- codex 是 workspace-write 时，第一个 `turn/start` 带上 `thread/start` 返回的 `sandboxPolicy`，`writableRoots` 追加 run 目录（run 目录在 `~/.agent/tend` 下，默认不可写）；read-only 什么都写不了，靠下一条。
- agent 请求执行的命令恰好是任务书写的那个 tend 的 `run note|ask|verdict|plan`（`shell.POSIX.Split` 能整条读成普通词；codex 的 `/bin/zsh -lc '…'` 外壳先剥掉；`plan -` 只接受带引号标记的 heredoc），节点直接批准，不进「等你」：codex 的 `requestApproval`、claude 的 `can_use_tool`（Bash）都一样。多一个词（`;`、展开、重定向、别的命令）就照常等人批。

### 实现

- `AgentProfile` 有带类型的 `effort` 和 `deny`，节点适配器翻译：claude `--effort`、`--disallowedTools`；codex `-c model_reasoning_effort=`（`max` 记作 `xhigh`），codex 没有对应开关，`deny` 对它不生效。节点靠 feature `agentdef` 声明支持；不支持的节点派发时被拒（`node_outdated`）。
- `model`、`permission`、`profile`（引用现有档案作基底）直接进冻结的档案；`machines.prefer` 在任务和项目都没指定机器时决定派到哪台（先挑在线的）。
- 说明书放进任务书（项目 context 之后）；`--append-system-prompt-file` / `developerInstructions` 未实现，它要扩节点的文件下发。
- `skills`、`mcp`、`hooks` 随节点 feature `files` 落地，只对 claude（见 [execution.md](execution.md)「实现」）；codex 的 hooks 和 mcp 被节点拒绝，预检提示 `def_pending`。
- `output`、`budget` 只保存不生效（未实现）。

## skills、MCP 和密钥怎么到目标机器

- **定义里只写名字**，值和文件在节点上。节点 `node.agents` 只报告每个 CLI 装没装、版本、登没登录，不报告 skills 和 MCP 名字：skills 由节点检查 `~/.claude/skills/<名字>` 已装好，没装就拒绝派发；MCP 按名字取节点 `node.mcp` 里的配置。
- **不改用户仓库**：
  - claude：run 目录里拼一个临时 plugin（skills、agents、hooks、MCP），用 `--plugin-dir` 只对这一次会话生效（2.1.283 `--help` 和官方 headless 文档都有；`--add-dir` 目录下的 `.claude/skills` 也会加载，作为备选）；
  - codex：app-server 的 `skills/extraRoots/set` 把 `<run>/skills` 加为额外根目录（0.155.1 协议 schema 里有），不用另建 `CODEX_HOME`。节点还可以用 `skills/list` 报告这台机器上有哪些 skills（未实现）。
- **可选的「同步 skills」**（未实现）：协调器把 `<home>/defs/skills/<name>/` 打包，随 `run.start` 发到节点缓存，按 hash 去重（Multica 做法）。现在要求 skills 在节点上已装好。
- MCP 和 env 的值永远不进 journal、不进定义文件，只在节点的 `node.json` 里（Multica 把 `custom_env` 存在服务端，是它文档里专门警告过的坑）。
