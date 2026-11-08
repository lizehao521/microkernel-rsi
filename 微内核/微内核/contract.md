# agentkernel 对外契约（待接件 · v0）

**这份文档是给「下一个要接它的人」看的，不是给读源码的人看的。**
目的只有一个：让接的时候**不必重读 2400 行源码去猜口径**。

| 项 | 值 |
|---|---|
| 状态 | **待接件**：本 module **之外**没有任何调用方。范围要说死：仓库内 `import agentkernel` 命中 **3 处**，都在 `cmd/`（`kcheck` 闸门、`kresearch` 真端点样例、`mcpkernel` stdio 外壳），它们是**内核自己的工具**，不是接入方 —— 拿它们当"已有人接"是假绿。`mcpkernel` 只把「谁来调」这件事从"等框架写好"变成"任何会发 JSON-RPC 的进程"，壳仍然在本 module 里 |
| 包的版本 | `module agentkernel` / `go 1.27`（`go.mod`） |
| 格位表来源 | 键名与字段路径由 `Cells()` **机械导出**，不是手抄。可执行的那一步是 `tasks/audit/celltable_audit.py`（见第 6 节）—— 它把这张表与 `kcheck` 第 5 节的实读逐格对。早先这里指向的生成器 `celltable.go` 不在仓库内，那句话当时不可执行 |
| 本文件写入前的回滚点 | `git-snapshot` 点 `mp-mujmoqkh-25f`（manifest 后端，覆盖 25 个文件） |
| 一句话 | 内核只管**账**：每格带「谁算的」、缺席要说得出理由、缺依据进不了门；它不调度、不联网、不写盘 |

---

## 0. 一发

一次模型调用 = 一发，发号 `Call` 从 1 起。两侧账：

- `InputMeta` —— 请求**装配完之后**：我们发了什么
- `OutputMeta` —— 响应**回来之后**：我们收到了什么

**记账单位是「格」，不是「键名」**：`Cells()` 交出 **49 格**，其中唯一键名 **48 个**——
`call` 在两侧各有一格（同一个量、两条记录）。任何统计必须说清自己数的是格还是键；
把两者混着说，就是这个包最恨的「一个名字住两个量」。

---

## 1. 四条不变量

1. **每格带出处**：`Basis` 的零值是 `unset`（=0），门口直接拒。不存在「默认按实测算」。
2. **缺席 ≠ 0 ≠ 忘了填**：三态分离。缺席必须带理由，且**缺席不可拿去做判定**。
3. **一格一量**：撞名判据靠**字段路径**——两侧都有 `call`（同为 `Call`）不算撞；
   两个字节量共用一个键名（字段路径不同）当场撞出来。`bytes_out` 这个名字被**单独点名禁掉**。
4. **一个定义只有一个 owner，且 owner 要被复核**：例如预算公式的唯一出口是 `DeriveBudget`，
   `InputMeta.Check()` 会把公式**重算一遍**再比对；手搭一份 `ContentCeiling=999999` 的预算进不了门。

---

## 2. 序列化契约（**接线的人必须按这一节对表**）

### 2.1 Basis —— 落盘是**整数**，渲染是字符串

`Basis` **没有自定义 JSON 序列化**（全包无 `MarshalJSON`/`UnmarshalJSON`），所以默认按 `int` 落：

| 常量 | 落盘 | `String()` / `Claim` 里的字 |
|---|---|---|
| `BasisUnset` | 0 | `unset` |
| `BasisMeasured` | 1 | `measured` |
| `BasisRelayed` | 2 | `relayed` |
| `BasisEstimated` | 3 | `estimated` |
| `BasisModelSays` | 4 | `model_says` |
| `BasisAbsent` | 5 | `absent` |

> **口径陷阱（2026-10-19 改准过一次，照原样记下）**：缺席有**两条**进账本的路，出处那一栏长得不一样。
>
> 1. **三态量**（`Measure{Absent:true}`）：`Cells()` 交出来的格子上 `Basis` 是 **0**
>    （`measureCell` 在 `m.Absent` 时把出处置零），由 `basisOf()` 在**落账时**翻译成 `BasisAbsent`(5)。
>    所以读 `Cells()` 的人**不能把 0 读成「忘了填」**——0 在这一层是「要么没填、要么这一格缺席」，
>    只有账本上那一条（`basis=5`）才是「这一发结构上没有这个数」。两处若对不上，`basisOf` 会拒，且点名是哪一格。
> 2. **一张 nil 名单**（`Cuts` 没交这个键）：`Cells()` **直接交 5**（`cutCell`），`basisOf` 不再翻译，
>    只给它那一句「没交这一档的名单，不是空表」。
>
> 10-19 之前这里写的是「`Cells()` 交出来的格子上 `Basis` 永远不是 5」。**那句话现在错**，
> 而错的方向是危险的：谁照它写过「见到 5 就是账本自己翻的，键名表里不会有」，
> 就会把 `folded`/`clipped`/`dropped` 三档漏掉。

### 2.2 账本条目的命名与指纹

| 项 | 值 |
|---|---|
| `KindModelInput` / `KindModelOutput` | `"model_input"` / `"model_output"` |
| 账本条目名 | `<kind>:<key>`，例如 `model_input:content_bytes`（逐格一条） |
| `Claim` | 人话版本，短；**永不为空**（空值落成常量 `emptyClaim`，另有「空表说自己的名字」的零话） |
| `Payload` | `json.Marshal(值)` 的字节；**全**，给第三方重算 |
| `PayloadH` | `Payload` 的 SHA-256 十六进制（64 位） |
| `Hash`（seal） | sha256(`Seq\nSession\nActor\nStep\nKind\nClaim\nBasis.String()\nSource\nAt(RFC3339Nano,UTC)\nPayloadH\nPrevHash`)，两段之间用 `\n` |
| `Entry.Payload` | tag 是 `json:"-"` ⇒ **不随 Entry 序列化**。落盘方要自己决定带不带原像；不带就只剩指纹，第三方**重算不出原值** |

### 2.3 字节格式（JSONL）—— 内核拥有格式，框架仍然拥有磁盘

`journal.go` 交出去的是 `[]byte`，**内核一行都不写磁盘**（那份文件连 `os` 都没 import）。
接线的人要决定的是下面这一格，而不是"要不要自己写一遍 Marshal"：

| 入口 | 作用 | 必须知道的口径 |
|---|---|---|
| `MarshalJournal(docs, mode)` | 编成 JSONL，一行一条 | `mode` 的零值 `PayloadUnset` **被门口拒**；不许靠默认值过关 |
| `ReadJournal(b)` | 解回 `Journal`（不验链） | 解不开的行进 `BadLines` 并**带行号**；`Lines` 与 `Parsed` 是两个分母 |
| `CheckChain(docs)` | 三态体检：过 / 没过 / 核不了 | `Ledger.Verify()` 就是委派到这一份，判据只有这一个 owner |
| `LoadLedger(b)` | 读成一本**能接着记**的账本 | 链不符拒绝载入；没有原像**不是**拒收的理由 |

读侧那一半（`CellsOf` / `Keys` / `AbsentKeys`）按 **`session` + 发号 + 侧** 三个轴认「同一发」，
与写侧 `alreadyRecorded` 是同一个定义。少带 `session` 不是「读不到」而是**读错**：
两个 session 各记一次第 1 发，只按发号与侧过滤会回 40 格（这一条是 MCP 挂载实测撞出来的，
现在由 `TestCellsOfKeepsTwoSessionsApart` 与慢闸针 `j10` 钉着）。

`payload_b64` 那一格的三态（**别把它读成两态**）：

| 字节流里的样子 | 读回来 | 含义 |
|---|---|---|
| `"payload_b64":"ag1=="` | `WithPayload++` | 带原像，第三方能重算 `payload_sha256` |
| `"payload_b64":""` | `WithPayload++` | 带了一个**空原像**（absent 那一档在内存里就是 nil，它的指纹就是对空算的） |
| `"payload_b64":null`（或这个字段整个省略） | `NoPayload++`、`payloadAbsent=true` | 没有原像：**核不了**，不是篡改 —— 今天编码器写的就是 `null` |

