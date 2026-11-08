package kernel

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 这一份文件守 journal.go 那份格式：编出去要能原样读回来、读回来要能接着核链，
// 而**「手里没有原像」与「内容被人改过」必须说两句话**。
// 每条判据两头都有 —— 只测「好的能过」等于「永远报警也算通过」。

// journalFixture 搭一本确定性的账：三发、时间戳写死（不取 time.Now ——
// 一个会漂的时间让「编完读回来逐格相等」变成一条看运气的判据）。
func journalFixture(t *testing.T) *Ledger {
	t.Helper()
	l := NewLedger()
	base := time.Date(2026, 10, 12, 8, 30, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		rec := Record{
			Session: "sess-j", Actor: "assembler", Step: i + 1,
			Kind:  fmt.Sprintf("%s:call", KindModelInput),
			Claim: fmt.Sprintf("第 %d 发", i+1), Basis: BasisMeasured,
			Payload: []byte(fmt.Sprintf("%d", i+1)),
			At:      base.Add(time.Duration(i) * time.Second),
		}
		if _, err := l.Append(rec); err != nil {
			t.Fatalf("夹具落账被拒：%v", err)
		}
	}
	if err := l.Verify(); err != nil {
		t.Fatalf("夹具的链自己就该自洽：%v", err)
	}
	return l
}

// ① 带不带原像必须由调用方说，不许有个默认值替他说。
// 反面的形状：PayloadUnset 悄悄等于「不带」——那正是不变量 1 要拦的
// 「忘了填被读成一个正经选择」，而且第三方重算不出原值这件事就没人签字了。
func TestMarshalRefusesAnUnsetPayloadMode(t *testing.T) {
	docs := journalFixture(t).Docs()
	_, err := MarshalJournal(docs, PayloadUnset)
	if err == nil {
		t.Fatal("PayloadUnset 该被拒 —— 「带不带原像」不许由一个零值替调用方决定")
	}
	if !strings.Contains(err.Error(), "原像") {
		t.Errorf("拒因要点名是「原像」这一格没填，别让人去查别的规则：%v", err)
	}
	for _, mode := range []PayloadMode{PayloadOmitted, PayloadIncluded} {
		b, err := MarshalJournal(docs, mode)
		if err != nil {
			t.Errorf("合法档位 %v 不该被拒：%v", mode, err)
		}
		if len(b) == 0 {
			t.Errorf("档位 %v 编出了 0 字节 —— 三发账编成空串是一条假绿", mode)
		}
	}
}

// ② 整发往返：每一格都必须原样回来，且读回来的链要能**逐条比对过原像**。
// 期望值全部从写出去的那一份现算，不抄常量 —— 抄常量等于把答案写进考卷。
func TestJournalRoundTripKeepsEveryCellRecomputable(t *testing.T) {
	src := journalFixture(t)
	docs := src.Docs()
	b, err := MarshalJournal(docs, PayloadIncluded)
	if err != nil {
		t.Fatalf("带原像编码该成功：%v", err)
	}
	j, err := ReadJournal(b)
	if err != nil {
		t.Fatalf("自己编出去的文件读不开：%v", err)
	}
	if j.Parsed != len(docs) {
		t.Fatalf("读回 %d 条，写出去 %d 条", j.Parsed, len(docs))
	}
	if j.WithPayload != len(docs) || j.NoPayload != 0 {
		t.Errorf("选了带原像却读出 带=%d 不带=%d —— 原像在编码这一步就丢了", j.WithPayload, j.NoPayload)
	}
	rep := CheckChain(j.Docs)
	if !rep.Decidable() || rep.Checked != len(docs) {
		t.Errorf("往返后的链该是「核过且自洽」，比对过原像 %d/%d 条：%s", rep.Checked, len(docs), rep)
	}
	for i := range docs {
		got, want := j.Docs[i], docs[i]
		if got.Seq != want.Seq || got.Kind != want.Kind || got.Claim != want.Claim ||
			got.Basis != want.Basis || got.Source != want.Source || got.Session != want.Session ||
			got.Actor != want.Actor || got.Step != want.Step || got.PayloadH != want.PayloadH ||
			got.PrevHash != want.PrevHash || got.Hash != want.Hash || !got.At.Equal(want.At) ||
			string(got.Payload) != string(want.Payload) {
			t.Errorf("第 %d 条往返后变了形：\n 写出去 %+v\n 读回来 %+v", i+1, want, got)
		}
	}
	// 读回来的那一份重新 Verify 也必须过 —— 这条把「逐格相等」升级成「链级判据认这份文件」。
	if err := CheckChain(j.Docs).Mismatch; err != "" {
		t.Errorf("往返后链不符：%s", err)
	}
}

