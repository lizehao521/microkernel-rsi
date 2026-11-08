# 第四发故障形状：`protocol` 之前产不出来，产出来之后被同一条互核挡在门外

日期：2026-10-13 · 端点：**本机假端点，不花钱**（`tasks/probe/fakellm.py`，`127.0.0.1:8799`）
· 驱动：`tasks/mcp/tools_probe.py`（`PROTO_TURN=1` 只跑一发）· 本轮**没动内核一行 Go、没动任何判据文件**

## 0. 这一支要量什么

在册的账里故障只有三档有过真形状：`transport`（空端口）、`config`（真 401）、`malformed_action`（截断与空手）。
`http`/`budget`/`protocol` 三档从来没有。`http` 那一档假端点早轮量过（429 + `Retry-After`），
但没归档进册；`budget` 是我们自己那侧的上限，得改驱动才撞得出；
**`protocol` 是唯一一发"对端明明回了话、我们却说不出它回了什么"的形状** ——
而它正好是缺口 10 那根轴的另一个端点：如果"能不能带状态码"真的只是"是不是 http"，
那 `protocol`（对端回了 200）就该带码；如果它是"有没有收到对端响应"，`protocol` 也必须能带码。
两种读法在这一发上会给出**相反**的账，所以它值一次不花钱的真跑。

形状怎么造的：给假端点加一个旋钮（`max_tokens == 5` —— 真那五发用的是 64/96/6/4，撞不上），
它回 **HTTP 200 + `Content-Length` 如实 + body 是断的 JSON**：

```
{"choices": [{"message": {"content": "和是 4
```

连接成了、状态是成功、46 个字节真给了，话不成话。这是 `protocol` 唯一的真形状，不是我们猜的。

## 1. 第一件事：这一档**在任何调用方都产不出来**（不是没开格，是没有采集路径）

什么都没改先跑一遍（`报告/协议档-装不出来那次的traceback.txt`）：

```
File "…\tasks\mcp\tools_probe.py", line 112, in post
    j = json.loads(raw.decode("utf-8"))
json.decoder.JSONDecodeError: Unterminated string starting at: line 1 column 38 (char 37)
```

`post()` 的非流式分支里 `json.loads` 是**裸调**的：它的错误处理只有 `HTTPError`（有状态码那一族）
和"别的异常"（`transport` 那一族）两条，**200 + 解不出来**这件事落在两条之间的空档里，
于是整个驱动 traceback 出去 —— 那一发**连账都没落**，`Failure.Kind` 那格根本没机会被填。

所以内核取值表里的 `protocol` 一档，此前的状态不是"账上没有它的真数据"，而是
**"我们没有任何一条路径能把这一档写进账"**。这正是 contract §4 那一族：
一个格位在表上、形状上合法、却从没被任何采集点喂过 —— 与"这个量不存在"读起来一模一样。
README 里那句「没有死格，只有还没接的端点」今天被这一发**收窄**了：
`protocol` 那一格不是端点没接上，是**调用方没写那段代码**。修的是调用方（`tools_probe.py` 加了 `proto_broken` 分支）。

## 2. 第二件事：修好之后，照实记被内核整发拒收

诚实装配（`PROTO_WITNESS=code`，默认）：`Kind=protocol`、`Status=200`（量到的）、
`Detail` 放**对端那 46 个字节的原话**（一个字没改）。内核回话（`报告/协议档-照实记被拒.txt`）：

```
整发拒收（拒收累计 1 次）：kernel: 输出侧账不自洽，第 1 发整发拒收（输入侧也不写）：
kernel: protocol 不是 http 那一类，却带着状态码 200
tally：账 0 条（）；被拒的写入 1 次
```

代价是**当场量出来的**，不是形容：为了留住那个 200，**整发 47 格全丢**，
其中包含与"是不是 http"毫无关系的实测格 `wire_bytes_in=46`、`wire_bytes_out=671`。
`报告/样例-协议档-照实记.jsonl` 是一个**货真价实的空文件** —— 那比散文更能说明这一档今天装不下自己。

