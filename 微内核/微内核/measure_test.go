package kernel

import (
	"strings"
	"testing"
)

// 每一条判据都配两头：坏的必须被拒、好的必须被收。
// 只有「好的能过」那一头的测试，等于「永远报警也算通过」。

// ① 缺席与 0 必须是两个形状。这一格对应真实发生过的事故：
// 非流式那一发的分帧数被写成 0，读的人看到的是「一帧都没收到」，
// 而那恰好是协议错的判据。
func TestAbsentIsNotZero(t *testing.T) {
	// 坏的一头：缺席却带着数。
	if err := (Measure{Absent: true, N: 7}).Check("分帧数"); err == nil {
		t.Errorf("缺席还带着数 7 竟被收了 —— 那 0 与「没量到」就分不开了")
	} else if !strings.Contains(err.Error(), "缺席") {
		t.Errorf("报的不是那一格：%v", err)
	}
	// 坏的另一头：缺席却带着出处（没量到的东西说不出是谁算的）。
	if err := (Measure{Absent: true, Basis: BasisMeasured}).Check("分帧数"); err == nil {
		t.Errorf("缺席还带着 measured 竟被收了")
	}
	// 好的一头：干净的缺席必须能过，且渲染出来不像 0。
	m := AbsentBecause("这一发不是流式的")
	if err := m.Check("分帧数"); err != nil {
		t.Fatalf("干净的缺席该收：%v", err)
	}
	s := m.String()
	for _, want := range []string{"没量到", "不是 0", "这一发不是流式的"} {
		if !strings.Contains(s, want) {
			t.Errorf("缺席那一格少了 %q：%q", want, s)
		}
	}
}

// ② 有数却没说谁算的 ⇒ 拒收。
func TestPresentWithoutBasisIsRefused(t *testing.T) {
	if err := (Measure{N: 1587}).Check("入 token"); err == nil {
		t.Errorf("一个光秃秃的 1587 竟被收了 —— 它会被下游当成实测")
	}
	if err := MeasuredOf(1587).Check("入字节"); err != nil {
		t.Errorf("说了是实测的该收：%v", err)
	}
	if got := MeasuredOf(0).String(); got != "0" {
		t.Errorf("真量到的 0 就该渲染成 0（它与缺席是两件事），得到 %q", got)
	}
}

// ③ 转述必须说清转述谁；估算必须在字形上认得出来。
func TestRelayNeedsSourceAndEstimateSaysSo(t *testing.T) {
	if err := RelayedOf(244, "  ").Check("出 token"); err == nil {
		t.Errorf("说是转述的却没说转述谁，竟被收了")
	}
	r := RelayedOf(244, "endpoint:agnes")
	if err := r.Check("出 token"); err != nil {
		t.Fatalf("带了出处的转述该收：%v", err)
	}
	if !strings.Contains(r.String(), "endpoint:agnes") {
		t.Errorf("转述那一格渲染时把出处丢了：%q", r.String())
	}
	e := EstimatedOf(1600, "按段字节数估")
	if err := e.Check("token 估算"); err != nil {
		t.Fatalf("估算该收：%v", err)
	}
	// 估算也必须说清估的是什么：一个说不出依据的估算没法被反驳，
	// 也就没法在将来被真读数替换掉。这一条与账本那层（Ledger.Append）是同一份判据，
	// 两处必须一起红或一起绿 —— 漂开的话，一份合法的 Measure 会在落账时才被拒。
	if err := EstimatedOf(1600, "  ").Check("token 估算"); err == nil {
		t.Errorf("说不出估算依据的估算竟被收了")
	}
	if _, err := NewLedger().Append(Record{Actor: "a", Kind: "k", Basis: BasisEstimated}); err == nil {
		t.Errorf("账本那一层也该拒（两处共用 needsSource 一份判据）")
	}
	// 一个光秃秃的 ≈1600 会被抄进结论，然后没人记得它是估的。
	if !strings.Contains(e.String(), "估算") || !strings.Contains(e.String(), "1600") {
		t.Errorf("估算渲染里没有「这是估算」那半句：%q", e.String())
	}
}

