import os, re, shutil, subprocess, sys, io, time

GO, SRC, WORK, OUT = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
# 第 5 个参数是**可选**的针名过滤器（逗号分隔的子串）。没有它整轮照旧全跑。
# 为什么要有：锚点漂了两根针就得等 62 根重跑一遍（每针一次全量 go test），
# 而那两分钟里机器什么也干不了。筛过的轮子在报告头上会明写「不算整轮结论」。
ONLY = [s for s in (sys.argv[5] if len(sys.argv) > 5 else "").split(",") if s.strip()]

MUT = [
    ("m13b-份额超过100也放行", "input.go",
     [('if sum > 100 {\n\t\treturn Budget{}, fmt.Errorf("kernel: %d 类段的份额相加是 %d，超过 100',
       'if false && sum > 100 {\n\t\treturn Budget{}, fmt.Errorf("kernel: %d 类段的份额相加是 %d，超过 100')],
     ["TestDeriveBudgetIsTheOnlyOwnerOfTheFormula"]),
    ("m20-预算公式不复核", "input.go",
     [("/ 100; b.ContentCeiling != want {", "/ 100; b.ContentCeiling != b.ContentCeiling {")],
     ["TestHandBuiltBudgetIsRefused"]),
    ("m21-回夹不必真收小", "input.go",
     [("if effective >= b.EffectiveCeiling {", "if false && effective >= b.EffectiveCeiling {")],
     ["TestClampMustActuallyShrink"]),
    ("m22-协议也允许裁", "input.go",
     [("case KindObserved, KindHistory, KindMemory, KindUser, KindNotice:",
       "case KindObserved, KindHistory, KindMemory, KindUser, KindNotice, KindSystem, KindSkill, KindToolSpec:")],
     ["TestOnlySystemIsWholeOrNothing"]),
    ("m23-token白名单变黑名单", "output.go",
     [("if !m.Absent && m.Basis != BasisRelayed {", "if false && !m.Absent && m.Basis != BasisRelayed {")],
     ["TestUsageIsRelayedNotMeasured", "TestTokenTotalOnlySumsTokens", "TestCheckCallRefusalIsMachineReadable"]),
    ("m24-没数到冒充没发出去", "output.go",
     [("return SentUnknown", "return SentNothing")],
     ["TestSentIsThreeState"]),
    ("m25-两个字节量又共用一个键名", "cells.go",
     [('cell("content_bytes", "ContentBytes"', 'cell("part_totals", "ContentBytes"')],
     ["TestPartBytesMustSumToContentBytes", "TestCallNumberStartsAtOne"]),
    ("m26-拼错的读者声明也收", "cells.go",
     [("if !found {", "if false && !found {")],
     ["TestUnreadCellsAreCountedAgainstADenominator"]),
    ("m27-框架模块前缀不匹配", "isolate.go",
     [('if path == m || strings.HasPrefix(path, m+"/") {', 'if false && (path == m || strings.HasPrefix(path, m+"/")) {')],
     ["TestIsolationGoesRedOnALeak", "TestClassifyImportEdgeShapes"]),
    ("m28-cgo算成标准库", "isolate.go",
     [('if path == "C" {', 'if false && path == "C" {')],
     ["TestCgoIsNotCountedAsStdlib", "TestClassifyImportEdgeShapes"]),
    ("m29-不扫子目录", "isolate.go",
     [("if d.IsDir() {\n\t\t\treturn nil\n\t\t}", "if d.IsDir() {\n\t\t\treturn filepath.SkipDir\n\t\t}")],
     ["TestSubdirectoriesAreScanned", "TestSelfIsolation"]),
    ("m30-裁剪表与段表不互核", "input.go",
     [("if k := kept[p.Kind]; k > p.Bytes {", "if k := kept[p.Kind]; false && k > p.Bytes {")],
     ["TestCutsCannotOutliveTheirPartRow"]),
    ("m31-通知说了却没正文也放行", "input.go",
     [('if m.Notice.Sent && strings.TrimSpace(m.Notice.Text) == "" {',
       'if false && m.Notice.Sent && strings.TrimSpace(m.Notice.Text) == "" {')],
     ["TestNoticeCannotContradictItself"]),
    ("m32-顺序表与段表个数不互核", "input.go",
     [("if ordered[p.Kind] != p.Parts {", "if false && ordered[p.Kind] != p.Parts {")],
     ["TestOrderMustMatchPartCounts"]),
    ("m33-故障说法带空白也放行", "output.go",
     [("if strings.TrimSpace(f.Detail) != f.Detail {", "if false && strings.TrimSpace(f.Detail) != f.Detail {")],
     ["TestFailureNeedsADetailAndOnlyHTTPCarriesAStatus"]),
    ("m34-对非流式也建议换非流式", "output.go",
     [("if m.Failure.Downgrade && !m.Streaming {", "if false && m.Failure.Downgrade && !m.Streaming {")],
     ["TestRetrySameAndDowngradeAreTwoQuestions"]),
    ("m35-模型id自相矛盾也放行", "output.go",
     [('if m.ModelSaid != (m.Model != "") {', 'if false && m.ModelSaid != (m.Model != "") {')],
     ["TestSaidVersusEmptyString"]),
    ("m36-连歪计数与这一发不互核", "output.go",
     [("if m.Malformed == 0 && m.MalformedStreak != 0 {", "if false && m.Malformed == 0 && m.MalformedStreak != 0 {")],
     ["TestMalformedCountAndStreakAreTwoQuantities"]),
    # m69/m70 判的是「能不能带状态码」这根轴（与 RetrySame 分开住的那张表）。
    # 这两根的锚点在 2026-10-13 那一轮换过一次：原来盯的是 `kind != http ⇒ status = 0`
    # 那两条按类别的互核 —— 它们被一发真 401 与一发 200 坏 body 各撞开一次之后删掉了
    # （报告/假key一发-401.md、报告/协议档一发-200坏body.md）。
    ("m69-没收到响应的类别也许带状态码", "output.go",
     [("if !f.Kind.MayCarryStatus() && f.Status.Decidable() {",
       "if false && !f.Kind.MayCarryStatus() && f.Status.Decidable() {")],
     ["TestFailureNeedsADetailAndOnlyHTTPCarriesAStatus"]),
    ("m70-收到过响应却不要求状态码", "output.go",
     [("if f.Kind.MayCarryStatus() && !f.Status.Decidable() {",
       "if false && f.Kind.MayCarryStatus() && !f.Status.Decidable() {")],
     ["TestFailureNeedsADetailAndOnlyHTTPCarriesAStatus"]),
    ("m71-越界的状态码也收（0 又成了默认长相）", "output.go",
     [("if f.Status.Decidable() && (f.Status.N < 100 || f.Status.N > 599) {",
       "if false && f.Status.Decidable() && (f.Status.N < 100 || f.Status.N > 599) {")],
     ["TestFailureNeedsADetailAndOnlyHTTPCarriesAStatus"]),
    ("m72-格式那一格说报了却报的是空串", "output.go",
     [('if m.WireFormatSaid != (m.WireFormat != "") {', 'if false && m.WireFormatSaid != (m.WireFormat != "") {')],
     ["TestWireFormatSaidMatchesAndNeedsAResponse"]),
    ("m73-没收到响应也能转述对端自报了格式", "output.go",
     [("if m.WireFormatSaid && !m.Failure.Kind.MayCarryStatus() && m.Failure.Kind != FailNone {",
       "if false && m.WireFormatSaid && !m.Failure.Kind.MayCarryStatus() && m.Failure.Kind != FailNone {")],
     ["TestWireFormatSaidMatchesAndNeedsAResponse"]),
    ("m74-通知说发了而段表里没有那一段", "input.go",
     [("if m.Notice.Sent && !noticeInParts {", "if false && m.Notice.Sent && !noticeInParts {")],
     ["TestNoticeSentIsPinnedToThePartTable"]),
    ("m75-段表里有通知段却不许标成没发", "input.go",
     [("if !m.Notice.Sent && noticeInParts {", "if false && !m.Notice.Sent && noticeInParts {")],
     ["TestNoticeSentIsPinnedToThePartTable"]),
    ("m76-挤掉一段历史不指来自第几发", "input.go",
     [("if c.RefCall <= 0 {", "if false && c.RefCall <= 0 {")],
     ["TestHistoryCutMustNameItsCall"]),
    ("m77-历史裁剪可以指向未来那一发", "input.go",
     [("if c.RefCall >= m.Call {", "if false && c.RefCall >= m.Call {")],
     ["TestHistoryCutMustNameItsCall"]),
    # m78/m79 钉的是**格子那一层**，不是判据那一层：
    # 把②③在 cells.go 里改回写死（fail_status 写 measured、wire_format 写 measured），
    # `Failure.check()` 一个字都没动，判据测试全绿 —— 只有这两根针会红。
    ("m78-状态码那一格又写死成实测", "cells.go",
     [('measureCell("fail_status", "Failure.Status", m.Failure.Status),',
       'cell("fail_status", "Failure.Status", m.Failure.Status.N, BasisMeasured),')],
     ["TestFailureCellsFollowTheStatusAxis"]),
    ("m79-对端自报的格式被记成我们量的", "cells.go",
     [('relayedCell("wire_format", "WireFormat", m.WireFormat, SourceEndpoint),',
       'cell("wire_format", "WireFormat", m.WireFormat, BasisMeasured),')],
     ["TestFailureCellsFollowTheStatusAxis"]),
    # m80/m81/m82 是写封板报告时自查补上的：那三支判据当时**有测试、无针**
    # （报告/封板报告-v1.md 第 5b 节）。口径①的第二半句「fail_detail 的出处跟着这张表」
    # 住的是 cells.go 里另一个函数，m78 拆的是 fail_status，碰不到它。
    ("m80-抄对端原话的那格又标成我们量的", "cells.go",
     [('relayedCell("fail_detail", "Failure.Detail", m.Failure.Detail, SourceEndpoint)',
       'cell("fail_detail", "Failure.Detail", m.Failure.Detail, BasisMeasured)')],
     ["TestFailureCellsFollowTheStatusAxis"]),
    ("m81-说是没故障却带着说法或状态码也放行", "output.go",
     [("if f.Kind == FailNone && (f.Detail != \"\" || f.Status.Decidable()) {",
       "if false && f.Kind == FailNone && (f.Detail != \"\" || f.Status.Decidable()) {")],
     ["TestFailureNeedsADetailAndOnlyHTTPCarriesAStatus"]),
    ("m82-知不知道对端自报格式被记成对端说的", "cells.go",
     [('cell("wire_format_said", "WireFormatSaid", m.WireFormatSaid, BasisMeasured),',
       'relayedCell("wire_format_said", "WireFormatSaid", m.WireFormatSaid, SourceEndpoint),')],
     ["TestFailureCellsFollowTheStatusAxis"]),
    # m83/m84：一本都还没有时不许念成"核过了、都对得上"（10-15 用户表态后落地的，
    # 实例见 报告/已落地-空链不许打勾.txt：工具面第一次 verify 读到 `✓ 链自洽（共 0 条…）`、
    # mcphttp_up.sh status 读到 `账 0 条（）`）。两根针落在同一个测试上不是重复：
    # 那是**两种渲染**（链的结论行 / 分桶行），各自被抹掉都得红。
    ("m83-空链戴着通过的那个勾", "journal.go",
     [("if r.Entries == 0 {", "if false && r.Entries == 0 {")],
     ["TestNothingBookedDoesNotWearGreen"]),
    ("m84-空账的分桶行留着空括号", "kernel.go",
     [("if t.Total == 0 {", "if false && t.Total == 0 {")],
     ["TestNothingBookedDoesNotWearGreen"]),
    ("m38-说话人不核", "input.go",
     [("if !c.Role.Known() {", "if false && !c.Role.Known() {")],
     ["TestCutNeedsRefRoleAndAFullFingerprint"]),
    ("m39-指纹只在一侧被守", "input.go",
     [("if len(s) != 64 {", "if false && len(s) != 64 {")],
     ["TestCutNeedsRefRoleAndAFullFingerprint", "TestSaysFingerprintAndKeptBytes"]),
    ("m40-空段表也放行", "input.go",
     [("if len(m.Parts) == 0 {", "if false && len(m.Parts) == 0 {")],
     ["TestEmptyPartsOrOrderIsRefused"]),
    ("m41-认不出模块名也静默放过", "isolate.go",
     [('if selfModule == "" {', 'if false && selfModule == "" {')],
     ["TestMissingToolchainIsReportedNotFaked"]),
    ("m42-生效额度可以大于公式额度", "input.go",
     [("if b.EffectiveCeiling > b.ContentCeiling {", "if false && b.EffectiveCeiling > b.ContentCeiling {")],
     ["TestHandBuiltBudgetIsRefused"]),
    ("m18b-份额那一格不排序", "input.go",
     [("\tsort.Strings(out)\n\treturn out\n}", "\tsort.Slice(out, func(i, j int) bool { return false })\n\treturn out\n}")],
     ["TestCellOrderIsStable"]),
    ("m43-反函数取整方向写反", "input.go",
     [("return (content*100 + int64(99-b.TransportSlackPct)) / int64(100-b.TransportSlackPct), nil",
       "return content*100 / int64(100-b.TransportSlackPct), nil")],
     ["TestObservationShareAndItsReverseAgree"]),
    ("m44-空键名表也算过", "cells.go",
     [("func CheckCells(side string, v any, cells []Cell) error {\n\tif len(cells) == 0 {",
       "func CheckCells(side string, v any, cells []Cell) error {\n\tif false && len(cells) == 0 {")],
     ["TestCellCountCarriesADenominator"]),
    ("m45-派生格不必写成方法", "cells.go",
     [('if strings.HasSuffix(path, "()") {', 'if false && strings.HasSuffix(path, "()") {')],
     ["TestDerivedCellMustBeWrittenAsAMethod"]),
    ("m46-不可信却不是缺席也放行", "measure.go",
     [("if m.Refused {\n\t\treturn fmt.Errorf", "if false && m.Refused {\n\t\treturn fmt.Errorf")],
     ["TestRefusedAndAbsentCannotImpersonateEachOther"]),
    ("m47-不可信渲染成没量到", "measure.go",
     [("\t\tif m.Refused {\n\t\t\ts =", "\t\tif false && m.Refused {\n\t\t\ts =")],
     ["TestDistrustedKeepsTheNumberItRefused"]),
    ("m48-分档表漏一档也不报", "input.go",
     [("if _, ok := seen[s]; !ok {", "if _, ok := seen[s]; false && !ok {")],
     ["TestCutListsTableCanBeCaughtBeingWrong"]),
    ("m49-空回话算正常收尾", "output.go",
     [("if m.Says.Bytes == 0 && m.ToolCalls == 0 && m.Failure.Kind == FailNone {",
       "if false && m.Says.Bytes == 0 && m.ToolCalls == 0 && m.Failure.Kind == FailNone {")],
     ["TestEmptyReplyIsNotANormalFinish"]),
    ("m51-空顺序表也放行", "input.go",
     [("if len(m.Order) == 0 {", "if false && len(m.Order) == 0 {")],
     ["TestEmptyPartsOrOrderIsRefused"]),
    ("m52-预算挡下可以没数到", "output.go",
     [("if m.WireBytesOut.Absent {", "if false && m.WireBytesOut.Absent {")],
     ["TestSentIsThreeState"]),
    # —— 本轮 M3 新增：record.go / kernel.go 那一层（元数据到账本的接线），此前一根针都没有 ——
    # 规矩1：plan 用 checkRecord 先过一遍（守「plan 放过的 commit 一定收」）
    ("m53-plan不再用账本判据先过一遍", "record.go",
     [("\t\tif err := checkRecord(rec); err != nil {", "\t\tif err := checkRecord(rec); false && err != nil {")],
     ["TestPlanRefusesWhatAppendWouldRefuse"]),
    # 规矩2：alreadyRecorded —— 同一发同一侧只许记一次
    ("m54-同一发同一侧可以记两遍", "record.go",
     [("\tif l.alreadyRecorded(session, step, kind) {", "\tif false && l.alreadyRecorded(session, step, kind) {")],
     ["TestSameCallCannotBeRecordedTwice", "TestNoHalfCallEvenWhenTranslationFailsLate"]),
    # 规矩3：RecordCall 两侧都 Check 过才动手（输入侧坏 ⇒ 输出侧也不写）
    ("m55-RecordCall不先核输入侧", "record.go",
     [("\tif err := in.Check(); err != nil {", "\tif err := in.Check(); false && err != nil {")],
     ["TestRecordCallNeverLeavesHalfACallOnTheLedger", "TestCheckCallRefusalIsMachineReadable"]),
    # 规矩4：RecordCall 两侧发号必须一致
    ("m56-两侧发号对不上也收", "record.go",
     [("\tif in.Call != out.Call {", "\tif false && in.Call != out.Call {")],
     ["TestRecordCallNeverLeavesHalfACallOnTheLedger"]),
    # 规矩5：claimOf 对空字符串不交出空串（emptyClaim）
    ("m57-claimOf把空串原样交回", "record.go",
     [("\t\tif x == \"\" {", "\t\tif false && x == \"\" {")],
     ["TestClaimOfNeverHandsBackAnEmptySentence", "TestClaimsReadBackAsSentencesComputedFromTheFixture"]),
    # 规矩6：basisOf 把缺席的 Measure 翻译成 BasisAbsent（不是 0、不是 Unset）
    ("m58-缺席不翻成absent档", "record.go",
     [("\t\t\treturn BasisAbsent, m.String(), nil", "\t\t\treturn BasisMeasured, m.String(), nil")],
     ["TestBasisOfRefusesEveryShapeOfBasisLying", "TestAbsentCellsLandOnTheLedgerAsAbsentNotZero", "TestTallyCountsRecordedAbsencesIntoTheAbsentBucket"]),
    # 规矩7：AbsentKeys 只挑 BasisAbsent 那一档
    ("m59-AbsentKeys不只挑absent档", "record.go",
     [("\tfor _, c := range CellsOf(docs, session, step, kind) {\n\t\tif c.Basis == BasisAbsent {",
       "\tfor _, c := range CellsOf(docs, session, step, kind) {\n\t\tif c.Basis != BasisAbsent {")],
     ["TestAbsentCellsLandOnTheLedgerAsAbsentNotZero"]),
    # 规矩8：CellsOf 按 session + step + <kind>: 前缀三个轴过滤（不串发、不串侧、不串账本）。
    # 这一根只拆「发号与前缀」那两根轴 —— session 那一轴由 j10 单独守，两根不许重叠。
    ("m60-CellsOf不按发与侧过滤", "record.go",
     [("\t\tif e.Session != session || e.Step != step || !strings.HasPrefix(e.Kind, prefix) {",
       "\t\tif e.Session != session || false && (e.Step != step || !strings.HasPrefix(e.Kind, prefix)) {")],
     ["TestCellsOfReadsBackOneCallOneSideOnly"]),
    # 规矩10：checkRecord 里「actor 与 kind 不能空」
    ("m62-checkRecord不要求actor与kind", "kernel.go",
     [("\tif r.Actor == \"\" || r.Kind == \"\" {", "\tif false && (r.Actor == \"\" || r.Kind == \"\") {")],
     ["TestPlanRefusesWhatAppendWouldRefuse"]),
    # 规矩11：commit 逐条落账并返回真条数（中途失败时那个 n 也必须是真的）
    ("m63-commit中途失败时条数报假", "record.go",
     [("\t\tif _, err := l.Append(r); err != nil {\n\t\t\treturn n, err\n\t\t}",
       "\t\tif _, err := l.Append(r); err != nil {\n\t\t\treturn 0, err\n\t\t}")],
     ["TestCommitAcceptsEveryShapePlanTranslates"]),
    # 额外：RecordInput 门口那道 Check（坏输入整发拒收、拒收有数）
    # 锚点在 2026-10-19 13:3x 换过一次：`l.countRejected++` 九处（现数 11 处）汇成 `l.noteRejected()` 那个 helper 之后，
    # 老锚点在 record.go 里命中 0 处 —— 整轮 80 根第一次落地就是这么红的：ANCHOR-BAD 2（m64/m66），退出码 1。
    # 这不是判据弱，是【尺子的锚点与源码脱钩】；改锚点不算修好，必须重跑到 RED-ONLY-NAMED 才算。
    ("m64-RecordInput不Check就记账", "record.go",
     [("\tif err := m.Check(); err != nil {\n\t\tl.noteRejected()\n\t\treturn 0, fmt.Errorf(\"kernel: 输入侧账不自洽，第 %d 发整发拒收：%v\", m.Call, err)",
       "\tif err := m.Check(); false && err != nil {\n\t\tl.noteRejected()\n\t\treturn 0, fmt.Errorf(\"kernel: 输入侧账不自洽，第 %d 发整发拒收：%v\", m.Call, err)")],
     ["TestBrokenInputIsRefusedWholeAndCounted", "TestUnserializableCellIsRefusedAndCounted"]),
    # 额外：plan 的空键名表那一支（直接喂 plan，绕开 CheckCells）
    ("m65-plan收空键名表", "record.go",
     [("\tif len(cells) == 0 {", "\tif false && len(cells) == 0 {")],
     ["TestPlanRefusesAnEmptyCellList"]),
    # 额外：RecordCall 的次序 —— 两侧全部翻译完才落账（改成边翻译边写就留半发）
    ("m66-RecordCall边翻译边落账留半发", "record.go",
     [("\toutRecs, err := l.plan(session, actor, out.Call, KindModelOutput, out.Cells())\n\tif err != nil {\n\t\tl.noteRejected()\n\t\treturn 0, err\n\t}\n\tn, err := l.commit(inRecs)\n\tif err != nil {\n\t\treturn n, err\n\t}\n\tm, err := l.commit(outRecs)\n\treturn n + m, err",
       "\tn, err := l.commit(inRecs)\n\tif err != nil {\n\t\treturn n, err\n\t}\n\toutRecs, err := l.plan(session, actor, out.Call, KindModelOutput, out.Cells())\n\tif err != nil {\n\t\tl.noteRejected()\n\t\treturn n, err\n\t}\n\tm, err := l.commit(outRecs)\n\treturn n + m, err")],
     ["TestNoHalfCallEvenWhenTranslationFailsLate"]),
    # 额外：basisOf「值是缺席、格子却带着出处」那一支
    ("m67-缺席值带出处也收", "record.go",
     [("\t\t\tif c.Basis != BasisUnset {", "\t\t\tif false && c.Basis != BasisUnset {")],
     ["TestBasisOfRefusesEveryShapeOfBasisLying"]),
    # 额外：basisOf「只有三态量和 nil 名单能报缺席」那一支
    # 锚点在 2026-10-19 换过一次：那一支里插进了一条 nil 名单的放行，老锚点那两行不再相邻。
    # 换锚点时必须同时确认命中仍是 1 处 —— `\tif c.Basis == BasisAbsent {` 单独拿出来
    # 在 AbsentKeys 里还有一处，只盯一行会扎错地方。
    # 点名第二条是 10-19 之后加的：这一支死了，nil 名单会带着「0 段（裁剪表是空的）」落账，
    # 而那句话是**链上**读得到的（mcpsrv 那根针量的是 Agent 实际看的两面，不只在结构体里）。
    # 第三条 m85/m86 那根结构体针在这支下也会红（上一轮的 m68 就印着它，实测过、不是推的），
    # 所以一并点名 —— 留成"红得更宽"就是把已经看见的连带证据写成意外。
    ("m68-非三态的值也能报缺席", "record.go",
     [("\tif c.Basis == BasisAbsent {\n\t\t// 三态量之外，只有一种值说得出「没有」这两个字：一张 nil 名单。",
       "\tif false && c.Basis == BasisAbsent {\n\t\t// 三态量之外，只有一种值说得出「没有」这两个字：一张 nil 名单。")],
     ["TestBasisOfRefusesEveryShapeOfBasisLying",
      "TestNilCutListLandsAsAbsentWhileAnEmptyOneStaysMeasured",
      "TestCutsAbsentVersusEmptyAreTwoSentencesOnTheWire"]),
    # —— 本轮 #66（10-19）：没交这个键，不许在账上署成实测过的「0 段」——
    # 这三根合起来才是那一条不变量：出处怎么定（m85）、nil 从哪来（m86）、谁能进 absent 档（m68）。
    # 少任何一根，「缺席 ≠ 0 ≠ 忘了填」在名单这一侧就退回散文。
    # 每根都点两条名：结构体那条（record_test）证明判据在，链上那条（mcpsrv）证明**调用方看得见** ——
    # 只有前者会把「内核里分了、外壳念成同一句」读成修好了。
    ("m85-没交的裁剪表又署成实测", "cells.go",
     [("\tif cuts == nil {\n\t\treturn Cell{Key: key, Field: field, Value: cuts, Basis: BasisAbsent}",
       "\tif false && cuts == nil {\n\t\treturn Cell{Key: key, Field: field, Value: cuts, Basis: BasisAbsent}")],
     ["TestNilCutListLandsAsAbsentWhileAnEmptyOneStaysMeasured",
      "TestCutsAbsentVersusEmptyAreTwoSentencesOnTheWire"]),
    ("m86-nil 名单又被抹成空表", "input.go",
     [("\tif m.Cuts == nil {\n\t\treturn nil\n\t}\n\tout := []Cut{}", "\tout := []Cut{}")],
     ["TestNilCutListLandsAsAbsentWhileAnEmptyOneStaysMeasured",
      "TestCutsAbsentVersusEmptyAreTwoSentencesOnTheWire"]),
    # —— 本轮 M4 新增：journal.go 那份字节格式（编得出去、读得回来、「没有原像」与「被人改过」）——
    # 这一族的核心是 j2/j9：把「手里没有原像」念成「内容被动过」，是一条会把真篡改淹掉的假警报。
    ("j1-空原像被当成没有原像", "journal.go",
     [("\t\tif e.payloadAbsent {", "\t\tif e.Payload == nil {")],
     ["TestEmptyPreimageRoundTripsAsPresentNotAbsent"]),
    ("j2-没有原像那行不记成无原像", "journal.go",
     [("\t\t\te.payloadAbsent = true\n", "")],
     ["TestOmittedPreimageIsNotAccusedOfTampering"]),
    ("j3-没填PayloadMode也放行", "journal.go",
     [("\tcase PayloadUnset:\n", "\tcase PayloadMode(99):\n")],
     ["TestMarshalRefusesAnUnsetPayloadMode"]),
    ("j4-选了带原像却不写字节", "journal.go",
     [("\t\t\tline.PayloadB64 = &b64", "\t\t\t_ = b64")],
     ["TestJournalRoundTripKeepsEveryCellRecomputable"]),
    ("j5-整格seal不重算", "journal.go",
     [("\t\tif h := e.seal(); h != e.Hash {", "\t\tif h := e.seal(); false && h != e.Hash {")],
     ["TestEditedClaimIsCaughtByTheSealNotThePayload"]),
    ("j6-payload不与指纹比对", "journal.go",
     [("\t\t\tif ph := sum(e.Payload); ph != e.PayloadH {",
       "\t\t\tif ph := sum(e.Payload); false && ph != e.PayloadH {")],
     ["TestTamperedPayloadIsStillCaught"]),
    ("j7-坏行静默丢掉不报错", "journal.go",
     [("\tif len(j.BadLines) > 0 {", "\tif false && len(j.BadLines) > 0 {")],
     ["TestBadLineIsReportedWithItsLineNumber", "TestEncodeJournalTextLoadsBackVerbatim"]),
    ("j8-读回来的账不填进账本", "journal.go",
     [("\tl.docs = j.Docs", "\tl.docs = nil")],
     ["TestLoadedLedgerRefusesToRecordTheSameCallTwice"]),
    # —— 10-25 A3 那一族：人读表头的判定搬进内核（`kernel.IsEncodeHeader`）之后配的 4 根。
    # 为什么这个族要四根而不是两根：这条判据同时欠着**两头**的坏法（认不出=出厂件喂不回装载器、
    # 认太宽=真坏账被放过），再加上它自己的两个分母口径（那一行不许进 Lines、不许恒说一句）。
    # 前两根是 10-25 现场那条「2941 行里 1 行解不开、整本拒」的复现与它的反面；
    # 后两根钉的是**计数口径**，它们坏了不改变"能不能读回来"，只改变"读回来的那句话长什么样"。
    ("j11-表头不认（出厂字节喂不回装载器）", "journal.go",
     [("\t\tif IsEncodeHeader(line) {", "\t\tif false && IsEncodeHeader(line) {")],
     ["TestHeaderPrefixedBytesLoadBackThroughTheKernel",
      "TestHeaderClauseAppearsOnlyWhenThereIsAHeader",
      # 外壳那支现在判的是**内核**认不认（外壳自己不再先剥一遍），所以它也在点名单里 ——
      # 10-25 把 splitEncodeHeader 删掉时，这条往返的唯一 owner 就变成了 journal.go。
      "TestEncodeJournalTextLoadsBackVerbatim",
      # 下面 10 支是 10-25 这一轮实测**宽出来的**，逐名打开核过都是同一句话的受害者：
      # 它们拿 encode_journal 交回的字节（第一行就是人读表头）写盘再 load_journal 读回，
      # 恒不认 ⇒ 那份字节当场读不回来。老规矩：名单写全，而不是把针改窄。
      # 逐名证据落在 Temp/ak-1025/j11-逐名核对.txt（函数所在文件:行 + 命中的关键词）。
      "TestAppendOnlyRefusesLoadButKeepsEncode", "TestEntryCapRefusesAndCountsSeparately",
      "TestGateCountsSurviveEncodeAndLoad", "TestNewGateDoesNotLieAboutCountersWhenFileCarriesThem",
      "TestCheckpointLoadsSelfConsistent", "TestHostRoundTripRefusesTamperedFile",
      "TestOmittedPayloadIsNotGreen", "TestResumeContinuesAndRefusesSecondRecording",
      "TestResumeRebuildsHistoryOnlyWhenFingerprintsMatch", "TestResumedRunContinuesTurnAxis"]),
    ("j12-任何非JSON行都算表头（真坏账被放过）", "journal.go",
     [("\treturn line != \"\" && !strings.HasPrefix(line, \"{\") && strings.Contains(line, EncodeHeaderMark)",
       "\treturn line != \"\" && !strings.HasPrefix(line, \"{\")")],
     ["TestEncodeHeaderLineIsTheOnlyThingTheKernelRecognizes",
      "TestHeaderPrefixedBytesLoadBackThroughTheKernel",
      "TestEncodeJournalTextLoadsBackVerbatim",
      # 宽出来那一支不是噪声：它比的是「同一发垃圾喂内核与喂交付件 mcpkernel.exe，两边都得拒」。
      # 判定放宽之后本地这一侧把垃圾当表头跳过了、交付件那一侧仍拒 ⇒ 两边判出两句话，正当的宽。
      "TestSameRulerAsShippedKernel"]),
    ("j13-表头也计进Lines（一句假篡改嫌疑）", "journal.go",
     [("\t\t\tj.HeaderLines++", "\t\t\tj.HeaderLines++\n\t\t\tj.Lines++")],
     ["TestHeaderPrefixedBytesLoadBackThroughTheKernel"]),
    ("j14-表头那句在没有表头时也说", "journal.go",
     [("\tif j.HeaderLines > 0 {", "\tif j.HeaderLines >= 0 {")],
     ["TestHeaderClauseAppearsOnlyWhenThereIsAHeader"]),
    ("j9-无原像时Verify直接报绿", "kernel.go",
     [("\tif len(rep.NoPreimage) > 0 {", "\tif false && len(rep.NoPreimage) > 0 {")],
     ["TestOmittedPreimageIsNotAccusedOfTampering",
      "TestVerifyDelegatesToTheSameJudgmentAsCheckChain"]),
    # j10 是 MCP 挂载实测撞出来的那一族：写侧按 session+发号+侧三个轴定身份，
    # 读侧曾经只认两个。一个 session 的测试永远撞不到，所以针也必须造两个 session。
    ("j10-读侧丢了session这一轴", "record.go",
     [("\t\tif e.Session != session || e.Step != step || !strings.HasPrefix(e.Kind, prefix) {",
       "\t\tif e.Step != step || !strings.HasPrefix(e.Kind, prefix) {")],
     ["TestCellsOfKeepsTwoSessionsApart"]),
    # —— kcheck 闸门针：跑 go run ./cmd/kcheck，要求退出码非 0（gate=True、wants 空）——
    # 这些判定 go test 覆盖不到：go test 从不手搭一条 BasisAbsent+空 Claim 的 Record，
    # 也从不直接读 Basis.Decidable()（只经 Measure.Decidable，而它对 Absent 短路）。
    # g1 守 kcheck 6.2「缺席必须带理由」
    ("g1-缺席无理由账本也收", "kernel.go",
     [("\tif r.Basis == BasisAbsent && strings.TrimSpace(r.Claim) == \"\" {",
       "\tif false && r.Basis == BasisAbsent && strings.TrimSpace(r.Claim) == \"\" {")],
     [], True),
    # g2 守 kcheck 6.3「哪几种出处必须带来源」（把 estimated 从名单里去掉）
    ("g2-必须带来源的名单少一档", "measure.go",
     [("func needsSource(b Basis) bool { return b == BasisRelayed || b == BasisEstimated }",
       "func needsSource(b Basis) bool { return b == BasisRelayed }")],
     [], True),
    # g3 守 kcheck 6.4「basis=absent 不可判定」
    ("g3-absent档也能拿去做判定", "kernel.go",
     [("func (b Basis) Decidable() bool { return b == BasisMeasured || b == BasisRelayed }",
       "func (b Basis) Decidable() bool { return b == BasisMeasured || b == BasisRelayed || b == BasisAbsent }")],
     [], True),
    # g4 守 kcheck 7.3「第三种答案不许长得像绿」：核不了的那一份报告必须打 —，不许打 ✓
    ("g4-核不了却打着✓", "journal.go",
     [('return "— 字段级自洽（"', 'return "✓ 字段级自洽（"')],
     [], True),
    # ==== 10-25 A1/A2 那一族：前缀 s = shell，针下在**外壳** `mcpsrv/mcpsrv.go` ====
    # 这一族是这根探针**第一次把针落在子包**里。以前 84 根的 fname 全住在根包，
    # 而 `sync_tree` 本来就按 os.walk 拷全部 `.go`、`probe_one` 用 os.path.join(d, fname) ——
    # 所以这一族同时是「子包能不能被这把尺子照到」的第一发实测（照不到会报 ANCHOR-BAD，不会假绿）。
    # 本族几根不在这里数（写死就会随加臂说谎，分母由汇总那行 `len(MUT)` 自己印）。
    # 为什么每一根都只改**一处**：#67 的教训是「一个能靠默认值走通的参数，迟早会被填错的一侧默认掉」，
    # 臂也一样 —— 一次改两处就分不清是哪一处让针红的。
    ("s1-读侧解码不拦未知键（#67 复活）", "mcpsrv/mcpsrv.go",
     [("\tdec := json.NewDecoder(bytes.NewReader(raw))\n\tdec.DisallowUnknownFields()\n\terr := dec.Decode(&v)",
       "\tdec := json.NewDecoder(bytes.NewReader(raw))\n\terr := dec.Decode(&v)")],
     ["TestWrongKeyLoadIsRefusedAndChainUntouched",
      "TestReadArgsNameStructFieldAndKeyTable",
      # 1025 整轮 s1 报红 3 条、点名只写了 2 条 ⇒ 补第三个证人：未知键不再当场拒，
      # 「读那一发不许塞进写那一桶」那根的口径前提就没了（它判的是拒没拒，不是桶涨没涨）。
      "TestReadSideRefusalsAreNotCountedInDoorBucket"]),
    # s2 守的是「为什么 struct 必须有名字」：匿名 struct 时 te.Struct 是空串，归因退化成
    # `arguments.xxx` —— 调用方拿不到可归属的坐标。这里直接把 te.Struct 掐掉，不改成匿名
    # （改匿名要动四个调用点，那就不是「一次只改一处」了）。
    ("s2-类型错不点名结构.字段（归因退回 arguments）", "mcpsrv/mcpsrv.go",
     [("\t\twho := te.Struct\n\t\tif who == \"\" {\n\t\t\twho = \"arguments\"\n\t\t}\n\t\treturn v,",
       "\t\twho := \"\"\n\t\tif who == \"\" {\n\t\t\twho = \"arguments\"\n\t\t}\n\t\treturn v,")],
     ["TestReadArgsNameStructFieldAndKeyTable"]),
    # s3 钉「键名表不是第二份名单」：这张表是从 struct 的 tag 现取的，取不出来时就只能念空表，
    # 而空表等于把「你写错了哪个键」这个问题重新丢回调用方。
    ("s3-键名表从 struct 长不出来（念空表）", "mcpsrv/mcpsrv.go",
     [("\tfor i := 0; i < t.NumField(); i++ {", "\tfor i := 0; i < 0; i++ {")],
     ["TestReadArgsNameStructFieldAndKeyTable",
      # 同上：空表让「你写错了哪个键」那句退化成没有候选 ⇒ #67 那一根的现场也一起红。
      "TestWrongKeyLoadIsRefusedAndChainUntouched"]),
    # s4/s5 是**同一道闸的两半**，所以配两根：空串这个罪名并进拒收那一句（s4），
    # 和「点名换了以后要说清那个 0 是谁的 0」（s5）。只配一根的话，另一半年就没人量过。
    ("s4-空串罪名不并进替换闸", "mcpsrv/mcpsrv.go",
     [("\t\t\tempty := \"\"\n\t\t\tif emptyText {", "\t\t\tempty := \"\"\n\t\t\tif false && emptyText {")],
     ["TestEmptyTextCrimeIsNamed"]),
    ("s5-点名换空账后不说那个 0 是谁的", "mcpsrv/mcpsrv.go",
     [("\t\tif emptyText {\n\t\t\t// 三态不塌", "\t\tif false && emptyText {\n\t\t\t// 三态不塌")],
     ["TestEmptyTextCrimeIsNamed"]),
    # s6 / s6b 钉**桶口径**的两头：载入被门口挡下算一次写请求（进 doorRefused），读侧三件不算。
    # s6 把 Add 换成 Load：数不涨，但回话照旧印「累计 N 次」—— 这正是 10-18 那一族
    # 「拒了但账上看不见」的复刻，所以针必须红在「另有 1 次」那一句上（`load_journal_door_test.go:105`）。
    # s6b 是反方向：把读那一发的拒塞进同一桶。只配 s6 一根的话，「桶不许顺手扩」这半句没人量过。
    ("s6-载入在门口被挡不计数", "mcpsrv/mcpsrv.go",
     [("\t\t\tn := s.doorRefused.Add(1)\n\t\t\treturn text(r.ID, fmt.Sprintf(\"%s（这类「外壳在门口挡下」累计 %d 次；链上一条没动，",
       "\t\t\tn := s.doorRefused.Load()\n\t\t\treturn text(r.ID, fmt.Sprintf(\"%s（这类「外壳在门口挡下」累计 %d 次；链上一条没动，")],
     ["TestWrongKeyLoadIsRefusedAndChainUntouched"]),
    ("s6b-读一发被挡塞进写那一桶（口径被顺手扩）", "mcpsrv/mcpsrv.go",
     [("\t\t\treturn text(r.ID, why+readSideNote, true)\n\t\t}\n\t\tkind := kernel.KindModelInput",
       "\t\t\tm := s.doorRefused.Add(1)\n\t\t\t_ = m\n\t\t\treturn text(r.ID, why+readSideNote, true)\n\t\t}\n\t\tkind := kernel.KindModelInput")],
     ["TestReadSideRefusalsAreNotCountedInDoorBucket"]),
    # ---- A2 那四根：承诺四栏（键名／档／原话／出处／指纹）里后两栏上没上屏、三态塌不塌、渲染栏会不会混进原话 ----
    # s7 用 `[0:0]` 而不是删参数：直接删 `cellCols(c)` 会让 Sprintf 少一个参数，
    # `go test` 默认跑的 vet(printf) 当场判 build failed —— 那是「崩」不是「红」（崩不算红）。
    ("s7-出处与指纹两栏不上屏", "mcpsrv/mcpsrv.go",
     [("c.Key, c.Basis, c.Claim, instrShapeTag(c.Claim), cellCols(c)))",
      "c.Key, c.Basis, c.Claim, instrShapeTag(c.Claim), cellCols(c)[0:0]))")],
     ["TestReadBackShowsSourceAndFingerprint", "TestOmittedJournalReadsBackAsNoPreimage",
      # 交付件那把尺子比的就是「我们这棵树比出厂件多印了两栏」⇒ 两栏不上屏，它记的那处差异也没了。
      # 这根针平时不参与本轮判断，红在这里是对的，只是名字先前没登记。
      "TestSameRulerAsShippedKernel"]),
    ("s8-无原像那档塌成原像在手", "mcpsrv/mcpsrv.go",
     # 95 根整轮里这一根多红过一条事件层针（TestEveryTurnSettlesExactlyOnce）；单独复筛没复现 ⇒ 不加名：
     # 那是那一轮机器的账，不是这一罪的证人（复筛读数 报告/臂检-筛针-s族补点名-1025.txt）。
     [("\tcase len(c.Payload) > 0:", "\tcase len(c.Payload) >= 0:")],
     ["TestFingerprintColumnKeepsTheThreeStatesApart", "TestOmittedJournalReadsBackAsNoPreimage"]),
    ("s9-出处为空塌成空串（三态塌）", "mcpsrv/mcpsrv.go",
     [("src := \"—（这一格没有出处栏：\" + c.Basis.String() + \" 档本来就不要求它，不是丢了）\"",
       "src := \"\"")],
     ["TestFingerprintColumnKeepsTheThreeStatesApart"]),
    # s10 是「往别人切好的句子里追加内容」这一族的反面：渲染栏切不掉，
    # 宿主 loop.go 那句「原话 == 重算的 sha256」就每一发都对不上 —— 针下在 cmd/agentd。
    ("s10-渲染栏没切掉（下游把渲染读成原话）", "mcpsrv/mcpsrv.go",
     [("\tif i := strings.Index(line, CellColsMark); i >= 0 {",
       "\tif i := strings.Index(line, CellColsMark); false && i >= 0 {")],
     ["TestStripCellColsReturnsThePreA2LineVerbatim", "TestHostClaimDropsTheRenderedCols",
      # 1025 两轮各现数一次：切不掉渲染栏，同一罪另外三个证人也红 —— 遮罩那根判的就是「只让渲染栏走」，
      # 续传那根拿原话重算 sha、交付件尺子比的是两棵树的 read_back 行，三个都得跟着红。
      "TestRenderedColsMaskOnlyLetsTheRenderedColumnsGo",
      "TestResumeRebuildsHistoryOnlyWhenFingerprintsMatch",
      "TestSameRulerAsShippedKernel"]),

    # ==== D 族：外发页的几何下限（1025 趟 D1 落的针）====
    # 五根臂各摘一处，都是叶子（删掉那一句、不动别的）。两根 viewport 分页数，
    # 证的是「删错哪一页，针就该红在哪一页的名字上」—— 只看红没红抓不到串页。
    # d5 摘掉一个 </div>：这根针的**标签配平**那一半平时不参与前两半的判断，
    # 若它写成了死代码，前四根臂没一根会替它说话。
    ("d1-三行页没声明viewport", "cmd/agentd/web.go",
     [('<meta charset="utf-8"><meta name="viewport" '
       'content="width=device-width,initial-scale=1"><title>agentd 事件面',
       '<meta charset="utf-8"><title>agentd 事件面')],
     ["TestEveryServedPageDeclaresViewportAndBreakpoint"]),
    ("d2-轨迹页没声明viewport", "cmd/agentd/trace.go",
     [('<meta charset="utf-8"><meta name="viewport" '
       'content="width=device-width,initial-scale=1"><title>agentd 轨迹解剖',
       '<meta charset="utf-8"><title>agentd 轨迹解剖')],
     ["TestEveryServedPageDeclaresViewportAndBreakpoint"]),
    ("d3-轨迹页没有断点", "cmd/agentd/trace.go",
     [('\tw(`@media (max-width:640px){body{margin:12px}.bar div{width:64px}'
       'td,th{padding:3px 4px}}</style>`)',
       '\tw(`</style>`)')],
     ["TestEveryServedPageDeclaresViewportAndBreakpoint"]),
    # d6 是上面那条新判据（样式段配平）的叶臂：只摘掉媒体块那个闭括号 ——
    # 「@media」这个串还在、断点还在，只有括号不配平。没有它，新加的那几行是死代码。
    ("d6-样式段少一个闭括号", "cmd/agentd/trace.go",
     [('td,th{padding:3px 4px}}', 'td,th{padding:3px 4px}')],
     ["TestEveryServedPageDeclaresViewportAndBreakpoint"]),
    ("d4-三行页没有断点", "cmd/agentd/web.go",
     [('.note{font-size:12px;color:#5a5a5a}@media (max-width:520px){body{margin:12px}'
       '.row b{flex:0 0 3em}.row span{flex:1 1 auto}',
       '.note{font-size:12px;color:#5a5a5a}')],
     ["TestEveryServedPageDeclaresViewportAndBreakpoint"]),
    ("d5-三行页标签不配平（配平那一半的牙）", "cmd/agentd/web.go",
     [('<div class="row"><b>现在</b><span id="now">没量过</span></div>',
       '<div class="row"><b>现在</b><span id="now">没量过</span>')],
     ["TestEveryServedPageDeclaresViewportAndBreakpoint"]),
    # d7–d9 是 10-25 D2 那一族：六档实测抓到 528/390/320 整页横溢（`报告/几何-出处页六档-1025.txt`，
    # 同一把尺子重量的改前页：100/238/308 px，撑宽者就是主表 min-content ~601px）——
    # viewport 那条 meta 管不了一张摆十列的表。修法是把每张表装进 .tw{overflow-x:auto}
    # 这个能横滚的壳，判据在 TestEveryTraceTableScrollsInsideWrapper（同一文件第 13 条）。
    # 三根臂各摘一处：d7 摘壳的牙（CSS），d8 摘主表的壳，d9 摘侧表的壳。
    # d8 只摘开标签 ⇒ <div 少一个、</div> 还在，配平那一半同时倒，所以要点两根针。
    # d9 只点新针：侧表只出现在「剧本+happy+20」那根针的页上；viewport 那根针的驱动
    # （buildDriver("session","")）不产侧表，它的页里根本没有这个锚点 —— 第一趟把两根都点上，
    # 结果 viewport 那根没红，整根针被记成 VACUOUS。点名的针必须真的会被这处改动打红。
    ("d7-壳有了但没配横滚（.tw 的牙）", "cmd/agentd/trace.go",
     [('.tw{overflow-x:auto}td,th{border:1px solid #ddd', 'td,th{border:1px solid #ddd')],
     ["TestEveryTraceTableScrollsInsideWrapper"]),
    ("d8-主表没装进能横滚的壳", "cmd/agentd/trace.go",
     [('<h2>一发解剖</h2><div class="tw"><table>', '<h2>一发解剖</h2><table>')],
     ["TestEveryTraceTableScrollsInsideWrapper",
      "TestEveryServedPageDeclaresViewportAndBreakpoint"]),
    ("d9-侧表没装进能横滚的壳", "cmd/agentd/trace.go",
     [('<div class="tw"><table><tr><th>%s</th>', '<table><tr><th>%s</th>')],
     ["TestEveryTraceTableScrollsInsideWrapper"]),
]