这是**第二个独立实例**（第一个是 `报告/假key一发-401.md` 的真 401）。
两发合起来能说的比一发多：`config`（对端回 401）与 `protocol`（对端回 200）
在"对端确实回了话"这一点上完全同类，却被同一条 `kind != http ⇒ status = 0` 一起挡在门外。
所以缺口 10 不是"401 这个特例 unlucky"，而是**这条判据按一个类别名冒充了另一根轴**。

## 3. 第三件事：让步那种装法落了账，但它写下一个**不存在的测量值**

同一发真响应，换成现行判据允许的装法（`PROTO_WITNESS=prose`：状态码归 0、
`Detail` 换成我们自己的判定句，这样标 `measured` 才名副其实）—— 通过了，`报告/协议档-让步装落了账.txt`：

```
账 47 条（absent=6 estimated=2 measured=37 model_says=1 relayed=1）；被拒的写入 0 次
✓ 链自洽（共 47 条，其中 47 条比对过原像）
PASS ×5（落账通过 / 链自洽 / (2) 提议是实测数 / (9) 前后同一档 / 账不带 key）
```

从账上把四格抠出来看（`报告/样例-协议档-让步装.jsonl`，`basis` 1=measured 2=relayed）：

| 格 | claim | 出处 |
|---|---|---|
| `model_output:wire_bytes_in` | `46` | measured |
| `model_output:fail_kind` | `protocol` | measured |
| `model_output:fail_status` | **`0`** | **measured** |
| `model_output:fail_detail` | `本地判定 protocol：对端回 46 字节、状态码 200，body 解不出约定的形状；…` | measured |

**这一行比 401 那一发更难堪，而且成因比"判据逼的"还深一层。** 读代码确认了是两处合起来逼的：

1. `cells.go:120` 把这一格的出处**写死**成 `BasisMeasured`：
   ```go
   cell("fail_status", "Failure.Status", m.Failure.Status, BasisMeasured),
   ```
   也就是说调用方**没有任何办法**把"没收到状态码"记成 `absent` —— 这一格结构上就只有"量到了"一种长相。
2. `contract.md` §2.4 的值域表把这条规矩写在纸上：「只有 `http` 那一类能带 `Status`」。

两条合起来：对端回了 200 的这一发，`fail_status` 只能长成「我们量到状态码是 0」。
HTTP 状态码里没有 0 这个值，而本包规矩一（README §3）明写 `measured` 的 `0` 意思是**量到了、值就是零**，
它与 `absent`（没量到，必须带 Why）是两种长相 —— 这一格却被代码定死成只有前者。
所以这不是"调用方填错了"，是**结构上必然记下一个不存在的测量值**。

同族的第一次（401）代价是"那个数只能住散文"；这一次代价是"那个格被填成一个假数"。
散文还只是读不到，假数是**会被读错**。

## 4. 这一发对那两种读法分别说什么

| | 面对 `protocol` + 200 会怎样 |
|---|---|
| 读法 A（给 `FailKind` 加两档） | 治不了。加进来的 `rejected`/`rate_limited` 都是**有码的**，而 `protocol` 有码这件事照样被"它不是那一档"挡住；除非再加第三、第四档，并且给每一档重述一次"有没有收到响应" —— 那正是规矩二禁的重复住法 |
| 读法 B（一张 `Kind → 许不许带状态码` 的表 + `fail_detail` 出处跟着它） | 直接接住：`protocol` 归"收到对端响应"那一侧 ⇒ 200 可以名正言顺进 `fail_status`，`Detail` 放对端原字并标 `relayed`，`fail_status` 不再是那个假 0 |