第三行是本包最容易被读错的一格。`Verify()` 在那种情况下报的话里有「没有原像」而**绝不**有「被动过」；
`ChainReport.String()` 开头打 `—` 不打 `✓`。把这两句话合并的说法（"指纹对不上就是内容被动过"）
会把 47 条完好无损的账全指控成篡改，于是下一次真篡改淹在假警报里。

**还有一句必须写在脸上的：这本账是审计件，不是恢复件。**
`payload_b64` 装的是**每一格自己的值**的原像（用来让第三方重算那一格的指纹），
不是模型说过的话。输出侧装着「模型说了什么」的格只有四格 ——
`model_says_bytes`、`model_says_kept`、`model_says_clipped`、`model_says_sha256`，
全是数与指纹，**没有一格存原文**；输入侧的题面、`observation`、system 说明书同样只有字节数与段名。
所以换新进程只带这本账，可以做到的事是：验链、读回每一格的数、
**拿别处存着的原文逐发重算指纹核对**（`tasks/mcp/replay.py` 真跑了这一条：5 发里可比 3 发、3 发全对上）；
做不到的事是：把下一发的 `messages` 重建出来。
账上确实有两处自由文本 —— `fail_detail` 与 `notice_text` —— 但那两处住的是**我们自己写的句子**，
不是模型的输出（这一条别读成「原文有地方放，是我没填」）。

**`PayloadOmitted` 的代价要写在脸上**：链仍然自洽（seal 盖的是 `PayloadH` 那 11 格），
但"第三方能按同一规则重算原值"这一半从此核不了 —— `Journal.String()` 会把这件事直接说出来，
别把它折进"读回 47 条"里（那一轮还是一发 47 格；现在是 49）。



#### 2.3b 闸门计数那一行（10-18 新加，**非链上**，接线的人必须认它）

`MarshalJournalWithGate(docs, mode, gate)` 在链行之后多写**一行**（v2 盖法）：

```json
{"gate":{"rejected":2,"precheck_rejected":10,"precheck_door":3,"door_refused":1,"bad_requests":4,"transport_refused":5,"state":1},
 "chain_head":"…","gate_sha256":"…","gate_version":2}
```

| 口径 | 为什么这样定 |
|---|---|
| 这一行**不是**一条账 | 它不进 `Docs`、不参与 `seal`、不改任何 `seq` ⇒ `verify` 那句「共 N 条」里的 N 只数链行。把拒收塞成 Entry 会让 `RecordCall` 的"写入被拒"语义变成两本账混数 |
| 六笔数**单位不同，永不相加** | 拒落账＝发；拒预检＝发；预检门口＝**一次预检请求**；落账门口＝一次写请求；协议层/传输层＝一条请求。合起来就答不出「谁拒的」（#42/#44/#46 三次踩的都是这个） |
| `state` 是这六个数的**口径**，并且**被盖进封条** | `1=这一程完整可数`、`2=只覆盖这一程（下界）`；`0=不可知`**不许落盘**（正确形状是没有这一行）。少了这一位，一本下界账导出再读回就被洗成全量 —— 10-18 深夜 M2 两路互审各独立撞到同一处，v1 盖法没有它，所以 `gate_version` 升到 2 |
| 封条防得住什么、**防不住**什么 | 盖的是「六个数 + 口径 + 当时链尾」。防得住：无意损坏、事后抵赖、把这行接到另一本账上（链尾对不上）。防不住：**铁了心伪造的人** —— 无密钥纯函数，他可以把数、口径、链尾一起改了再自己重算一遍这枚封条。所以这一栏是**可复核的声明，不是证据**；要证据看链上那 49 格 |
| **没有这一行 ⇒ 不可知，不是 0** | 老账本（10-18 之前编的）与别人手写的文件都没这一行。`Ledger.GateStatus()` 回"不可知"；本程又数到了东西则升成"只覆盖这一程"（下界），而**这个下界跟着落盘**，不再被往返洗掉 |
| 读侧硬规矩 | ① 别把它当链行扫（`ReadJournal` 认顶层 `gate` 键；解不开的计数行计进 `BadGateLines` + `BadLines`，**不**双计进 `GateLines`）；② 出现两行、排在链行之前、`gate_version` 不对、数为负、口径不是 1/2、封条不符 —— **全部拒载**，不许"取最后一行"；③ 印数之前先印口径 |

外壳侧两条与这一行相关的行为也写在脸上，免得各读各的：
`encode_journal` 在口径为**不可知**（从没有计数栏的账读回来、本程又一笔没数到）时**不写这一行**，
回话里说明「不替它写一行 0」；`load_journal` 那道闸只在**换了就会丢**时才要求 `acknowledge=replace`
（链上有条，或六笔里任一非零而手里这份文件没有计数栏）—— 文件自己带着计数栏时那六笔会跟着回来，
那种情况拦它就是堵死「把自己的账读回自己」这条正常路（外壳侧互审跑出的最小反例）。
`kernel.tally` 的回话从四笔扩到六笔 + 一句口径；`kaudit /v1/tally` 在没有这一行时把
`rejected_writes` **和整个 `gate` 对象**一起印成 JSON null（不是六个 0），另给 `gate_state` / `gate_line`。
`tally` 尾那句口径是**六笔合起来**的那一份（不是内核自己那两格的来历）：只有门口拒收的进程，
内核那两格仍是 0/不可知，而壳那几笔是本程实数到的 —— 那种场合印"不可知"就是假话，正确读数是"只覆盖这一程"。

**账本容量（外壳旋钮，不是内核判据）**：`mcphttp -max-entries N` 设这本账最多写多少条，
**默认 0 = 不拦**（那个数归部署方定，内核不擅自替你定；10-18 实测"60 发合法记账 = 105ms、账涨到 2940 条"，
没有上限就是现成的内存型 DoS）。到顶之后：`record_call` 一条都不写，`load_journal` 也不许把比上限还长的文件换进来
（载入是替换，换进来压不回去）。这类拒绝进的是**门口那一桶**（同一 owner、同一单位），
并在 `tally` 里单独披露子集「其中容量挡下 M 次，上限=没设 / N 条」—— 子集不是第七笔数，不相加。
`0` 的两种读法（"不拦"与"一条都不许写"）由唯一渲染处 `mcpsrv.CapWords()` 说清，横幅与 `tally` 都从它出。
**已知不对称**：stdio 面（`cmd/mcpkernel`）没有任何旗标，所以容量这道闸目前只在 HTTP 面可设。

**三颗部署开关（默认全关 = 现状一字不变）**

| 开关 | 作用 | 谁认得它 |
|---|---|---|
| `-max-entries N` | 账本条数上限；到顶 `record_call` 一条都不写、`load_journal` 不许换进更长的文件；进门口那一桶并披露子集 | 两面（HTTP 旗标 / stdio 旗标） |
| `AGENTKERNEL_APPEND_ONLY=1` 或 `-append-only` | 服务面**不提供换账本这条路**：`load_journal` 一律拒（要归档走 `encode_journal`，要读旧账另起进程） | 两面 |
| `AGENTKERNEL_REQUIRE_TOKEN=1` | 把"回环免口令"这个信任假设关掉：不带口令的请求连内核的门都到不了（401）；若同时 `AGENTKERNEL_TOKEN` 为空 ⇒ **503**，不"看着有闸其实没闸" | 只有 HTTP 面（stdio 进程由宿主自己 fork，能连上它的就是那个宿主） |

判定都收在 `mcpsrv` 一处（`SetEntryCap` / `SetAppendOnly` / `CapWords`），两个面各自只负责把旗标读进来 ——
同一件事两面各判一遍迟早漂开（本包规矩三）。横幅与判定同源：`requireToken()` 只有一个去处。

### 2.4 值域（枚举）


