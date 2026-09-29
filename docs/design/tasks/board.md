# 看板与视图的数据

看板的列怎么从任务的处境推导、允许哪些拖动、分组与筛选，以及卡片和任务详情需要的字段。实现：`internal/task`（`Situation`、`NeedsYou`）、`internal/server/web`（`fold.js`、`app.js` 的看板）、`internal/ui/tui`（`taskviews.go`）。

## 列

全部推导，不存。按任务的处境（`Situation`，见 [workflows.md](workflows.md)「处境与开始（实现）」）分五列，阶段写在卡片上，不单列「评审中」：

| 列 | 条件 |
|---|---|
| 等你 | 处境 `waiting`，卡片上写明原因：批计划、验收、权限、提问、等你回复、打回超限、verdict=blocked、合并冲突、超预算、run 失败、记账警告 |
| 运行中 | 处境 `running`：有未结束的 run |
| 排队 | 处境 `queued`：会自己往下走，卡片上写明在等什么 |
| 未开始 | `backlog` |
| 已结束 | `done`，`canceled` 也归这里 |

- 拖卡只允许等价于命令的四种移动：
  - 待办、没在跑、不走 workflow 的任务拖到「已结束」= 标完成；
  - 待办且没在跑的、或已结束的拖到「未开始」= 先不开始；
  - 未开始的拖到「排队 / 运行中」= `task.start`；
  - 已结束的拖回「等你」= 重开。

  放行、打回、取消不能拖。拖卡不能跳阶段：看板不是驱动执行的主入口，业内这类设计都没活下来。
- 分组：按项目、按需求、按机器、按 agent。筛选沿用 tend 的 `Query` 语法，加 `project:` `stage:` `role:` `needs:you`。

## 卡片与详情

- **卡片**：标题、编号、项目、阶段进度（`implement ✓ · review 1/2 · accept`）、agent@机器、run 状态点、attention 徽章、子任务进度环、打回次数、花费、分支和 PR。
- **详情**：
  - 任务书、验收标准、需求快照；
  - 子任务（按依赖分层）；
  - 阶段时间线：每个阶段的 run、verdict、findings、用量；
  - workpad；
  - 交付物：分支、diffstat、PR；
  - 当前 run 的实时输出、作答、插话。

两边界面上的实际样子见 [ui.md](ui.md)「首页综合看板」「页面清单」。