也就是说 §2/§3 这两发合起来给 读法 B 提供了**两个独立证据**，而 读法 A 一个都用不上。
我仍然不动判据文件（那是你的口径），但把话说清：现在账上每一发非 `http` 的故障，
`fail_status` 那一格都写着 measured `0`，读的人无法知道那是让步。

**这一发还查出了 读法 B 原本缺的第三件。** 待定那份文档只写了"把两条按类别的互核换成一张
`Kind → 许不许带状态码` 的表 + `fail_detail` 的出处跟着它"，那**只治了白名单里那一侧**：
`transport`/`budget` 这些压根没收到响应的类别，落在表外之后，`cells.go:120` 那句写死的
`BasisMeasured` 依然会把它们的状态码记成「量到了，是 0」。
所以第三件是：**`fail_status` 那一格的出处得跟着同一张表走** ——
表里"不许带码"的那些类别，这一格必须是 `absent` 且带 Why（"这一类按定义没收到对端响应，
不是量到状态码 0"），而"许带码"的类别才继续 `measured`。
这一件改的是**逐格出处**，方向是收紧不是放宽，所以它得配针、配 `celltable_audit`，也得算进 64 根那轮的分母里。

**这一件影响的存量是数过的，而且比我准备写的更难看。** 把九本在册样例账（`报告/样例-*.jsonl`，
含本轮这两本）里所有 `model_output:fail_status` 抠出来配着 `fail_kind` 看：**27 发落账，27 个 `0`，
出处全是 `measured`，没有一个例外**。其中 **22 发压根没有故障**（`fail_kind` 那格是空的）——
一次成功的往来在账上写着「我们量到 HTTP 状态码是 0」，而 HTTP 没有 0 这个码。
剩下 5 发是 `malformed_action`×2、`config`、`protocol`、`transport` 各一。

这说明那格 measured `0` **不是"故障类别装不下码"这一族的副作用，是这一格结构上只会说"量到了"**
（`cells.go:120` 那句写死的 `BasisMeasured`）。顺带这条清点也确认了另一件事：
**`http` 那一档在册账里仍然是 0 发** —— 早轮假端点撞出过 429，但那本账没归档进册，
所以"有状态码的故障"这一档至今只有 `报告/慢闸-挂载轮结论.md` 那类合成数据撑着。

顺带记下这一发引出的**新问题**（不属于缺口 10，别混着定）：`protocol.RetrySame()` 今天是 **false**。
可断的 body 有两种成因 —— 对端就是会说这套格式之外的话（重发没用），或者写到一半断了（重发可能有用了）。
现在的表把它一刀切成"原样重发没用"，而这一发的 46 字节到底是哪一种，我们**没有第二根轴能反证**。

## 5. 复现（不花钱，本机）

```sh
# 1) 起假端点
"C:/Users/Admin/sdk/py314/python.exe" tasks/probe/fakellm.py      # 监听 127.0.0.1:8799
# 2) 照实记（预期：整发拒收，jsonl 是空的）
AGNES_BASE="http://127.0.0.1:8799/v1" PROTO_TURN=1 AGNES_KEY="fake-key-not-secret" \
  TOOLS_REPORT=$TEMP/proto-fix.txt TOOLS_BOOK=$TEMP/proto-fix.jsonl \
  "C:/Users/Admin/sdk/py314/python.exe" tasks/mcp/tools_probe.py
# 3) 让步装（预期：47 条 + 链自洽，但 fail_status 是 measured 0）
  同上再加 PROTO_WITNESS=prose
```

不设 `PROTO_TURN` 时驱动那五发一字不动，真账 `报告/样例-工具提议.jsonl` 的形状不受影响；
本轮改动只在调用方（`post()` 的 `proto_broken` 分支、`meta_of()` 的 `protocol` 装配、两个环境变量旋钮）。
key 用的是明写的假串，两份 jsonl 里 `sk-` 出现 0 次（与判定"账落盘且不带 key"各查一遍）。