# ==== 起跑前的内存闸：机器饿着就别跑出「假不绿」 ====
# 10-15 那三轮里的 3 根 TEST-HANG（m39/m40/m46）事后在空机器上单挑全是 RED-ONLY-NAMED ——
# 也就是说：低内存下跑完整轮，报的是机器的账，不是判据的账。
# 所以这里在动任何文件之前先量一次可用物理内存：不够就退出码 3，**不产出读数文件**。
# 拦下的说明写到 <报告路径>.not-run —— 它带着 not-run 后缀，谁也不会把它读成一轮的结论。
#
# **为什么连读 5 次取中位，而不是读一次**（10-16 实测，原始件 `报告/内存采样-2根针轮-1016.csv`）：
# 同一台机器 6 秒窗口内读数能摆 **482 MB**（相邻 1 秒最大摆 250 MB、95 分位 159 MB），
# 单点读数量的不是「机器够不够」，是「那一瞬间撞上谁」。
#
# **阈值 900 的来路（也是同一份实测）**：一轮活跃工作把可用内存从起点读数吃到最低
# **降了 611 MB**（1036 → 425，全程 360 秒），而那一段跑完了 2 根针 + 两条对照、
# `TEST-HANG 0`、退出码 0 —— 即"起跑 ~1000 MB"是实测跑得完的形状；
# 反过来 10-14/10-15 那几次出超时的时刻，空闲只有 ~185–208 MB。
# 所以 900 不是拍的：它落在"实测能跑完的起跑值"附近，而下界那一侧有超时记录。
MIN_FREE_MB = int(os.environ.get("KPROBE_MIN_FREE_MB", "900"))
MEM_SAMPLES = int(os.environ.get("KPROBE_MEM_SAMPLES", "5"))
MEM_SAMPLE_GAP = float(os.environ.get("KPROBE_MEM_GAP", "1"))
# 有超时界的针还是可能超时（这台 4 GB 的机器会）。超时之后自动把那几根**单挑再验一次**：
# 单挑能落地就说明第一轮那根超时是机器的账，按复验结论记账；单挑还超时才是真坏针。
HANG_RECHECK = os.environ.get("KPROBE_HANG_RECHECK", "1") == "1"