// ③ 这一条是整份文件存在的理由：不带原像的文件**不许**被念成篡改。
// 反面的形状（改之前就真实存在）：ReadJournal 解不出 payload 就留 nil，
// Verify 里那句 sum(nil) != PayloadH 于是把每一条都报成「内容被动过」——
// 一个「没带原像」被念成了「有人改了账」，这是规矩一那一族最贵的实例。
func TestOmittedPreimageIsNotAccusedOfTampering(t *testing.T) {
	docs := journalFixture(t).Docs()
	b, err := MarshalJournal(docs, PayloadOmitted)
	if err != nil {
		t.Fatalf("不带原像编码该成功：%v", err)
	}
	l, j, rep, err := LoadLedger(b)
	if err != nil {
		t.Fatalf("没有原像不是拒收的理由（那是 PayloadOmitted 那一档认了的取舍）：%v", err)
	}
	if rep.Mismatch != "" {
		t.Errorf("无原像被念成了篡改：%s", rep.Mismatch)
	}
	if len(rep.NoPreimage) != len(docs) {
		t.Errorf("核不了那一栏该数满 %d 条，实际 %d 条 —— 少数是漏报，多数是把好账冤枉成没核", len(docs), len(rep.NoPreimage))
	}
	if rep.Decidable() {
		t.Error("一条原像都没比对过的报告不许 Decidable —— 那是把「核不了」读成绿")
	}
	if !strings.Contains(rep.String(), "核不了") {
		t.Errorf("报告自己要把「核不了」说出来，别只留个数字：%s", rep)
	}
	if j.NoPayload != len(docs) || !strings.Contains(j.String(), "没有原像") {
		t.Errorf("Journal 要把「全部没有原像」印出来：%s（NoPayload=%d）", j, j.NoPayload)
	}
	// 同一个状态从 Verify 那句话里也必须能读成「无从重算」，而不是「被动过」。
	err = l.Verify()
	if err == nil {
		t.Fatal("没有原像时 Verify 不许直接报绿 —— 「第三方能重算」那一半确实没核")
	}
	if strings.Contains(err.Error(), "被动过") {
		t.Errorf("无原像被指控成篡改，真篡改就会混在这堆假警报里：%v", err)
	}
	if !strings.Contains(err.Error(), "没有原像") {
		t.Errorf("拒因要点名「没有原像」：%v", err)
	}
}

// ④ 反过来：有了原像就必须真的能抓到篡改。
// 少了这一条，③ 就退化成了「一律跳过 payload 比对」——那是永远不报警也算通过。
func TestTamperedPayloadIsStillCaught(t *testing.T) {
	docs := journalFixture(t).Docs()
	b, err := MarshalJournal(docs, PayloadIncluded)
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != len(docs) {
		t.Fatalf("JSONL 一行一条：期望 %d 行，实际 %d 行", len(docs), len(lines))
	}
	// 把第 2 行的原像换成别的字节（合法 base64，所以解码不会先红 ——
	// 红必须来自指纹比对那一步，不能来自读不懂）。
	var l2 journalLine
	if err := json.Unmarshal([]byte(lines[1]), &l2); err != nil {
		t.Fatalf("第 2 行解不开：%v", err)
	}
	if l2.PayloadB64 == nil {
		t.Fatal("选了带原像，第 2 行却没有 payload_b64 —— 编码这一步就漏了")
	}
	evil := base64.StdEncoding.EncodeToString([]byte("9"))
	l2.PayloadB64 = &evil
	tampered, err := json.Marshal(l2)
	if err != nil {
		t.Fatalf("重编失败：%v", err)
	}
	lines[1] = string(tampered)
	rep0 := CheckChain(docs)
	if !rep0.Decidable() {
		t.Fatalf("基线就该是可判定的（否则④测的是空针）：%s", rep0)
	}
	j, err := ReadJournal([]byte(strings.Join(lines, "\n") + "\n"))
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	rep := CheckChain(j.Docs)
	if rep.Mismatch == "" {
		t.Fatal("改了原像却没被抓到 —— 那 PayloadH 这一格在链上没人复核")
	}
	if !strings.Contains(rep.Mismatch, "内容被动过") {
		t.Errorf("拒因要点名「内容被动过」（与「没有原像」是两句话）：%s", rep.Mismatch)
	}
	if !strings.Contains(rep.String(), "✗") {
		t.Errorf("不符的报告要带 ✗，别让人读成通过：%s", rep)
	}
}

