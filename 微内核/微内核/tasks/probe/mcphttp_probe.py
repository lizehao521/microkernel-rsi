#!/usr/bin/env python
# -*- coding: utf-8 -*-
"""mcphttp_probe —— MCP 这两面（`mcpsrv` + `cmd/mcphttp`）的"能红一次"记录。

为什么不塞进封板那 76 根：那 76 根是**已冻的内核判据表**的针（48 键 / 49 格，凭据
`报告/封板轮-整轮-1015-012457.txt`）。`mcpsrv`/`cmd/mcphttp` 是封板之后新长的外壳，
塞进去等于宣布那份凭据作废、重跑一轮 1.6 小时。所以它有自己的针表、**自己印的分母**
——与 `tasks/probe/kaudit_probe.py` 同一规格、同一个理由。

它扎的是两类承诺：
  · 传输层的（405 与口令无关、口令漏不漏内容、请求体上限、垃圾体不许塌成"没回话"、
    通知回 202 而不是空 200、非回环无口令不许启动）
  · 协议层的（分母不许用推出来的、工具数不许有第二个 owner、少一个轴不许照答、
    顶掉一本账要点名 acknowledge）

两条放轮前必守的规矩（这一轮就是靠它们把 3 根坏针纠回来的）：
  · **点名单要写全**：一根针只要打红了没点名的测试，就会被读成 RED-WIDER＝"读数不可信"。
    例：`h6` 把 202 改成 200，除 `TestNotificationIs202NoBody` 外还打断
    `TestBrokenArgumentsMustNotCollapseIntoNotification` 里"真通知仍必须 202 空体"那条反面断言——
    那是正当的宽，所以名单补全，而不是把针改窄。
  · **锚点漂了不修＝留一根咬不住代码的针**：它会一路报 ANCHOR-BAD/VACUOUS（`s12` 钉在零调用者的
    死封装上、`s18` 钉在已被 `out := s.route(r)` 合并掉的两行上，两根上一轮都 VACUOUS）。
    钉之前先按最终形状把每个锚点数到命中 1 次；`b'...'` 里只许出现 ASCII，中文说明放 `#` 注释。

用法（项目根）：
    C:/Users/Admin/sdk/py314/python.exe tasks/probe/mcphttp_probe.py <go.exe> <报告文件> [子串,过滤]
"""
import os
import re
import subprocess
import sys

GO = sys.argv[1] if len(sys.argv) > 1 else r"C:\Users\Admin\sdk\go\bin\go.exe"
REPORT = sys.argv[2] if len(sys.argv) > 2 else os.path.join(
    os.environ.get("TEMP", "."), "mcphttp-probe-report.txt")
FILTER = [f for f in (sys.argv[3].split(",") if len(sys.argv) > 3 and sys.argv[3] else []) if f]

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
TIMEOUT = int(os.environ.get("MCP_PROBE_TIMEOUT", "600"))
PKGS = ["./mcpsrv", "./cmd/mcphttp"]
HTTP = os.path.join("cmd", "mcphttp", "main.go")
SRV = os.path.join("mcpsrv", "mcpsrv.go")
# 10-25 起多一个取材文件：人读表头的判定搬进了内核（kernel.IsEncodeHeader），
# s13/s14 这两根钉的仍是**外壳那支往返测试**，但被改的字节在 journal.go 里 ——
# 外壳不再自己剥表头，所以"认不认"这件事只有内核一个 owner，针也就只能扎在内核那一行上。
JRNL = "journal.go"