def free_mem_mb():
    """问一次 Windows 要可用物理内存；问不到就返回 -1（当作未知、不拦）。"""
    try:
        q = subprocess.run(["powershell", "-NoProfile", "-Command",
                            "(Get-CimInstance Win32_OperatingSystem).FreePhysicalMemory / 1KB"],
                           capture_output=True, text=True, timeout=60)
        return int(float((q.stdout or "0").strip() or 0))
    except Exception:
        return -1


def free_series(n, gap):
    """连读 n 次，每次隔 gap 秒。读到未知（-1）就停在中途 —— 宁可用较短的样本也不要拿假数凑。"""
    xs = []
    for i in range(max(1, n)):
        v = free_mem_mb()
        if v < 0:
            break
        xs.append(v)
        if i + 1 < n:
            time.sleep(gap)
    return xs


def _median(xs):
    s = sorted(xs)
    return s[len(s) // 2] if s else -1


_readings = free_series(MEM_SAMPLES, MEM_SAMPLE_GAP)
_free = _median(_readings)
_series_txt = " ".join("%d" % v for v in _readings) if _readings else "读不到"
if _free >= 0 and _free < MIN_FREE_MB:
    note = io.open(OUT + ".not-run", "w", encoding="utf-8")
    note.write("!! 起跑前内存闸拦下：连读 %d 次 = %s MB，中位 %d MB < 阈值 %d MB。\n"
               % (len(_readings), _series_txt, _free, MIN_FREE_MB))
    note.write("   这一轮没有跑，%s 这个读数文件也不存在 —— 别把它读成任何结论。\n" % OUT)
    note.write("   低内存下跑完只会把机器的问题报成判据的问题（10-15 那 3 根 TEST-HANG 就是这个形状）。\n")
    note.write("   要么等机器空出来；要么明确压低阈值 KPROBE_MIN_FREE_MB=<MB> 再跑，\n")
    note.write("   并把那条读数标成「在拥挤机器上跑的」。\n")
    note.close()
    print("NOT_RUN: median %dMB < %dMB (readings: %s) (note: %s.not-run)"
          % (_free, MIN_FREE_MB, _series_txt.replace(" ", ","), OUT))
    sys.exit(3)
_gate_line = "起跑前内存闸：连读 %d 次 = %s MB，中位 %s MB（阈值 %d MB）—— 放行" % (
    len(_readings), _series_txt, _free if _free >= 0 else "未知", MIN_FREE_MB)

env = dict(os.environ)
env["PATH"] = env.get("PATH", "") + os.pathsep + os.path.dirname(GO)
# 10-25 现场：对照 0 在临时拷贝里必红（`--- FAIL: TestSameRulerAsShippedKernel`），而树里 `go test ./...` 是绿的。
# 那一根不只看源码，还要拿**交付件里那枚 mcpkernel.exe** 对账；`sync_tree` 按扩展名只拷
# .go/.mod/.sum（见它那段为什么），所以那枚 exe 永远拷不进来，而它也不在 %TEMP% 的任何上级目录里。
# 这一红说的是「探针少拿了东西」，不是「判据弱」—— 把它读成后者就等于让整轮结论作废。
# 测试自己留了环境变量这条路（`same_ruler_test.go:25` 先读 AGENTKERNEL_SHIPPED_MCPKERNEL），照它接上；
# 文件不在就**如实印这一行**并且不塞一个空值：宁可让那一根当场红着说"没跑"，不许静默跳过冒充绿。
SHIPPED = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(SRC))),
                       "构建-1017", "agentkernel", "bin", "mcpkernel.exe")