// ⑤ 改一个**没被原像覆盖**的字段（claim）：payload 比对不会动，必须由整格 seal 抓到。
// 这一条把 CheckChain 的第 4 件事单独钉住，而不是让它蹭第 3 件事的红。
func TestEditedClaimIsCaughtByTheSealNotThePayload(t *testing.T) {
	docs := journalFixture(t).Docs()
	b, err := MarshalJournal(docs, PayloadIncluded)
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	edited := strings.Replace(string(b), `"claim":"第 2 发"`, `"claim":"第 2 发（改过的）"`, 1)
	if edited == string(b) {
		t.Fatal("针没落上去：一个 claim 都没改到 —— 这条测试成了空针")
	}
	j, err := ReadJournal([]byte(edited))
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	rep := CheckChain(j.Docs)
	if rep.Mismatch == "" {
		t.Fatal("改了 claim 却没被抓到")
	}
	if strings.Contains(rep.Mismatch, "内容被动过") {
		t.Errorf("改 claim 不该报成「内容被动过」（payload 没动），要说清是整格指纹：%s", rep.Mismatch)
	}
	if !strings.Contains(rep.Mismatch, "整格指纹重算不符") {
		t.Errorf("拒因该点名整格 seal：%s", rep.Mismatch)
	}
}

// ⑥ 空原像 ≠ 没有原像。absent 那一档落账时本来就不带值，Payload 是 nil、
// PayloadH 是对「空」算出来的；把这样一条编成 payload_b64:"" 再读回来，
// 必须读成「带原像、且比对通过」，而不是「没有原像、核不了」。
// 这一条专门钉住我在这份文件里差点踩的那个坑：拿 len(Payload) 当「没有原像」的信号。
func TestEmptyPreimageRoundTripsAsPresentNotAbsent(t *testing.T) {
	l := NewLedger()
	if _, err := l.Append(Record{
		Session: "sess-j", Actor: "assembler", Step: 1,
		Kind: KindModelInput + ":first_byte_ms", Claim: "这一发非流式，没有首帧",
		Basis: BasisAbsent,
	}); err != nil {
		t.Fatalf("带理由的缺席该被收下：%v", err)
	}
	docs := l.Docs()
	if docs[0].Payload != nil {
		t.Fatalf("夹具的前提变了：这条的 Payload 不是 nil（%q）", docs[0].Payload)
	}
	b, err := MarshalJournal(docs, PayloadIncluded)
	if err != nil {
		t.Fatalf("带原像编码该成功（内存里 Payload 为 nil 的那条，它的原像就是「空」，编成 payload_b64:\"\" 是如实的）：%v", err)
	}
	if !strings.Contains(string(b), `"payload_b64":""`) {
		t.Errorf("这一行该带一个空原像字段，而不是省略它 —— 省略=没带，空=带了一个空值：%s", b)
	}
	j, err := ReadJournal(b)
	if err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	if j.WithPayload != 1 || j.NoPayload != 0 {
		t.Errorf("一个空原像被读成了「没有原像」：带=%d 不带=%d —— 这正是本包要消灭的那一族", j.WithPayload, j.NoPayload)
	}
	if rep := CheckChain(j.Docs); !rep.Decidable() {
		t.Errorf("空原像那条本该比对得过（sum(空) 就是它的 PayloadH）：%s", rep)
	}
	// 更要紧的是**编出去之前**那一步：同一条记录在内存里 Payload 就是 nil，
	// 而它的指纹本来就是「对空算出来的」—— 手里明明有它的原像（那个空），
	// 所以它必须算「比对过」，不许进「核不了」。
	// 反面（这根针要抓的形状）：拿 e.Payload == nil 当「没有原像」的信号。
	// 那样一切在内存里就没带值的记录（absent 那一整档）都会被念成核不了，
	// 而真正没原像的只有「从盘上读回来、那一行省略了 payload_b64」这一种。
	if memRep := CheckChain(docs); !memRep.Decidable() || memRep.Checked != len(docs) {
		t.Errorf("内存里 Payload 为 nil 不等于「没有原像」（它的指纹就是对空算出来的）：%s", memRep)
	}
}