| 类型 | 取值 | 备注 |
|---|---|---|
| `PartKind` | `system` `skill` `memory` `history` `toolspec` `observation` `user` `truncation-notice` | `MayFold` = observation/history；`MayShrink` = observation/history（**「整段没进」只许用在可分片的类段上**） |
| `Role` | `system` `user` `assistant` `tool` | 裁剪记录必须带 |
| `Squeeze` | `""`(没被挤) `folded` `tail-dropped` `dropped-whole` `budget-exhausted` | 账上的三档键名是 `folded`/`clipped`/`dropped`；`clipped` 对应 `tail-dropped` |
| `FailKind` | `""`(正常) `transport` `config` `http` `protocol` `malformed_action` `budget` | **两根分开的轴**：`MayCarryStatus()` = http/config/protocol/malformed_action（收到过对端响应才有码可记），`RetrySame()` = transport/http。以前用「是不是 http」一根轴冒充两根，被一发真 401 与一发 200 坏 body 各撞开一次 |
| `SentState` | `没数到` / `数到了，是 0` / `数到了，非 0` | `sent` 那一格就是这个三态的字符串 |
| `SHA256` 字段 | 必须 **64 位十六进制**，唯一的判据函数是 `checkFingerprint` | **裁剪记录（含「整段没进」）也必须有指纹**——空串会被拒 |
| `MaxFreeText` | 一格自由文本 ≤ **2000 字符**（按字符数，不是字节） | 10-18 深夜新增，钉在 `Notice.Why` / `Notice.Text` / `Failure.Detail` / `Source` / `Claim` 上；超限**整发拒收**、拒因点名是哪一格。理由见 `报告/攻击面实测-能否对Agent下毒-1018深夜.md`：请求体本来有 4 MiB 上限而**单格没有**，一段 `SYSTEM OVERRIDE` 落账后会被 `read_back` **原样**交给下一个读账的 Agent，还盖着 `measured`。**这条判据只管长度不管语义**（很短的一句指令照样收）；读侧另有一句 `⚠指令形状` 提示，它**不是判据、不改存储字节**，也不算防线 —— 防线只有一条：读账的人不许把 `claim` 当指令执行 |

---

### 2.5 写侧 wire 形状（**Agent 最少要接对的那一份**，机器从代码导出）

**这一节是 2026-10-19 补的，补的是一个当场付了价的洞。** 同一天放两个陌生 Agent 进来接闸门，
一个**给了这份 contract**、一个**不给**：给文档的那一个被门口拒了两次（`Budget.Shares` 填成数组、
`Cuts` 填成 `{folded,clipped,dropped}` 一个对象），不给的那一次过。拒它那两条，第 3 节那一列写的是
**读侧格名**（`shares`、`Cuts#folded`），第 5 节写的是三处形状踩坑，**全文没写出任何一个字段的 JSON 类型**；
而 `tools/list` 交出去的 `inputSchema` 两侧都只到 `{"type":"object"}` 加一句散文
—— 那是刻意的（`mcpsrv.go` 里 `sideProps()` 上面那段说清了理由：一份谁都没核过的 schema 是假绿）。
机器可读的接口真相只住在 Go struct 里，所以这一节从 struct 反射导出，不手抄。

**重跑与核对**（在本包目录下跑，`contract.md`/`README.md` 按相对路径读）：

```
py -3 -c "..."                                # 见 §6 那条：先确认 go 在哪
go run ./cmd/kwire -emit                      # 把下面三张表从代码里重新导一遍（stdout）
go run ./cmd/kwire                            # 对账，三态退出码与 §6 同一条纪律
go run ./cmd/kwire -out 报告/接口对账-最新.txt # 逐写点完整读数（UTF-8）
```

它量的是「**最少接口**」，不是全部能力：`record_call` / `check_call` 那一份参数里，调用方
**必须写对的每一个键**、它的 JSON 类型、它是不是门口整块必填、以及**少写它会发生什么**。
最后一列一个人也不许写。它的来路是：先造三份处处自洽、**真落进了账、链也验过**的夹具
（正常发 / 故障发 / 参数被拒发），每处删掉一个键，原样喂给门口那道闸、那份严格解码、那一条 `RecordCall`，
看它撞到哪儿。夹具自己过不了闸，这把尺直接报「核不了」—— 它不自证就不许报「一致」。

| 「少写这一处会怎样」 | 意思 |
|---|---|
| `门口拒` | 整块没给，`refuseMissingBlocks` 当场拒，两侧一条都不写 |
| `落账拒` | 解得开，但内核判据不让过 —— 「没说」被认出来了 |
| `落账但记缺席` | 解得开、也落了账，而那一格在账上说的是「没交」而不是一个数。**这是这一列里唯一合格的一档** |
| `静默落账` | **解得开、也落得了账，而账上有格子因此署上了 `measured`**。缺席冒充那一族的读数，逐格列在下面的空洞表里 |
| `落账不变` | 这一格在三份夹具里本来就是零值，删与不删是同一份参数 —— 这一条对照测不出事 |
| `正常发=… 故障发=… 参数被拒发=…` | 三份夹具各撞到的不一样，就分别点名，不合并成一句好听的 |

**读数（2026-10-19，两次，都是终端自己印的）**：侧根键 27、单位成员 39、实跑 378 次、
其中 143 次是空读（叶子格本来就是零值，**不计入空洞**）。首跑空洞 **11 处**；
把 `Cuts` 那一处按缺席改掉之后（`cutCell` / `basisOf`，见 §4 第四条）**10 处**。
侧根上的键若在夹具里全是零值，这把尺会直接报「核不了」而不是放过 —— 所以这个数是**下限**，不是全集：
夹具没覆盖到的写法不在这里说话。

**首跑量到什么、现在还在不在**：27 个侧根键里，`input` 的 `Cuts` 是**名单型**那唯一一处「整块没给却能静静落账、
还把 `folded` 那一格署成 `measured`」的 —— **已修**：没交这个键 ⇒ 三档都落成 `absent`，
原像是 `null`；交了一张空表 ⇒ 才是 `measured` 加那句「0 段（裁剪表是空的）」。
侧根上**还剩 1 处**，但它不是名单：`output ToolCalls` 的类型是 `int`，删掉这个键与填 `0` 在 Go 的解码里
是同一个值，这把尺量得出它少写了、内核分不出。**这一处不在本次改动里**，能分开它的改法是把字段改成指针，
而「字段类型变了就是接口变了」（§5 第 0 步）是另一轮、另一件事 —— 不是顺手能并进来的。
除这 1 处之外的侧根键，要么被门口拒、要么被内核判据拒。

剩下 **10 处 = 侧根 1 处（`ToolCalls`，标量）＋ 块里的叶子 9 处**（`Notice.Trimmed`、`Notice.Why`、
五处 `Measure.N`、`Says.Kept`、`Failure.Downgrade`）：整块给了、块里某一格没说 ——
门只管「这一整块在不在」，不管「块里哪一格没填」（`mcpsrv.go` 那段覆盖范围自己写明了这条边界）。
这个 1/9 的分档由尺子自己印（`报告/接口对账-空洞分档-1019.txt` 第 3 行 `删一处仍能落账的 10 个（侧根 1 ＋ 块里叶子 9）`；
判据是写点里带不带 `.`）—— 10-19 我先按手数把 `ToolCalls` 归进了块里，那句是错的，所以把它做成印出来的数。
`WireBytesOut.N` 那一处最贵：删掉它，账上连着把 `sent` 署成「没发出去（数到了，是 0 字节）」、
把 `cost_countable` 署成不算数 —— 一笔可能已经花掉的钱，在账上变成了没花。
**这一族没跟着 `Cuts` 一起改**：`Measure` 的 `N` 与 `Basis` 是两处，删掉 `N` 时 `Basis` 还在，
要修得先定「谁有权改署一个已带出处的数」那一条，不在本次改动里。