if os.path.isfile(SHIPPED):
    env["AGENTKERNEL_SHIPPED_MCPKERNEL"] = SHIPPED
    _shipped_note = "接上交付件那枚尺子：%s（对照 0 里 TestSameRulerAsShippedKernel 才有对手）" % SHIPPED
else:
    _shipped_note = "!! 交付件那枚 mcpkernel.exe 不在（%s）—— 对照 0 会红在 TestSameRulerAsShippedKernel 上，" \
                    "那是「没跑」不是「判据弱」，别拿它当整轮失守" % SHIPPED
rep = io.open(OUT, "w", encoding="utf-8")
def w(s):
    rep.write(s + "\n"); rep.flush()
w(_gate_line)
w(_shipped_note)
w("")

summary = []
gate_names = set()   # 名字在这集合里的 = kcheck 针；用来把两类针分开数

# 每一根针都必须有一个上限。**没有上限时，「这根针在慢慢编」和「这根针把 go test 挂死了」
# 从外面长得一模一样** —— 2026-10-14 的第二轮整轮就在第 8 根上 68 分钟不落地，
# 而当时这台机器只剩 208 MB 空闲物理内存（4 GB 的箱子，一次全量 test 现拷现编本来就到极限）。
# 超时归成 TEST-HANG：它是一根坏针（参与非 0 退出），既不是绿，也不许冒充「判据弱」。
# 想调界走环境变量 KPROBE_TIMEOUT（秒），默认 600；`go test` 自己也带一个 -timeout 兜运行期死循环。
GO_TIMEOUT = int(os.environ.get("KPROBE_TIMEOUT", "600"))
GO_TEST_TIMEOUT = "%ds" % max(60, GO_TIMEOUT - 120)
HANG = "TEST-HANG"


