# 当前架构模型 · 证据索引

范围：`agentkernel` 仓库现状（`package kernel` + `cmd/` + `tasks/`），日期 2026-10-13。
只画**已经存在的**东西；每条节点/边都给 sourceRef 与置信度。缺的边明确标 `absent`，不补成想象。

## 节点

| id | 是什么 | sourceRef | 置信 |
|---|---|---|---|
| `kernel.measure` | 出处三态的载体：`Basis` 六档，零值是 `Unset` | `kernel.go:1-13`（四条不变量第 1 条）、`measure.go` | 确认 |
| `kernel.input` | 输入侧形状：`PartKind` 八类、`MayFold`/`MayShrink`、`Cut` 三档、预算夹紧 | `input.go`、`contract.md` §2.4 值域表 | 确认 |
| `kernel.output` | 输出侧形状：`Failure`、`FailKind` 七档、`RetrySame()`、`Check()` 互核 | `output.go`、`contract.md:127` | 确认 |
| `kernel.cells` | 47 格键名表 + 每格出处在此定死 | `cells.go`；格位表实测由驱动调 `kernel.celltable` 读回（头部写"当前 47 格…唯一键名 46 个"） | 确认 |
| `kernel.record` | 整发装配的门：`CellsOf`/`AbsentKeys`/去重/自相矛盾 ⇒ 整发拒收 | `record.go`、`kernel.go` 不变量 3、4 | 确认 |
| `kernel.journal` | 哈希链账本 + 三态 `CheckChain` + `Marshal/ReadJournal`；**无锁 ⇒ 单请求串行** | `journal.go`、README §2.2 | 确认 |
| `kernel.isolate` | 用 `go/ast` 读源码自证依赖方向（不 `net/http`、不写盘） | `isolate.go:141 CheckIsolation`、`isolate.go:372 SelfIsolation`；`grep -n "net/http" *.go` 无命中 | 确认 |
| `shell.mcpkernel` | MCP stdio 外壳，10 个工具全转发到内核 | `cmd/mcpkernel/main.go:104-120`（注册表）、`:157-271`（分发） | 确认 |
| `shell.kcheck` | Go 侧自检二进制，退出码为准 | `cmd/kcheck/main.go`（§6.5 读侧三轴那条是本 session 加的） | 确认 |
| `shell.kresearch` | Deep Research 端点调用方（内核第一个真调用方） | `cmd/kresearch/main.go`、README §2.1 | 确认 |
| `drv.probes` | 真跑驱动三个：`llm_probe` / `qa_loop` / `tools_probe` | `tasks/mcp/*.py`；端点与模型名 `tools_probe.py:26-28` | 确认 |
| `drv.offline` | 离线核对：`replay.py` 一次端点都不打 | `tasks/mcp/replay.py`、README §2.4 末段 | 确认 |
| `drv.crossproc` | 跨进程外壳自测：`drive.py` / `fork.py` | `tasks/mcp/drive.py`、`fork.py` | 确认 |
| `gate.slow` | 慢闸 64 针（`go test` 60 + `kcheck` 4），锚点是精确源码文本 | `tasks/probe/kprobe.py`、`报告/慢闸64针-补针后整轮.txt` | 确认 |
| `gate.audit` | 格位表审计 `celltable_audit.py` | `tasks/audit/celltable_audit.py` | 确认 |
| `ext.llm` | OpenAI 兼容对话端点 `agnes-3.0-flash` | `tools_probe.py:26-28`（`AGNES_BASE`/`AGNES_MODEL` 可覆盖） | 确认 |
| `ext.fake` | 本机假端点（定死回答，不花钱；`max_tokens==5` 出 200+断 body） | `tasks/probe/fakellm.py:17-31`、`报告/协议档一发-200坏body.md` §0 | 确认 |
| `store.ledger` | 落盘 JSONL 账本 | 由**驱动**写（`encode_journal` 交出字节后自己落盘）；内核不写盘 | 确认 |

## 边

