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
- **临时事件**：`temp: true` 的事件没有 `id`，带 `key`；同一个 `key` 的后一条替换前一条，既没有 `text` 也没有 `title` 的一条表示把它去掉。有三种：
  - 还没写完换行的最后一行，如果它不是 JSON，就当临时事件显示；是 JSON 的就等这一行写完。`key` 是「`file`:行首偏移」。
  - agent 正在写的消息（节点的 `partial.json`，见 [node.md](node.md)「逐字输出」），只在输出流里有：`kind` 是 `say` 或 `think`，`key` 是 `p:` 加节点给的 key。
  - codex 还在跑的命令（也来自 `partial.json`），`output.Running` 生成：`kind: cmd`，`key` 同上，`tool` `call` `family: shell` `title` `more` `input` 和最终的 `cmd` 一样，`output` 是输出的最后 3 行，`bytes` 是到这时为止的字节数；不知道命令时 `title` 用 `tool`，所以它总有 `title`。
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
| `you` | `Join`：agent 交回来的一条消息（见下） | `input`（send id） `text` `by` `mode` `at` |
| `resolved` | `Join`：`marks.jsonl` 的 `resolved` | `request` `by` `decision` `at` |
| `interrupt` | `Join`：`marks.jsonl` 的 `interrupt` | `n`（打断的那一轮） `by` `at` |
| `mark` | `Join`：`marks.jsonl` 的 `hook` | `event`（`hook`） `name` `phase` `exit`（`end` 时） |
| `gap` | 输出流：这一段取不回来了 | `from` `to`（`{file, off}`） |

`you`、`resolved`、`interrupt`、`mark` 由 `Join` 合成，`gap` 由输出流加上，都不在 `Parse` 里；`Join` 合成的事件 `stream` 是 `tend`。

**用量**：claude 在 `result` 上带 `usage`，是这一轮的；codex app-server 的 `thread/tokenUsage/updated` 是一条 `sys{name:"usage"}`，数是整个线程到这时为止的总数；codex exec 在 `turn.completed` 上带。`input` 不含缓存命中，命中的在 `cache_read`。

**提问和审批**是 `tool{family: "ask", request}`，`request` 和节点给出的 `agent.Request.ID` 一样：
- claude 的 `control_request` `can_use_tool`：`tool` 是被请求的工具，`title` 按那个工具的 family 生成，没有就用 `description`；`request` 是 `request_id`。
- codex 的 `item/commandExecution/requestApproval`（`tool: "shell"`）、`item/fileChange/requestApproval`（`apply_patch`）、`item/permissions/requestApproval`（`permissions`）、`item/tool/requestUserInput`（`question`）：`request` 是 `rpc-` 加上 JSON-RPC 的 `id` 原文，`input` 是整个 `params`。
- 运行自己的上报（`tend run note|ask|verdict|plan`，节点不问人就放行，`agent.OwnReport`）不出事件。解析器不在节点上，认的是名叫 `tend` 的程序，不比较路径。

**不出事件的**：
- claude：`stream_event`（新的日志里没有了，增量进 `partial.json`；旧日志里的丢弃）、别的 `control_*`、`command_lifecycle`、`rate_limit_event`、`redacted_thinking`。
- codex：增量（method 以 `delta` 结尾，新的日志里没有了）、`item/started`、`item/updated`、`item.started`、`item.updated`，前缀是 `hook/` `mcpServer/` `account/` `remoteControl/` `serverRequest/` `thread/status/` `thread/goal/` `turn/diff/` 的通知，成功的 JSON-RPC 回应。
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

节点在写日志前把整份文件的副本移出了行（[node.md](node.md)「瘦身」）：原处是 `output.Ref`，`{"$blob":"<sha256>","bytes":n,"lines":k}` 或 `{"$omit":"<字段>","bytes":n,"lines":k,"cap"?:true}`。解析时字符串字段读作 `output.Text`（字符串或引用），edit 的 `+加 −减` 用引用里的 `lines`，所以标题和瘦身前一样。

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

## 标记和 journal：`Join`

`Join(events, marks, turn, said, seen)` 在协调器上把一段事件补全，翻页和输出流都用它（`internal/output/join.go`）：
- **你的消息按行认，不按标记认**：claude 交回来的 user 行（`isReplay`）带着 `uuid`，codex 的 `userMessage` 条目带着 `clientId`，`Parse` 把它记在 user 事件上（`Echo`，不出现在 JSON 里）。协调器按这次运行在 journal 里的 `sends` 对上：codex 的 `clientId` 就是 send id，claude 的 `uuid` 是 `node.UUIDFor(run, send id)`。对上的 user 事件改成 `you`，`text`、`by`、`mode` 取 journal 的，`at` 行上没有就取发送的时间；同一条消息（`seen`）再出现就不再显示。对不上的（任务书、终端里打的字）照旧是 `user`。
  - 为什么不按 `input` 标记认：监督进程先写行、后写标记，输出流正好读在两者之间时，这一行会先作为 `user` 推出去，标记晚到就改不回来。`input` 标记只表示位置和「agent 已接收」（`seen`）。