class _TimedOut(object):
    returncode = 124
    stdout = ""
    stderr = ""


def bounded(args, cwd):
    # 低内存档在这里集中注入：test/build/run 三种调用都吃编译并行度，
    # 逐个调用点各写一遍一定会漏（漏掉的那一个就足够把机器打回 paging）。
    if PFLAG and args and args[0] in ("test", "build", "run"):
        args = [args[0]] + PFLAG + list(args[1:])
    try:
        return subprocess.run([GO] + args, cwd=cwd, env=env, capture_output=True,
                              text=True, encoding="utf-8", errors="replace",
                              timeout=GO_TIMEOUT)
    except subprocess.TimeoutExpired:
        t = _TimedOut()
        t.stdout = "probe-timeout: 超过 %d 秒没跑完（不是绿，也不是判据弱）" % GO_TIMEOUT
        return t


def hung(r):
    return r.returncode == 124 and "probe-timeout" in (r.stdout or "")


# 低内存档：`go test` 默认按核数并行编译，峰值内存正好撞穿这台 4 GB 机器剩下的那 ~180 MB
# —— 表现不是"慢"而是整机 paging 到 `powershell` 都十分钟不返回。加 `-p 1` 把编译串行化：
# 单根针更慢，但不换页，整轮反而第一次跑得完。用 KPROBE_LOW_MEM=0 关掉。
LOW_MEM = os.environ.get("KPROBE_LOW_MEM", "1") == "1"
PFLAG = ["-p", "1"] if LOW_MEM else []


