# 收藏

收藏流程、`/tend` skill 的职责与输出协议、收藏记录的结构与读写语义。实现：`internal/tend`（`Rec`、`Store`、`Patch`）、`cmd/tend`（`add`）、`skills/tend/SKILL.md`。

## 收藏流程

1. 用户在 Claude Code 或 Codex 当前会话执行 `/tend`。
2. Skill 根据当前会话生成结构化 JSON（只含语义字段）。
3. Skill 将 JSON 通过 stdin 交给 `tend add`。
4. Core 自动补充环境元数据、校验并追加写入 JSONL。会话目录（索引里的主仓库，否则 `cwd`）属于某个项目时，`project` 写成项目名，skill 给的名字只在没有项目时用；项目从哪来见 [cli-and-config.md](cli-and-config.md)「项目表」。
5. 返回简短确认：标题、项目、标签、记录 ID。
6. 同一 Provider + Session ID 已存在时更新该记录，不重复创建。

当前会话的身份怎么识别见 [resume.md](resume.md)「Provider」和 [external-behaviour.md](external-behaviour.md)「Session ID 获取」。

## `/tend` Skill 要求

### 职责

- 基于当前完整对话生成语义字段。
- 识别当前会话真正解决的问题、当前进度和下一步。
- 调用 `tend add` 写入。
- **不采集环境元数据**：provider、session id、cwd、git、Herdr 上下文全部由 Core 自己读，Skill 传了也会被忽略。这样两边不会各实现一遍。

### 输出协议

```json
{
  "schema_version": 1,
  "title": "notes-api 搜索分页游标漂移排障",
  "label": "notes 分页排障",
  "summary": "排查搜索接口按 updated_at 排序时游标分页重复返回同一条。确认同秒更新的记录游标不唯一，后端回退到按 id 兜底排序；已整理复现步骤，下一步向后端团队提 ticket。",
  "tags": ["notes-api", "pagination", "cursor", "debug"],
  "project": "notes-api",
  "work_type": "debug",
  "status": "done"
}
```

`status` 取 `todo` / `doing` / `done`，与数据模型里的字段同名同值，默认 `done`。

### 生成质量

- Skill 正文是英文（模型指令），`description` 里中英触发词都有；title / summary 跟对话语言走，标签一律小写 kebab-case。
- JSON 用带引号的 heredoc 送进 `tend add`（`tend add <<'EOF' … EOF`），单引号包 JSON 会被撇号截断。
- 标题 12–40 个中文字符或 6–14 个英文词，突出对象和工作结果；禁止复用原始 Session 标题。
- `label` 是标题的压缩版，≤ 10 个中文字或 20 列英文，给 Herdr tab 栏用；可省略。
- 摘要 80–250 个中文字符或 50–150 个英文词，包含目标、关键结论、当前状态/下一步。
- 标签 2–5 个主题词，不重复 project / work_type，不放单号；先复用已有标签。
- 不写入密码、token、连接串、完整私密文件内容；不确定的字段省略，不得编造。

## 数据模型

存储是 append-only JSONL：`~/.agent/tend/records.jsonl`，一行一条完整记录。

### 记录结构

```go
type Rec struct {
    ID        string   // 内部 id，与 provider session id 解耦
    Schema    int
    Provider  string   // claude | codex
    SessionID string
    Title     string
    Label     string   // 短标题，给 Herdr tab 用；可空
    Summary   string
    Project   string
    WorkType  string
    Tags      []string

    // 三维互不影响：收藏与否（FavoritedAt 有值即收藏）、看板状态 Status、归档与否（ArchivedAt 有值即归档）。
    Status      string     // todo | doing | done；入库的记录缺省 done，没收藏也没标过的会话为空（「未标注」，不算进行中）
    FavoritedAt *time.Time // 空 = 未收藏（只标过状态的会话）
    ArchivedAt  *time.Time // 归档只是从默认列表里收起来，不动 Status

    Cwd, GitRoot, GitRemote, GitBranch, Hostname string

    TranscriptPath string  // 会话记录文件，用于失效检测
    PinnedPath     string  // tend pin 之后的硬链位置

    HerdrWorkspace, HerdrTab string

    SessionStartedAt *time.Time // 会话创建时间，从记录文件首条 timestamp 读；时间线按它排
    UpdatedAt        time.Time
    LastResumedAt    *time.Time
    ResumeCount      int

    Supersedes string  // 接续被 /clear 或 --fork-session 切断的同一件工作
    Deleted    bool    // 墓碑
}
```

### 读写语义

- 写入：追加一行；同 `ID` 后写的覆盖先写的；并发写用 flock 串行化。改一条记录一律走 `Store.Update`：持锁、文件被别的进程改过就先重载，再按 ID（没有 ID 按会话）找到最新那份、改、追加，新会话这时才分配 ID（写失败收回）；按 ID 找不到（别的进程删了）就报错（`ErrDeleted`），不把记录写回来。`Store.Edit` 是同一件事，只是改之前可以拒绝（节点的 `put` 用它判 `expect`），拒绝时什么都不写。`Put` 写之前文件已被别人改过时不更新「已同步」标记，下一次 `Update` 照样重载。
- 改什么一律是补丁 `tend.Patch`：每个非空字段是「设成这个值」，同一个补丁执行两次结果不变；TUI、CLI（`favorite` / `archive` / `status` / `done` / `edit`）、fzf 和节点的 `put` 都是 `Store.Update(r, func(r) { patch.Apply(r, now) })` 这一句。收藏、归档设为真且原来没有时记当前时间，原来有就保留；补丁带了 `favorited_at` / `archived_at` 时记这个时间（CLI 的 `favorite` / `archive` 每次都记当下，撤销放回原来的时间）。标题、摘要、短标题去掉首尾空白，标签过 `Normalize`。`Patch.Undo(改之前的记录)` 是放回这些字段原值的补丁，TUI 和 Web 的撤销都用它。收藏 / 归档 / 完成的切换只有一处：`Rec.ToggleFavorite` / `ToggleArchived` / `ToggleStatus` 按当前值算出补丁（再按一次「完成」回到 `doing`）。
- 状态文件（索引缓存、回收站清单、配置、全文库状态、词表、worktree 表）整份替换时一律 `fileio.WriteAtomic`：同目录唯一临时名 + rename，进程崩溃或并发写不会留半截（不 fsync）；目标是软链接时写到它指向的文件。索引缓存有读不了的行不算错误，余下的重扫，下一次保存整份重写。
- 读取：全量载入，按 `ID` 去重取最后一条，丢弃墓碑，按 `Rec.When()`（会话创建时间，缺失退到收藏时间）倒序。
- `tend doctor` 顺手给缺 `session_started_at` 的老记录补上（文件还在才补得到）。
- 单行解析失败只跳过该行并告警，不让整个收藏夹打不开。
- `tend doctor --compact` 原子重写文件，丢弃历史版本与墓碑。不自动 compact。

### 幂等

幂等 key 是 `(Provider, SessionID)`。Claude 与 Codex 正常 resume 都沿用原 session id
（`claude --fork-session` 的语义是"创建新 ID 而非复用原有的"，即默认复用），
所以重复 `/tend` 是更新而非新建：语义字段和环境元数据整体刷新，`favorited_at` 也刷成当前时间
（它的含义是「最近一次收藏」）；时间线按会话创建时间排，位置不变。`id`、恢复次数保留。

只有 `/clear` 和显式 `--fork-session` 会切断 id 连续性，这时用
`tend add --supersede <旧记录 id>` 让新会话继承旧记录的身份。依据见 [external-behaviour.md](external-behaviour.md)「Resume 命令」。
