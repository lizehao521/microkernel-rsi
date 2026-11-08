# 挂载实测：把内核包成一个 MCP 进程，然后真去调它

日期：2026-10-13 · 外壳：`cmd/mcpkernel/main.go`（10 个工具）· 驱动：`tasks/mcp/drive.py`
全文往来：`报告/挂载-MCP-往来.txt`（25 条请求，23 条判定，0 条红）

> **本文件写的是第一轮（单进程）。** 第二轮（三个进程、同一份起点）见 `报告/挂载-跨进程实测.md`，
> 它把这里的三处数字改了：判定 23 → **27** 条；`kernel.read_back` 多了一个**必填的 `session` 轴**
> （读侧曾把两个 session 的同号同侧并成 40 格）；`kernel.load_journal` 在非空账本上要 **`acknowledge=replace`**。
> 下面正文里的 23 条与旧签名是**当时的形状**，不改写 —— 改了就看不出第二轮改变了什么。

---

## 1. 为什么要挂

这个包最大的一条批评写在 `contract.md` 第 0 节：**本 module 之外没有任何调用方**。
自造输入证明的是接线，不是口径；而「等框架写好再来接」这件事在这台机器上根本等不到
（`runloop` 不在这台机器上，见 README 第 7 节）。

MCP 把「调用方」从"等一个 Go 框架"变成"任何会发换行分隔 JSON-RPC 的进程"。
挂上之后第一件事不是演示功能，是**让批评变成可测的命题**：外壳能拿到的只有导出符号，
它拿不到的（未导出字段、包内约定）就再也骗不了我们。

依赖方向没变：`package kernel` 至今没有 import `net/http`，也没有 import MCP 的任何东西。
`cmd/mcpkernel` 是壳，不是包的一部分。

---

## 2. 测出来的四件事

### 2.1 格位表可以现场要，不必抄文档

```
kernel.celltable →  当前 47 格（输入侧 20 + 输出侧 27）；唯一键名 46 个
```

这一条顺手修掉一个我自己在写壳时留下的假数：原先那句「唯一键名 = 两侧条数 − 1」是**推出来的**，
不是数出来的。改成遍历 `Cells()` 收集键名再 `len()`，并把三个分母一起报出来（格 / 每侧的格 / 唯一键）——
推出来的数迟早会漂，而漂了的数比没有数更有害。

### 2.2 拒收穿过外壳仍然是拒收，而且次数看得见

四次故意递坏形状，四次当场红（`isError=true`），链上始终 47 条、不多一条：

| 递进去的形状 | 内核说的话 |
|---|---|
| 声明读 `wire_bytes_outt`（拼错一个字母） | 声明了一个不在键名表里的键 —— 拼错的声明会让一格死格看起来是活的 |
| 非流式，却把分帧数记成**实测 0** | 非流式没有这一格，写数就是编数 |
| 报「预算挡下」，过网线字节却记成**没数到** | 预算闸在发之前就拦，那一格必然是数到的 0 |
| 输入侧第 1 发 / 输出侧第 9 发 | 两侧的发号对不上 —— 它们不是同一发（所以整发拒收，不写半发） |

```
kernel.tally → 账 47 条（absent=6 estimated=2 measured=37 model_says=1 relayed=1）；被拒的写入 4 次
```

`被拒的写入 4 次` 这一栏是这层唯一的意义所在：被拒如果不计数，下一次读的人会以为没发生过。

顺带修掉壳里的第二个假数：`kernel.tally` 原先自己写死了一张
`measured/relayed/estimated/model_says/absent/unset` 的键表去数分桶。
多一种出处档，这张表会**静悄悄漏掉它** —— 正是不变量三那一族。改成用 `Ledger.String()`，渲染只有一份。

### 2.3 「断开即失」的一半被调用方收回去

```
kernel.encode_journal {"payload":"omitted"} → 20149 字节 JSONL，47 行（写盘是你的事，内核只交字节）
kernel.load_journal   <那 20149 字节>       → 从 47 行里读回 47 条，链上现在 47 条；原像 0 条在手
kernel.verify                               → — 字段级自洽（共 47 条，其中 0 条比对过原像）；
                                                核不了：47 条没有原像（第 1、2、…（共 47 条））
kernel.record_call    <同一发再来一遍>       → 整发拒收：model_input 第 1 发（session=kresearch）已经记过了
```

第三行是这一层存在的理由：**没有原像 ≠ 内容被动过**，而且它也不打 `✓`（第三种答案不许长得像绿）。
第四行是 README 缺口第 8 条里「跨重启去重」那一半 —— 现在它由一个进程外的调用方走通了一次。