def sync_tree(d):
    """把取材树按 SRC 逐字节修干净，返回修了几个文件。

    范围只含**能编译的东西**（`.go` / `go.mod` / `go.sum`）：针改的都是 `.go`，
    闸门与测试也只读 Go 源码 —— 而 `报告/` 那 114 个文件（样例账、调研原文，约 5 MB）
    在这台只剩 ~190 MB 内存的机器上，正是 `copytree` 要几十分钟的元凶：
    2026-10-14 那一轮就是印完两条对照之后卡在拷贝上，40 分钟没有任何 `go`/`compile` 进程。
    **少拷了东西不会被静默吞掉**：对照 0 要求在这棵树上 `go test ./...` 全过
    （PASS 条数由它自己印，这里不钉死某一个数——钉死了就会在下一次加包的时候变成假红）
    且 `cmd/kcheck` 退出 0 —— 少拷了任何测试或闸门真需要的文件，那一根对照当场就不成立、退出码 2。
    要退回「整棵树都拷」：KPROBE_FULLTREE=1。
    """
    n = 0
    ext = None if os.environ.get("KPROBE_FULLTREE") == "1" else (".go", ".mod", ".sum")
    for base, dirs, files in os.walk(SRC):
        rel = os.path.relpath(base, SRC)
        for fn in files:
            if ext and not fn.endswith(ext):
                continue
            s = os.path.join(base, fn)
            t = os.path.join(d, fn) if rel == "." else os.path.join(d, rel, fn)
            if _copy_if_different(s, t):
                n += 1
    # 有些测试读的不是 Go 源码，而是盘上真落过的那本账（cmd/kaudit 就靠它）。
    # 这类文件必须**逐个点名**加进来，而不是整份 报告/ 一起拷：
    # 少点一个，`go test ./...` 会在探针的临时拷贝里因为"文件不在"而红，
    # 那红不是判据弱，是探针自己漏拿了东西 —— 而它会被读成整轮失守。
    for rel in EXTRA_DATA_FILES:
        if _copy_if_different(os.path.join(SRC, rel), os.path.join(d, rel)):
            n += 1
    return n


# 测试要读的非 Go 文件（相对 SRC）。
EXTRA_DATA_FILES = (os.path.join("报告", "样例-agent.jsonl"),)


def _copy_if_different(s, t):
    """字节不一致才写；返回是否写了。"""
    try:
        sb = io.open(s, "rb").read()
    except Exception:
        return False
    tb = None
    if os.path.exists(t):
        try:
            tb = io.open(t, "rb").read()
        except Exception:
            tb = None
    if tb == sb:
        return False
    dd = os.path.dirname(t)
    if not os.path.isdir(dd):
        os.makedirs(dd)
    io.open(t, "wb").write(sb)
    return True


def run_in(d, args):
    # 对照那两趟也要带上限：干净拷贝若因内存压力永远编不完，这一轮同样会无声卡死在开头。
    return bounded(args, d)


def fresh(name):
    d = os.path.join(WORK, name)
    # 不再 copytree 整棵树：只把能编译的文件放进来（见 sync_tree 那段为什么）。
    os.makedirs(d, exist_ok=True)
    sync_tree(d)
    return d


def first_bad(out):
    for ln in (out or "").splitlines():
        s = ln.strip()
        if ".go:" in s or "build failed" in s or s.startswith("--- FAIL"):
            return s[:200]
    return (out or "").strip()[-200:]


