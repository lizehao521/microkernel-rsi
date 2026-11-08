# rsi · 两代改进对账（RSI 分支原创扩展）

判「这一代比上一代改进算不算数」：产物指纹、判据集、读数三件互核，
判定与两份 manifest 一起锚进内核哈希链。**消费冻结内核，不新增 49 格以外的格子；
RSI 循环本身不住在这里**——本包是循环外的对账件。

## 用法

```bash
go run ./cmd/krsi <上一代.json> <这一代.json> <报告.txt> <账本.jsonl>
```

退出码：`0=improved`（算数）`1=not_improved`（读数不支持）`2=refused`（点名哪条判据）`3=undecidable`（量不到，说理由）。
账本已存在则接着记（同一代发同一份 manifest 幂等；同一代发不同 manifest/判定 → R6 拒）。

## manifest

见 `rsi.go` 的 `Manifest`；basis 照内核整数枚举（contract §2.1）：
1=measured 2=relayed 3=estimated 4=model_says 5=absent。
absent 必须带 `why_absent`；relayed 必须带 `source`；比率读数带 `denominator`（正数）。

## 判据目录（声明 6 / 实现 6，TestCatalogMatchesImplementation 机械守）

| id | 判 | 红的形状 |
|---|---|---|
| R1 | 结构自洽 | 缺格、缺席无理由、basis 不在档、代际不增 |
| R2 | 产物真变了 | 两代指纹集相同（同字节换标签） |
| R3 | 判据没放水 | loosened/removed、指纹变了没交 diff |
| R4 | 读数可判定 | estimated/model_says 冒充证据；诚实缺席 → undecidable 不塌 |
| R5 | 改进为真 | 指标没严格变好；无分母比率；两侧都没读数 → undecidable |
| R6 | 锚定可验 | 同一代不同 manifest/判定重记；链 Verify 不过 |

## 尺子会红（反臂）

```bash
python tasks/probe/rsi_probe.py <go.exe> <本包目录> <临时工作目录> <报告输出路径>
```

针表以 `tasks/probe/rsi_probe.py` 的 `MUT` 为准（恒等放行、放水也收、拒收被截胡这一族），
每根要求点名测试真红；正对照（未造破坏）必须先绿，否则整轮作废。根数由报告自印。

## 给原系统带来什么

- 内核从「记单次调用」扩到「记代际改进声明」：生产者交两代 manifest，内核链锚定判定，
  第三方可按原像重算（PayloadIncluded）。
- 「改进算不算数」第一次有了四态不塌的机械判据：量不到（undecidable）不冒充没改进，
  不可判定（model_says/estimated）不冒充证据。
- 判据放水（loosened/removed）在门口被拒——自进化循环最贵的失败形（改尺子冒充改产品）有闸。

## 证据边界

- 示例与测试走出来的绿 = 剧本绿；真实 RSI 生产者 NOT_RUN。
- 本包判「读数支不支持改进声明」，不判产物质量本身；判据集由生产者交，R3 防放水、不防判据本来就错。
- 链证「写下来的没被动过」，不证「该写的都写了」（内核不变量 2）。