<!-- kwire:params -->
| 侧 | 调用方要写的键 | JSON 类型 | 整块必填 | 少写这一处会怎样 |
|---|---|---|---|---|
| input | Call | number | 否 | 落账拒 |
| input | Order | array<string(PartKind)> | 否 | 落账拒 |
| input | Parts | array<unit PartCount> | 否 | 落账拒 |
| input | ContentBytes | number | 否 | 落账拒 |
| input | Tokens | unit Measure | 否 | 落账拒 |
| input | Budget | unit Budget | 否 | 落账拒 |
| input | Cuts | array<unit Cut> | 否 | 落账但记缺席 |
| input | Notice | unit Notice | 是 | 门口拒 |
| output | Call | number | 否 | 落账拒 |
| output | Streaming | boolean | 否 | 正常发=落账拒 故障发=落账不变 参数被拒发=落账拒 |
| output | Finish | unit Finish | 是 | 门口拒 |
| output | Model | string | 否 | 落账拒 |
| output | ModelSaid | boolean | 否 | 落账拒 |
| output | WireFormat | string | 否 | 正常发=落账拒 故障发=落账不变 参数被拒发=落账拒 |
| output | WireFormatSaid | boolean | 否 | 正常发=落账拒 故障发=落账不变 参数被拒发=落账拒 |
| output | Says | unit Says | 否 | 落账拒 |
| output | UsageIn | unit Measure | 否 | 落账拒 |
| output | UsageOut | unit Measure | 否 | 落账拒 |
| output | WireBytesOut | unit Measure | 否 | 落账拒 |
| output | WireBytesIn | unit Measure | 否 | 落账拒 |
| output | Chunks | unit Measure | 否 | 落账拒 |
| output | FirstByteMs | unit Measure | 否 | 落账拒 |
| output | MaxGapMs | unit Measure | 否 | 落账拒 |
| output | ToolCalls | number | 否 | 正常发=静默落账 故障发=落账不变 参数被拒发=落账拒 |
| output | Malformed | number | 否 | 正常发=落账不变 故障发=落账不变 参数被拒发=落账拒 |
| output | MalformedStreak | number | 否 | 正常发=落账不变 故障发=落账不变 参数被拒发=落账拒 |
| output | Failure | unit Failure | 是 | 门口拒 |

<!-- kwire:units -->
| 单位 | 成员 | JSON 类型 |
|---|---|---|
| Budget | CallBytesCeiling | number |
| Budget | ClampRounds | number |
| Budget | ContentCeiling | number |
| Budget | EffectiveCeiling | number |
| Budget | FixedOverhead | unit Measure |
| Budget | LeftoverPct | number |
| Budget | Shares | object<键 string(PartKind) 到 number> |
| Budget | TransportSlackPct | number |
| Cut | FromBytes | number |
| Cut | Kind | string(PartKind) |
| Cut | Ref | string |
| Cut | RefCall | number |
| Cut | Role | string(Role) |
| Cut | SHA256 | string |
| Cut | Squeeze | string(Squeeze) |
| Cut | ToBytes | number |
| Failure | Detail | string |
| Failure | Downgrade | boolean |
| Failure | Kind | string(FailKind) |
| Failure | RetryAfterMs | unit Measure |
| Failure | Status | unit Measure |
| Finish | Reason | string |
| Finish | Said | boolean |
| Measure | Absent | boolean |
| Measure | Basis | number(Basis) |
| Measure | N | number |
| Measure | Refused | boolean |
| Measure | Source | string |
| Measure | Why | string |
| Notice | Sent | boolean |
| Notice | Text | string |
| Notice | Trimmed | boolean |
| Notice | Why | string |
| PartCount | Bytes | number |
| PartCount | Kind | string(PartKind) |
| PartCount | Parts | number |
| Says | Bytes | number |
| Says | Kept | number |
| Says | SHA256 | string |

<!-- kwire:holes -->
| 侧 | 少写的那一处 | 落账后被改署成实测的格 | 三份夹具各撞到哪儿 |
|---|---|---|---|
| input | Notice.Trimmed | input Notice.Trimmed | 正常发=静默落账 故障发=静默落账 参数被拒发=静默落账 |
| input | Notice.Why | input Notice.Why | 正常发=静默落账 故障发=静默落账 参数被拒发=静默落账 |
| output | Chunks.N | output Chunks | 正常发=静默落账 故障发=落账不变 参数被拒发=静默落账 |
| output | Failure.Downgrade | output Failure.Downgrade | 正常发=落账不变 故障发=落账不变 参数被拒发=静默落账 |
| output | FirstByteMs.N | output FirstByteMs | 正常发=静默落账 故障发=落账不变 参数被拒发=静默落账 |
| output | MaxGapMs.N | output MaxGapMs | 正常发=静默落账 故障发=落账不变 参数被拒发=静默落账 |
| output | Says.Kept | output Says.Clipped()、output Says.Kept | 正常发=静默落账 故障发=落账不变 参数被拒发=静默落账 |
| output | ToolCalls | output ToolCalls | 正常发=静默落账 故障发=落账不变 参数被拒发=落账拒 |
| output | WireBytesIn.N | output WireBytesIn | 正常发=静默落账 故障发=静默落账 参数被拒发=静默落账 |
| output | WireBytesOut.N | output CostCountable()、output Sent()、output WireBytesOut | 正常发=静默落账 故障发=静默落账 参数被拒发=静默落账 |

---

## 3. 格位表（48 键 / 49 格）

`basis_on_empty_sample` 一列是**空样本**（`InputMeta{}` / `OutputMeta{}`）上 `Cells()` 报的出处，不是「必须长这样」；
`owner` / `presence_policy` 两列是接线时由**接入方**确认的，不是内核声明（内核不认识框架）。

> **这一列怎么读（2026-10-13 对账时补）**：空样本上大多数格报 `unset`，因为 `Measure` 的零值出处就是 `unset`。
> 但 `fixed_overhead`、`tokens` 这类格在**真装配出来的一发**上会报 `estimated`（`DeriveBudget` 要求写明估的是什么）。
> 所以这一列**不是取值约束**，只是「这张表当初是从哪种形状上抄下来的」的记录。
> 把它读成「这一发应该填 estimated」会读错方向：空样本上填 `estimated` 反而会被 `Check()` 拒（有出处却没值）。
> 2026-10-13 之前这一行写的是 `estimated` —— 那是从一份**填过的**样例抄来的，与列定义不符，已改回实读的 `unset`。
>
> **2026-10-19 这一列里有三格从 `measured` 改成了 `absent`**（`folded`/`clipped`/`dropped`）：那一张裁剪表
> 「没交这个键」与「交了一张空表」以前在这一列长得一模一样，都是 `measured`。现在前者报 `absent`、
> 后者仍报 `measured`，而这一列第一次能分出这两种人。**在册的旧账不会跟着变** ——
> 改动之前落的那几千条里，这三格是 `measured` +「0 段（裁剪表是空的）」，
> 拿改动前后的读数做比较时不许把它们当成同一个量。

