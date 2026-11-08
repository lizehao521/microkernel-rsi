# 内核 Go 树（`微内核/微内核`）· 局部指南

只写这一面与根指南不同的地方。口径、工具链、备份规矩见 `../../AGENTS.md`。

## 这块是什么

被动的记账件：可生产的调用件 + 判据 + 状态与契约。**运行时不调度、不联网、不执行工具、不写盘**——
`encode_journal` 只把整本账编成 JSONL 字节交回调用方，落盘天生在宿主侧。这是边界不是缺陷，别说成「挡住了」。

`cmd/` 下七个面各有 owner：`kcheck`（快闸）、`agentd`（Go 宿主，循环住在这里）、
`mcpkernel` / `mcphttp` / `kwire`（传送）、`kaudit`（只读审计）、`kresearch`。

## 不许动的

- 判据表：48 键 / 49 格，`README.md:631` §8 已封板停止新增。想加就撞「改判据表要三件配齐」。
- `contract.md` 是 wire 形状的单一 owner（§2.5 才有 Basis 整数枚举、PartCount 行字段）；schema 看不出来。
- 同一时间有别的会话在跑测评，动文件前先确认没被并行改过。

## 这一面的闸

工作目录 `微内核/微内核`：

```bash
C:/Users/Admin/sdk/go/bin/go.exe run ./cmd/kcheck
C:/Users/Admin/sdk/go/bin/go.exe test ./cmd/agentd/ -count=1
# 事件层八条针 + 臂 + 覆盖差集，一条命令（工作目录在事件层那面，退出码 0 才算过）
cd ../../事件层-1022 && C:/Users/Admin/sdk/py314/python.exe 验收-事件层-1022.py C:/Users/Admin/sdk/go/bin/go.exe "C:/Users/Admin/Desktop/微内核/微内核/微内核"
```

`kcheck` 会往临时拷贝逐根制造破坏，要求点名的判据接住；锚点命中数不是 1 处算空针。针的根数由它自印，别抄进文档。
`验收-事件层-1022.py` 的**道数与针名从 `../../研究-协同工作-1022.md` §8.2 现读**，臂数从 `读数/臂检-全部族.jsonl` 现数；
它还会点出「事件层文件里没被任何一条针覆盖的 Test」——那行不等于 0 时，八行全绿也不许说成收口。

## `cmd/agentd` 特有（改宿主循环前必须知道）

- 六态退出码：0 剧本 / 2 这一路跑不了 / 3 轮次额度到 / 4 账上 `fail_kind` 指向停 / 5 这本账到上限 / 6 外部打断（`main.go:15-21`）。
- SIGINT **先交账再走**（`main.go:82-90`）；`-read-back=false` 是核心 KPI 的反臂，关掉后账本触发次数必须掉回 0。
- `-resume` 先核链，链不符就不往下写（拒写 rc=2）。跨进程发号按增量对，别拿绝对值——绝对值数的可能是整个进程那本账。
- 轨迹解剖页是 **`closeOut` 时才写**（`trace.go` 的 `writeTrace`）：它是事后审计件，运行中回答不了「现在怎样」。
  「过程实时可见」那一半已经落在宿主的事件层（E1–E9，见 `../../事件层-1022/`）：`-events` 落 append-only 日志、
  `--events-serve` 起 SSE（`/events` 播帧、`/` 出那张三行页），`-progress-tick` 到点补一条进度或解释。
  **页上的发数与逐发条数只从事件日志读**（`traceLedger`），数不到就写「没量过」，不许退回内存里的 `r.shot` 顶数。

## 读数与证据

判据根数、项数由闸件自己印，这一面没有可手抄的基线；历史读数与轮次在 `报告/`，「最新」按 mtime 认。
`报告/面板方案-待批.txt` 里那三个决定没圈之前，面板那块不许开工。

另有一件 Node 侧的三件套内核（**不在本目录**，在 `C:/Users/Admin/Desktop/Agent与模型/Agent项目/Agent项目/工程熵治理/agentkernel/`），
入口 `node cli.js verify` / `node cli.js redtest`，产物 `out/verify_report.json`、`out/mutation_report.json`；
读那份 JSON 时注意 `results` 是按判据 id 键的对象，不是数组。别把两件的读数混成一本。