- **其余标记按位置并进来**：`resolved`、`interrupt`、`hook` 成为事件，放在同一偏移上的行事件之前（`marks.jsonl` 里标记不按偏移排序，`Join` 自己排）；`turn` 取前一个事件的。`id` 是「标记所在的 `file`:偏移:`m`+它在 `marks.jsonl` 里的字节位置」，和行事件不会重号，客户端取 `file` 的办法（去掉最后两段）照常能用。`start`、`turn`、`exit`、`roll`、`input` 不出事件。
- **谁做的以 journal 为准**：`resolved` 的 `by`、`decision` 取 journal 里这个请求的回答（协调器另记一张 `(run, request)` 的表，因为节点取走回答后 `Run.answers` 就不留了；agent 自己撤回的请求没有回答，`by` 为空）；`interrupt` 的 `by` 取 `Run.interrupt`，按 ask id 对上。

## 实时：`run.output.watch`

`run.output.watch{run, from?}` 是流（[wire.md](wire.md)「流」），权限和 `run.output.page` 一样，看不到的运行回 `not_found`。推送：
- `open{cursor, mode}`：`resume` 从 `cursor` 接着来；`gap` 带 `from`、`to`，表示 `from` 之后到这一段开头取不回来了，客户端用翻页补。
- `run.output{events, cursor?}`：`cursor` 是 `{file, off, marks}`，客户端原样存着，重开时作为 `from` 带回来；只有临时事件的推送不带它。临时事件带 `key`，整行到了以后，它的第一个事件带上同一个 `key` 替换掉临时事件。
- 运行结束、输出读完，回 `result{reason: done}`；机器断开或节点上没有这个运行了回 `gone`；可见范围变了（`reshapes` 的信封、`Coord.Reaffirm`）以后看不到了回 `unauthorized`；节点太旧、没有 `run.follow.watch` 回 `unsupported`；hub 满了回 `busy`。

协调器上每个运行一个 hub（`internal/coord/output.go`），所有订阅者共用：
- **打开**：第一个订阅者来时，先用 `run.output.page` 的读法取最后一页（200 个事件）作为第一批，再从这一页的末尾（带 `run.tail` 回的 `marks_to`）在节点上开 `run.follow.watch`。已经结束的运行也这样开：节点读到末尾就回 `done`。
- **解析一次、编码一次**：节点推来的行用 `Parse` 读、`Join` 补全，每 100 ms 合一批，编码一次，用 `wire.Stream.PushRaw` 发给所有订阅者；一条推送不超过 256 KiB。
- **缓冲**：留最近 2000 个事件或 1 MiB 的批次。订阅者带的 `from` 是某一批的末尾，就从下一批给起；等于最新的游标就只接上实时的；比缓冲还早，推 `open{gap}` 再给整个缓冲。
- **正在写的消息**：节点推来的 `partial` 里每条变了的出一个临时事件（`turn` 取当前的）；最终事件到了接过它的 `key`：claude 的 `assistant` 行和 codex 的 `item/completed` 解析出的 say / think / cmd 带着来源的消息 id 或条目 id（`Event.Src`，不出现在 JSON 里），和还在写的那条 `src`、`kind` 都对上就带上它的 `key`，原地替换。已经被接过的 `key` 再出现在 `partial` 里就不管（最近 64 个）；从 `partial` 里消失、又没等到最终事件的，推一个同 `key`、没有 `text` 的临时事件把它去掉。同一批里同一个 `key` 的临时事件只留最后一条；缓冲里只有临时事件、同一组 `key` 的批次只留最新的一批。所以一个运行的临时事件最多约每 200 ms 一次（节点跟随的节拍）。
- **慢订阅者**：流用 `FullGap`，被丢的推送记下第一条的起点；下一批之前先推一个 `gap{from, to}` 事件，别的订阅者和节点那边不受影响。
- **关闭**：最后一个订阅者走后留 10 s（又有人来就接着用），然后取消节点上的跟随。运行结束（节点回 `done`）、节点断开或跟随出错时，所有订阅者的流随之结束，hub 删掉；下一个订阅者重新冷启动。
- **在用**：hub 每收到节点的一批就刷新机器的 `busyAt`，模式一不会在有人看输出时因空闲断开。
- 最多 64 个 hub；常数在 `internal/coord/limits.go`。
- 模式一换人持锁：游标是节点上的位置，和谁当协调器无关；新协调器上的 hub 按冷启动来。

## 密度

时间线的三档密度是 `output.Densities`（`brief`、`standard`、`detailed`，默认 `standard`），每种步骤在各档怎么显示是 `output.DensityShow`，截断的行数是 `BriefLines`、`SayFold`、`DiffCut`、`FailTail`、`DetailEnds`、`RunningTail`。这张表由 `tools/protogen` 写进网页的 `proto.js`，网页和以后的 TUI 读同一张表。