| key | sides | field | basis_on_empty_sample | owner（待接入方确认） | presence_policy |
|---|---|---|---|---|---|
| `call` | both | `Call` | measured | 框架·发号 | required |
| `parts` | input | `Order` | measured | 框架·装配器 | required |
| `part_totals` | input | `Parts` | measured | 框架·装配器 | required |
| `content_bytes` | input | `ContentBytes` | measured | 框架·装配器 | required |
| `tokens` | input | `Tokens` | unset | 框架·分词/转述 | required_or_absent_with_reason |
| `call_bytes_ceiling` | input | `Budget.CallBytesCeiling` | measured | 人·配置 | required |
| `transport_slack_pct` | input | `Budget.TransportSlackPct` | measured | 人·配置 | required |
| `content_ceiling` | input | `Budget.ContentCeiling` | measured | 内核·公式 | required |
| `effective_ceiling` | input | `Budget.EffectiveCeiling` | measured | 框架·回夹 | required |
| `clamp_rounds` | input | `Budget.ClampRounds` | measured | 框架·回夹 | required |
| `leftover_pct` | input | `Budget.LeftoverPct` | measured | 内核·公式 | required |
| `fixed_overhead` | input | `Budget.FixedOverhead` | unset | 框架·骨架统计 | required_or_absent_with_reason |
| `shares` | input | `Budget.Shares` | measured | 人·配置 | required |
| `notice_sent` | input | `Notice.Sent` | measured | 框架·裁剪器 | required —— 钉在段表上（见 §5 那条）|
| `notice_trimmed` | input | `Notice.Trimmed` | measured | 框架·裁剪器 | required |
| `notice_why` | input | `Notice.Why` | measured | 框架·裁剪器 | required |
| `notice_text` | input | `Notice.Text` | measured | 框架·裁剪器 | required |
| `folded` | input | `Cuts#folded` | absent | 框架·裁剪器 | required |
| `clipped` | input | `Cuts#clipped` | absent | 框架·裁剪器 | required |
| `dropped` | input | `Cuts#dropped` | absent | 框架·裁剪器 | required |
| `streaming` | output | `Streaming` | measured | 框架·调用器 | required |
| `finish` | output | `Finish.Reason` | relayed | 对端·响应 | required_or_absent_with_reason |
| `finish_said` | output | `Finish.Said` | measured | 框架·调用器 | required |
| `endpoint_model` | output | `Model` | model_says | 对端·自报 | required_or_absent_with_reason |
| `endpoint_model_said` | output | `ModelSaid` | measured | 框架·调用器 | required |
| `wire_format` | output | `WireFormat` | relayed | 对端·响应体 `object` | required_or_absent_with_reason |
| `wire_format_said` | output | `WireFormatSaid` | measured | 框架·调用器 | required |
| `model_says_bytes` | output | `Says.Bytes` | measured | 框架·响应体 | required |
| `model_says_kept` | output | `Says.Kept` | measured | 框架·响应体 | required |
| `model_says_clipped` | output | `Says.Clipped()` | measured | 框架·响应体 | required |
| `model_says_sha256` | output | `Says.SHA256` | measured | 框架·响应体 | required |
| `usage_in` | output | `UsageIn` | unset | 对端·自报 | required_or_absent_with_reason |
| `usage_out` | output | `UsageOut` | unset | 对端·自报 | required_or_absent_with_reason |
| `wire_bytes_out` | output | `WireBytesOut` | unset | 传输层·计数 | required |
| `wire_bytes_in` | output | `WireBytesIn` | unset | 传输层·计数 | required |
| `chunks` | output | `Chunks` | unset | 传输层·流式回调 | absent_with_reason_when_nonstream |
| `first_byte_ms` | output | `FirstByteMs` | unset | 传输层·流式回调 | absent_with_reason_when_nonstream |
| `max_gap_ms` | output | `MaxGapMs` | unset | 传输层·流式回调 | absent_with_reason_when_nonstream |
| `tool_calls` | output | `ToolCalls` | measured | 框架·响应解析 | required |
| `malformed_actions` | output | `Malformed` | measured | 框架·响应解析 | required |
| `malformed_streak` | output | `MalformedStreak` | measured | 框架·响应解析 | required |
| `fail_kind` | output | `Failure.Kind` | measured | 框架·调用器 | required |
| `fail_detail` | output | `Failure.Detail` | measured | 对端/框架·说法 | required_when_failed |
| `fail_status` | output | `Failure.Status` | unset | 对端·HTTP | required_or_absent_with_reason |
| `retry_after_ms` | output | `Failure.RetryAfterMs` | unset | 对端·响应头 | optional_absent_with_reason |
| `downgrade` | output | `Failure.Downgrade` | measured | 框架·策略 | required |
| `sent` | output | `Sent()` | measured | 框架·调用器 | required |
| `cost_countable` | output | `CostCountable()` | measured | 内核·派生 | required |

**presence_policy 的取值含义**：`required`＝每发都得有；`required_or_absent_with_reason`＝要么有值、要么记 `absent` 且带理由；
`absent_with_reason_when_nonstream`＝非流式那一发必须记 `absent+理由`（写 0 直接拒）；
`required_when_failed`＝只在报故障那一发要求；`optional_absent_with_reason`＝没这一格就记 `absent+理由`。

---

**这一轮新增/改动的四行，解释住在这里（表的第 4 栏要留给机器读的出处 token）：**

- `fail_status` —— 出处**随故障类别那根轴而变**（`FailKind.MayCarryStatus()`）：
  收到过对端响应的那几类（`http`/`config`/`protocol`/`malformed_action`）是 `measured`；
  `transport`/`budget` 与没故障的那一发是 `absent` 带理由。
  以前这一格在 `cells.go` 里被写死成 `measured`，于是**每一发正常往来都在账上说「量到状态码是 0」**
  （实测：在册 27 发全是那个长相）。
- `wire_format` / `wire_format_said` —— 对端在响应体顶层自己写的 `object`（实测非流式
  `chat.completion`、流式 `chat.completion.chunk`，两支不同，见 报告/格式自报-流式对照.txt）。
  只能转述：填我们自己那套接线常量（`openai-chat/v1`）就是转述对端没说过的话。
  另有一条互核：说对端自报了格式，却是 `transport`/`budget` 那一类（压根没收到响应）⇒ 整发拒收。
- `notice_sent` —— **钉在段表上**：`Notice.Sent=true` 要求 `Order` 里真有 `truncation-notice` 那一段，
  有那段却没记 Sent 同样拒。实例是真撞出来的（不是设想）：驱动把折叠排在附通知之前，
  于是有一发裁剪表多了记录、`Sent` 写着"是"、段里没有通知，内核当时整发照收
  （报告/挤历史-判生死.md 第 7 节）。
- `Cut.RefCall` —— 被挤掉的**历史**段必须指名它来自第几发，且那一发必须早于当前发
  （指 0 或指未来都拒）。`Ref` 是人写的散文，这是第一个能机器用的跨发引用
  （报告/挤历史-判生死.md：三把指纹单位不同，"它是第几发"当时只在散文里）。

## 4. 已知空洞（**别当已实现**）

**第一轮（证据源 runloop，真模型端点）** 实测后 **10 格从未被填过**：`fixed_overhead` `tokens` `chunks`
`first_byte_ms` `max_gap_ms` `retry_after_ms` `usage_in` `usage_out` `wire_bytes_in` `wire_bytes_out`。
原因不是疏忽，是**采集点不存在**：`usage_*` 的 owner 是「对端·自报」、`wire_bytes_*` 是「传输层·计数」、
三格流式是「传输层·流式回调」——这三条通路在接入方一侧还没建。接的时候要么建通路，要么按
`absent+理由` 记（**不是记 0**）。

**第二轮（2026-10-13，`cmd/kresearch` 接 Deep Research HTTP 服务，见 `报告/实测-真端点形状缺陷.md`）**
上面这 10 格里 **7 格被真量到了**：`chunks` `first_byte_ms` `max_gap_ms` `wire_bytes_in` `wire_bytes_out`
记成 `measured`（1368 个事件块 / 首帧 90ms / 最长静默 16819ms / 收到 495461 字节 / 请求体 0 字节），
`fixed_overhead` `tokens` 记成 `estimated` 并写明估的是什么。
**仍然填不上的 3 格是 `usage_in` `usage_out` `retry_after_ms`** ——
这一轮的原因换了：不是我们没有采集点，而是**这个端点不提供 usage 字段、成功的往来也不给 Retry-After**。
所以「建通路」这一条要分成两种活：传输层与流式那几格接了客户端就有；对端自报那两格**取决于对端给不给**，
给不出来的时候如实记缺席，比记一个推导出来的数诚实。

