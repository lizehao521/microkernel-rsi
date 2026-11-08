package kernel

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 这一份文件守 record.go 那道接线：元数据两侧的**每一格**都必须真进哈希链账本，
// 缺席必须落成 absent 档而不是 0，坏账必须整发拒收且拒收有数，账上不许留半发。
// 每条判据两头都有 —— 只测「好的能过」等于「永远报警也算通过」。

// sortedCellKeys 把键名表的键名取出来排好序，供与账本读回来的那份逐格比对。
// 期望值一律从 Cells() 现算，不抄常量：抄常量等于把答案手抄进考卷，夹具一改就误报。
func sortedCellKeys(cells []Cell) []string {
	out := make([]string, 0, len(cells))
	for _, c := range cells {
		out = append(out, c.Key)
	}
	sort.Strings(out)
	return out
}

func containsKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

// ① 往返对齐：键名表交出几格，账上就得有几条，键名表逐格相同。
// 少一格 ⇒ 那个量在链上无从重算（「没写进去」会被读成「没发生」）；
// 多一格 ⇒ 账上住了一个核不回结构体的幽灵键。两个方向都靠「条数 + 逐格」抓住。
func TestRecordInputRoundTripsCellForCellIntoTheLedger(t *testing.T) {
	in := validInput(t)
	l := NewLedger()
	n, err := l.RecordInput("sess-1", "assembler", in)
	if err != nil {
		t.Fatalf("自洽的输入侧该被整发记账：%v", err)
	}
	cells := in.Cells()
	if len(cells) == 0 {
		t.Fatal("夹具的键名表是空的 —— 这条测试成了空针")
	}
	if n != len(cells) {
		t.Errorf("键名表 %d 格，账上记了 %d 条 —— 少一格是一个量无从重算，多一格是幽灵键", len(cells), n)
	}
	got := Keys(l.Docs(), "sess-1", in.Call, KindModelInput)
	want := sortedCellKeys(cells)
	if len(got) != len(want) {
		t.Fatalf("读回来 %d 个键名，键名表有 %d 个", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 格：账上是 %q，键名表上是 %q", i+1, got[i], want[i])
		}
	}
	// 每一格的指纹都必须能由它自己的 payload 重算出来：
	// 「拿账上这一行能核回原值」这句话靠这里成立，不靠注释。
	for _, c := range CellsOf(l.Docs(), "sess-1", in.Call, KindModelInput) {
		if len(c.Payload) == 0 {
			t.Errorf("%s 那一格的 payload 是空的 —— 无从核起", c.Key)
		}
		if c.PayloadSHA != sha(string(c.Payload)) {
			t.Errorf("%s 那一格的指纹与它的 payload 对不上", c.Key)
		}
	}
}

// ② 缺席在账本上也必须是缺席，不是 0。
// 真实缺陷形状：runloop 把非流式那一发的分帧数写成 0，而 0 会被下一个人读成
// 「一帧都没收到」—— 那恰好是协议错的判据。所以 AbsentKeys 必须点名没量到的那几格，
// 且每一格的人话自己说出「没量到」；流式那一发的同名三格则必须**不**在名单里。
//
// 一处与任务口径的偏离，刻意为之：这份夹具里缺席的不止分帧三格 ——
// validOutput 自带的 retry_after_ms（「这一发没故障」）也是缺席。
// 所以「正好三格」按夹具自己算出来的键名表写：三格是必须点名的子集，整表不许多也不许少。
func TestAbsentCellsLandOnTheLedgerAsAbsentNotZero(t *testing.T) {
	out := validNonStreaming(t)
	l := NewLedger()
	n, err := l.RecordOutput("sess-1", "endpoint-watcher", out)
	if err != nil {
		t.Fatalf("非流式那一发该被记账：%v", err)
	}
	if n != len(out.Cells()) {
		t.Errorf("键名表 %d 格，账上记了 %d 条", len(out.Cells()), n)
	}
	docs := l.Docs()
	want := []string{}
	for _, c := range out.Cells() {
		if m, ok := c.Value.(Measure); ok && m.Absent {
			want = append(want, c.Key)
		}
	}
	sort.Strings(want)
	got := AbsentKeys(docs, "sess-1", out.Call, KindModelOutput)
	if len(got) != len(want) {
		t.Fatalf("AbsentKeys 有 %d 个（%v），键名表算出来 %d 个（%v）", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个：AbsentKeys 里是 %q，键名表算出来是 %q", i+1, got[i], want[i])
		}
	}
	streamKeys := []string{"chunks", "first_byte_ms", "max_gap_ms"}
	for _, k := range streamKeys {
		if !containsKey(got, k) {
			t.Errorf("非流式那一发的 %s 必须被点名成缺席 —— 否则它会被读成 0", k)
		}
	}
	for _, c := range CellsOf(docs, "sess-1", out.Call, KindModelOutput) {
		if !containsKey(streamKeys, c.Key) {
			continue
		}
		if c.Basis != BasisAbsent {
			t.Errorf("%s 那一格在账上的出处是 %s，不是 absent 档 —— 缺席被翻译成了别的东西", c.Key, c.Basis)
		}
		if !strings.Contains(c.Claim, "没量到") {
			t.Errorf("%s 那一格的人话是 %q，没说「没量到」—— 它会被当成一个值读走", c.Key, c.Claim)
		}
	}
	// 反方向：流式那一发的三格必须不在 AbsentKeys 里 —— 该量到的数不许被丢成缺席。
	s := validOutput(t)
	l2 := NewLedger()
	if _, err := l2.RecordOutput("sess-1", "endpoint-watcher", s); err != nil {
		t.Fatalf("流式那一发该被记账：%v", err)
	}
	got2 := AbsentKeys(l2.Docs(), "sess-1", s.Call, KindModelOutput)
	for _, k := range streamKeys {
		if containsKey(got2, k) {
			t.Errorf("流式那一发的 %s 竟被记成缺席 —— 该量到的数被丢成了「没量到」", k)
		}
	}
	// 缺席是逐格判的，不是整侧判的。这一格必须还在名单里，否则上面三条「不在」
	// 可能只是因为 AbsentKeys 永远交空表 —— 那条判据就成了空针。
	if !containsKey(got2, "retry_after_ms") {
		t.Errorf("流式那一发的 retry_after_ms 该仍是缺席，得到 %v", got2)
	}
}

// ③ 坏账整发拒收，且拒收有数。
// 只断「报错了」不够：一个先写进去再报错的实现照样能过 ——
// 所以还必须钉住账上一条都没多出来、Rejected 正好 +1。
// 错误信息里必须带发号：报红说不出是哪一发，下一个人就得去猜。
func TestBrokenInputIsRefusedWholeAndCounted(t *testing.T) {
	l := NewLedger()
	in := validInput(t)
	n, err := l.RecordInput("sess-1", "assembler", in)
	if err != nil {
		t.Fatalf("好的那一发该先记进去（不然「账不变」没有基线）：%v", err)
	}
	before := len(l.Docs())
	if before != n || before == 0 {
		t.Fatalf("基线不对：记了 %d 条，账上读回来 %d 条", n, before)
	}
	if l.Rejected() != 0 {
		t.Fatalf("基线上不该有拒收，得到 %d", l.Rejected())
	}
	bad := validInput(t)
	bad.Call = 7 // 发号刻意与好那发不同：那句话必须把它点出来
	bad.ContentBytes++
	n2, err := l.RecordInput("sess-1", "assembler", bad)
	if err == nil {
		t.Fatal("内容总字节与段表之和对不上，竟被收了")
	}
	if !strings.Contains(err.Error(), "第 7 发") {
		t.Errorf("那句拒收的话没带发号：%v", err)
	}
	if n2 != 0 {
		t.Errorf("被拒的那一发报出来写了 %d 条", n2)
	}
	if got := len(l.Docs()); got != before {
		t.Errorf("账上从 %d 条变成了 %d 条 —— 被拒的那一发留下了记录，「没写进去」会被读成「写了一半」", before, got)
	}
	if l.Rejected() != 1 {
		t.Errorf("拒收该正好 +1（不然「没写进去」会被读成「没发生」），得到 %d", l.Rejected())
	}
}