- 步骤的种类：事件的 `kind`，工具调用取它的 family，连续的读和搜是 `group`，人说的话是 `you`，单独的结果是 `output`；表里没有的按 `other`。
- 格子的取值：`hide` 不显示；`sum` 在简洁档并进这一轮的一行小结，出错的单独一行；`row` 单独一行、收起；`open` 单独一行、展开；`warn` 只显示警告；`hook` 只显示 hook 的标记；`note` 只在带错误、用量或花费时显示。
- 不管哪一档，出错的步骤、还在跑的命令、没答的问题都自己展开；任务书在简洁和标准档只露前 `BriefLines` 行，agent 的话在标准档超过 `SayFold` 行时折起。

## 翻页：`run.output.page`

`run.output.page{run, before, n, raw?, file?}` 回 `{events, from, to, earliest, file, prev?, turn?, raw?}`，权限和 `run.tail` 一样（能读这个 run 的任务就能读）。实现在 `internal/coord/output.go`。
- 页里的事件经 `Join` 补全，标记取自 `run.tail` 回的 `marks`。
- 协调器转问节点的 `run.tail{clip}`（`raw` 时不带 `clip`）：第一次读 `before`（`-1` 是末尾）之前的 64 KiB，不够 `n` 个事件（默认 200，最多 1000）就接着往前读，每次加倍，读到日志开头、一共 1 MiB 或者读不动了为止；拼起来的行从节点给的轮次起逐行交给 `Parse`。只送开头的行（`head`）成一个 `raw` 事件；被截过的行（有 `size`），它的事件都带 `truncated.line`（原行长），要全文用 `run.line`。只回文字的旧节点，文字按行切开照样读。
- 超过 `n` 个就只留最后 `n` 个，再往前补齐第一个事件所在那一行的其余事件：页从一行的开头切。`from` 是这一行的偏移，往前翻一页就用它作 `before`；`to` 是最后一个完整行的末尾；还没写完的最后一行作为临时事件放在最后。
- `earliest` 是还能取到的最早位置，现在总是 0；`from` 等于它就是到了这一代日志的开头，这时 `prev` 给出上一代（`.1`）的 ID，往前翻就用 `{file: prev, before: -1}`。
- `file` 带上一页的日志 ID：当前这一代或 `.1` 都认；两者都不是回 `stale`，客户端丢掉手上的页重新从末尾取。
- `raw` 时另回这一页的原文（从 `from` 起，含没写完的最后一行），给「原始行」用。
- `turn`：节点照 `marks.jsonl` 给出这一页开头的轮次，页里再往后数，所以事件的 `turn` 和从头读整份日志的一样；`turn` 是第一个事件的轮次。节点的标记里没有这一代日志（旧节点、手写的日志）时，事件里的 `turn` 清空，也不回 `turn`。

CLI 的 `tend run logs` 原样打印日志，仍用 `run.tail`。

## 单个事件、查找和改动

这几个方法都按 `run.output.page` 的权限判（能读这个 run 的任务就能读，看不到回 `not_found`），再转给运行所在机器的节点；实现在 `internal/coord/runread.go`。节点没有这个方法时回 `unsupported`（`detail` 是方法名），客户端退回自己的办法（查找退回用 `run.output.page` 取页再找）。`run.output.item`、`run.diff`、`run.blob` 和 `run.output.page` 一样走 `Bulk`，排在推送之后。
- `run.output.item{run, id}` → `{event, blobs?: [{blob, bytes}]}`：一个事件，什么都不省。`id` 是页里的写法，也可以带运行（`<run>/<file>:<off>:<n>`，前缀要和 `run` 一致）；标记的事件（`m<pos>`）和写错的回 `bad_request`，这一行里没有这个序号回 `not_found`。协调器用 `run.line` 取出整行（最多 1 MiB），把行里的 `$blob` 引用用 `run.blob` 换回原文，一共最多 1 MiB，放不下的留在原处、列进 `blobs`，客户端接着用 `run.blob` 取；然后用 `Whole` 解析（和 `Parse` 一样，只是工具输出不截头尾），经 `Join` 补全。事件的 `turn` 不给：单独一行不知道自己在第几轮。行本身超过 1 MiB 时回一个 `raw` 事件，带 `truncated.line`。
- `run.output.find{run, q, file?, before, limit?}`：参数和结果照节点的（[node.md](node.md)「读输出」），`limit` 最多 200；命中的 `id` 加上运行，写成 `<run>/<file>:<off>:<n>`。只查一次运行：`next{file, before}` 原样给客户端，下一次带回来；没有 `next` 就是这次运行找完了，接着找同一对话里前一次运行由客户端决定（它知道对话由哪些运行接成，各次可能在不同机器上）。客户端取消时，协调器把取消转给节点。
- `run.changes`、`run.diff`、`run.blob`：参数和结果照节点的（[node.md](node.md)「改动」）。`all` 由协调器填：看的人是运行所在机器的主人才为真，客户端带的不算；管理员不是机器主人就不算。所以目录运行里，别人只看到 agent 碰过的文件和 `hidden` 的个数，`run.diff` 也只认这些路径（节点按 `all` 核对）；模式一只有一个人，他是所有机器的主人。