// ④ 只有实测与转述能拿去做判定；估算与模型自述不能。
func TestDecidableExcludesEstimateAndModelSays(t *testing.T) {
	cases := []struct {
		m    Measure
		want bool
		why  string
	}{
		{MeasuredOf(3), true, "我们自己数的"},
		{RelayedOf(3, "grader"), true, "能追到出处"},
		{EstimatedOf(3, "夹具"), false, "没校准过"},
		{Measure{N: 3, Basis: BasisModelSays}, false, "说话人给自己打分"},
		{AbsentBecause("没量到"), false, "没有数"},
	}
	for _, c := range cases {
		if got := c.m.Decidable(); got != c.want {
			t.Errorf("%s：Decidable 该是 %v，得到 %v", c.why, c.want, got)
		}
	}
}

// ⑤ 模型自述的数不许进合计。这一条要连「合计没被污染」一起断言 ——
// 只断「报错了」的话，一个先把数加进去再报错的实现也能过。
func TestTotalRefusesModelSays(t *testing.T) {
	var tot Total
	if err := tot.Add(MeasuredOf(100)); err != nil {
		t.Fatal(err)
	}
	if err := tot.Add(Measure{N: 999, Basis: BasisModelSays}); err == nil {
		t.Errorf("模型自述的 999 竟进了合计")
	}
	if tot.N() != 100 {
		t.Errorf("被拒的那一笔还是把合计推到了 %d —— 报错之前先把数加进去了", tot.N())
	}
	if tot.Counts() != 1 {
		t.Errorf("分母该是 1 笔，得到 %d（被拒的不该占分母）", tot.Counts())
	}
}

// ⑥ 混进一笔估算或缺席之后，「合计」就不许自称是一个完整的事实。
func TestTotalSaysWhenItIsNotACompleteFact(t *testing.T) {
	var all Total
	for _, n := range []int64{100, 200} {
		if err := all.Add(MeasuredOf(n)); err != nil {
			t.Fatal(err)
		}
	}
	if !all.Complete() {
		t.Errorf("两笔都是实测，该算完整事实：%s", all.String())
	}
	if !strings.Contains(all.String(), "实测 2 笔") {
		t.Errorf("合计没带分母：%q", all.String())
	}

	var mixed Total
	if err := mixed.Add(MeasuredOf(100)); err != nil {
		t.Fatal(err)
	}
	if err := mixed.Add(EstimatedOf(50, "夹具")); err != nil {
		t.Fatal(err)
	}
	if mixed.Complete() {
		t.Errorf("混了一笔估算还说自己是完整事实")
	}
	s := mixed.String()
	for _, want := range []string{"估算 1 笔", "不是一个完整的事实"} {
		if !strings.Contains(s, want) {
			t.Errorf("合计那句话少了 %q：%q", want, s)
		}
	}

	var withAbsent Total
	if err := withAbsent.Add(MeasuredOf(100)); err != nil {
		t.Fatal(err)
	}
	if err := withAbsent.Add(AbsentBecause("对端没回")); err != nil {
		t.Fatal(err)
	}
	if withAbsent.Complete() {
		t.Errorf("有一笔缺席还说自己是完整事实")
	}
	if !strings.Contains(withAbsent.String(), "缺席 1 笔") {
		t.Errorf("缺席那一笔没进分母：%q", withAbsent.String())
	}
}

// ⑦ 一笔都没有的合计不许渲染成「合计 0」—— 那会被读成「量到了，是零」。
func TestEmptyTotalSaysItIsEmpty(t *testing.T) {
	var tot Total
	if got := tot.String(); !strings.Contains(got, "空的") {
		t.Errorf("空合计渲染成 %q，会被读成量到了一个 0", got)
	}
	if tot.N() != 0 || tot.Counts() != 0 {
		t.Errorf("空合计的数是 %d / 分母 %d", tot.N(), tot.Counts())
	}
}