**第三轮（2026-10-13，`tasks/mcp/llm_probe.py` 经挂载好的内核接一个 OpenAI 兼容的对话端点，
见 `报告/接入LLM-第一发.md` 与 `报告/样例-LLM两发.jsonl`）**
上面那 3 格**又填上两格**：`usage_in` `usage_out` 第一次拿到真数（93/18、127/10），
出处是**转述** —— 分词器在对端，记成实测就是假绿。`endpoint_model` 不再是「模型没说」
（自报 `agnes-3.0-flash`，档位仍是 `model_says`：永远是线索不是事实），`finish` 第一次有真值 `stop`。
**只剩 `retry_after_ms` 仍然缺席**：这两发都是 200，没断，也就没有要等多久（这里缺席是对的）。
所以第 200-203 行那句「取决于对端给不给」今天被正面验证了，并且推掉一个更强的说法：
**格位表里没有死格，只有还没接到的端点**。以后不许写「某格永远填不上」，要写就写「这个端点不给」。

第四处**形状**缺陷也是这一轮才现形的，方向和规矩二相反：
`tokens`（输入侧）与 `usage_in`（输出侧）是**同一个物理量的两格**，实测各说各话。
调用方把第 2 发的 `tokens` 沿用了第 1 发的 93，而端点对第 2 发报的是 127 ——
**两格一起落了账，出处字符串一模一样，内核一条都没拦**；按两个键名分别累加是 186 与 220，
两张成本分母是 214 与 248。撞名判据看不见这一族，因为两个名字本来就不该撞。
现形的条件正是上面那件事：上一轮的端点不给 usage，`usage_in` 恒为缺席，结构上撞不出矛盾。
修不修是口径问题（钉成相等 / 允许不等但必须带理由 / 输入侧只留估算那一档），**这份文档不替你定**。

同一轮还翻出三处**形状**缺陷（不是数值错，是格子不够/口径串了）：
`wire_bytes_out` 对无请求体的发会渲染成「没发出去」；46 个键名（封板后 48 个）里**没有一格装整发耗时**；
而隔离闸把包内核（116 个包）与整棵树（208 个包）压成同一个读数。详见那份报告的发现 1、2、4。

**第四轮（2026-10-13，`tasks/mcp/tools_probe.py` 带 `tools` 打同一个对话端点，
见 `报告/工具提议-实测.md` 与 `报告/样例-工具提议.jsonl`）**
工具那一族三格第一次有**真响应上的非零数**：`tool_calls=2`、`malformed_actions=1`、
`malformed_streak=1`（流式那一发的第二个提议被截成 `{"a": 9`，参数解不出 JSON）。
`finish` 拿到第二种非 stop 真形状 `tool_calls`（出处 `relayed`）；输入侧 `toolspec`、`observation`、
`truncation-notice` 三个段类第一次被真数填上，`Cuts` 三档里 `dropped` 有了第一条真记录
（被省掉那条 317 字节、带原文指纹，第三方能核回是哪一段）。
另外两件事要写给接线的人：
**内核第一次当面拒了一次装配**（HTTP 200、原话 0 字节、0 个提议却按正常收尾记 —— 拒得对，改调用方），
**以及一个歪提议会把下一发毒掉**（对端回 400：`arguments must be valid JSON`），
所以「省略它并把省略记进裁剪表」不是排版选择，是通路问题。

这一轮新出两处形状缺陷，都是**改判据表**的事，这份文档不替你定（详 README 第 8 节缺口 10、11）。
**第一处先要纠正本文自己写错的一句**：它一度说 `failure.kind` 与 `failure.status` 互不复核 ——
其实**复核在**（`kind=http ⇒ status≠0`、`kind≠http ⇒ status=0`），而把它撞出来的是一发故意的假 key：
真 401 按 `FailConfig`（取值表写的正是「密钥错」）装配，整发被拒，原话
「config 不是 http 那一类，却带着状态码 401」。于是开着的问题是：**`FailConfig` 那三种成因在 HTTP 上
全都带 4xx，而只有 `http` 那一档许带那个数** —— 一条真 401 要么丢了码记 `config`，
要么为了留住那个数改口记 `http`，而后者把 `RetrySame()` 翻成 true（账上写「原样再发有用」，
实际只会再吃一次同样的 400/401）。中间那一轮把真 400 记成 `http` 就是走通了后者，**内核收下了**：
这条互核挡得住「config 带码」，挡不住「把 config 谎报成 http」，因为类别那一格是调用方自己填的。
另外 `fail_detail` 的出处**跟着状态码走**（`≠0 ⇒ relayed`，否则 `measured`），
所以记 `config` 时把对端原话抄进去，那一格会被标成「我们量的」——
现在调用方只写自己构造的判定句（标 measured 名副其实），代价是那个 401 **只能住在散文里**。
> **上面这两句到 2026-10-13 封板轮为止是历史，别再照它们写代码。**
> 那两条按类别的互核已删除，换成 `FailKind.MayCarryStatus()`（`http`/`config`/`protocol`/`malformed_action`
> = 收到过对端响应，才有码可记），`fail_detail` 与 `fail_status` 两格的出处都跟着这张表：
> 真 401 现在**码照实记、对端原话照实抄并标 `relayed`**，`transport`/`budget` 与没故障的那一发
> 的 `fail_status` 是 `absent` 带理由 —— 「量到状态码 0」这个形状从根上没了
> （在册 27 发全是那个长相）。挡不住「把 config 谎报成 http」那一半仍然成立：类别还是调用方自己填的，
> 这一格依旧没有第二根轴能反证它。详见 报告/假key一发-401.md、报告/挤历史-判生死.md 第 6 节。
第二处：工具提议的**内容**（哪个工具、参数是什么）在账上无处可放，
而 `malformed_action` 一个档位装了两种成因（歪了一个 vs 根本没有），分开它们的只有 `fail_detail` 那句自由文本。

其他三条按 README 第 8 节照搬，不许被写成已验证：

1. **读者声明表是空的**：内核能机械核的是写侧（每个字段都有格替它落账）；
   「落了账但没人读」那一半要接入方声明，内核自己核不到。声明走 `kernel.declare_reader`（见 §5 第 2b 步），
   它的读数走 `kernel.readers_report` —— **这两只工具 2026-10-19 之前在本文件里一次都没出现过**，
   而它们是这一条缺口唯一的出口；由 `cmd/kwire` 那条「工具没有一处文档」的检钉住（每个注册名都得在
   `contract.md` 或 `README.md` 里至少出现一次）。
2. **隔离闸看不见三条路**：`plugin.Open`、`os/exec` 起一个框架进程、把框架结构体塞进 `Cell.Value` 那个 `any`。
3. **`Failure.Detail` 的脱敏是契约、不是被核过的不变量**（擦码器在框架那一侧，内核 import 不到）。

**已修的一条（2026-10-18，不算进上面那三条）**：「闸门拦了几次」这个唯一 KPI 原先有两处答不出：
   ① `kernel.check_call`（预检）三个拒收出口**一个数都不记**；② 已记的 `Rejected()` 只在内存，
   `encode_journal` 交出的字节里没有它的位置，`LoadLedger` 也就无从恢复 ⇒ 只读审计面 `kaudit`
   对任何文件账本都印 `rejected_writes=0`。两个互不相知的外部调用方各撞过一次
   （8797 那本 `rejected_writes=0`、8799 那本四栏全 0 而同一链上有 10 发真账）。
   现在的形状：预检拒的进 `precheck_rejected`（内核判据）与 `precheck_door`（门口），
   六笔数编进**一行非链上**的 `gate` 记录并各盖封条（见 2.3b），`LoadLedger` 恢复它。
   **仍然答不出的**：这一行不在链上，所以它证明不了自己——只能证明"与当时链尾一起被盖过"；
   跨进程/跨实例的比对没有（8797 与 8799 是两本账，工具不报实例是谁，也不报另一本存不存在）；
   以及**调用方自己数了几次工具调用，账上仍没有那一格**（外人自报的 130/110/24–26 次无从复算）。

**第四条空洞（2026-10-19，由 `cmd/kwire` 一处一处删出来问出来的，不是设想的）**：
「整块没给」与「给了，但里面某一格没填」在账上**曾经是同一种形状** —— 都落得进，都被署 `measured`。
逐名清单就是 §2.5 那张带锚点的空洞表（机器导出，改一处判据就会红），这里说它的三档与最贵的两条：