# (名字, 文件, 原字节, 坏字节, 点名该红的测试)   —— 锚点一律纯 ASCII（Go 源码里的中文不能进 b'' 字面量）
NEEDLES = [
    ("h1-写请求先吃401", HTTP,
     b'\t\tif r.Method != http.MethodPost {',
     b'\t\tif false {',
     ["TestNonPostIs405RegardlessOfAuth"]),
    ("h2-405不告知只许POST", HTTP,
     b'\t\t\tw.Header().Set("Allow", "POST")\n', b'',
     ["TestNonPostIs405RegardlessOfAuth"]),
    ("h3-HTTP面不查口令", HTTP,
     b'\t\tif !h.auth(w, r) {',
     b'\t\tif false {',
     ["TestTokenGate"]),
    ("h4-请求体不设上限", HTTP,
     b'http.MaxBytesReader(w, r.Body, maxBody)', b'r.Body',
     ["TestOverLimitBodyRefused"]),
    ("h5-垃圾体塌成没回话", HTTP,
     b'\tif !mcpsrvLooksLikeRPC(body) {',
     b'\tif false {',
     # 10-17 补全：门被短路之后，`#44` 那笔计数自然也不动了 —— 这是正当的宽，不是漏判。
     # 老规矩是名单写全，而不是把针改窄（h6 那一轮就是这么转绿的）。
     ["TestGarbageBodyIs400", "TestDoorRefusalIsCounted", "TestBucketsDoNotMerge"]),
    # h6 的点名单上一轮漏写了一支，被读成 RED-WIDER（"红得比点名宽"）——老规矩是**名单要写全**。
    # TestBrokenArgumentsMustNotCollapseIntoNotification 里除了正面（坏体不许塌成没回话），还有一条
    # "真通知仍必须 202 空体"的反面断言（防"永远 400"骗过正面）；这根针把 202 改成 200，那条反面断言
    # 必然被打断。所以它是**正当的宽**，不是漏判，名单就地补全。
    ("h6-通知回空200", HTTP,
     b'\t\tw.WriteHeader(http.StatusAccepted)',
     b'\t\tw.WriteHeader(http.StatusOK)',
     ["TestNotificationIs202NoBody", "TestBrokenArgumentsMustNotCollapseIntoNotification"]),
    ("h7-网段无口令也起来", HTTP,
     b'\tif os.Getenv("AGENTKERNEL_TOKEN") == "" {',
     b'\tif false {',
     ["TestCheckListenRefusesPublicBindWithoutToken"]),
    ("s8-键名分母用推出来的", SRV,
     b'nIn+nOut, nIn, nOut, len(seen), joinLines(sb)',
     b'nIn+nOut, nIn, nOut, nIn+nOut, joinLines(sb)',
     ["TestCelltableAgreesWithKernelAndFreeze", "TestRPCOverHTTP"]),
    ("s9-工具数有了第二个owner", SRV,
     b'func ToolCount() int { return len(tools()) }',
     b'func ToolCount() int { return 12 }',
     ["TestToolCountIsTheSameOwnerAsTheList", "TestRPCOverHTTP"]),
    ("s10-少一个轴也照答", SRV,
     b'if strings.TrimSpace(a.Session) == "" {',
     b'if strings.TrimSpace(a.Session) == "\\x00impossible" {',
     ["TestReadBackRefusesEmptySession"]),
    ("s11-顶掉账本不点名也放行", SRV,
     b'if have := len(s.ledger.Docs()); have > 0 && a.Acknowledge != "replace" {',
     b'if have := len(s.ledger.Docs()); have > 0 && false {',
     ["TestLoadJournalNeedsAcknowledgeToReplace"]),
    # 10-16 稳定性那两条：排队与超时。它们都是我今天自己写进去的洞，
    # 所以**必须有针能让它们再红一次**，不然修等于没修。
    # 重钉说明（上一轮这根是 VACUOUS）：旧锚 `func (s *Server) TrySerialize(...) ([]byte, bool) {` 钉的是
    # 一个**零调用者的死封装**——HTTP 面只走 TrySerializeKind，针咬在改不到的代码上，永远咬不红。
    # 现在钉在唯一那份排队入口 TrySerializeKind 的签名行上：注入一个"不等锁、当场处理完就 return"的早返回，
    # 旧函数体留在早返回之后（Go 允许不可达代码）。Go 不允许「多值调用与其它值并列 return」，
    # 所以先把 HandleOutcome 的两值接住再 return，而不是 `return s.HandleOutcome(raw), true, OutcomeReply`。
    ("s12-排队不成立（直接处理）", SRV,
     b'func (s *Server) TrySerializeKind(raw []byte, wait time.Duration) ([]byte, bool, Outcome) {',
     b'func (s *Server) TrySerializeKind(raw []byte, wait time.Duration) ([]byte, bool, Outcome) { s12b, s12k := s.HandleOutcome(raw); return s12b, true, s12k',
     ["TestQueueTimeoutIs503AndCounted"]),
    ("h8-服务端超时又变回不限", HTTP,
     b'ReadHeaderTimeout: readHeaderTimeout,',
     b'ReadHeaderTimeout: 0,',
     ["TestServerTimeoutsAreSet"]),
    # 10-16 A/B 测评撞出来的第二个洞（#40）：闲置界太短会杀掉挂载自己的连接，而**进程还活着**，
    # 从外面只看得到"工具没了"。这根钉的是那个**数值量级**，不只是"非零"——
    # 上一根 h8 只钉"非零"，恰好放过了 120 秒这个杀手（这就是那条教训的形状）。
    ("h9-闲置界又变回120秒（杀挂载连接）", HTTP,
     b'idleTimeout       = 30 * time.Minute',
     b'idleTimeout       = 120 * time.Second',
     ["TestServerTimeoutsAreSet"]),
    # 10-16 测评撞出来的那条：encode 交回的表头，load 得认（正反两面各一根针）。
    # 只留一根的话，"认得太窄（读不回来）"和"认得太宽（吃掉真账）"这两种坏法只剩一种能被发现。
    # 10-25 重钉：这两根原先扎在 mcpsrv.go 的 splitEncodeHeader 上，那个函数已经删了
    # （判定搬进内核 kernel.IsEncodeHeader，见 journal.go）。锚点漂了不修＝留一根咬不住代码的针，
    # 老规矩：钉之前先按最终形状在目标文件里数到命中 1 次。
    # 判据仍是外壳那支 TestEncodeJournalTextLoadsBackVerbatim —— 它现在证的是"内核认"，
    # 因为外壳那一层已经不再自己先剥一遍了（这条往返只剩一个 owner）。
    #
    # 10-25 整组重新数过（本轮实测两份名单）："认太宽"其实是**两种**坏法，各配一根：
    #   s14 放宽成"任何非 JSON 行都算" ⇒ 真坏账被放过（只有往返那支红）；
    #   s14b 加一条"第一行永远当表头跳" ⇒ 第一条**真账**被吃掉（四支红，正是老 mutation 的那一族）。
    # 原先一根 s14 用"条件恒真"的写法一次覆盖了两种 —— 搬进内核之后分成两个可坏的位置，
    # 就照两个位置各钉一根，别再让一根针的名单去混两种坏法。
    ("s13-表头不认（往返读不回来）", JRNL,
     b'\t\tif IsEncodeHeader(line) {',
     b'\t\tif false && IsEncodeHeader(line) {',
     ["TestEncodeJournalTextLoadsBackVerbatim",
      # 宽出来的这 4 支逐名打开核过：它们都是「encode 交回的字节（第一行就是表头）写盘、
      # 再 load_journal 读回」这同一条路，恒不认 ⇒ 那份字节当场读不回来。正当的宽，名单写全。
      "TestAppendOnlyRefusesLoadButKeepsEncode", "TestEntryCapRefusesAndCountsSeparately",
      "TestGateCountsSurviveEncodeAndLoad",
      "TestNewGateDoesNotLieAboutCountersWhenFileCarriesThem"]),
    ("s14-任何非JSON行都当表头（真坏账被放过）", JRNL,
     b'return line != "" && !strings.HasPrefix(line, "{") && strings.Contains(line, EncodeHeaderMark)',
     b'return line != "" && !strings.HasPrefix(line, "{")',
     ["TestEncodeJournalTextLoadsBackVerbatim"]),
    ("s14b-第一行永远当表头跳（第一条真账被吃）", JRNL,
     b'\t\tif IsEncodeHeader(line) {',
     b'\t\tif IsEncodeHeader(line) || i == 0 {',
     ["TestEncodeJournalTextLoadsBackVerbatim", "TestLoadJournalNeedsAcknowledgeToReplace",
      "TestLoadJournalThenVerifyOverHTTP", "TestVerifyOnEmptyChainRefusesGreen",
      # 宽出来这两支逐名打开过罪名（Temp/ak-1025/s14b-全量那两段的罪名.txt）：
      # 两句都是「预检拒了但没点名是去重门 …… 这份账的副本造不出来（kernel: 读回来的账本链不符：
      # 第 1 条 seq=2 不连续（中间有被抽走的记录））」—— 正是这一根针要制造的那条坏法本身，
      # 不是旁路噪声：第一条真账被吃掉 ⇒ 链从 seq=2 开头。名单写全。
      "TestCutsAbsentVersusEmptyAreTwoSentencesOnTheWire", "TestPreCheckPredictsDedupGate"]),
    # #42 的两面：认得太窄（整块没给照收）与认得太宽（谁都进不来）。
    # 只留一根的话，另一头的坏法就没人能反向破坏它。
    ("s15-整块没给被当成填了零值", SRV,
     b'if _, has := m[k]; !has {',
     b'if _, has := m[k]; false && !has {',
     ["TestShellDoesNotInventMissingBlocks"]),
    ("s16-存在性检查写宽成永远拒", SRV,
     b'if _, has := m[k]; !has {',
     b'if _, has := m[k]; !has || true {',
     # 10-17 补全：存在性检查恒拒之后，预检那两支的**基线**（合法一份必须先过）也正当被打断 ——
     # 多红的是新加的 #20 两支，不是针咬宽了。老规矩：名单写全。
     ["TestShellDoesNotInventMissingBlocks",
      "TestCheckCallRefusalIsMachineReadable", "TestPreCheckPredictsDedupGate",
      # 25 根那轮补的第四支：#46 那支也有"合法一份必须先过"的基线，恒拒一定把它打断 —— 正当的宽。
      "TestUnknownWriteKeysAreRefusedAndNamed"]),
    # #41：「解不开」与「真通知」塌成同一个 nil，于是坏请求拿到 202 空正文。
    # 两根各钉一头：塌回去（坏请求又变哑）、和把通知也当错误（客户端再也发不了通知）。
    ("s17-解不开的请求又塌成nil", SRV,
     b'\tif err := json.Unmarshal(raw, &r); err != nil {',
     b'\tif err := json.Unmarshal(raw, &r); false && err != nil {',
     ["TestHandleSeparatesParseErrorFromNotification",
      "TestBrokenArgumentsMustNotCollapseIntoNotification"]),
    # 锚点两次跟着那次拆分挪过位；而且第一版把中文注释写进了 b'...' —— 直接
    # SyntaxError: bytes can only contain ASCII literal characters（这坑在项目记忆里写着，我照踩）。
    # 现在锚 ASCII 那两行，且整文件里唯一（命中数 =1，探针会替你盯住不等于 1 的情况）。
    # 重钉说明（上一轮这根也是 VACUOUS）：旧锚 `s.route(r)\n\t\treturn nil, OutcomeNone` 钉的那两行已经不存在了
    # ——`route` 现在在 `out := s.route(r)` 处只被调一次，通知的真分类只剩这一个地方：`if out == nil` ⇒ OutcomeNone。
    # 这根针钉的就是"把真通知也当错误回"这种坏法（分类算反了，客户端再也发不了通知）。
    # 这根自己就是那条教训的第二次上演：上一轮它 VACUOUS（锚钉在两行已被合并的形状上），这一轮按
    # "通知的真分类处"重钉成两行锚 —— 结果 node1 收尾时往 `if out == nil {` 和 `return nil, OutcomeNone`
    # **中间插了两行注释**，两行锚当场断成命中 0（本轮只读复核实测：两行锚 0 次、单行 1 次）。
    # 所以钉成**一行**、并且只钉那个返回值：它是全文件唯一的 `return nil, OutcomeNone`，
    # 上面漂多少注释都咬得住；注释里也别再写死行号（行号会变，形状不会）。
    # arity 跟着 `parseErrorReply` 的最终签名（id, why, code）写三参数 —— 写两参数这根会读成
    # BUILD-BROKE 而不是红，那正好把"针咬不住"伪装成"代码坏了"。
    ("s18-真通知也被当错误回", SRV,
     b'return nil, OutcomeNone',
     b'return parseErrorReply(r.ID, "x", -32600), OutcomeBad',
     ["TestHandleSeparatesParseErrorFromNotification",
      "TestBrokenArgumentsMustNotCollapseIntoNotification", "TestNotificationIs202NoBody",
      # 22 根那轮读成 RED-WIDER 多出的两支，核过都是**正当的宽**（都判"通知没有回话"这同一件事）：
      # TestNotificationHasNoReply 走 `Handle`（nil 才叫没回话），TestRequestWithoutMethodIsInvalidNotNotification
      # 的反面断言判的是"真通知仍须 nil + OutcomeNone"。把通知改成有回话，这两支必然红 ⇒ 名单补全。
      "TestNotificationHasNoReply", "TestRequestWithoutMethodIsInvalidNotNotification"]),
    # 状态码那一头单独立一根：分类算对了、HTTP 没跟上，客户端看到的仍然是一次成功的传输。
    # （规范原话：收下这条输入失败 ⇒ MUST return an HTTP error status code。）
    ("s19-协议层判无效的请求状态码又回200", HTTP,
     b'if kind == mcpsrv.OutcomeBad {',
     b'if false && kind == mcpsrv.OutcomeBad {',
     ["TestBrokenArgumentsMustNotCollapseIntoNotification"]),
    # 第 22 根（#41 落地时我自己犯的那第二次错）：`r.Method == ""` 曾被当成"通知"送去 route，
    # 于是无效请求安静地拿一个 202 —— 通知是"有 method、无 id"那种，没有 method 的根本不是请求。
    # 这根钉在新写的"没有 method ⇒ 无效请求"那一支的判别式上：把它短路掉，这条消息就掉进 route 的
    # default（拿到 200／202 而不是 400），测试必须因此报红；针咬不到的形状就是又把它塌回静默。
    # 名单暂点名协议层那一支。若测试文件最终把"没有 method ⇒ 400"那段也写进
    # TestBrokenArgumentsMustNotCollapseIntoNotification（HTTP 面，计划 C 让它加），这根会同时打断它，
    # 那时按老规矩把它一并列上。
    # 名单两支都点：协议层那支（TestRequestWithoutMethodIsInvalidNotNotification）之外，
    # HTTP 面那段"无 method ⇒ 400 + 点名 method"也确实会被这根打断 —— 上一轮 h6 就是漏写名单读成
    # RED-WIDER 的，老规矩是**名单写全**，而不是把针改窄。
    # 名单两支都点，但 22 根那轮教过我它们**不是同一回事**：那时 HTTP 面发的是 `{"id":5}`（连 "method"
    # 键都没有），被传输层的 mcpsrvLooksLikeRPC 先挡成 400，分类那一支根本没执行，于是这根读成
    # "没红的点名"—— 测试绿着，却证明不了分类。现在测试改发 `{"method":""}`（有键、空串，过了门），
    # 这根短路掉 `r.Method == ""` 就会让它真的红。
    ("s20-没有method又被当通知", SRV,
     b'if r.Method == "" {',
     b'if false && r.Method == "" {',
     ["TestRequestWithoutMethodIsInvalidNotNotification",
      "TestBrokenArgumentsMustNotCollapseIntoNotification"]),
    # s21/s22 是 10-17 那一对（#20 结构化 verdict、#44 门拒计数）的针。来历不是"新功能配一根"，
    # 是说谎者靶场 A0 量到的形状：判据拒了、正文点名了，而机器读的那一位仍是 false / 那一笔根本没数。
    # 名单先按"能红的最小集合"写，红得更宽就照上一轮 h6 的老规矩补全名单，而不是把针改窄。
    ("s21-预检的布尔位又写死成false", SRV,
     b'return text(r.ID, msg, bad)',
     # 坏字节留着 `bad` 这个变量（`bad && false`）：写成 `text(r.ID, msg, false)` 会让它变成
     # "声明了没用"，那一趟编译直接死 —— 崩不算红，10-16 那两根 BUILD-BROKE 就是这么来的。
     b'return text(r.ID, msg, bad && false)',
     ["TestCheckCallRefusalIsMachineReadable", "TestPreCheckPredictsDedupGate"]),
    ("s22-门挡下的又不计数", HTTP,
     b'n := h.srv.NoteTransportRefusal()',
     b'n := int64(0)',
     ["TestDoorRefusalIsCounted", "TestBucketsDoNotMerge"]),
    # s23：#46 的那道解码闸。坏字节换成一个**照样能编译**的合法选项（`UseNumber`），
    # 于是未知键重新静默落空 —— 针不能靠把代码改坏来"红"（崩不算红，10-16 那两根 BUILD-BROKE 记着呢）。
    ("s23-写侧未知键又开始静默落空", SRV,
     b'dec.DisallowUnknownFields()',
     b'dec.UseNumber()',
     ["TestUnknownWriteKeysAreRefusedAndNamed"]),
    # s24：#49 的针。坏字节把第三笔的话术换成与第五笔同一句 —— 两桶共用一句话时，
    # 读 tally 的人答不出「是谁拒的」，这正是这个项目最不肯让的一件事。
    # 锚点用转义字节（中文进不了 b'' 字面量），命中须是 1 处。
    # 10-17 原锚「条请求被协议层判成无效」在 mcpsrv.go 里命中 **2** 处（tally 那句与 parseErrorReply
    # 的兜底句），而落破坏用的是 replace(...,1) —— 只改第一处。今天第一处恰好是 tally，所以这根针测的
    # 仍是它名字说的那件事；但两处顺序一挪，它会静悄悄改到回话那句、名字与实效分家。
    # 所以锚前缀补成 '%d '（tally 用 %d 渲染，回话用「这条」），命中数回到 1。
    # 补前缀要 old/new 一起补：只补 old 那一侧，落完破坏就等于把 tally 里的一个 %d 吃掉，
    # 这根针测的就换成「格式动词与参数对不上」那件事（本轮实测：那样只红 3 条，点名第 4 条不动）。
    ("s24-两桶又共用同一句话", SRV,
     b'%d \xe6\x9d\xa1\xe8\xaf\xb7\xe6\xb1\x82\xe8\xa2\xab\xe5\x8d\x8f\xe8\xae\xae\xe5\xb1\x82\xe5\x88\xa4\xe6\x88\x90\xe6\x97\xa0\xe6\x95\x88',
     b'%d \xe6\x9d\xa1\xe8\xaf\xb7\xe6\xb1\x82\xe8\xbf\x9e\xe5\x8d\x8f\xe8\xae\xae\xe5\xb1\x82\xe9\x83\xbd\xe6\xb2\xa1\xe5\x88\xb0',
     ["TestBucketsDoNotMerge", "TestBrokenArgumentsMustNotCollapseIntoNotification", "TestDoorRefusalIsCounted", "TestHandleSeparatesParseErrorFromNotification"]),
]