// ⑦ 读回来的账本要能接着记，而且**去重闸看得见旧账**。
// 这是缺口第 8 条里「跨账本」那一半的一半解药：alreadyRecorded 认的是同一个内存
// Ledger 里的 session + 发号 + 侧，所以进程重启后从盘上读回来再记同一发，必须被拒。
// 反面的形状：读回来只当数据放着、docs 没填进账本，于是同一发记两遍而链照样自洽。
func TestLoadedLedgerRefusesToRecordTheSameCallTwice(t *testing.T) {
	in, out := validInput(t), validOutput(t)
	w := NewLedger()
	n, err := w.RecordCall("sess-j", "assembler", in, out)
	if err != nil {
		t.Fatalf("整发落账失败：%v", err)
	}
	b, err := MarshalJournal(w.Docs(), PayloadIncluded)
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	l, j, _, err := LoadLedger(b)
	if err != nil {
		t.Fatalf("载入失败：%v", err)
	}
	if j.Parsed != n || len(l.Docs()) != n {
		t.Fatalf("读回 %d 条、账本里 %d 条，写出去 %d 条", j.Parsed, len(l.Docs()), n)
	}
	_, errAgain := l.RecordCall("sess-j", "assembler", in, out)
	if errAgain == nil {
		t.Fatal("重启后把同一发再记一遍必须被拒 —— 让它过去，链上就会有两套同一发的字节与 token")
	}
	if !strings.Contains(errAgain.Error(), "已经记过") {
		t.Errorf("拒因要点名去重闸（别蹭别的规则的红）：%v", errAgain)
	}
	if l.Rejected() != 1 || len(l.Docs()) != n {
		t.Errorf("重记被拒了却没计数/链还长了：Rejected=%d、Docs=%d 条（基线 %d 条）", l.Rejected(), len(l.Docs()), n)
	}
	// 下一发必须照常记得进去 —— 载入不是把账本封死。
	next := in
	next.Call++
	nextOut := out
	nextOut.Call++
	if _, err := l.RecordCall("sess-j", "assembler", next, nextOut); err != nil {
		t.Errorf("载入后的账本该能接着记下一发：%v", err)
	}
	if err := l.Verify(); err != nil {
		t.Errorf("载入后新记的账链不符：%v", err)
	}
}

// ⑧ 坏掉的一行必须带行号报出来，而且不许被静默丢掉。
// 反面的形状：解不开就 continue，最后返回 46 条 —— 读的人以为账上只有 46 条，
// 而真凶是那行坏了。链检查会把它表现成一次截断，于是下一个人去查「谁抽走了一条」。
func TestBadLineIsReportedWithItsLineNumber(t *testing.T) {
	docs := journalFixture(t).Docs()
	b, err := MarshalJournal(docs, PayloadIncluded)
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	lines[1] = "{这不是 JSON"
	j, err := ReadJournal([]byte(strings.Join(lines, "\n") + "\n"))
	if err == nil {
		t.Fatal("一行解不开必须返回错误 —— 静默少一条比多一条更难查")
	}
	if !strings.Contains(err.Error(), "第 2 行") {
		t.Errorf("拒因要点名是第几行（「有一条坏了」没法去查）：%v", err)
	}
	if j.Lines != len(docs) || j.Parsed != len(docs)-1 {
		t.Errorf("分母该把「扫了几行/解开几行」分开：Lines=%d Parsed=%d（写出去 %d 条）", j.Lines, j.Parsed, len(docs))
	}
	if !strings.Contains(j.String(), "解不开") {
		t.Errorf("Journal 自己要把坏行数说出来，别只报条数：%s", j)
	}
}