- **侧根上的名单型那 1 处已修（2026-10-19 当晚）**：`input` 的 `Cuts`。原来整块没给照样落账，
  而 `folded` 那一格署上 `measured` —— 这一条就是看板上的 #66，10-17 立案、10-19 被两个独立分析
  在同 3430 条上撞成两种读数之后才量清规模（`folded`/`clipped` 在 70/70 发上都是 `basis=1, payload=[]`，
  而 `dropped` 有 6 条真记录 —— 那本账答不出「调用方报了空表」与「调用方压根没提裁剪」的差别）。
  改的是这三格出处的来路：没交这个键 ⇒ 名单是 nil ⇒ 三格都落 `absent`，原像 `null`，
  人话是「没交这一档的名单，不是空表」；交一张空表 ⇒ 才是 `measured` 加「0 段（裁剪表是空的）」，原像 `[]`。
  接线三处：`input.go` 的 `CutList`（不再把 nil 抹成空表）、`cells.go` 的 `cutCell`（认 nil）、
  `record.go` 的 `basisOf`（给 nil 名单开 `absent` 那一档，同时把「谁能报缺席」收窄成
  三态量与 nil 名单两种）。**在册旧账不改**：改动之前落的那几千条里这三格仍是 `measured`，
  跨改动比较时它们不是同一个量。
- **块里的叶子有 2 处**：`Notice.Trimmed`、`Notice.Why`。门只管整块在不在，块里某一格没说，它不管。
- **`Measure.N` 那一族有 5 处**（`Chunks` / `FirstByteMs` / `MaxGapMs` / `WireBytesIn` / `WireBytesOut`）：
  删掉 `N` 之后 `Basis` 还在、还署着实测，于是「没数到」在账上变成「数到了，是 0」。
  **这一族没跟着 `Cuts` 一起动**，理由是它缺一条还没定的口径：`N` 与 `Basis` 是两处，
  删 `N` 时 `Basis` 是调用方**自己写上来的**，要改署就得由内核去推翻一格已带出处的值 ——
  那是比 #66 大的一刀，得先定「谁有权改署」。今天它仍是洞。
  **最贵的是 `WireBytesOut.N`** —— 它连着把 `sent` 署成「没发出去（数到了，是 0 字节）」、
  把 `cost_countable` 署成不算数：一笔可能已经花出去的钱，在账上变成了没花。
  另有 `Says.Kept`（裁没裁过那一格由它两格算）、`Failure.Downgrade` 同族。
- **侧根上的标量型那 1 处仍在**：`output ToolCalls`，类型是 `int`。删掉这个键与给它填 `0`，
  `json.Unmarshal` 出来是同一个值 —— 门口那道闸只管**嵌套一层的整块**（`mcpsrv.go` 那段把这条边界写在自己注释里），
  内核收到的已经是解好的结构体，更分不出。`cmd/kwire` 量得出"少写了还能落账"，但修它要改字段类型（指针），
  那是接口形状的事：字段类型一变就是接口一变，旧写法当场被拒而不是兼容地跑起来。**本轮没动它，也没把它写成已修。**

名单型那一处修完之后，剩下 10 处 = **块里的叶子 9 处 ＋ 侧根上的标量 1 处**（尺子自己印这档，
`报告/接口对账-空洞分档-1019.txt` 第 3 行）：块里那些门管不到，`Check()` 也管不到（它收到的是已经解好的结构体，
分不清「这个字段是零值」与「这个字段没交」—— 那是 JSON 解码之前的事实，只有 `cmd/kwire` 那一种
「拿真参数删一处再喂进去」的跑法量得到）；侧根上那 1 处标量则是**连解码都分不出**，比块里那一档更难，
不是"再加一条门判据"能解决的。

---

## 5. 接的时候按这个顺序（不含任何改动到别处）

0. **先按封板版的形状对齐四处入参**（`InputMeta`/`OutputMeta` 没有独立 wire 形状，
   键名就是 Go 导出字段名 —— 所以**字段类型变了就是接口变了**，旧写法不会"兼容地跑起来"，会当场拒；
   最后一行那一处**不拒**，它改的是落进账本的那句话，所以更要在接线前对表）：

   | 字段 | 封板后的形状 | 填错的后果 |
   |---|---|---|
   | `Failure.Status` | 不再是裸整数，是一个 Measure 对象：`{"N":401,"Basis":1,"Source":"","Absent":false,"Refused":false,"Why":""}`；没收到对端响应那一类（`transport`/`budget`）与没故障那一发填 `{"N":0,"Basis":0,"Absent":true,"Why":"…为什么没有…"}` | 把"没收到"写成 `N:0,Basis:1` 会被"量到状态码 0"那一族挡下。**交整数 `401` 现在当场被拒**：那份严格解码会点名 `OutputMeta.Failure.Status 类型不对`，不会静默变成"没填"（2026-10-19 按 `cmd/kwire` 改准，此处旧话已过期） |
   | `OutputMeta.WireFormat` / `WireFormatSaid` | 新增两格。装**对端在响应体顶层自报的原字**（`chat.completion` / `chat.completion.chunk`），不是我们自己那套接线常量；没收到响应时两者填 `""` / `false` | 说 `Said=true` 而值是空串 ⇒ 拒；说报了格式却归到 `transport`/`budget` 那一类 ⇒ 拒 |
   | `Cut.RefCall` | 裁剪记录新增一格：被挤掉的那段来自**第几发**。`history` 类必填且 `> 0` 且 `< 当前发号` | 指 0（没说来自哪一发）或指未来那一发 ⇒ 整发拒收 |
   | `InputMeta.Cuts` | **没挤掉任何东西 = 交一张空表 `"Cuts": []`，不是省掉这个键**。省掉这个键 ⇒ `folded`/`clipped`/`dropped` 三格在账上落成 `absent`（人话「没交这一档的名单，不是空表」、原像 `null`）；交空表 ⇒ 三格落 `measured`（「0 段（裁剪表是空的）」、原像 `[]`） | 两种都收，但**它们是两句不同的话**：一个说「这一发确实一档都没挤」，一个说「调用方没提裁剪」。想报「没裁剪」却省键，账上得到的是一格缺席，不是零 |

   还有一条不需要改字段、但会被新判据挡住的：**`Notice.Sent=true` 必须真在 `Order` 里有
   `truncation-notice` 那一段**（反向同样）。通知段一旦进了历史就会随后续每发一起发出去，
   所以"这一发没发通知"在后面的发上是假的 —— 从真发出去的段里读，别由裁剪表宣称。

   前三条是 2026-10-13 用挂载好的外壳一条条问出来的（`报告/契约核对-入参形状.txt`，8 项）；
   第四条是 2026-10-19 由 `cmd/kwire` 一处一处删出来问出来的，**当天就改了代码**（看板 #66）。
   其中一条把契约自己的错抓了出来 —— 但**那次给的解释有两处今天已经不成立，而这句话在文档里一直活到
   2026-10-19**，才被 `cmd/kwire` 里 `staleClaims` 那张表点名。按今天的代码改准：

   10-13 看到的**现象**是真的：交整数 `401` 时 `kernel.check_call` 回「两侧账：自洽」，而
   `kernel.record_call` 拒收「fail_status 那一格进不了账：有值 0 却没说谁算的」。当时归因两条，一条位置说错、一条已修：

   - ①「`cmd/mcpkernel` 的 `args[T]` 把解码错误丢了」—— **两处都不对**。那个泛型辅助件住在
     `mcpsrv/mcpsrv.go`（stdio 与 HTTP 两面共用同一个包，`cmd/mcpkernel` 里没有它），而且它
     **从来不在写路径上**：全文四个调用点是 `read_back`、`declare_reader`、`encode_journal`、`load_journal`。
     写侧（`check_call` / `record_call`）用的是同一份 `decodeCallArgs`：`DisallowUnknownFields` 递归到嵌套结构体，
     类型写错点名到 `InputMeta.Call` 这样的「结构体.字段」并补一句「外壳不会替你把它补成零值」，
     未知键名点名那个键、再从键名表里列出归一化后能对上的是哪几格（不止一格时**全列出来、不替你猜**）。
     所以「把类型写错造成没填、再落成一个 measured 0」那一族在写侧从根上没有 ——
     10-17 那次冷接实测（`报告/冷接上手-1017.txt` 第五节）就是它的来路。
     **读侧那四个仍然会丢解码错误**，其中三处后面跟着一道自己会拒的闸（今天逐条实读过：
     `encode_journal` 认不出的 `payload` 一律拒、并把原值引在回话里；`Readers.Declare` 空键名当场拒；`read_back` 一条都不写）。
     **但 10-19 中午 M5 那轮跑出一处反例，这一档的分类当场作废**：`load_journal` 的那道闸只管「有没有点名 `acknowledge=replace`」，
     管不住「点名的字节根本没进门」——发 `{"journal":<一份合法的非空账>,"acknowledge":"replace"}`（正确键名是 `text`），
     `a.Text` 解成空串、旧版那一层没有非空检查、空串喂 `kernel.LoadLedger` 得到一份合法的"0 行 0 条"空账，
     旧 `:593`（现 `:609`）那道闸因为 `acknowledge` 已点名而放行，旧 `:604`（现 `:636`）直接 `s.ledger = next` ⇒ **整本账被静默换成空账，回话 `isError=false`、还念一句「从 0 行里读回 0 条，链上现在 0 条」**。
     同错键名而不带 `acknowledge` 的对照臂当场被拒 ⇒ 通行证正是那个用来"防止静默替换"的字段本身。
     所以它是**「静默改状态」那一档**，不是「回话不清楚」那一档 —— **（1025 趟更新）**代码与针已按批准的 A1 落进树（§3 那一条 + 臂 `kprobe.py` s 族 11 根），**但 §12 第 4 条那一发真实例还没发 ⇒ 这一条仍不许读成「已验收」**；旧锚的整体漂移见 `报告/遗留修复-1025.txt` §9c。
   - ②「第 1 步（先问）比第 2 步（落账）松」—— **已经不成立**。今天两面共用同一道门口闸
     （`refuseMissingBlocks`）与同一份解码，而 `check_call` 除了跑两侧 `Check()`，还在
     **当前这本账的副本**（`ledgerCopy()`）上真跑一遍 `RecordCall`、一条都不写回。
     副本与真账只差「写回」这一件事，判据不是两套 —— 预检放过的落得进，预检拒的也照样进桶
     （内核判据进 `precheck_rejected`、门口进 `precheck_door`）且 `isError=true`
     （#20 之前它只在正文里说拒、标志位仍报 false，只读标志位的调用方拿到的是「通过」）。
     所以 `check_call` 今天**可以**当预检通过证用 —— 它证的是**这一本账此刻**的状态：
     `load_journal` 换过一本之后，同一份参数完全可能是另一种答案，而那一变不在它的承诺里。