lines, console = [], []


def w(s):
    lines.append(s)


def say(s):
    console.append(s)


def run(args):
    env = dict(os.environ)
    env["PATH"] = os.path.dirname(GO) + os.pathsep + env.get("PATH", "")
    try:
        p = subprocess.run([GO] + args, cwd=ROOT, capture_output=True, text=True,
                           encoding="utf-8", errors="replace", timeout=TIMEOUT)
        return p.returncode, (p.stdout or "") + (p.stderr or "")
    except subprocess.TimeoutExpired:
        return 124, "probe-timeout: %d 秒没跑完（不是绿）" % TIMEOUT


def testmap(out):
    return sorted(set(re.findall(r"^--- FAIL: (\S+)", out, re.M)))


def verdicts(out):
    return bool(re.search(r"^--- (PASS|FAIL): ", out, re.M))


def broke(out):
    """Go 对"包内坏了"和"测试装配坏了"印两个不同标记，只认一个的那一个会伪装成干净。"""
    return "[build failed]" in out or "[setup failed]" in out


originals = {}
for f in {n[1] for n in NEEDLES}:
    originals[f] = open(os.path.join(ROOT, f), "rb").read()

w("==== 对照 ====")
code, out = run(["test", "-p", "1", "-count", "1", "-timeout", "120s", "-v"] + PKGS)
npass = len(re.findall(r"^--- PASS: ", out, re.M))
nfail = len(re.findall(r"^--- FAIL: ", out, re.M))
w("对照 0（未改动的树）：go test 退出码 %d、PASS %d 条、FAIL %d 条（该 0 / >0 / 0）" % (code, npass, nfail))
baseline_ok = code == 0 and nfail == 0 and npass > 0
if not baseline_ok:
    w("!! 干净树上就没全过 —— 这一轮不算，先修树。")
    for ln in out.splitlines():
        if ".go:" in ln or "build failed" in ln or ln.startswith("--- FAIL"):
            w("   " + ln[:200])
            break
    open(REPORT, "w", encoding="utf-8").write("\n".join(lines) + "\n")
    for c in console:
        print(c)
    print("BASELINE NOT GREEN -> abort")
    sys.exit(2)