// ⑨ 字节流的字段名必须与 Entry 自己的 json tag 一一对上。
// 为什么这条值得单独钉：框架里最自然的写法是 `json.Marshal(entry)`，
// 而 journalLine 是手写的另一份形状 —— 谁改了 Entry 上一个 tag，
// 旧文件与新格式就会**各自都解得开、内容却不相等**，那是一场不报警的漂。
func TestJournalFieldNamesMatchEntryTags(t *testing.T) {
	fromEntry := map[string]bool{}
	for _, f := range reflect.VisibleFields(reflect.TypeOf(Entry{})) {
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		fromEntry[tag] = true
	}
	fromLine := map[string]bool{}
	for _, f := range reflect.VisibleFields(reflect.TypeOf(journalLine{})) {
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		fromLine[tag] = true
	}
	// payload_b64 是格式层独有的（原像带不带由它说），所以它不参与对表。
	delete(fromLine, "payload_b64")
	var onlyEntry, onlyLine []string
	for k := range fromEntry {
		if !fromLine[k] {
			onlyEntry = append(onlyEntry, k)
		}
	}
	for k := range fromLine {
		if !fromEntry[k] {
			onlyLine = append(onlyLine, k)
		}
	}
	if len(onlyEntry) > 0 || len(onlyLine) > 0 {
		t.Errorf("格式与 Entry 的字段名漂了：Entry 独有 %v、journalLine 独有 %v ——"+
			"两边各自解得开但内容不等，是一场不报警的漂移", onlyEntry, onlyLine)
	}
}

// ⑩ CheckChain 是 Verify 的唯一判据（规矩三：一个定义只有一个 owner）。
// 两份规则的话，「内存里绿的」与「读回来红的」迟早同时出现，而那时没人说得清哪份对。
func TestVerifyDelegatesToTheSameJudgmentAsCheckChain(t *testing.T) {
	docs := journalFixture(t).Docs()
	b, err := MarshalJournal(docs, PayloadOmitted)
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	l, _, rep, err := LoadLedger(b)
	if err != nil {
		t.Fatalf("载入失败：%v", err)
	}
	// 同一份 docs，两条路读回来的结论必须一模一样（连措辞的指向都要一致）。
	vErr := l.Verify()
	if vErr == nil || rep.Decidable() {
		t.Fatalf("两条路必须同时降级：Verify 报了=%v、CheckChain.Decidable=%v", vErr, rep.Decidable())
	}
	if strings.Contains(vErr.Error(), "被动过") != strings.Contains(rep.Mismatch, "被动过") {
		t.Errorf("Verify 与 CheckChain 对「是不是篡改」的判断不一致：Verify=%v / Mismatch=%q", vErr, rep.Mismatch)
	}
	// 再拿一本纯内存的账（原像都在）确认两条路也同时是绿的。
	if err := journalFixture(t).Verify(); err != nil {
		t.Errorf("内存账本（原像齐全）Verify 该是绿的：%v", err)
	}
}

// TestNothingBookedDoesNotWearGreen：一本都还没有，不许被念成「核过了、全对得上」。
// 两个方向都要判，否则把判据写成"永远不打勾"也照样绿：
//
//	①空账：不许出现 ✓，且必须点名「尚无账可核 / 一格都没记过」；
//	②有账且原像齐全：仍然必须打 ✓、分桶行仍然必须有档名。
//
// 这一支在 10-15 之前**没有任何测试管过**空账 —— 所以那个 ✓ 是渲染顺手套上的，
// 而不是谁判过的（实例：`mcphttp_up.sh status` 读到 `账 0 条（）`，工具面第一次 verify 读到
// `✓ 链自洽（共 0 条，其中 0 条比对过原像）`，两份原话在 报告/工具面挂载实测-1015.txt）。
//
// 边界也说清：`Verify()` 与 `Decidable()` 的语义没动 —— 它们说的是"手里有没有不符、字节够不够重算"，
// 空账上那两句确实都成立。所以 `Verify()==nil` **不是通过证书**（与 contract.md 对 check_call 的
// 措辞同一族），堵住"读成人话就像通过"这一层的是下面这两个渲染。
func TestNothingBookedDoesNotWearGreen(t *testing.T) {
	empty := CheckChain(nil)
	if strings.Contains(empty.String(), "✓") {
		t.Errorf("空链打了 ✓（那会被读成「核过了、都对得上」）：%s", empty.String())
	}
	if !strings.Contains(empty.String(), "尚无账可核") {
		t.Errorf("空链没点名「尚无账可核」：%s", empty.String())
	}
	if s := NewLedger().String(); strings.Contains(s, "（）") || strings.Contains(s, "=") {
		t.Errorf("空账的分桶行长得像一张填好的表（档名=数字）：%s", s)
	} else if !strings.Contains(s, "一格都没记过") {
		t.Errorf("空账没点名「一格都没记过」：%s", s)
	}

	// 反面对照：真有账、原像齐全。
	good := journalFixture(t)
	rep := CheckChain(good.Docs())
	if !strings.Contains(rep.String(), "✓ 链自洽") {
		t.Errorf("有账且核过之后不许丢 ✓：%s", rep.String())
	}
	if s := good.String(); strings.Contains(s, "（）") || !strings.Contains(s, "=") {
		t.Errorf("非空账的分桶行该有档名=计数：%s", s)
	}
}

