# 运行输出

一次运行的 `output.log` 怎么读成事件。实现：`internal/output`，它是唯一的解析器；TUI、Web 和手机都只负责显示，不各自再分类。

## 事件

```json
{"id":"a1:9:48213:0","off":48213,"kind":"say","text":"先跑一遍测试。","turn":1}
{"id":"a1:9:48213:1","off":48213,"kind":"tool","tool":"Bash","call":"toolu_01","family":"shell","title":"go test ./checkout/...","input":{"command":"go test ./checkout/..."},"turn":1}
{"id":"a1:9:51002:0","off":51002,"kind":"tool_result","ref":"toolu_01","output":"--- FAIL: TestPayTimeout…","lines":2310,"bytes":201840,"error":true,"truncated":{"output":183422},"turn":1}
{"id":"a1:9:60001:0","off":60001,"kind":"result","text":"改完了。","usage":{"input":48000,"output":3100,"cache_read":22000},"cost":0.62,"dur_ms":720000,"turn":1}
{"off":61200,"kind":"say","temp":true,"key":"a1:9:61200","text":"正在写的这一"}
```

- **身份**：`id` 是「`file`:行首偏移:行内序号」，`file` 是日志的 `fileio.ID`。一行可以产出几个事件，靠行内序号区分。
- **只追加**：工具的结果是单独一条 `tool_result`，用 `ref` 指向那次调用的 `call`；渲染层按 `ref` 把两条合成一条，结果先到、调用后到也能合（往前翻页时就是这样）。用量也是单独的事件，不回填到前面的事件上。
- **临时事件**：`temp: true` 的事件没有 `id`，带 `key`；同一个 `key` 的后一条替换前一条。现在只有一种：还没写完换行的最后一行，如果它不是 JSON，就当临时事件显示；是 JSON 的就等这一行写完。
- **不看 provider**：按行的形状认。claude 的 stream-json 有 `type`；codex app-server 的 JSON-RPC 有 `method`，或者带 `id` 加 `result` / `error`；codex exec 的 `type` 带点（`item.completed`），或者是 `error`。fake agent 写的是 claude 的形状，照 claude 读。
- **`at`**：行上写着时间时才有（codex 的 `emittedAtMs`），RFC 3339。

### 种类

| `kind` | 来源 | 字段 |
|---|---|---|
| `user` | claude 的 `user` 消息里的文字（内容可能是字符串）；codex 的 `userMessage` | `text` |
| `say` | claude 的 `text` 块；codex 的 `agentMessage` / `agent_message`；不是 JSON 的行 | `text` |
| `think` | claude 的 `thinking` 块；codex 的 `reasoning`（app-server 取 `summary`）。文字为空的不出事件 | `text` |
| `tool` | claude 的 `tool_use`；提问和审批（见下）；codex 的 `webSearch`、`turn/plan/updated`、exec 的 `web_search` / `todo_list` | `tool` `call` `family` `title` `more` `input`；提问和审批另带 `request` |
| `tool_result` | claude 的 `tool_result` 块 | `ref` `output` `lines` `bytes` `error`（`is_error`） `truncated` |
| `cmd` | codex 的 `commandExecution` / `command_execution`，带输出 | `tool` `call` `family` `title` `more` `input`（`{"command"}`） `output` `exit` `dur_ms` `lines` `bytes` `error` `truncated` |
| `edit` | codex 的 `fileChange` / `file_change` | `files` `diff` `title` `more` `error` |
| `mcp` | codex 的 `mcpToolCall` / `mcp_tool_call` | `server` `tool` `input` `output` `error` |
| `sys` | claude 的 `system`（`name` 是 subtype，另带 `model`）；codex 的 `thread/started` `turn/started` `thread.started` `turn.started` `deprecationNotice`；`warning` 和会重试的 `error`（`level: "warning"`）；用量 | `name` `level` `text` `usage` |
| `result` | claude 的 `result`；codex 的 `turn/completed`、`turn.completed`、`turn.failed` | `text` `error` `usage` `cost` `dur_ms` |
| `error` | codex 不再重试的 `error` 通知、exec 的 `error`、JSON-RPC 的错误回应 | `text` |
| `raw` | 认不出的 JSON 行，和 claude 消息里认不出的块 | `text`（原文） |

`gap`、`mark` 和 `stream` 字段由节点和协调器的输出流加上，不在解析器里。

**用量**：claude 在 `result` 上带 `usage`，是这一轮的；codex app-server 的 `thread/tokenUsage/updated` 是一条 `sys{name:"usage"}`，数是整个线程到这时为止的总数；codex exec 在 `turn.completed` 上带。`input` 不含缓存命中，命中的在 `cache_read`。

**提问和审批**是 `tool{family: "ask", request}`，`request` 和节点给出的 `agent.Request.ID` 一样：
- claude 的 `control_request` `can_use_tool`：`tool` 是被请求的工具，`title` 按那个工具的 family 生成，没有就用 `description`；`request` 是 `request_id`。
- codex 的 `item/commandExecution/requestApproval`（`tool: "shell"`）、`item/fileChange/requestApproval`（`apply_patch`）、`item/permissions/requestApproval`（`permissions`）、`item/tool/requestUserInput`（`question`）：`request` 是 `rpc-` 加上 JSON-RPC 的 `id` 原文，`input` 是整个 `params`。
- 运行自己的上报（`tend run note|ask|verdict|plan`，节点不问人就放行，`agent.OwnReport`）不出事件。解析器不在节点上，认的是名叫 `tend` 的程序，不比较路径。