# 对照 T：弄坏一个测试文件，脚本必须认出"测试压根没跑起来"。
TST = os.path.join(ROOT, "mcpsrv", "mcpsrv_test.go")
t_orig = open(TST, "rb").read()
open(TST, "wb").write(t_orig + b"\nfunc brokenSyntax( {\n")
code_t, out_t = run(["test", "-p", "1", "-count", "1"] + PKGS)
open(TST, "wb").write(t_orig)
t_restored = open(TST, "rb").read() == t_orig
w("对照 T（弄坏测试文件）：退出码 %d（该非 0）、认出坏了标记=%s、有 PASS/FAIL 判决行=%s（该 True / False）、修回一致=%s"
  % (code_t, broke(out_t), verdicts(out_t), t_restored))
control_t_ok = code_t != 0 and broke(out_t) and not verdicts(out_t) and t_restored
w("")

sel = [n for n in NEEDLES if not FILTER or any(n[0].startswith(p) for p in FILTER)]
if FILTER and len(sel) != len(NEEDLES):
    w("!! 这一轮**筛过针**（只跑 %d 根，表里共 %d 根）—— 它不能当整轮结论用" % (len(sel), len(NEEDLES)))
w("")

results = []
touched = 0
# 本轮**真改过**的文件。取材那句原来数的是 `originals`（整个 NEEDLES 表里出现过的文件），
# 筛过针的那一轮就跟着说「只改 3 个源文件」—— 而那一轮实际只动了 journal.go 一个。
# 一句自己印的取材话也不能对被筛掉的轮子说谎（和"针数不是整轮分母"同一族）。
ran_on = []
dirty = False
notsingle = []
for name, rel, old, new, named in sel:
    path = os.path.join(ROOT, rel)
    cur = open(path, "rb").read()
    if cur != originals[rel]:
        w("!! %s 起手时 %s 不干净（上一根针没修回去）—— 这一轮不算" % (name, rel))
        results.append((name, "TREE-DIRTY", ""))
        dirty = True
        break
    hits = cur.count(old)
    if hits == 0:
        w("\n== %s  锚点在 %s 里找不到（形状变了，这根针已失效）" % (name, rel))
        results.append((name, "ANCHOR-BAD", ""))
        continue
    if hits != 1:
        # 不判红、不改分类，只把形状印出来：命中多于 1 时「这根针改的是不是它名字说的那一处」
        # 就成了没人核的假设 —— README 要求钉前人工数锚，这里是它的机器版（只报数，不改判）。
        notsingle.append("%s（%s 里命中 %d 处）" % (name, rel, hits))
    open(path, "wb").write(cur.replace(old, new, 1))
    touched += 1
    if rel not in ran_on:
        ran_on.append(rel)
    code, out = run(["test", "-p", "1", "-count", "1", "-timeout", "120s", "-v"] + PKGS)
    open(path, "wb").write(originals[rel])
    if open(path, "rb").read() != originals[rel]:
        w("!! %s 修回后字节不一致" % name)
        results.append((name, "RESTORE-BAD", ""))
        dirty = True
        break
    failed = testmap(out)
    if not verdicts(out):
        status = "BUILD-BROKE" if broke(out) else "TEST-BROKE(没跑成)"
    elif code == 0 and not failed:
        status = "VACUOUS"
    elif set(failed) == set(named):
        status = "RED-ONLY-NAMED"
    else:
        status = "RED-WIDER"
    w("\n== %s  [%s]" % (name, rel))
    w("   点名该红：%s" % ", ".join(named))
    w("   实际报红：%s" % (", ".join(failed) if failed else "无"))
    if status == "RED-WIDER":
        extra = sorted(set(failed) - set(named))
        miss = sorted(set(named) - set(failed))
        w("   多出点名的：%s ｜ 没红的点名：%s" % (", ".join(extra) or "无", ", ".join(miss) or "无"))
    w("   全量共红 %d 条" % len(failed))
    results.append((name, status, str(code)))