// ⑪ 认表头的那一句判定：**只有那一句话算表头**，别的都不算。
//
// 为什么判定住在这里而不是外壳：交付件（`构建-1017/客服靶场/导出/账本-全量-1018.jsonl`）的第一行
// 就是外壳 encode 出去的那句人读表头，而此前只有外壳认它 —— 按出厂 API 直接喂 `kernel.LoadLedger`
// 的那条路上，2941 行被读成「有一行解不开」、整本拒。字节是内核交的，认它的不该分内外壳。
// 两头都要判：恒 false 会把往返打死；放宽成"任何非 JSON 行"会把真坏账放过去（少一条还报绿）。
func TestEncodeHeaderLineIsTheOnlyThingTheKernelRecognizes(t *testing.T) {
	docs := journalFixture(t).Docs()
	b, err := MarshalJournal(docs, PayloadIncluded)
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	firstDoc := strings.Split(string(b), "\n")[0]
	// 表头用内核自己的固定串拼出来 —— 测试里再抄一遍字面量，就成了第二个 owner。
	head := "1483862 " + EncodeHeaderMark + "2940 行链上条目 + 1 行闸门计数（写盘是你的事，内核只交字节）"
	for _, c := range []struct {
		why  string
		line string
		want bool
	}{
		{"真表头", head, true},
		{"链行（以 { 开头，永远不是表头）", firstDoc, false},
		{"别的非 JSON 废话（放宽就漏这一条）", "这不是账本 ，2940 行", false},
		{"空行（本来就不是账，也不许计进表头数）", "   ", false},
		{"带固定串但确实是 JSON 的一格", `{"note":"字节 JSONL，"}`, false},
	} {
		if got := IsEncodeHeader(c.line); got != c.want {
			t.Errorf("%s：IsEncodeHeader 回了 %v，期望 %v ｜ 原文 %q", c.why, got, c.want, c.line)
		}
	}
}