# ======== 对照 0：干净拷贝必须全过 ========
# 没有这一根，「全部空针」在两种解释之间是歧义的：我的判据弱，还是拷贝/工具链坏了。
# 2026-09-27 真烧掉过一整轮：record_test.go 一个类型错误让 48 根 go-test 针同时 VACUOUS，
# 而 go build 是绿的（它不编译 _test.go），汇总看着像大面积失守，其实是探针自己在报自己的故障。

w("==== 对照 ====")
base = fresh("_baseline")
bt = run_in(base, ["test", "-count=1", "-timeout", GO_TEST_TIMEOUT, "-v", "./..."])
bout = (bt.stdout or "") + (bt.stderr or "")
npass = len(re.findall(r"^--- PASS: ", bout, re.M))
nfail = len(re.findall(r"^--- FAIL: ", bout, re.M))
bg = run_in(base, ["run", "./cmd/kcheck"])
baseline_ok = (bt.returncode == 0 and npass > 0 and nfail == 0 and bg.returncode == 0)
w("对照 0（干净拷贝）：go test 退出码 %d、PASS %d 条、FAIL %d 条；kcheck 退出码 %d"
  % (bt.returncode, npass, nfail, bg.returncode))
if not baseline_ok:
    # npass == 0 单独说一句：go test 退出码 0 而一条都没跑，是「ok 可能意味着 0 条」那一族。
    if bt.returncode == 0 and npass == 0:
        w("!! 干净拷贝上 go test 退出码是 0 却一条 PASS 都没有 —— 判据压根没跑，这不是绿。")
    w("!! 干净拷贝就没过 —— 后面每一根针的结论都不可信，先修树再跑探针。")
    w("   " + first_bad(bout if nfail or bt.returncode else (bg.stdout or "") + (bg.stderr or "")))

# ======== 对照 T：探针自己必须能认出「测试没跑起来」 ========
# 这一根把 TEST-BROKE 那个守卫钉住。只被手工验过一次的守卫等于没验：
# 将来谁把那条守卫改窄，测试文件编译不过时全部 go-test 针又会一起读成空针，
# 而汇总会把探针自己的故障报成被测代码的缺陷。
# 判据是一个**组合**：go build 必须绿（它不编译测试文件）、go test 必须跑不起来 ——
# 这个组合正是 BUILD-BROKE 抓不到、而 VACUOUS 会误报的那一格。
tb = fresh("_testbroke")
tp = os.path.join(tb, "record_test.go")
ttxt = io.open(tp, encoding="utf-8").read()
T_ANCHOR = "emptyKinds, emptyCuts := claimOf([]PartKind{}), claimOf([]Cut{})"
T_BAD = "emptyKinds, emptyCuts := claimOf([]PartKind{}), []Cut{}"
if ttxt.count(T_ANCHOR) != 1:
    testbroke_ok = False
    w("对照 T：锚点命中 %d 处（该是 1 处）—— 这一根对照是空的，换锚点" % ttxt.count(T_ANCHOR))
else:
    io.open(tp, "w", encoding="utf-8", newline="\n").write(ttxt.replace(T_ANCHOR, T_BAD))
    gb = run_in(tb, ["build", "./..."])
    gt = run_in(tb, ["test", "-run", "^TestDeriveBudgetIsTheOnlyOwnerOfTheFormula$",
                     "-count=1", "-v", "./..."])
    gout = (gt.stdout or "") + (gt.stderr or "")
    has_verdict = bool(re.search(r"^--- (PASS|FAIL): ", gout, re.M))
    testbroke_ok = (gb.returncode == 0 and "[build failed]" in gout and not has_verdict)
    w("对照 T（故意弄坏一个测试文件）：go build 退出码 %d（该 0）、"
      "go test 输出含 [build failed]=%s、有 PASS/FAIL 行=%s（该 False）"
      % (gb.returncode, "[build failed]" in gout, has_verdict))
    if not testbroke_ok:
        w("!! 对照 T 不成立 —— TEST-BROKE 那个守卫认不出「测试没跑起来」。"
          "那么下一次测试文件编译不过时，全部 go-test 针又会被读成空针。")
        w("   " + first_bad(gout))

w("")
if ONLY:
    # 「其余 N 根没跑」要说真数：以前这里印的是 len(MUT)（表里共几根），
    # 于是筛掉 2 根的一轮会写成「其余 76 根没跑」—— 分母与「没跑」是两件事，
    # 混着一句说就是读数骗人。真跳过几根，循环里另有一个准确计数器（页脚用那个）。
    _not_run = sum(1 for _e in MUT if not any(_s in _e[0] for _s in ONLY))
    w("!! 这一轮**筛过针**（只跑名字里含 %s 的），其余 %d 根没跑（表里共 %d 根）—— "
      "它不能当整轮结论用，只够回答「筛出来的这几根针咬不咬得住」"
      % ("/".join(ONLY), _not_run, len(MUT)))
    w("")

skipped = 0

# —— 共享一棵拷贝：76 根针复用同一份树，每根针前按 SRC 逐字节把它修干净 ——
# 为什么非改不可：第三轮整轮从 06:55 起 **47 分钟一根针都没落地**，而当时机器上
# 没有任何 go/compile/link 进程 —— 卡的是 python 自己的 `copytree`/`rmtree`：
# 那两步没有上限（subprocess 的超时管不到它们），而工作目录里已经堆了 42 份整树拷贝。
# 换成一棵共享树之后，每根针的开销是「比对 190 个文件 + 只重写被上一根针改过的那一个」，
# 与「每根针现拷一棵树」等价：针与针之间唯一的串扰渠道就是那个被改的文件，而它每次都被修回原字节。
# 想退回老写法：KPROBE_INPLACE=0。
INPLACE = os.environ.get("KPROBE_INPLACE", "1") == "1"
repaired_total = 0


def ensure_clean(d):
    """共享树每次修干净（实现住在上面 sync_tree 那一处，别在这里再写一遍规则）。"""
    return sync_tree(d)


SHARED = fresh("_shared") if INPLACE else None
if INPLACE:
    _first = ensure_clean(SHARED)
    w("共享树起手检查：修回 %d 个文件（该是 0 —— 不是 0 说明这棵树不是刚拷的）" % _first)
    if _first:
        repaired_total += _first
    w("")

def probe_one(entry, d):
    """把一根针落在这棵（已经修干净的）树上，跑一趟，返回 (状态, 附注)。

    为什么抽成函数：TEST-HANG 之后要「同一根针再单挑验一次」。落针的规则
    （锚点必须命中恰好一次、崩不算红、测试没跑起来不算空针）一旦有两份写法就会分叉，
    而分叉的那一份正好是复验用的那份 —— 复验结论就跟第一趟不可比了。
    """
    name, fname, edits, wants = entry[0], entry[1], entry[2], entry[3]
    gate = bool(entry[4]) if len(entry) > 4 else False
    p = os.path.join(d, fname)
    txt = io.open(p, encoding="utf-8").read()
    for frm, to in edits:
        n = txt.count(frm)
        if n != 1:
            w("!! %s: 锚点命中 %d 处（该是 1 处）-> %r" % (name, n, frm[:70]))
            return "ANCHOR-BAD", ""
        txt = txt.replace(frm, to)
    io.open(p, "w", encoding="utf-8", newline="\n").write(txt)
    b = bounded(["build", "./..."], d)
    if hung(b):
        w("!! %s: go build 超过 %d 秒没回来 —— 这根针没有结论，归坏针" % (name, GO_TIMEOUT))
        return HANG, "build 超时"
    if b.returncode != 0:
        w("!! %s: 拷贝编译不过（崩不算红）：%s" % (name, (b.stderr or "").strip()[:260]))
        return "BUILD-BROKE", ""
    # —— kcheck 针：跑 go run ./cmd/kcheck，要求退出码非 0（编译已在上面过了一遍，崩不算红）——
    if gate:
        g = bounded(["run", "./cmd/kcheck"], d)
        if hung(g):
            return HANG, "kcheck 超时"
        gout = (g.stdout or "") + (g.stderr or "")
        # 真正的闸门红：kcheck 末尾印「✗ N 项没过」并 os.Exit(1)。
        # 崩（panic / 运行期报错）虽然退出码也非 0，但那不是「判据报红」，单独归一类。
        gate_red = ("\u9879\u6ca1\u8fc7" in gout)  # 「项没过」
        crashed = ("panic:" in gout) or ("runtime error:" in gout)
        w("")
        w("== %s  [kcheck 针]" % name)
        w("   点名该红：go run ./cmd/kcheck 退出码非 0")
        w("   实际退出码：%d" % g.returncode)
        for ln in (g.stdout or "").splitlines():
            if "\u2717" in ln:  # ✗ —— 只把报红的那几行抄进 UTF-8 报告，便于分诊
                w("   闸门报红：%s" % ln.strip()[:220])
        if g.returncode == 0:
            return "GATE-GREEN", "kcheck 竟然全过（空针）"
        if gate_red and not crashed:
            return "GATE-RED", "exit=%d" % g.returncode
        return "GATE-CRASH", "退出码非 0 但不是判据报红（崩不算红）：%s" % gout.strip()[-200:]
    # —— go test 针：只跑**一次全量**。
    # 以前每根针跑两趟（一趟 `-run` 只跑点名的、一趟全量），而「红得更宽」这件事实
    # 就是「全量里有点名之外的测试红了」—— 一趟全量同时给得出这两件事，两趟是重复劳动，
    # 在这台机器上等于把整轮的耗时翻倍。分类规则一个字没改。
    full = bounded(["test", "-count=1", "-timeout", GO_TEST_TIMEOUT, "-v", "./..."], d)
    out = (full.stdout or "") + (full.stderr or "")
    if hung(full) or "test timed out" in out:
        w("!! %s: 这一趟超过 %d 秒没回来（或 go 自己报 test timed out）—— 归坏针，不是空针"
          % (name, GO_TIMEOUT))
        return HANG, "test 超时"
    # 「没跑成」不是「一条都没红」。测试文件编译不过时 go build 照样是绿的
    # （它不编译 _test.go），而 go test 一条判据都跑不了 —— 那一整轮会被读成「全部空针」，
    # 把探针自己的故障报成被测代码的缺陷。2026-09-27 真发生过一次：
    # record_test.go 一个类型错误让 48 根 go-test 针同时 VACUOUS，而汇总看着像大面积失守。
    if "[build failed]" in out or "build failed" in out or not re.search(r"^--- (PASS|FAIL): ", out, re.M):
        first = ""
        for ln in out.splitlines():
            if ".go:" in ln or "build failed" in ln:
                first = ln.strip()
                break
        w("!! %s: 测试没跑起来（编译失败或一条都没匹配到，崩不算红也不算空针）：%s" % (name, first[:200]))
        return "TEST-BROKE", first[:120]
    failed = sorted(set(re.findall(r"^--- FAIL: (\S+)", out, re.M)))
    missed = [x for x in wants if x not in failed]
    wider = [x for x in failed if x not in wants]
    w("")
    w("== %s" % name)
    w("   点名该红：%s" % ", ".join(wants))
    w("   实际报红：%s" % (", ".join(x for x in failed if x in wants) or "（一条都没红）"))
    w("   没红的点名：%s" % (", ".join(missed) or "无"))
    w("   全量共红 %d 条%s" % (len(failed), (": " + ", ".join(failed)) if len(failed) <= 6 else
                               ("（宽出点名的：%d 条）" % len(wider) if wider else "")))
    if missed:
        return "VACUOUS", ",".join(missed)
    if wider:
        return "RED-WIDER", str(len(failed))
    return "RED-ONLY-NAMED", str(len(failed))