left = [f for f in originals if open(os.path.join(ROOT, f), "rb").read() != originals[f]]
if left:
    w("!! 收尾时这些文件还不干净：%s" % ", ".join(left))
    dirty = True

w("\n==== 汇总 ====")
for name, status, extra in results:
    w("%-30s %-16s exit=%s" % (name, status, extra))
kinds = {}
for _, status, _ in results:
    kinds[status] = kinds.get(status, 0) + 1
w("")
w("针数 %d（表里 %d，本轮实际跑 %d）；分类：%s"
  % (len(NEEDLES), len(NEEDLES), len(results),
     "、".join("%s %d" % (k, v) for k, v in sorted(kinds.items()))))
w("锚不唯一的针 %d 根%s（replace 只动第一处）"
  % (len(notsingle), "：" + "、".join(notsingle) if notsingle else "（无 —— 每根针的锚在各自文件里都只命中 1 处）"))
w("取材方式：本轮真改了 %d 个源文件（%s），每根针后按原字节修回（累计改过 %d 次）；"
  "整张表涉及 %d 个文件（筛过针时这个数不作数，只说明表里都有哪些文件）"
  % (len(ran_on), "、".join(sorted(ran_on)) if ran_on else "（无 —— 没有一根针真的动了文件）",
     touched, len(originals)))