1. **先读，不写**：拿一段真实调用，把两侧账构造出来，只调 `InputMeta.Check()` / `OutputMeta.Check()`，看它报什么。
2. **只落账、不落盘**：`Ledger.RecordCall` → `Ledger.Verify()`，确认链自洽、拒收有数。
2b. **声明你读了哪一格**：`kernel.declare_reader`，三个参数 `reader` / `key` / `side` 全必填。
    这一只是 §4 第 1 条那个空白的唯一填法：落了账但没人读的那些格子，只有
    `kernel.readers_report` 能报「N 格里 M 格没有任何声明者读」，而它读的就是这张声明表。
    三条得写在文档里，因为它们都不符合直觉（2026-10-19 由一个陌生调用方各撞一次）：
    ① 键名拼错当场被拒 —— 它拿 `InputMeta{}.Cells()` / `OutputMeta{}.Cells()` 逐格对，
    回话原话是「拼错的声明会让一格死格看起来是活的」；
    ② **同一个 `(reader, key)` 声明两遍同样被拒**，理由由内核自己说（「同一个读者声明两遍会把分母数大」），
    而**去重只看 `key`、不看 `side`** —— `call` 那一格两侧同名，于是同一读者只能声明它一次，
    想两侧各声明一次的路径今天不存在（这是实读 `Readers.Declare` 读出来的形状，别照着它设计）；
    ③ 被拒的声明**不进任何一笔数**：六笔数只管写侧与门口，读者这一面今天只有 `readers_report` 一个出口。
3. **再谈落盘**：格式用 `MarshalJournal` / `ReadJournal`（见 2.3），**别自己 `json.Marshal(entry)`** ——
   那一条最自然的写法会默认丢掉 `Payload` 的原像。要决定的是 `PayloadMode`：带原像（第三方可重算，
   但敏感原文跟着落盘）或不带（只留指纹，"能重算"那一半从此核不了）。写文件那一步仍是框架做。
   重启后要接着记，用 `LoadLedger` —— 它把旧账真填进账本，去重闸因此看得见历史。
4. **最后谈并发**：`Ledger` **没有锁**，且去重闸 `alreadyRecorded` 每次落账**全账本线扫**（O(N²)）。
   多 goroutine 共用一本账不在承诺之内——要么调用方串行化，要么各用各的账本。

---

## 6. 怎么核这张表（防它漂）

```
py -3 tasks/audit/celltable_audit.py <go.exe 路径> <本包目录> [报告输出路径]
```

它**不复制第二份真相**：实读那一侧就地解析 `go run ./cmd/kcheck` 第 5 节自己印的输出，
文档那一侧解析本文件第 3 节的表，然后逐格对四项 —— 键名、`sides`、字段路径、空样本出处。
报告写进 `报告/格位表一致性-最新.txt`（UTF-8），终端只留一行纯 ASCII
—— 因为 Windows 的 GBK 控制台打不出 `✓`，脚本会**崩在打印那一行**，而崩会被读成不合格（2026-10-13 实测到过一次）。

三态，与闸门同一条纪律：**0 = 一致；1 = 漂了（逐格点名）；2 = 核不了**
（没有 go、闸门没过、表解析不出行）。「核不了」不许被读成合格。

**这一份是 2026-10-13 补的。** 在此之前这里写的是「在 `%TEMP%\kernel-attach` 下的 `cellcount` 子模块里
`go run celltable.go`」，而 `celltable.go` **从来不在这个仓库里** —— 于是「由 `Cells()` 机械导出、不是手抄」
这句话在实际意义上是不可执行的：它靠上一次有人手动跑过。补上这根检之后，改任何一个键名而忘了改这张表，**这里会红**。
已经真红过一次：`fixed_overhead` 那一行的空样本出处写的是 `estimated`，实读是 `unset`（已按代码改回）。

**判据**：`go test ./...` 与 `go run ./cmd/kcheck` 全绿时，这份表才可信
（**测试有几条以 `go test -v` 自己数的为准**，这份文件不抄 —— 抄了就会漂，而漂了的数比没有数更有害）；
表与代码不一致时，**以代码为准，改这份文件**。

---

## 7. 这份文件自己没做的事

- 这份文件写下的那天没改内核（写入前回滚点 `mp-mujmoqkh-25f`）。**但代码之后动过**：
  2026-10-13 的挂载第二轮把读侧三个入口（`CellsOf`/`Keys`/`AbsentKeys`）加了 `session` 这一根轴，
  并补了 `TestCellsOfKeepsTwoSessionsApart` 与慢闸针 `j10`（见 README 2.3 与 `报告/挂载-跨进程实测.md`）。
  这张表与代码不一致时以代码为准 —— 那条纪律今天用上了。
- `owner` / `presence_policy` 两列是**接线视角的提案**，不是内核声明，也没被任何判据守着。
- 没有接入方签名，没有真端点数据——它只是一张表，不是已验证的契约。
- 第 4 节那三处形状缺陷之外，今天还有一条同族的：**读侧曾经少一根轴**（见上一条与 README 2.3）。
  它是「写侧能核不等于读侧能用」的一个具体形状，也是挂成进程之后才暴露出来的。