// ⑧ 被判成不可信的对端自报数：收下理由、丢掉数，且理由里要留着它当时给的那个数。
// 对应真实形状：对端回了一个 2 亿以上的 token 数，照抄会让合计溢出成负数。
func TestDistrustedKeepsTheNumberItRefused(t *testing.T) {
	m := Distrusted("对端自报的 usage 不可能是真的", 900000000)
	if err := m.Check("入 token"); err != nil {
		t.Fatalf("被判不可信的那一格该能落账：%v", err)
	}
	if !m.Absent || !m.Refused || m.N != 0 {
		t.Errorf("判成不可信之后还留着数 %d（Absent=%v Refused=%v）", m.N, m.Absent, m.Refused)
	}
	if !strings.Contains(m.Why, "900000000") {
		t.Errorf("理由里没留住对端当时给的那个数：%q —— 下一个人就无从追问", m.Why)
	}
	// 它渲染出来的那句话必须与「结构上没有这一格」不一样：
	// 对被判不可信的那一格说「没量到」是假话 —— 我们量到了，只是没收。
	plain := AbsentBecause("这一发不分帧")
	if m.String() == plain.String() {
		t.Errorf("两种缺席渲染成了同一句 %q —— 那就分不出「没量到」与「量到了但没收」", m.String())
	}
	if !strings.Contains(m.String(), "不可信") {
		t.Errorf("被判不可信那一格的渲染里没说出它为什么不可信：%q", m.String())
	}
	var tot Total
	if err := tot.Add(m); err != nil {
		t.Fatalf("被判不可信的一格该能进合计的分母：%v", err)
	}
	if tot.N() != 0 || tot.Counts() != 1 {
		t.Errorf("它该贡献 0 与 1 笔分母，得到 %d / %d", tot.N(), tot.Counts())
	}
	if tot.Complete() {
		t.Errorf("只有一笔缺席却说自己是完整事实")
	}
	// 那一笔必须在合计那句话里看得见，否则它只是被吞掉了。
	if !strings.Contains(tot.String(), "被判成不可信") {
		t.Errorf("被拒的那一笔没进合计的分母：%q", tot.String())
	}
}

// ⑨ 「被判成不可信」与「缺席」不许互相冒充，两个方向都要拦。
func TestRefusedAndAbsentCannotImpersonateEachOther(t *testing.T) {
	// 说不可信、却不是缺席 ⇒ 那它到底收没收这个数。
	if err := (Measure{Refused: true, N: 5, Basis: BasisMeasured}).Check("入 token"); err == nil {
		t.Errorf("Refused 却不是缺席，竟被收了")
	}
	// 缺席 + 不可信、却没留理由 ⇒ 下一个人无从追问，也留不住对端给的那个数。
	if err := (Measure{Absent: true, Refused: true}).Check("入 token"); err == nil {
		t.Errorf("被判成不可信却没说为什么，竟被收了")
	}
	// 干净的两种缺席都要能过，且渲染不同。
	a, b := AbsentBecause("结构上没有"), AbsentBecause("")
	if err := a.Check("x"); err != nil {
		t.Errorf("带理由的缺席该收：%v", err)
	}
	if err := b.Check("x"); err != nil {
		t.Errorf("不带理由的缺席也该收（理由不是必填项，只有 Refused 时才必填）：%v", err)
	}
	if a.String() == b.String() {
		t.Errorf("带理由与不带理由渲染成了同一句 %q —— 那一句理由就白写了", a.String())
	}
	if !strings.Contains(b.String(), "没量到") || strings.Contains(b.String(), "：") {
		t.Errorf("不带理由的缺席渲染出了一个空尾巴：%q", b.String())
	}
}