| from → to | 语义 | sourceRef | 置信 |
|---|---|---|---|
| `drv.probes` → `ext.llm` | 一发真调用 | `tools_probe.py:74-79` | 确认（三轮 13 发真数据在册） |
| `drv.probes` → `ext.fake` | 自检管线时打本地假端点 | README §2.4、本轮 `PROTO_TURN` 跑 | 确认 |
| `drv.probes` → `shell.mcpkernel` | `check_call` / `record_call` / `encode_journal` / `load_journal` | `client.py:17 Kernel`、`tools_probe.py:355` 附近 | 确认 |
| `shell.mcpkernel` → `kernel.record` | 10 个工具全部转发 | `main.go:157-271` 的 case 分发 | 确认 |
| `kernel.record` → `kernel.journal` | 通过的整发落进哈希链 | `record.go` + `journal.go` | 确认 |
| `kernel.record` ⇸ | **拒收不留半条**（两侧都不写，拒收另计一格） | `kernel.go` 不变量 3；实测两次：`报告/工具提议-实测.md`、`报告/协议档一发-200坏body.md` §2 | 确认 |
| `shell.mcpkernel` → `drv.probes` | 读侧：`tally` / `verify` / `read_back` | `tools_probe.py:460,463,472`；`qa_loop.py:270,289` | 确认 |
| `drv.probes` → `store.ledger` | 驱动把 `encode_journal` 的字节写盘 | `tools_probe.py` `TOOLS_BOOK` | 确认 |
| `drv.offline` → `store.ledger` → `shell.mcpkernel` | 新进程 `load_journal` 读回整本账再 `verify` | `replay.py`、README §2.4 | 确认 |
| **`kernel.record` ⇢ `drv.probes`（下一发的装配）** | **读侧结论改变下一步发什么** | **无：见下面 absent 清单** | **absent（确认地没有）** |

## 确认地"没有"的东西（current-state 事实，不是建议）

1. **读侧 → 下一发装配这条回边不存在。** 两个真跑驱动里，`read_back`/`tally`/`verify` 全部落在
   **回合循环之后**的判定段：`tools_probe.py` 循环在 `:356`，读侧最早在 `:460`；
   `qa_loop.py` 循环在 `:263`，读侧最早在 `:270`。循环体里"下一发怎么装配"用的全是
   本地 `if`（如 `:239` 的 `if t["http_error"]`、`:262` 的 `elif bad:`），一次都没问过内核。
   所以内核的"矫正"今天只发生在**写侧门口**（拒收装配错误的人 = 我），不发生在模型的下一步上。
2. **没有 runloop。** `tasks/` 下没有任何循环体把账本当输入。
3. **`declare_reader` 有工具没常规使用者。** 注册在 `main.go:113`，实际调用只在 `drive.py`（自测），
   真跑驱动里 0 次 —— "落了账但没人读"这一半内核自己核不到，声明机制建好了没接上。
4. **`Ledger` 无锁**：单请求串行是 README §2.2 写明的现状，不是并发实现。
5. **`http` 那一档在册账里 0 发真形状**（本轮清点 27 发：`malformed_action`×2、`config`、`protocol`、`transport` 各 1，其余 22 发无故障）。早轮假端点撞出过 429 但那本账没归档进册。
6. **没有跨发的"历史 ↔ 原话"比对 —— 但真实故障实例数为 0。** 实测（`报告/走私测试-历史与账不比对.md`）：
   把第 2 发模型原话 `和是 42。` 改成 `和是 43。` 发进第 3 发历史，内核**接受整发**、链自洽、被拒 0，
   两本 235 条的账只有 1 格不同（`first_byte_ms` 1→2 毫秒噪声）—— 改动在 47 格里隐形。
   **必须连着读的那半句：那句话是我手动改的（`SMUGGLE=1` 旋钮），本仓库从没自然发生过这件事。**
   所以这条是"能力缺口演示"，不是观测到的故障，**不构成改造依据**。
   真会发生的那一族只有一条非人造路径：预算挤不下时对 `history` 段做 `tail-dropped`
   （`input.go` 的 `MayShrink` 明确允许），挤完发出去的就不是指纹那句 —— 这条**本轮没跑过**，仍是假设。

## 待定 / 低置信

| 项 | 状态 |
|---|---|
| `fail_kind`↔`status` 那条互核该怎么改（两根轴 vs 加两档） | 等口径；两个独立实例的证据在 `报告/待定-两个状态与格式那一格.md` §0 |
| `wire_format` 走 `relayed` 还是两格 | 等口径；实测对照在 §3.2（`chat.completion` vs `chat.completion.chunk`） |
| 缺口 11（工具提议的名字与参数无处可放）、缺口 12（审计件≠恢复件） | 已定：缓到接 runloop 一起 |
| 响应头那三条自报值（request-id / 网关版本 / 对端成本） | 46 键里无处可放 **且采集点不存在**（`getheaders()` 从未被真跑驱动读过） |