w("对照 0（干净树全过）：%s；对照 T（认得出测试没跑起来）：%s"
  % ("成立" if baseline_ok else "不成立", "成立" if control_t_ok else "不成立"))
bad = sum(kinds.get(k, 0) for k in ("VACUOUS", "ANCHOR-BAD", "BUILD-BROKE", "TEST-BROKE(没跑成)",
                                    "TREE-DIRTY", "RESTORE-BAD",
                                    # 10-16 22 根那轮的读数逼出来的：RED-WIDER 以前不进 bad，
                                    # 于是"红得比点名宽"两轮都印过那句"无空针、无坏针"并退出 0。
                                    # 项目自己的口径是**名单没写全＝这一轮读数不可引用**（s14 那次就是
                                    # 靠"补全名单再重跑"才转绿的），那它就不该读成绿。宁可停下补名单。
                                    "RED-WIDER"))
green = (not bad) and control_t_ok and len(results) == len(NEEDLES) and not dirty
if not green:
    w("!! 有坏针、红得比点名宽、对照不成立或树没修干净 —— 这一轮不绿，退出码 1")
else:
    w("每条承诺都有一根针能把它抹红，且只抹红点名的那些；无空针、无坏针、无宽红，两根对照都成立、收尾树是干净的。")
open(REPORT, "w", encoding="utf-8").write("\n".join(lines) + "\n")

for c in console:
    print(c)
print("NEEDLES=%d %s" % (len(results), " ".join("%s=%d" % (k, v) for k, v in sorted(kinds.items()))))
print("REPORT=" + REPORT)
sys.exit(0 if green else 1)