反向对照（证明第二行不是一律不报）：把第 4 条的 `claim` 改掉再载入 ——

```
kernel.load_journal <改过一条的行> → 载入被拒：第 4 条整格指纹重算不符（某格被改）want=1a4495981e77 got=d07f7469326e
kernel.verify                      → — 字段级自洽…（上一次失败的载入没有污染当前这本账）
```

### 2.4 读者声明第一次有活干

```
kernel.readers_report → 输入侧：键名表 20 格，其中 20 格没有任何声明者读
                        输出侧：键名表 27 格，其中 26 格没有任何声明者读
```

`wire_bytes_in` 那一格从「没人读」的名单里掉出去了。这一栏内核自己核不到（缺口第 1 条），
只能由调用方声明 —— 而声明今天第一次有了来源。

---

## 3. 挂这件事的代价（不许写成没代价）

1. **账活在这个进程的记忆里**，断开连接即失；持久化是调用方的事（拿 `encode_journal` 的字节自己写盘）。
2. **一次只处理一条请求**。`Ledger` 没有锁（缺口第 6 条），所以壳里没有 goroutine、没有并发会话。
   两个进程各挂一本账、各写各的，这一层完全不解决。
3. **工具入参没有 schema**。内核对「一发的元数据」没有独立的 wire 形状，导出的是 Go 结构体，
   所以 `input`/`output` 的键名就是导出字段名（`Call`、`WireBytesOut`、`UsageIn`…）。
   `tools/list` 里只写了字段清单并注明「把它写成 schema 会是假绿 —— 谁都没核过它」。
4. **没有鉴权、没有会话**。它假设调用方是本地进程；任何本机能起这个 exe 的进程都能写这本账。
5. **`PayloadUnset` 在壳口被换成了字符串校验**：`payload` 只认 `included` / `omitted`，
   传 `"maybe"` 当场拒。原先我写的是「不等于 omitted 就按 included」—— 那等于外壳替调用方决定带不带原像，
   正是内核在 `PayloadMode` 上拦的那件事。这是这次挂载**发现的一个真缺陷**，已改。

---

## 4. 这次没做、也没声称做了的

- 没改内核任何一行判据。这一句可核：本轮改过的文件只有
  `cmd/mcpkernel/main.go`、`tasks/mcp/drive.py`、`README.md`、`contract.md` 与本文件；
  `cells.go / input.go / output.go / record.go / measure.go / isolate.go` 的 mtime 还停在 09-29，
  `kernel.go / journal.go` 停在上一轮（落盘）的 10-12 22:3x。
  快照后端本轮没有新的点（`points.jsonl` 只有 2 条，最新是 2026-09-27 的 `mp-mujmq8gh-26f`），
  所以这里用的是 mtime 而不是逐字节比对 —— **这一点要说小：mtime 只证明「没被写过」，不证明内容对**。
- **慢闸（61 针）本轮没复跑**。针制的是判据文件，本轮没碰它们；如果动到判据，这一条必须补跑。
- 复跑的：`go test -v ./...` → 99 条 PASS、0 FAIL（`报告/全量测试-挂载轮.txt`）；
  `go run ./cmd/kcheck` → 退出码 0，末行「✓ 全部通过（项数由上面自己数，不抄在这里）」（`报告/快闸-挂载轮.txt`）；
  `tasks/audit/celltable_audit.py` → `status=OK keys=46 cells=47 rows=46 drift=0`。
- 挂进本会话的 MCP 面：**配置已写，但这一轮还没生效**。
  写到 `Desktop\微内核\.qoder\settings.local.json`（local 档：只对本项目、不进版本库），
  内容为 `agentkernel` → `Desktop\微内核\.bin\mcpkernel.exe`（stdio）。
  本会话当前的工具面上查不到 `mcp__agentkernel__*`，需要跑一次 `/mcp reload` 才会挂上。
  选 local 而不是 user/project 的理由：这是「试试效果」，不想把一台机器上的实验默认变成全局或共享配置。

---

## 5. 怎么自己复现

```bash
export PATH="$PATH:/c/Users/Admin/sdk/go/bin"
cd 微内核
/c/Users/Admin/sdk/py314/python.exe tasks/mcp/drive.py
# 期望：MCP DRIVE status=OK checks=23 red=0
# 控制台只打 ASCII（GBK 打不出 ✓，这是 README 第 1 节警告过的那个坑），全文在 报告/挂载-MCP-往来.txt
```