// ④ 不许写半发。「有请求没响应」（或反过来）是下游最难诊断的形状 ——
// 它长得像管路断了，而根因其实在记账这边。三种坏形状逐个来：
// 输入侧坏、输出侧坏、两侧发号对不上，每种都必须两侧一条不写、拒收正好计一次。
func TestRecordCallNeverLeavesHalfACallOnTheLedger(t *testing.T) {
	for _, c := range []struct {
		name string
		in   func(*testing.T) InputMeta
		out  func(*testing.T) OutputMeta
		want string
	}{
		{"输入侧坏", func(t *testing.T) InputMeta { m := validInput(t); m.ContentBytes++; return m }, validOutput, "输出侧也不写"},
		{"输出侧坏", validInput, func(t *testing.T) OutputMeta { m := validOutput(t); m.Finish.Said = false; return m }, "输入侧也不写"},
		{"两侧发号对不上", validInput, func(t *testing.T) OutputMeta { m := validOutput(t); m.Call = 2; return m }, "发号对不上"},
	} {
		l := NewLedger()
		n, err := l.RecordCall("sess-1", "assembler", c.in(t), c.out(t))
		if err == nil {
			t.Errorf("%s：RecordCall 竟收了 —— 这条分支是空针", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：那句话没说清另一侧的下场（要带 %q）：%v", c.name, c.want, err)
		}
		if n != 0 {
			t.Errorf("%s：报出来写了 %d 条", c.name, n)
		}
		if got := len(l.Docs()); got != 0 {
			t.Errorf("%s：账上竟多了 %d 条 —— 那正是半发", c.name, got)
		}
		if l.Rejected() != 1 {
			t.Errorf("%s：拒收该正好计一次，得到 %d", c.name, l.Rejected())
		}
	}
}

// ⑤ 好的那头：对齐的一发必须整发进账。没有这一头，上面几条全绿也可能只是
// 「什么都拒收」—— 一个永远报警的闸门同样能通过所有拒收测试。
func TestAnAlignedCallIsRecordedOnBothSides(t *testing.T) {
	in, out := validInput(t), validOutput(t)
	l := NewLedger()
	n, err := l.RecordCall("sess-1", "assembler", in, out)
	if err != nil {
		t.Fatalf("对齐的一发该被整发记账：%v", err)
	}
	want := len(in.Cells()) + len(out.Cells())
	if want == 0 {
		t.Fatal("夹具两侧键名表都是空的 —— 这条测试成了空针")
	}
	if n != want {
		t.Errorf("两侧共 %d 格，记了 %d 条", want, n)
	}
	if got := len(l.Docs()); got != n {
		t.Errorf("返回说记了 %d 条，账上实际 %d 条 —— 两个数得是同一批记录数出来的", n, got)
	}
	if l.Rejected() != 0 {
		t.Errorf("正常的一发不该有拒收计数，得到 %d", l.Rejected())
	}
	if err := l.Verify(); err != nil {
		t.Errorf("记完的链必须能重算通过：%v", err)
	}
}

// ⑥ 盖在元数据上的链必须真能重算：没动过是绿的，改一句人话必须红。
// 否则链上那些哈希只是装饰 —— 谁改了哪一格的说法都不会被发现，
// 而这份账存在的理由恰恰是「写下来的没被动过」可以被第三方核。
func TestRecordedChainRecomputesAndCatchesTampering(t *testing.T) {
	l := NewLedger()
	if _, err := l.RecordCall("sess-1", "assembler", validInput(t), validOutput(t)); err != nil {
		t.Fatalf("对齐的一发该被记账：%v", err)
	}
	if len(l.docs) < 3 {
		t.Fatalf("账上只有 %d 条 —— 改不到中间那条，这条测试成了空针", len(l.docs))
	}
	if err := l.Verify(); err != nil {
		t.Fatalf("没动过的账必须能重算通过：%v", err)
	}
	// 同包直接动非导出字段（照 kernel_test.go 那一条的写法）：
	// 只改一句人话、不动指纹，重算必须抓到。
	k := len(l.docs) / 2
	l.docs[k].Claim += "（这句话被人改过）"
	err := l.Verify()
	if err == nil {
		t.Fatal("改了一条的 Claim 之后 Verify 竟然过了 —— 链是装饰")
	}
	if !strings.Contains(err.Error(), "整格指纹重算不符") {
		t.Errorf("改 Claim 报出来的该是整格指纹那一句，得到 %q", err.Error())
	}
}

// ⑦ basisOf 是「格子」到「账本条目出处」的唯一翻译官。五个拒绝分支各说各的话：
// 共用一句话等于没说是哪种坏（runloop 里「一句空红比不报更糟」那一族）。
// 好的那头两条：如实记的缺席必须翻译成 absent 档并带着理由 ——
// 缺席被翻成 0 或 unset 正是这个包要消灭的根形状；出处两处一致的实测量原样放行。
func TestBasisOfRefusesEveryShapeOfBasisLying(t *testing.T) {
	refused := []struct {
		name string
		c    Cell
		want string
	}{
		{"值是缺席、格子却带着出处", Cell{Key: "chunks", Field: "Chunks", Value: AbsentBecause("这一发不分帧"), Basis: BasisMeasured}, "值是缺席"},
		{"值有数、却没说谁算的", Cell{Key: "chunks", Field: "Chunks", Value: MeasuredOf(37), Basis: BasisUnset}, "没说谁算的"},
		{"两处出处漂了", Cell{Key: "tokens", Field: "Tokens", Value: MeasuredOf(1600), Basis: BasisRelayed, Source: "endpoint"}, "两处出处对不上"},
		{"非三态的值没带出处", Cell{Key: "call", Field: "Call", Value: int64(7), Basis: BasisUnset}, "没带出处"},
		{"非三态的值报缺席", Cell{Key: "call", Field: "Call", Value: int64(7), Basis: BasisAbsent}, "只有三态量和 nil 名单能报缺席"},
	}
	msgs := make([]string, 0, len(refused))
	for _, c := range refused {
		b, claim, err := basisOf(c.c)
		if err == nil {
			t.Errorf("%s：basisOf 竟收了（翻译成 %s / %q）—— 这条分支是空针", c.name, b, claim)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：那句话没点名形状（要带 %q）：%v", c.name, c.want, err)
		}
		if b != BasisUnset || claim != "" {
			t.Errorf("%s：拒了的翻译还交出了结果（%s / %q）—— 调用方可能拿它当好翻译落账", c.name, b, claim)
		}
		msgs = append(msgs, err.Error())
	}
	// 五句话必须两两可区分：分不出来等于没说是哪一格坏。
	for i := range msgs {
		for j := i + 1; j < len(msgs); j++ {
			if msgs[i] == msgs[j] {
				t.Errorf("第 %d 与第 %d 个分支说了同一句话：%q", i+1, j+1, msgs[i])
			}
		}
	}
	// 好向一：如实记的缺席翻译成 absent 档，且人话自己说出「没量到，不是 0」。
	b, claim, err := basisOf(Cell{Key: "chunks", Field: "Chunks", Value: AbsentBecause("这一发不分帧"), Basis: BasisUnset})
	if err != nil {
		t.Fatalf("如实记的缺席该被收下：%v", err)
	}
	if b != BasisAbsent {
		t.Errorf("缺席必须翻译成 absent 档，得到 %s —— 少了这一档，缺席就只能落成 0", b)
	}
	if strings.TrimSpace(claim) == "" {
		t.Error("翻译出来的 absent 没带理由 —— 它与「忘了填」在账上就分不开了，而 absent 档存在的全部理由就是把它俩分开")
	}
	if !strings.Contains(claim, "没量到") {
		t.Errorf("缺席的人话必须说出「没量到」，得到 %q", claim)
	}
	// 好向二：有值、两处出处一致的实测量原样放行，人话就是那个数。
	b, claim, err = basisOf(Cell{Key: "chunks", Field: "Chunks", Value: MeasuredOf(37), Basis: BasisMeasured})
	if err != nil || b != BasisMeasured || claim != "37" {
		t.Errorf("自洽的实测格该原样放行（measured / 37），得到 %s / %q / err=%v", b, claim, err)
	}
}

// withoutNoticePart 把夹具改成「这一发没挤任何东西，所以也没发通知」那一种形状。
// 段表与 Notice 必须一起改：`Check()` 把「发出去了」钉在真发出去的段表上，
// 只清 Notice 而留着 truncation-notice 那一段会被整发拒收，而那不是本条要测的事。
func withoutNoticePart(in InputMeta) InputMeta {
	order := make([]PartKind, 0, len(in.Order))
	for _, k := range in.Order {
		if k != KindNotice {
			order = append(order, k)
		}
	}
	parts := make([]PartCount, 0, len(in.Parts))
	var sum int64
	for _, p := range in.Parts {
		if p.Kind == KindNotice {
			continue
		}
		parts = append(parts, p)
		sum += p.Bytes
	}
	in.Order, in.Parts, in.ContentBytes, in.Notice = order, parts, sum, Notice{}
	return in
}

// 没交的裁剪表必须落成缺席，不是「实测：0 段」。
//
// 两个事实原先住在同一格上：调用方没交 `Cuts` 这个键 ⇒ 解码后是一张 nil 名单；
// 交了一张空表 ⇒ 非 nil 空。前者是「这一发没有裁剪这一说」，后者才是
// 「查过，一档都没发生」。一律标 measured 的话，账上两种都成一句
// 「0 段（裁剪表是空的）」，而拿它当证据的常常是下一个 Agent —— 它会据此说「没挤过东西」。
//
// 两头都测：只测 nil 那一头，等于把「空表也被记成缺席」这个改过头的反向缺陷放过去。
func TestNilCutListLandsAsAbsentWhileAnEmptyOneStaysMeasured(t *testing.T) {
	nilIn := withoutNoticePart(validInput(t))
	nilIn.Cuts = nil
	emptyIn := withoutNoticePart(validInput(t))
	emptyIn.Cuts = []Cut{}

	for _, tc := range []struct {
		name  string
		sess  string
		in    InputMeta
		basis Basis
		claim string
	}{
		{"没交这张表", "sess-cuts-nil", nilIn, BasisAbsent, absentListClaim},
		{"交了一张空表", "sess-cuts-empty", emptyIn, BasisMeasured, "0 段（裁剪表是空的）"},
	} {
		if err := tc.in.Check(); err != nil {
			t.Fatalf("夹具 %s 自己就不自洽：%v", tc.name, err)
		}
		l := NewLedger()
		if _, err := l.RecordCall(tc.sess, "assembler", tc.in, validOutput(t)); err != nil {
			t.Fatalf("%s：整发该被收下（缺席不是拒收的理由）：%v", tc.name, err)
		}
		// 档名从 CutLists 那**一份**定义里取，不在这里抄一遍：抄了的话，
		// 将来加一档会测不到，而这一条判据管的正是「每一档都得说得出自己」。
		for _, cl := range CutLists {
			key := cl.Key
			c := wantCell(t, recordedByKey(l.Docs(), tc.sess, 1, KindModelInput), key)
			if c.Basis != tc.basis {
				t.Errorf("%s：%s 那一格落成了 %s，该是 %s", tc.name, key, c.Basis, tc.basis)
			}
			if c.Claim != tc.claim {
				t.Errorf("%s：%s 那一格的人话是 %q，该是 %q", tc.name, key, c.Claim, tc.claim)
			}
			// 缺席那一张的原像是 null，空表那一张是 [] —— 第三方重算时分的也正是这一个差别。
			want := "[]"
			if tc.basis == BasisAbsent {
				want = "null"
			}
			if string(c.Payload) != want {
				t.Errorf("%s：%s 那一格的原像是 %s，该是 %s —— 出处改了而原像没改，等于只改了一半",
					tc.name, key, c.Payload, want)
			}
		}
		got := AbsentKeys(l.Docs(), tc.sess, 1, KindModelInput)
		if tc.basis == BasisAbsent {
			for _, cl := range CutLists {
				if !containsKey(got, cl.Key) {
					t.Errorf("%s：%s 没被 AbsentKeys 点名（%v）—— 读回来还是分不清没交与空表", tc.name, cl.Key, got)
				}
			}
		} else if len(got) != 0 {
			t.Errorf("%s：空表不该算缺席，AbsentKeys 却点了 %v", tc.name, got)
		}
	}

	// 两句人话必须不同：共用一句，这一次改动就等于只在出处那一栏改了个数字，
	// 而「谁读这句话」读到的仍然是同一个长相。
	if absentListClaim == emptyClaim || absentListClaim == "0 段（裁剪表是空的）" {
		t.Errorf("nil 名单那句话与空表那句话长得一样：%q", absentListClaim)
	}
	// 空样本（`InputMeta{}`）上这三格现在报 absent —— 格位表 §3 那一列就是从这里抄的。
	for _, c := range (InputMeta{}).Cells() {
		if !strings.HasPrefix(c.Field, "Cuts#") {
			continue
		}
		if c.Basis != BasisAbsent {
			t.Errorf("空样本上 %s 报 %s，该报 %s", c.Key, c.Basis, BasisAbsent)
		}
	}
}

// ⑧ 那一格的人话不许是空的：一格人话是空的，读的人会以为那一格没写。
// 空表、否、0 这三种「看起来像没有」的形状都必须翻译出一句非空的话；
// 裁剪表有内容时那句话还必须带分母（几段）—— 没有分母，
// 就分不清是「折了一段」还是「这一档整个没了」。
func TestClaimOfNeverHandsBackAnEmptySentence(t *testing.T) {
	for _, c := range []struct {
		name string
		v    any
	}{
		{"空裁剪表", []Cut{}},
		{"空段名表", []PartKind{}},
		{"否", false},
		{"零（int64）", int64(0)},
	} {
		if got := claimOf(c.v); strings.TrimSpace(got) == "" {
			t.Errorf("claimOf（%s）交出了空话 %q —— 读的人会以为那一格没写", c.name, got)
		}
	}
	cuts := []Cut{
		{Kind: KindObserved, Ref: "step3:read_file:docs/a.md", Squeeze: SqueezeFolded},
		{Kind: KindHistory, Ref: "step4:recall", Squeeze: SqueezeTailDropped},
	}
	got := claimOf(cuts)
	if want := strconv.Itoa(len(cuts)) + " 段"; !strings.Contains(got, want) {
		t.Errorf("%d 段裁剪的那句话没带分母 %q：%q", len(cuts), want, got)
	}
	// claimOf 对任何输入都不许交出空字符串：一个空的 Claim 在账本上与「这一格没写」
	// 长得一样，而读的人分不清「值是空串」与「没有值」—— 那就是「缺席长成 0」那一族，
	// 只是换到了字符串上。真会走到这一支的格子有三个：notice_why、fail_kind、fail_detail。
	// （这一条曾经是刻意放宽的「现状钉住式断言」，因为 record.go 当时把空串原样交回来；
	//   补上人话之后按路标翻转成纪律版。）
	if s := claimOf(""); s == "" {
		t.Errorf("claimOf 对空字符串交回了空串 —— 它在账本上会被读成「这一格没写」")
	} else if s != emptyClaim {
		t.Errorf("空串该渲染成那一句共用的话 %q，得到 %q", emptyClaim, s)
	}
	// 空的切片与空的字符串表同样不许交出空串。
	for _, v := range []any{[]PartKind{}, []string{}, []PartCount{}} {
		if s := claimOf(v); strings.TrimSpace(s) == "" {
			t.Errorf("claimOf(%T) 交回了空串", v)
		}
	}
	// 两张不同的空表不许共用一句话。它们曾经都渲染成「0 段」，
	// 于是单看 Claim 分不清「发送顺序表是空的」与「裁剪表是空的」——
	// 键名不同不算违「一键一量」，但那句话是给人读的，人只读那句话。
	// 这一条不钉住的话，把两句合回一句不会有任何东西变红。
	emptyKinds, emptyCuts := claimOf([]PartKind{}), claimOf([]Cut{})
	if emptyKinds == emptyCuts {
		t.Errorf("空的顺序表与空的裁剪表渲染成了同一句 %q", emptyKinds)
	}
	for _, s := range []struct{ got, want string }{{emptyKinds, "顺序"}, {emptyCuts, "裁剪"}} {
		if !strings.Contains(s.got, s.want) {
			t.Errorf("那句话没点出自己说的是哪张表（该含 %q）：%q", s.want, s.got)
		}
	}
	if s := claimOf([]PartCount{}); !strings.Contains(s, "类") {
		t.Errorf("空的段表该说「类」而不是「段」：%q", s)
	}
	l := NewLedger()
	in := validInput(t)
	if _, err := l.RecordInput("sess-1", "assembler", in); err != nil {
		t.Fatalf("夹具该被记账：%v", err)
	}
	found := false
	for _, c := range CellsOf(l.Docs(), "sess-1", in.Call, KindModelInput) {
		// 账上**每一格**的 Claim 都不许是空的 —— 逐格核，不只核 notice_why 那一格。
		if strings.TrimSpace(c.Claim) == "" {
			t.Errorf("%s 那一格在账上的 Claim 是空的 —— 它会被读成「这一格没写」", c.Key)
		}
		if c.Key != "notice_why" {
			continue
		}
		found = true
		if c.Claim != emptyClaim {
			t.Errorf("notice_why 那一格该说「这一格是空的」，得到 %q", c.Claim)
		}
	}
	if !found {
		t.Error("账上没读回 notice_why —— 这条判据成了空针")
	}
}

// ⑧b 同一发不许记两遍。哈希链只证明「写下来的没被动过」，
// 证明不了「不该写的没写」：记两遍之后链照样自洽，
// 而任何按键名扫账本的分析层会把字节与 token 加两遍。
func TestSameCallCannotBeRecordedTwice(t *testing.T) {
	l := NewLedger()
	in, out := validInput(t), validOutput(t)
	n, err := l.RecordCall("sess-1", "runner", in, out)
	if err != nil {
		t.Fatalf("第一发该被记：%v", err)
	}
	if n != len(in.Cells())+len(out.Cells()) {
		t.Fatalf("第一发该记 %d 条，得到 %d", len(in.Cells())+len(out.Cells()), n)
	}
	before := len(l.Docs())
	// 三个入口都要拦，不只是 RecordCall。
	if _, err := l.RecordCall("sess-1", "runner", in, out); err == nil {
		t.Errorf("同一发记第二遍竟被收了")
	} else if !strings.Contains(err.Error(), "已经记过") {
		t.Errorf("那句话没说出真凶：%v", err)
	}
	if _, err := l.RecordInput("sess-1", "assembler", in); err == nil {
		t.Errorf("输入侧重复记竟被收了")
	}
	if _, err := l.RecordOutput("sess-1", "runner", out); err == nil {
		t.Errorf("输出侧重复记竟被收了")
	}
	if got := len(l.Docs()); got != before {
		t.Errorf("重复的三次尝试往链上添了 %d 条（该一条都不添）", got-before)
	}
	if l.Rejected() != 3 {
		t.Errorf("三次被拒都该有数，得到 %d", l.Rejected())
	}
	// 换一个 session 就是另一趟，同一发号该能再记 —— 拦的是「同一趟里重复」，不是「发号重复」。
	if n2, err := l.RecordInput("sess-2", "assembler", in); err != nil {
		t.Errorf("换一个 session 记同一发号该被收：%v", err)
	} else if want := len(in.Cells()); n2 != want {
		t.Errorf("换 session 那一发该整发记 %d 条，得到 %d 条", want, n2)
	}
	// 同一 session 的下一发也该能记。
	next := validInput(t)
	next.Call = 2
	if n3, err := l.RecordInput("sess-1", "assembler", next); err != nil {
		t.Errorf("同一 session 的第 2 发该被收：%v", err)
	} else if want := len(next.Cells()); n3 != want {
		t.Errorf("第 2 发该整发记 %d 条，得到 %d 条", want, n3)
	}
	if err := l.Verify(); err != nil {
		t.Errorf("记了这么多之后链该还能重算：%v", err)
	}
}

// ⑧c 「不写半发」必须是结构性的，不是涌现出来的。
// 判据分三层：
//  1. plan 自己不写（把一侧的格子在**翻译那一步**弄坏，账上一条都不许有）；
//  2. RecordCall 的**次序**：两侧都翻译完才开始落账。这一层原先没有判据 ——
//     只调 plan 的测试对「把 RecordCall 改回边翻译边写」是空针（plan 还在、还不写，
//     测试照样绿）。要让它红，必须找到一种「Check 放行、而 plan 在**后一侧**才拒」
//     的形状走公共入口：「同一发只许记一次」正好是唯一一把这样的钥匙 ——
//     先把输出侧单独记进去，再走 RecordCall，输入侧 plan 成功、输出侧 plan 被拒。
//     两阶段 ⇒ 一条都不写；边翻译边写 ⇒ 输入侧已经落在链上，那就是半发；
//  3. 好的那头：正常的一发必须能写进去（只测坏的那一头，等于永远报警也算通过）。
func TestNoHalfCallEvenWhenTranslationFailsLate(t *testing.T) {
	l := NewLedger()
	in, out := validInput(t), validOutput(t)
	// 手搭一格翻译不了的：值是一个 channel，json.Marshal 会拒。
	// 这一格绕过 Check（Check 核的是 InputMeta 的字段，不是手搭的 Cell），
	// 所以它只能在 plan 那一步被发现 —— 而那一步必须在任何一条落账之前。
	bad := append(append([]Cell{}, in.Cells()...), cell("nope", "Call", make(chan int), BasisMeasured))
	recs, err := l.plan("sess-1", "runner", in.Call, KindModelInput, bad)
	if err == nil {
		t.Fatalf("一格序列化不了的账竟被翻译过去了（%d 条）", len(recs))
	}
	if recs != nil {
		t.Errorf("翻译失败时不该交出半份记录，得到 %d 条", len(recs))
	}
	if got := len(l.Docs()); got != 0 {
		t.Errorf("翻译失败却往链上写了 %d 条 —— 那就是半发", got)
	}
	// 而 RecordCall 那一路：输出侧翻译失败时，输入侧也必须一条都不写。
	l2 := NewLedger()
	if _, err := l2.plan("s", "a", out.Call, KindModelOutput,
		append(append([]Cell{}, out.Cells()...), cell("nope", "Call", make(chan int), BasisMeasured))); err == nil {
		t.Fatalf("输出侧那一发本该翻译失败")
	}
	if got := len(l2.Docs()); got != 0 {
		t.Errorf("输出侧翻译失败，链上却有 %d 条", got)
	}
	// 对照：正常的一发必须能写进去（只测坏的那一头，等于永远报警也算通过）。
	n, err := l2.RecordCall("s", "a", in, out)
	if want := len(in.Cells()) + len(out.Cells()); err != nil || n != want {
		t.Errorf("正常的一发该记 %d 条，得到 %d 条，err=%v", want, n, err)
	}
	// 第 2 层：公共入口上的次序判据。输出侧已经单独记过 ⇒ RecordCall 里
	// 输入侧 plan 成功、输出侧 plan 被拒 —— 若实现是「翻译完一侧就落账一侧」，
	// 输入侧此刻已经在链上，下面「账上一条没多」那行就会红。
	l3 := NewLedger()
	outOnly := validOutput(t)
	nOut, err := l3.RecordOutput("sess-1", "endpoint-watcher", outOnly)
	if err != nil {
		t.Fatalf("单独记输出侧该被收：%v", err)
	}
	if got := len(l3.Docs()); got != nOut || nOut == 0 {
		t.Fatalf("基线不对：报记了 %d 条，账上 %d 条", nOut, got)
	}
	n3, err := l3.RecordCall("sess-1", "runner", validInput(t), outOnly)
	if err == nil {
		t.Fatalf("输出侧那一发已经记过，RecordCall 竟被收了（%d 条）", n3)
	}
	if !strings.Contains(err.Error(), "已经记过") || !strings.Contains(err.Error(), KindModelOutput) {
		t.Errorf("那句话没点名是哪一侧已经记过（要带 %q）：%v", KindModelOutput, err)
	}
	if n3 != 0 {
		t.Errorf("报出来写了 %d 条 —— 输出侧翻译被拒时输入侧一条都不该写", n3)
	}
	if got := len(l3.Docs()); got != nOut {
		t.Errorf("账上从 %d 条变成了 %d 条 —— 输入侧在输出侧翻译失败**之前**就落了账，那正是半发；"+
			"两侧都 plan 完才 commit 就是为了堵这个形状", nOut, got)
	}
	if l3.Rejected() != 1 {
		t.Errorf("这一次拒收该正好计一次，得到 %d", l3.Rejected())
	}
	// 好的一头：同一 session、同一发号，**输入侧**并没被记过 ——
	// 单记输入该被收（拦的是「同侧同发重复」，不是「这个发号出现过」）。
	// 这一条同时核 alreadyRecorded 按键名前缀分侧，不误伤另一侧。
	nIn, err := l3.RecordInput("sess-1", "assembler", validInput(t))
	if err != nil {
		t.Errorf("输出侧已在账上时，输入侧单记该被收：%v", err)
	} else if want := len(in.Cells()); nIn != want {
		t.Errorf("输入侧该整发记 %d 条，得到 %d 条", want, nIn)
	}
	if err := l3.Verify(); err != nil {
		t.Errorf("记了这些之后链该还能重算：%v", err)
	}
}

// ⑧d commit 那句「走到这一步就不该再失败 —— plan 已经核过出处与序列化，
// 而 Append 的判据与 plan 同源」是一条规矩，此前没有任何判据读过它。
// 正向：plan 收下的**每一种出处档**（absent/measured/relayed/estimated/model_says）
// 与非三态值，commit 都必须落得进去 —— 若将来 Append 加了一条 plan 没镜像的判据，
// 这条会红，那正是那句注释被打破的声音。
// 反向：万一中途失败，「已写进去的那些留在链上」也是注释里写明的后果，
// 用手搭记录钉住它（commit 只该吃 plan 出来的记录，这里刻意违例一次来钉行为）：
// 前半条留链、返回已写条数、计数由 Append 那一份出，commit 不重复计。
func TestCommitAcceptsEveryShapePlanTranslates(t *testing.T) {
	cells := []Cell{
		measureCell("m_absent", "Chunks", AbsentBecause("这一发结构上没有这一格")),
		measureCell("m_measured", "Chunks", MeasuredOf(37)),
		measureCell("m_relayed", "UsageIn", RelayedOf(1587, "endpoint:agnes")),
		measureCell("m_estimated", "Tokens", EstimatedOf(1600, "按段字节数估")),
		measureCell("m_model_says", "Says.Bytes", Measure{N: 3, Basis: BasisModelSays}),
		cell("s", "Notice.Text", "一句话", BasisMeasured),
		cell("i", "Call", 7, BasisMeasured),
		cell("i64", "ContentBytes", int64(6560), BasisMeasured),
		cell("b", "Notice.Sent", true, BasisMeasured),
		relayedCell("r", "Finish.Reason", "stop", SourceEndpoint),
	}
	l := NewLedger()
	recs, err := l.plan("sess-1", "assembler", 9, KindModelInput, cells)
	if err != nil {
		t.Fatalf("这 %d 格都是 plan 该收下的形状：%v", len(cells), err)
	}
	if len(recs) != len(cells) {
		t.Fatalf("plan 交回 %d 条，手搭的键名表有 %d 格", len(recs), len(cells))
	}
	n, err := l.commit(recs)
	if err != nil {
		t.Errorf("plan 收下的记录 commit 竟拒了 —— 「Append 的判据与 plan 同源」被打破了：%v", err)
	}
	if n != len(cells) {
		t.Errorf("%d 格该全部落账，得到 %d 条", len(cells), n)
	}
	if l.Rejected() != 0 {
		t.Errorf("一路绿灯不该有拒收，得到 %d", l.Rejected())
	}
	if err := l.Verify(); err != nil {
		t.Errorf("链该能重算：%v", err)
	}
	// 反向：中途失败时条数与计数都要说真话，已写的那条留在链上。
	l2 := NewLedger()
	good := Record{Session: "s", Actor: "a", Step: 1, Kind: KindModelInput + ":x", Claim: "1", Basis: BasisMeasured, Payload: []byte("1")}
	broken := Record{Session: "s", Actor: "a", Step: 1, Kind: KindModelInput + ":y", Basis: BasisUnset}
	n2, err := l2.commit([]Record{good, broken})
	if err == nil {
		t.Fatal("不带出处的记录 commit 竟收了")
	}
	if n2 != 1 {
		t.Errorf("第一条已经落账，该报 1 条，得到 %d", n2)
	}
	if got := len(l2.Docs()); got != 1 {
		t.Errorf("链上该留下已写的那 1 条（commit 注释写明的后果），得到 %d 条", got)
	}
	if l2.Rejected() != 1 {
		t.Errorf("拒收该由 Append 计正好一次（commit 不重复计），得到 %d", l2.Rejected())
	}
}

// ⑪b plan 的「一格都没有」那一支在公共路径上到不了 —— CheckCells 站在前面，
// 空键名表早被「一格都没交出来」拒掉。与 ⑪ 同理直接喂 plan：不测的话，
// 这一支可以整行删掉而不红（Check 替它挡着），而它是「plan 不吃空表」的唯一执行点。
func TestPlanRefusesAnEmptyCellList(t *testing.T) {
	l := NewLedger()
	recs, err := l.plan("sess-1", "assembler", 1, KindModelInput, nil)
	if err == nil {
		t.Fatalf("空键名表竟被翻译过去了（%d 条）", len(recs))
	}
	if !strings.Contains(err.Error(), "一格都没有") {
		t.Errorf("那句话没说清空表（要带「一格都没有」）：%v", err)
	}
	if recs != nil {
		t.Errorf("拒了还交出 %d 条", len(recs))
	}
	if got := len(l.Docs()); got != 0 {
		t.Errorf("账上多了 %d 条", got)
	}
	if l.Rejected() != 0 {
		t.Errorf("plan 不是门口，不该自己计拒收数，得到 %d", l.Rejected())
	}
}

// ⑧e plan 必须在**写任何一条之前**就拒掉 Append 会拒的记录。
// 这一条守的是「plan 放过的，commit 一定收」那个保证本身 ——
// 而它成立的理由不是运气，是两层共用 checkRecord 那**一份**判据。
// 少了这条判据，将来谁把 checkRecord 的某一处复制回 Append 里单独改，
// 两阶段就退化回「把半发推迟到 commit 中途」，而没有东西会红。
func TestPlanRefusesWhatAppendWouldRefuse(t *testing.T) {
	in := validInput(t)
	cells := in.Cells()
	l := NewLedger()

	// actor 为空：Append 会拒。plan 必须也拒，且一条都不写。
	if _, err := l.plan("sess", "", 1, KindModelInput, cells); err == nil {
		t.Errorf("actor 为空，plan 竟放过了 —— commit 会在第一条上失败，那就是半发")
	} else if !strings.Contains(err.Error(), "账本不会收") {
		t.Errorf("那句话没说出是账本那一层不会收：%v", err)
	}
	if got := len(l.Docs()); got != 0 {
		t.Errorf("plan 被拒之后链上竟有 %d 条", got)
	}

	// 转述/估算那一格抹掉出处：Append 会拒（needsSource 那一份判据），plan 也必须拒。
	// 先确认夹具里真有这样一格，否则这一发反证是空的。
	var hit int
	noSource := make([]Cell, 0, len(cells))
	for _, c := range cells {
		if needsSource(c.Basis) {
			hit++
			c.Source = ""
		}
		noSource = append(noSource, c)
	}
	if hit == 0 {
		t.Fatalf("输入侧一格「必须带来源」的都没有，这一发反证是空的")
	}
	if _, err := l.plan("sess", "runner", 1, KindModelInput, noSource); err == nil {
		t.Errorf("抹掉出处之后 plan 竟放过了 —— 它与 Append 用的不是同一份判据")
	}
	if got := len(l.Docs()); got != 0 {
		t.Errorf("plan 被拒之后链上竟有 %d 条", got)
	}

	// 对照：同一份格子带上 actor 与出处，plan 过 ⇒ commit 必须全收，一条不落。
	recs, err := l.plan("sess", "runner", 1, KindModelInput, cells)
	if err != nil {
		t.Fatalf("合法的一份该被翻译过去：%v", err)
	}
	if len(recs) != len(cells) {
		t.Fatalf("翻译出来 %d 条，该是 %d 条（一格一条）", len(recs), len(cells))
	}
	n, err := l.commit(recs)
	if err != nil {
		t.Errorf("plan 放过的 commit 竟拒了：%v —— 两层用的不是同一份判据", err)
	}
	if n != len(recs) {
		t.Errorf("commit 只落了 %d 条，plan 交出来 %d 条", n, len(recs))
	}
	if got := len(l.Docs()); got != len(cells) {
		t.Errorf("链上该有 %d 条，得到 %d", len(cells), got)
	}
	if err := l.Verify(); err != nil {
		t.Errorf("链该还能重算：%v", err)
	}
}

// ⑨ 分桶必须数对：记成缺席的格子全落进 absent 桶，不多不少。
// 期望值从夹具自己的键名表里数出来，不抄常量 —— 抄一个常量进测试，
// 夹具一改这条就会以「算错了」的口吻误报，或者更糟：跟着常量一起错。
func TestTallyCountsRecordedAbsencesIntoTheAbsentBucket(t *testing.T) {
	out := validNonStreaming(t)
	l := NewLedger()
	n, err := l.RecordOutput("sess-1", "endpoint-watcher", out)
	if err != nil {
		t.Fatalf("非流式那一发该被记账：%v", err)
	}
	want := 0
	for _, c := range out.Cells() {
		if m, ok := c.Value.(Measure); ok && m.Absent {
			want++
		}
	}
	if want == 0 {
		t.Fatal("这份夹具里没有缺席格 —— 这条断言成了空针")
	}
	tt := l.Tally()
	if tt.ByBasis["absent"] != want {
		t.Errorf("键名表算出 %d 格缺席，absent 桶里是 %d", want, tt.ByBasis["absent"])
	}
	if tt.Total != n {
		t.Errorf("Total 该等于这一发记进去的 %d 条，得到 %d", n, tt.Total)
	}
	sum := 0
	for k, v := range tt.ByBasis {
		if k == "unset" {
			t.Error("账上出现了 unset 桶 —— 忘了填该在门口被拒，不该被数进链上")
		}
		sum += v
	}
	if sum != tt.Total {
		t.Errorf("各桶相加 %d ≠ Total %d —— 有格子被数了两遍或没数", sum, tt.Total)
	}
}

// ⑩ 读回来必须按发、按侧过滤：同一本账上记两发、两侧都记，
// CellsOf 只许拿出那一发那一侧，一条不多一条不少。
// 两发的夹具形状完全相同、只有 call 那格的值不同 —— 所以还要用 call 格的人话
// 做非空针对照：过滤若失守（比如把全部条目都交出来），条数对不上之外，
// call 格也会混进另一发的值。
func TestCellsOfReadsBackOneCallOneSideOnly(t *testing.T) {
	l := NewLedger()
	in1, out1 := validInput(t), validOutput(t)
	in2, out2 := validInput(t), validOutput(t)
	in2.Call, out2.Call = 2, 2
	if _, err := l.RecordCall("sess-1", "assembler", in1, out1); err != nil {
		t.Fatalf("第 1 发该被记账：%v", err)
	}
	if _, err := l.RecordCall("sess-1", "assembler", in2, out2); err != nil {
		t.Fatalf("第 2 发该被记账：%v", err)
	}
	docs := l.Docs()
	if want := len(in1.Cells()) + len(out1.Cells()) + len(in2.Cells()) + len(out2.Cells()); len(docs) != want {
		t.Fatalf("两发两侧共该有 %d 条，账上是 %d 条", want, len(docs))
	}
	got := CellsOf(docs, "sess-1", 1, KindModelInput)
	wantKeys := sortedCellKeys(in1.Cells())
	if len(got) != len(wantKeys) {
		t.Fatalf("第 1 发输入侧该有 %d 格，读回来 %d 格 —— 多一条是混进了第 2 发或输出侧，少一条是丢了一格", len(wantKeys), len(got))
	}
	for i := range got {
		if got[i].Key != wantKeys[i] {
			t.Errorf("第 %d 格：读回来 %q，该是 %q", i+1, got[i].Key, wantKeys[i])
		}
	}
	for _, c := range got {
		if c.Key == "call" && c.Claim != "1" {
			t.Errorf("第 1 发输入侧的 call 格说的是 %q —— 混进了别的发", c.Claim)
		}
	}
	got2 := CellsOf(docs, "sess-1", 2, KindModelOutput)
	if want := len(out2.Cells()); len(got2) != want {
		t.Fatalf("第 2 发输出侧该有 %d 格，读回来 %d 格", want, len(got2))
	}
	for _, c := range got2 {
		if c.Key == "call" && c.Claim != "2" {
			t.Errorf("第 2 发输出侧的 call 格说的是 %q —— 混进了别的发", c.Claim)
		}
	}
}

// TestCellsOfKeepsTwoSessionsApart 钉住读侧的第三根轴。
// 反面是实测出来的，不是设想：把内核挂成 MCP 之后，两个 session 各记一次第 1 发，
// 只认「发号 + 侧」的读侧当场回了 40 格 —— 两发被并成一片，
// 而写侧 alreadyRecorded(session, step, kind) 明明按三个轴定身份。
// 一个 session 的测试永远撞不到这一条，所以这里必须是两个。
func TestCellsOfKeepsTwoSessionsApart(t *testing.T) {
	l := NewLedger()
	in, out := validInput(t), validOutput(t)
	for _, sess := range []string{"sess-a", "sess-b"} {
		if _, err := l.RecordCall(sess, "assembler", in, out); err != nil {
			t.Fatalf("%s 那一发记不进去：%v", sess, err)
		}
	}
	docs := l.Docs()
	if want := 2*len(in.Cells()) + 2*len(out.Cells()); len(docs) != want {
		t.Fatalf("两本各一发该有 %d 条，链上 %d 条", want, len(docs))
	}
	for _, sess := range []string{"sess-a", "sess-b"} {
		if got := CellsOf(docs, sess, in.Call, KindModelInput); len(got) != len(in.Cells()) {
			t.Errorf("session=%s 的第 %d 发输入侧读回 %d 格，键名表是 %d 格 —— 多出来的是别的 session 同号同侧的格",
				sess, in.Call, len(got), len(in.Cells()))
		}
		if got, want := Keys(docs, sess, out.Call, KindModelOutput), sortedCellKeys(out.Cells()); len(got) != len(want) {
			t.Errorf("session=%s 的输出侧键名有 %d 个，键名表 %d 个", sess, len(got), len(want))
		}
	}
	// 缺席那一栏也要分得开：并了 session 会把两本的缺席数成双份。
	joined := AbsentKeys(docs, "sess-a", out.Call, KindModelOutput)
	both := append(AbsentKeys(docs, "sess-a", out.Call, KindModelOutput),
		AbsentKeys(docs, "sess-b", out.Call, KindModelOutput)...)
	if len(both) != 2*len(joined) {
		t.Errorf("两本的缺席相加是 %d 条，单本 %d 条 —— 有一本被另一本盖掉了", len(both), len(joined))
	}
	// 报一个不存在的 session 必须读回空，而不是读回「第一个碰上的同号发」。
	if got := CellsOf(docs, "sess-c", in.Call, KindModelInput); len(got) != 0 {
		t.Errorf("没有的 session 读回 %d 格，该是 0 格", len(got))
	}
}

// ⑪ 序列化不了的格子进不了账。
// 这一分支在公共路径上到不了（Check 站在前面，键名表里的值类型也全都序列化得了），
// 所以直接喂手搭的格子给非导出的 plan：一格连 payload 都留不下来的说法
// 若混进了链，「payload 给第三方重算」那半句承诺就成了假话。
//
// 注意 plan **不自己计拒收数** —— 计数住在 RecordInput/RecordOutput/RecordCall 那一层
// （它们才是门口），由上面那几条「整发拒收 + Rejected() +1」的判据守着。
// 这里反过来断言 plan 不重复计数：一处拒收被数两遍，与一次都没数，是同一族错。
func TestUnserializableCellIsRefusedAndCounted(t *testing.T) {
	l := NewLedger()
	recs, err := l.plan("sess-1", "assembler", 1, KindModelInput, []Cell{
		{Key: "weird", Field: "Weird", Value: make(chan int), Basis: BasisMeasured},
	})
	if err == nil {
		t.Fatalf("一个 chan 值的格子竟被翻译过去了（%d 条）—— 链上的 payload 无从重算", len(recs))
	}
	if !strings.Contains(err.Error(), "序列化不了") {
		t.Errorf("那句话没说清拒收的原因：%v", err)
	}
	if recs != nil {
		t.Errorf("翻译失败时不该交出半份记录，得到 %d 条", len(recs))
	}
	if got := len(l.Docs()); got != 0 {
		t.Errorf("账上竟多了 %d 条", got)
	}
	if l.Rejected() != 0 {
		t.Errorf("plan 不是门口，不该自己计拒收数（那会与上层重复计），得到 %d", l.Rejected())
	}
	// 而门口那一层确实会计数 —— 用一条 Check 就能拦下的坏账走公共路径核一遍。
	bad := validInput(t)
	bad.ContentBytes++
	if _, err := l.RecordInput("sess-1", "assembler", bad); err == nil {
		t.Fatalf("坏账竟被收了")
	}
	if l.Rejected() != 1 {
		t.Errorf("门口那一层该把这次拒收计上，得到 %d", l.Rejected())
	}
}

// recordedByKey 把某一发某一侧读回来的格子按键名索引，供逐格断言用。
func recordedByKey(docs []Entry, session string, step int, kind string) map[string]RecordedCell {
	m := map[string]RecordedCell{}
	for _, c := range CellsOf(docs, session, step, kind) {
		m[c.Key] = c
	}
	return m
}

// wantCell 取一格；取不到就地报红 —— 键名拼错时，针对它的断言若只是「跳过」，
// 那条判据就成了空针。
func wantCell(t *testing.T, m map[string]RecordedCell, key string) RecordedCell {
	t.Helper()
	c, ok := m[key]
	if !ok {
		t.Fatalf("账上没读回 %s 那一格 —— 针对它的判据成了空针", key)
	}
	return c
}

// blankSentence 渲染出来只有空白 —— 用来踩 claimOf default 那一支里
// 「渲染成空白也必须交出共用那句话」的分支（夹具里没有任何值走到它）。
type blankSentence struct{}

func (blankSentence) String() string { return "   " }

// ⑫ 人话的翻译必须逐类型核到位：claimOf 的每一个 case 都要有一条**真在执行的**
// 判据读过它的输出，且期望值一律从夹具现算 —— 否则某个 case 悄悄改错
// （比如按类聚合那句把字节加错、段名表漏了一段），没有任何东西会红。
// ⑧ 只核了「不空」，不核对不对；这一条核对不对。
// 它同时守住两处此前没有判据读过的注释/字段：
//   - claimOf 注释点名的三个空格子（notice_why、fail_kind、fail_detail）—— ⑧ 只核了 notice_why；
//   - RecordedCell.Source 的往返（出处跟着格子一起落账、读得回来）。
func TestClaimsReadBackAsSentencesComputedFromTheFixture(t *testing.T) {
	in, out := validInput(t), validOutput(t)
	l := NewLedger()
	if _, err := l.RecordCall("sess-1", "assembler", in, out); err != nil {
		t.Fatalf("对齐的一发该被记账：%v", err)
	}
	docs := l.Docs()
	inCells := recordedByKey(docs, "sess-1", in.Call, KindModelInput)
	outCells := recordedByKey(docs, "sess-1", out.Call, KindModelOutput)

	// string（非空）：对端与端点说的原话原样进人话。
	if got, want := wantCell(t, outCells, "finish").Claim, out.Finish.Reason; got != want {
		t.Errorf("finish 那一格的人话该是 %q，得到 %q", want, got)
	}
	if got, want := wantCell(t, outCells, "endpoint_model").Claim, out.Model; got != want {
		t.Errorf("endpoint_model 那一格的人话该是 %q，得到 %q", want, got)
	}
	if got, want := wantCell(t, inCells, "notice_text").Claim, in.Notice.Text; got != want {
		t.Errorf("notice_text 那一格的人话该是 %q，得到 %q", want, got)
	}
	// string（空）：claimOf 注释点名的三个格子都必须说那句共用的话。
	for _, c := range []RecordedCell{wantCell(t, inCells, "notice_why"), wantCell(t, outCells, "fail_kind"), wantCell(t, outCells, "fail_detail")} {
		if c.Claim != emptyClaim {
			t.Errorf("%s 那一格的人话该是 %q，得到 %q", c.Key, emptyClaim, c.Claim)
		}
	}
	// bool 两头：期望从夹具现算，「是/否」翻反了必须红。
	for _, c := range []struct {
		got  RecordedCell
		want bool
	}{
		{wantCell(t, inCells, "notice_sent"), in.Notice.Sent},
		{wantCell(t, inCells, "notice_trimmed"), in.Notice.Trimmed},
		{wantCell(t, outCells, "finish_said"), out.Finish.Said},
		{wantCell(t, outCells, "endpoint_model_said"), out.ModelSaid},
		{wantCell(t, outCells, "model_says_clipped"), out.Says.Clipped()},
		{wantCell(t, outCells, "cost_countable"), out.CostCountable()},
	} {
		want := "否"
		if c.want {
			want = "是"
		}
		if c.got.Claim != want {
			t.Errorf("%s 那一格的人话该是 %q（夹具算出来是 %v），得到 %q", c.got.Key, want, c.want, c.got.Claim)
		}
	}
	// int 与 int64：期望从夹具现算。
	if got, want := wantCell(t, inCells, "call").Claim, strconv.Itoa(in.Call); got != want {
		t.Errorf("call 那一格的人话该是 %q，得到 %q", want, got)
	}
	if got, want := wantCell(t, outCells, "tool_calls").Claim, strconv.Itoa(out.ToolCalls); got != want {
		t.Errorf("tool_calls 那一格的人话该是 %q，得到 %q", want, got)
	}
	if got, want := wantCell(t, inCells, "content_bytes").Claim, strconv.FormatInt(in.ContentBytes, 10); got != want {
		t.Errorf("content_bytes 那一格的人话该是 %q，得到 %q", want, got)
	}
	if got, want := wantCell(t, outCells, "model_says_bytes").Claim, strconv.FormatInt(out.Says.Bytes, 10); got != want {
		t.Errorf("model_says_bytes 那一格的人话该是 %q，得到 %q", want, got)
	}
	// []PartKind（非空）：分母是发送顺序表的长度，且每一段名都在那句话里。
	parts := wantCell(t, inCells, "parts").Claim
	if head := strconv.Itoa(len(in.Order)) + " 段："; !strings.HasPrefix(parts, head) {
		t.Errorf("parts 那一格的人话该以 %q 起头（分母是发送顺序表的 %d 项），得到 %q", head, len(in.Order), parts)
	}
	for _, k := range in.Order {
		if !strings.Contains(parts, string(k)) {
			t.Errorf("parts 那一格的人话漏了 %s：%q", k, parts)
		}
	}
	// []PartCount（非空）：类数从段表现数；字节合计用 in.ContentBytes ——
	// Check 已钉住「各类段相加 == 内容总字节」，所以这是一个**独立于**
	// claimOf 那圈加法的期望值：它把字节加错就会红。
	totals := wantCell(t, inCells, "part_totals").Claim
	head := strconv.Itoa(len(in.Parts)) + " 类共 " + strconv.FormatInt(in.ContentBytes, 10) + " 字节："
	if !strings.HasPrefix(totals, head) {
		t.Errorf("part_totals 那一格的人话该以 %q 起头，得到 %q", head, totals)
	}
	for _, p := range in.Parts {
		entry := string(p.Kind) + "=" + strconv.Itoa(p.Parts)
		if !strings.Contains(totals, entry) {
			t.Errorf("part_totals 那一格的人话漏了 %s：%q", entry, totals)
		}
	}
	// []string（非空）：份额表逐项都在，「键=值」从 map 现算。
	shares := wantCell(t, inCells, "shares").Claim
	for k, v := range in.Budget.Shares {
		entry := string(k) + "=" + strconv.Itoa(v)
		if !strings.Contains(shares, entry) {
			t.Errorf("shares 那一格的人话漏了 %s：%q", entry, shares)
		}
	}
	// []Cut（非空）：⑧ 核过手搭表的分母，这里核真夹具那一格逐段带 ref。
	folded := wantCell(t, inCells, "folded").Claim
	if head := strconv.Itoa(len(in.Folded())) + " 段："; !strings.HasPrefix(folded, head) {
		t.Errorf("folded 那一格的人话该以 %q 起头（分母是折叠名单的 %d 段），得到 %q", head, len(in.Folded()), folded)
	}
	for _, c := range in.Folded() {
		if !strings.Contains(folded, c.Ref) {
			t.Errorf("folded 那一格的人话漏了 %s：%q", c.Ref, folded)
		}
	}
	// 三态量的人话就是 Measure.String()：估算、转述、实测各挑一格核。
	if got, want := wantCell(t, inCells, "tokens").Claim, in.Tokens.String(); got != want {
		t.Errorf("tokens 那一格的人话该是 %q，得到 %q", want, got)
	}
	if got, want := wantCell(t, outCells, "usage_in").Claim, out.UsageIn.String(); got != want {
		t.Errorf("usage_in 那一格的人话该是 %q，得到 %q", want, got)
	}
	if got, want := wantCell(t, outCells, "chunks").Claim, out.Chunks.String(); got != want {
		t.Errorf("chunks 那一格的人话该是 %q，得到 %q", want, got)
	}
	// 派生格：sent 那句人话是 Sent().String()。
	if got, want := wantCell(t, outCells, "sent").Claim, out.Sent().String(); got != want {
		t.Errorf("sent 那一格的人话该是 %q，得到 %q", want, got)
	}
	// Source 必须跟着格子一起往返（此前没有任何判据读过 RecordedCell.Source）。
	if got := wantCell(t, outCells, "finish").Source; got != SourceEndpoint {
		t.Errorf("finish 那一格读回来的出处该是 %q，得到 %q", SourceEndpoint, got)
	}
	if got, want := wantCell(t, outCells, "usage_in").Source, out.UsageIn.Source; got != want {
		t.Errorf("usage_in 那一格读回来的出处该是 %q，得到 %q", want, got)
	}
	if got, want := wantCell(t, inCells, "tokens").Source, in.Tokens.Source; got != want {
		t.Errorf("tokens 那一格读回来的出处该是 %q，得到 %q", want, got)
	}
	if got := wantCell(t, inCells, "call").Source; got != "" {
		t.Errorf("实测格读回来的出处该是空的（它不转述谁），得到 %q", got)
	}
	// default 那一支：常规渲染与「渲染成空白」两头。夹具里没有任何值走到它，直接喂。
	if got := claimOf(2.5); got != "2.5" {
		t.Errorf("claimOf 对 default 那一支该原样渲染出 %q，得到 %q", "2.5", got)
	}
	if got := claimOf(blankSentence{}); got != emptyClaim {
		t.Errorf("渲染出来只有空白的值该落成共用那句话 %q，得到 %q", emptyClaim, got)
	}
}