**不出事件的**：
- claude：`stream_event`（增量在逐字输出做之前一律丢弃）、别的 `control_*`、`command_lifecycle`、`rate_limit_event`、`redacted_thinking`。
- codex：增量（method 以 `delta` 结尾）、`item/started`、`item/updated`、`item.started`、`item.updated`，前缀是 `hook/` `mcpServer/` `account/` `remoteControl/` `serverRequest/` `thread/status/` `thread/goal/` `turn/diff/` 的通知，成功的 JSON-RPC 回应。
- 空行；行尾的 `\r` 去掉。多行文字保留成一条事件，拆行是渲染层的事。

## 工具事件

**family**：`shell`、`read`、`search`、`edit`、`web`、`agent`、`plan`、`ask`、`mcp`、`other`。工具名到 family 的对照表只有一份，是 `output.Families`；claude 的 MCP 工具（`mcp__server__tool`）都是 `mcp`，表里没有的是 `other`。

**`title`** 是一行摘要，最多 200 个字符；**`more`** 是摘要略掉的数量，由渲染层翻译成文字：

| family | `title` | `more` |
|---|---|---|
| shell | 命令的第一行；`sh` / `bash` / `zsh` 的 `-c` / `-lc` 包着的，取里面的命令 | 其余的行数 |
| read | 路径，有范围时加 `:起-止` | — |
| search | `模式 · 路径`（或 glob） | — |
| edit | 路径和 `+加 −减`（claude 按新旧文字的行数算，codex 按 diff 算） | 其余的文件数 |
| web | 域名加路径；搜索是搜的词 | — |
| agent | 子 agent 的描述 | — |
| plan | 完成的步数 / 总步数 | — |
| ask | 第一个问题；审批是命令或理由 | 其余的问题数 |
| mcp | `server.tool` 加上参数里第一个字符串值 | — |
| other | 参数里第一个字符串值 | — |

**输出**（`tool_result`、`cmd`、`mcp` 的 `output`）：不超过 80 行、32 KiB 时原样；否则留头 40 行和尾 40 行，每头最多 16 KiB，中间放一行 `…`。`lines` 和 `bytes` 是原来的总行数和字节数，`truncated.output` 是略掉的字节数。

**`parent`**：子 agent 内部的事件指向发起它的那次调用（claude 的 `parent_tool_use_id`）。

## 轮次

`Parse(file, off, text, State)` 从 `off`（一行的开头）读起，回答事件、读完以后的 `State` 和读到哪里（最后一个完整的行之后）。`State{Turn, Closed}` 是这一页开头所在的轮次，和那一轮的 `result` 到了没有；从日志开头读时是 `{1, false}`。

- `result` 把状态设成 `Closed`。
- `Closed` 之后，下一条开轮的事件把 `Turn` 加一：`user` `say` `think` `tool` `cmd` `edit` `mcp`，或者 `sys` 里的 `init`、`turn/started`、`turn.started`。
- `tool_result` 不开新的一轮；一轮当中进来的消息（插话）也不开，因为那时还没有 `result`。
- 从中间某一页冷启动：只要给出那一页开头的 `State`，读出的 `id` 和 `turn` 就和从头读整份日志一样（测试逐行切页核对）。

## 分组

`Items(events)` 把事件排成时间线上的项，是纯函数，翻页以后重新算：
- 调用也在这批事件里的 `tool_result` 归到调用那一项（`Results` 给出调用到结果的对应），不单独成项；临时事件不排进去。
- 同一个 `parent` 下，连续的 `read` / `search` 调用合成一组，别的事件一出现就断开。提问和审批不进组。
- 项的 `id` 是第一条事件的 `id`；`Has(id)` 对组里任何一条的 `id` 都成立。往前翻一页、组的第一条变了以后，按原来的 `id` 记下的展开状态还能找到这一组。

## 翻页：`run.output.page`

`run.output.page{run, before, n, raw?, file?}` 回 `{events, from, to, earliest, file, prev?, turn?, raw?}`，权限和 `run.tail` 一样（能读这个 run 的任务就能读）。实现在 `internal/coord/output.go`。
- 协调器转问节点的 `run.tail{clip}`（`raw` 时不带 `clip`）：第一次读 `before`（`-1` 是末尾）之前的 64 KiB，不够 `n` 个事件（默认 200，最多 1000）就接着往前读，每次加倍，读到日志开头、一共 1 MiB 或者读不动了为止；拼起来的行从节点给的轮次起逐行交给 `Parse`。只送开头的行（`head`）成一个 `raw` 事件；被截过的行（有 `size`），它的事件都带 `truncated.line`（原行长），要全文用 `run.line`。只回文字的旧节点，文字按行切开照样读。
- 超过 `n` 个就只留最后 `n` 个，再往前补齐第一个事件所在那一行的其余事件：页从一行的开头切。`from` 是这一行的偏移，往前翻一页就用它作 `before`；`to` 是最后一个完整行的末尾；还没写完的最后一行作为临时事件放在最后。
- `earliest` 是还能取到的最早位置，现在总是 0；`from` 等于它就是到了这一代日志的开头，这时 `prev` 给出上一代（`.1`）的 ID，往前翻就用 `{file: prev, before: -1}`。
- `file` 带上一页的日志 ID：当前这一代或 `.1` 都认；两者都不是回 `stale`，客户端丢掉手上的页重新从末尾取。
- `raw` 时另回这一页的原文（从 `from` 起，含没写完的最后一行），给「原始行」用。
- `turn`：节点照 `marks.jsonl` 给出这一页开头的轮次，页里再往后数，所以事件的 `turn` 和从头读整份日志的一样；`turn` 是第一个事件的轮次。节点的标记里没有这一代日志（旧节点、手写的日志）时，事件里的 `turn` 清空，也不回 `turn`。

CLI 的 `tend run logs` 原样打印日志，仍用 `run.tail`。