// ⑫ 出厂形状的第一次真往返：表头 + 三条账的字节，原样喂 kernel.LoadLedger。
//
// 期望的不是「剥一行之后能过」，是**这一份字节本身**能过，而且那一行既不算账也不算坏账：
// Lines 里根本没有它（HeaderLines 单列），所以 String 不会替读者编出一句「有 1 行解不开」
// —— 那是本包最恨的形状换了个住处：一行没带账的字节被读成一次篡改嫌疑。
// 反面同发带着：把表头换成别的废话，必须仍然整本拒（认表头不是"永远放过第一行"）。
func TestHeaderPrefixedBytesLoadBackThroughTheKernel(t *testing.T) {
	docs := journalFixture(t).Docs()
	b, err := MarshalJournal(docs, PayloadIncluded)
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	head := "1483862 " + EncodeHeaderMark + "3 行链上条目 + 没有计数栏（写盘是你的事，内核只交字节）\n"

	l, j, rep, err := LoadLedger([]byte(head + string(b)))
	if err != nil {
		t.Fatalf("带表头的出厂字节喂不回自己的装载器：%v（BadLines=%v）", err, j.BadLines)
	}
	if j.HeaderLines != 1 {
		t.Errorf("表头该单列计数：HeaderLines=%d（期望 1）", j.HeaderLines)
	}
	if j.Lines != len(docs) || j.Parsed != len(docs) {
		t.Errorf("表头不该进 Lines 的分母：Lines=%d Parsed=%d（账有 %d 条）", j.Lines, j.Parsed, len(docs))
	}
	if len(j.BadLines) != 0 {
		t.Errorf("认出来的表头不许当坏账：%v", j.BadLines)
	}
	if len(l.Docs()) != len(docs) {
		t.Errorf("装进来的条数变了：进去 %d 回来 %d", len(docs), len(l.Docs()))
	}
	if !rep.Decidable() {
		t.Errorf("往返后的链该是「核过且自洽」：%s", rep)
	}
	if err := l.Verify(); err != nil {
		t.Errorf("带表头读回来的账本 Verify 红：%v", err)
	}
	if s := j.String(); !strings.Contains(s, "人读表头") || strings.Contains(s, "解不开") {
		t.Errorf("String 要说清那一行是表头而不是坏账：%s", s)
	}

	// 反面：第一行是别的非 JSON 文本 —— 整本拒，且罪名是"解不开"而不是"表头"。
	g := head + string(b)
	g = strings.Replace(g, EncodeHeaderMark, "这不是账本 ，", 1)
	l2, j2, _, err := LoadLedger([]byte(g))
	if err == nil {
		t.Fatalf("第一行被换成别的废话还收下（认表头写宽了）：HeaderLines=%d", j2.HeaderLines)
	}
	if l2 != nil {
		t.Errorf("拒了就不该有账本在手：%v", l2)
	}
	if j2.HeaderLines != 0 {
		t.Errorf("那句废话不是表头，不许计进 HeaderLines：%d", j2.HeaderLines)
	}
	if len(j2.BadLines) != 1 || !strings.Contains(j2.BadLines[0], "第 1 行") {
		t.Errorf("要逐行点名是第几行解不开（「有一行坏了」没法去查）：%v", j2.BadLines)
	}
	if j2.Lines != len(docs)+1 || j2.Parsed != len(docs) {
		t.Errorf("坏账那一行必须留在 Lines 分母里：Lines=%d Parsed=%d", j2.Lines, j2.Parsed)
	}

	// 第三态：一份一条账都没有、只有表头的字节 —— 不许被念成「空账本一切正常」，
	// 也不许被念成「有 1 行解不开」。它说的是：这本账是空的，另外你多给了一行说明。
	j3, err3 := ReadJournal([]byte(head))
	if err3 != nil {
		t.Errorf("只有表头的字节不是坏账，不该返回错误：%v", err3)
	}
	if s := j3.String(); !strings.Contains(s, "读回 0 条") || !strings.Contains(s, "人读表头") {
		t.Errorf("空账加一行说明，两句都要说出来：%s", s)
	}
}

// ⑬ 表头那句只在真有的时候说。
// 恒说会教出下一个「计数栏：有（拒落账 0）」那种假读数；恒不说则字节里那一行没人认领。
func TestHeaderClauseAppearsOnlyWhenThereIsAHeader(t *testing.T) {
	docs := journalFixture(t).Docs()
	b, err := MarshalJournal(docs, PayloadIncluded)
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	plain, err := ReadJournal(b)
	if err != nil {
		t.Fatalf("不带表头的一份该读得开：%v", err)
	}
	if s := plain.String(); strings.Contains(s, "表头") {
		t.Errorf("这份字节里一行表头都没有，String 却凭空报了一句：%s", s)
	}
	withHead, err := ReadJournal(append([]byte("9 "+EncodeHeaderMark+"3 行（写盘是你的事，内核只交字节）\n"), b...))
	if err != nil {
		t.Fatalf("带表头的一份该读得开：%v", err)
	}
	s := withHead.String()
	if !strings.Contains(s, "另有 1 行人读表头") {
		t.Errorf("数出来的那一行没上屏（读的人会以为他把这些字节完整带走了）：%s", s)
	}
	if withHead.HeaderLines != 1 {
		t.Errorf("屏上那句与格子对不上：HeaderLines=%d", withHead.HeaderLines)
	}
}
