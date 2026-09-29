# agent 适配器

provider 适配器与 agent 档案：档案字段、选机器、有类型的参数、需要节点 feature 的字段、任务书怎么传、探测命令行。实现：`internal/agent`；agent 定义的解析与编译在 `internal/defs`（见 [tasks/agent-definitions.md](../tasks/agent-definitions.md)「Agent 定义」）。

## Provider 与档案

- `Provider`：`Name` `Caps` `Installed` `Launch` `Resume` `Fork` `Start`。
- 档案 `config.agents[]`：`{name, provider, model, effort, deny, permission, args, command, stdin, machine}`；内置档案 `claude`、`codex`、`fake`。
- `machine` 是这个档案唯一能跑的机器：派发或 `run.continue` 到别的机器回 `bad "agent X runs on M"`；agent 定义的 `machines.require` 只有一项时编译成它。
- 选机器的顺序：派发参数 > 档案 `machine` > 任务 `machine` > 项目 `defaults.machine` > agent 定义 `machines.prefer`（第一台已连上的），模式二一个都没有时回 `bad machine`。
- `effort`（low / medium / high / xhigh / max）和 `deny`（工具名）是有类型的字段：claude `--effort`、`--disallowedTools`；codex `-c model_reasoning_effort=`（max 记作 xhigh），不支持 `deny`。带这两项的 run 要节点声明 feature `agentdef`。
- agent 定义（`internal/defs`，Markdown + YAML frontmatter）编译成档案，同名时优先于 `config.agents`；说明书放在任务书前面。
- 档案在协调器解析、随 `spec` 冻结下发；节点在模式二下再按本机白名单校验（见 [deployment.md](deployment.md)「模式二」）。

## 需要节点 feature 的字段

旧节点会静默忽略这些字段，所以各要一个节点 feature，缺它的节点回 `node_outdated`（见 [overview.md](overview.md)「已定决策」）。

- workflow 阶段的 run：`verdict`（任务书末尾加 `tend run verdict` 约定）要 feature `verdict`；`check`（项目 `hooks.check` 的 argv：agent 退出码 0 后监督进程在工作目录里跑，30 分钟上限，输出接进 `output.log`，结果 `{argv, exit, tail}` 进 `state.json`）要 feature `check`，节点还要 `node.allow_hooks` 或 `allow_bypass`。
- `run.start` 的 `work`（`agent.Workspace`：checkout、分支、祖先分支、base、remote、只读、合并、setup / cleanup hook）要 feature `worktree`：节点在 `<checkout>-wt/` 下建任务的工作树或只读副本，或者只做合并不跑 agent，结果 `{dir, branch, head, commits, diffstat, discarded, merged, conflict, pr, warnings}` 进 `state.json` 的 `work`（见 [tasks/execution.md](../tasks/execution.md)「实现」）。
- `planner` 的 run 要 feature `plan`：任务书末尾附 `tend run plan` 的约定，计划（JSON）进 `state.json` 的 `plan`。
- 档案的 `hooks` `mcp` `skills`（来自 agent 定义）要 feature `files`，只对 claude：节点把 hooks 写成 run 目录的 `settings.json`、按名字从 `node.mcp` 取服务器写成 `mcp.json`，检查 skills 已装。

## 任务书传递

- background：claude、codex 由监督进程把 `prompt.md` 作为第一条消息发出（见 [node.md](node.md)「双向流（stream）」）；command 模板 `stdin: true` 时经 stdin，否则只能用 `{prompt_file}`。
- herdr：首条消息是「读 `<run 目录>/prompt.md` 并完成」，加 `--add-dir <run 目录>`。
- 模板占位符只有 `{prompt_file}` `{model}` `{dir}`；Windows 上目标是 `.cmd` / `.bat` 时，参数含 cmd.exe 元字符（`& | < > ^ % ! "` 换行）就拒绝（npm 装的 `codex.cmd` 照常能跑）。
- 后台 claude（双向流和无界面启动）都带 `--include-partial-messages`：正在写的消息以 `stream_event` 增量流出来，监督进程把它拼进 `partial.json`，不写日志（见 [node.md](node.md)「逐字输出」）；codex app-server 本来就发增量。
- 后台 claude 走双向流时放开 `AskUserQuestion`（提问能远程作答）；非双向的无界面启动（`Headless` 而非 `Stream`）仍加 `--disallowedTools AskUserQuestion`。

## 续跑与探测

- `Caps.Continue`：能无界面续一个会话（claude、codex、fake）；`LaunchSpec.Resume` 是要续的会话。
- `agent.Probe(provider)`：`claude --version` + `claude auth status`（只读 JSON 的 `loggedIn`，不留账号），`codex --version` + `codex login status`（退出码 0 = 已登录，输出含 "Not logged in" = 没登录），各 10 s 超时；只问命令行，不读凭据文件。其它 provider 视为可用。
- pi / OpenCode / Grok / Gemini 的 command 模板示例写进 README，参数按各 CLI 的能力。