def tree_for(name, tag=""):
    """给一根针备一棵树：共享树模式修干净复用（返回修回的文件数），否则现拷一份。"""
    if INPLACE:
        return SHARED, ensure_clean(SHARED)
    d = os.path.join(WORK, tag + re.sub(r"[^A-Za-z0-9]", "_", name))
    if os.path.exists(d):
        shutil.rmtree(d)
    shutil.copytree(SRC, d)
    return d, 0


hang_rows = []      # 第一趟超时的针：(在 summary 里的行号, 针)
recheck_ran = 0     # 单挑复验实际跑了几根

for entry in MUT:
    name = entry[0]
    if ONLY and not any(s in name for s in ONLY):
        skipped += 1
        continue
    if (bool(entry[4]) if len(entry) > 4 else False):
        gate_names.add(name)
    d, repaired = tree_for(name)
    repaired_total += repaired
    st, extra = probe_one(entry, d)
    summary.append((name, st, extra))
    if st == HANG:
        hang_rows.append((len(summary) - 1, entry))

# ==== TEST-HANG 自动单根复验 ====
# 有上限不等于有结论：一根针超时，可能是判据慢，也可能是这台 4 GB 的机器在换页。
# 10-15 那一轮三根 TEST-HANG（m39/m40/m46）事后在空机器上单挑全是 RED-ONLY-NAMED ——
# 那一轮把机器的账记到了判据头上，而发现它靠的是我事后手工一根根重跑。
# 现在把这件事写进脚本：超时的针自动单挑再验一次，按复验的结论记账；复验仍超时才留在超时。
if hang_rows:
    w("")
    w("==== TEST-HANG 自动单根复验（%d 根）====" % len(hang_rows))
    if not HANG_RECHECK:
        w("!! KPROBE_HANG_RECHECK=0：复验被显式关掉，这几根按超时记账。")
    for idx, entry in hang_rows:
        name = entry[0]
        if not HANG_RECHECK:
            break
        _fm = free_mem_mb()
        w("")
        w("-- 复验 %s（第一趟超时；此刻可用 %s MB）--" % (name, _fm if _fm >= 0 else "未知"))
        d, repaired = tree_for(name, "recheck_")
        repaired_total += repaired
        recheck_ran += 1
        st, extra = probe_one(entry, d)
        if st == HANG:
            w("   复验仍超时 —— 这不是机器的账，这根针留在超时档（坏针，这一轮不绿）")
            summary[idx] = (name, HANG, (extra or "") + " / 复验仍超时")
        else:
            w("   复验判定：%s %s —— 单挑能落地，第一趟那根超时记在机器头上；按复验结论记账"
              % (st, extra or ""))
            summary[idx] = (name, st, (extra or "") + " / 单根复验")

polluted = 0
if INPLACE:
    # 收尾再修一次：正常只该修回 1 个文件（最后一根针改的那一个）。
    # 多于 1 就是有针改了我们名单之外的文件 —— 那这一轮的「一次只改一处」前提就不成立了。
    polluted = ensure_clean(SHARED)
    w("")
    w("共享树收尾：修回 %d 个文件（该是 1 —— 就是最后一根针改的那一个；多于 1 说明有针改了名单之外的文件）"
      % polluted)
    if polluted > 1:
        w("!! 共享树被改了我们名单之外的 %d 个文件 —— 「一次只改一处」这个前提破了，这一轮不算。"
          % polluted)
    repaired_total += polluted

w("")
w("==== 汇总 ====")
for name, st, extra in summary:
    w("%-42s %-14s %s" % (name, st, extra))

def cnt(status):
    return len([x for x in summary if x[1] == status])

gate_total = len([x for x in summary if x[0] in gate_names])
gotest_total = len(summary) - gate_total
w("")
w("—— 两类针分开数 ——")
w("go test 针 %d 根：RED-ONLY-NAMED %d、RED-WIDER %d、VACUOUS %d、TEST-BROKE(没跑成) %d、%s(超时) %d" % (
    gotest_total, cnt("RED-ONLY-NAMED"), cnt("RED-WIDER"), cnt("VACUOUS"), cnt("TEST-BROKE"),
    HANG, cnt(HANG)))
w("kcheck  针 %d 根：GATE-RED %d、GATE-GREEN(空) %d、GATE-CRASH(崩) %d" % (
    gate_total, cnt("GATE-RED"), cnt("GATE-GREEN"), cnt("GATE-CRASH")))
w("两类共用：ANCHOR-BAD %d、BUILD-BROKE %d" % (cnt("ANCHOR-BAD"), cnt("BUILD-BROKE")))
_recheck_note = ("" if not recheck_ran else
                 "；另有 %d 根是 TEST-HANG 的单挑复验（复验每跑一根，修回计数就多一次）"
                 % recheck_ran)
w("取材方式：%s（累计按 SRC 逐字节修回 %d 个文件次）%s" % (
    "一棵共享树，每根针前修干净" if INPLACE else "每根针现拷一棵树（KPROBE_INPLACE=0）",
    repaired_total, _recheck_note))
bad = [x for x in summary if x[1] in
       ("VACUOUS", "ANCHOR-BAD", "BUILD-BROKE", "TEST-BROKE", "GATE-GREEN", "GATE-CRASH", HANG)]
w("")
w("针数 %d（go test %d + kcheck %d）；只有点名的红 %d；红得更宽 %d；闸门报红 %d；空针/坏针 %d" % (
    len(summary), gotest_total, gate_total,
    cnt("RED-ONLY-NAMED"), cnt("RED-WIDER"), cnt("GATE-RED"), len(bad)))
if skipped:
    w("!! 筛过的轮子：跳过 %d 根（表里共 %d 根）—— 上面那个「针数」不是整轮的分母"
      % (skipped, len(MUT)))
if bad:
    w("空针/坏针清单：")
    for name, st, extra in bad:
        w("  %-42s %-14s %s" % (name, st, extra))

# 两根对照是**整轮结论的前提**，不是第 52、53 根针。
# 对照 0 不过 ⇒ 树本身是坏的，那些"空针"说的是树不是判据；
# 对照 T 不过 ⇒ 探针认不出"测试没跑起来"，那些"空针"可能全是同一个工具故障。
# 两种情况都必须让整轮以非 0 退出，否则一份坏报告长得和一份好报告一样。
w("")
w("—— 两根对照 ——")
w("对照 0（干净拷贝全过）：%s" % ("成立" if baseline_ok else "不成立"))
w("对照 T（探针认得出「测试没跑起来」）：%s" % ("成立" if testbroke_ok else "不成立"))
if not baseline_ok:
    w("")
    w("!! 整轮结论不可信：干净拷贝就没过。先修树，再跑探针。")
if not testbroke_ok:
    w("")
    w("!! 整轮结论不可信：TEST-BROKE 那个守卫失效了，"
      "上面任何一根 VACUOUS 都可能只是「测试没跑起来」被读成了「判据是空的」。")
if bad or polluted > 1:
    w("")
    w("!! 这一轮**不绿**：%d 根针没咬住（VACUOUS / 坏针 / 崩 / 超时）%s —— 退出码 1。"
      % (len(bad), "；另外共享树被改了名单之外的 %d 个文件" % polluted if polluted > 1 else ""))
    w("   退出码分成四档：0 干净、1 有针没咬住、2 对照不成立（那种连读数都不可信）、"
      "3 起跑前内存闸拦下（那一次连报告文件都不写，说明在 <报告路径>.not-run）。"
      "第一轮整轮表里有 2 根 VACUOUS，脚本当时照样退出 0 —— "
      "「有一根针是空的」与「这一轮全绿」共用一个 0，等于没说。")
rep.close()
print("PROBE DONE: %d mutations (%d go-test + %d kcheck); gate-red %d; vacuous/broken %d; "
      "baseline %s; testbroke-control %s" % (
          len(summary), gotest_total, gate_total, cnt("GATE-RED"), len(bad),
          "OK" if baseline_ok else "FAILED", "OK" if testbroke_ok else "FAILED"))
if not baseline_ok or not testbroke_ok:
    sys.exit(2)
# 空针/坏针也必须让这一轮非 0 退出。理由与上面那两条对照同源，而且第一轮整轮就是活例子：
# 表里有 2 根 VACUOUS（破坏落上了却一条测试都没红），脚本当时**照样退出 0** ——
# 一份「针没咬住」的报告和一份干净的报告长得一模一样，退出码这一栏等于没说。
# 对照不成立更重（读数全都不可信），所以仍然先 exit 2，用 1 与 2 区分这两种坏。
if bad or polluted > 1:
    sys.exit(1)
