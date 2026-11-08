package kernel

// gate_test.go 守「闸门拦了几次」这一栏的落盘与读回。
//
// 这一族在 10-17/10-18 被两个互不相知的调用方各撞过一次：预检拒收一个数都不记，
// 而已记的拒收数**活不过落盘**（只读审计面对任何文件账本都印 0）。
// 所以这里的判据重点是反臂：**没有那一栏时必须说"不可知"，不许说 0。**

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func sampleGate() Gate {
	return Gate{Rejected: 3, PrecheckRejected: 10, PrecheckDoor: 2, DoorRefused: 1, BadRequests: 4,
		TransportRefused: 5, State: GateLive}
}

// gateLineAt 把编好的字节流按行拆开，返回链行与计数行。
func splitGateLines(t *testing.T, b []byte) (chain []string, gate string) {
	t.Helper()
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.Contains(l, `"gate"`) {
			gate = l
			continue
		}
		chain = append(chain, l)
	}
	return chain, gate
}

// ① 证红：六笔数原样回来，而链一个字节都没为它改动。
func TestGateRoundTripsWithoutTouchingTheChain(t *testing.T) {
	l := journalFixture(t)
	docs := l.Docs()
	plain, err := MarshalJournal(docs, PayloadIncluded)
	if err != nil {
		t.Fatalf("夹具编码被拒：%v", err)
	}
	withGate, err := MarshalJournalWithGate(docs, PayloadIncluded, sampleGate())
	if err != nil {
		t.Fatalf("带计数编码被拒：%v", err)
	}
	// 链那一半必须**逐字节相同**：计数不许动分母，否则 verify 的「共 N 条」变成两本账混数。
	chain, gate := splitGateLines(t, withGate)
	if got := strings.Join(chain, "\n") + "\n"; got != string(plain) {
		t.Errorf("加了计数栏之后链行变了 —— 链上字节被动过（这正是要防的）")
	}
	if gate == "" {
		t.Fatal("没编出计数行：那 KPI 还是活不过落盘")
	}
	l2, j, rep, err := LoadLedger(withGate)
	if err != nil {
		t.Fatalf("带计数的账本读回被拒：%v（计数栏问题=%q）", err, j.GateProblem)
	}
	if rep.Entries != len(docs) || j.Parsed != len(docs) {
		t.Errorf("链条数被计数栏带跑了：CheckChain=%d 条、Parsed=%d 条、期望 %d", rep.Entries, j.Parsed, len(docs))
	}
	if j.GateLines != 1 || !j.HasGate {
		t.Errorf("计数栏的下落没记对：GateLines=%d、HasGate=%v", j.GateLines, j.HasGate)
	}
	if got := l2.Gate(); got.Rejected != 3 || got.PrecheckRejected != 10 {
		t.Errorf("内核那两笔没回来：%+v（发出去的是 %+v）", got, sampleGate())
	}
	if l2.GateStatus() != GateLive {
		t.Errorf("带计数读回来该是 GateLive，得到 %v", l2.GateStatus())
	}
	if j.Gate != sampleGate() {
		t.Errorf("外壳那四笔没原样带着： %+v ≠ %+v", j.Gate, sampleGate())
	}
	if err := l2.Verify(); err != nil {
		t.Errorf("读回来还能接着核链，得到：%v", err)
	}
}

// ② 反臂（这一条是本次改动的全部理由）：老格式的账本里没有计数栏，
// 读回来必须报**不可知**，而不是一个长得像实测的 0。
func TestLedgerWithoutGateLineIsUnknownNotZero(t *testing.T) {
	docs := journalFixture(t).Docs()
	old, err := MarshalJournal(docs, PayloadIncluded) // 旧调用方：不带计数
	if err != nil {
		t.Fatalf("编码被拒：%v", err)
	}
	l, j, _, err := LoadLedger(old)
	if err != nil {
		t.Fatalf("老格式账本该照样能读：%v", err)
	}
	if j.HasGate || j.GateLines != 0 {
		t.Errorf("这份字节里根本没有计数行，却报 HasGate=%v、GateLines=%d", j.HasGate, j.GateLines)
	}
	if l.GateStatus() != GateUnknown {
		t.Fatalf("没计数栏时状态该是 GateUnknown，得到 %v —— 这正是把「没记过」读成「没发生」的入口", l.GateStatus())
	}
	if l.Rejected() != 0 {
		t.Errorf("不可知时数本身必须还是 0（数与口径是两格），得到 %d", l.Rejected())
	}
	if s := j.String(); !strings.Contains(s, "不可知") {
		t.Errorf("Journal.String 要替读者把这一栏的下落说出来，得到：%s", s)
	}
	if s := j.String(); strings.Contains(s, "行解不开") {
		t.Errorf("没有计数行不该被念成「有一行解不开」，得到：%s", s)
	}
}

// ③ 反臂：改了六笔数里的任意一格而没重算封条 ⇒ 拒载，并且点名是封条不对。
func TestEditedGateNumbersFailTheSeal(t *testing.T) {
	docs := journalFixture(t).Docs()
	b, err := MarshalJournalWithGate(docs, PayloadIncluded, sampleGate())
	if err != nil {
		t.Fatalf("编码被拒：%v", err)
	}
	line := strings.TrimRight(string(b), "\n")
	tail := line[strings.LastIndex(line, "\n")+1:]
	var gl map[string]any
	if err := json.Unmarshal([]byte(tail), &gl); err != nil {
		t.Fatalf("夹具自己那一行解不开：%v", err)
	}
	g := gl["gate"].(map[string]any)
	g["precheck_rejected"] = float64(9999) // 有人把拦下的次数改大
	mutated, err := json.Marshal(gl)
	if err != nil {
		t.Fatalf("改夹具失败：%v", err)
	}
	edited := line[:strings.LastIndex(line, "\n")+1] + string(mutated) + "\n"
	_, j, _, err := LoadLedger([]byte(edited))
	if err == nil {
		t.Fatal("改了计数却没重算封条竟被收下 —— 这一栏就成了可以自己填的数，卖点「改一行没人知道」当场作废")
	}
	if !strings.Contains(err.Error(), "封条") {
		t.Errorf("拒因要点名「封条」，别让读的人去查链：%v", err)
	}
	if !strings.Contains(j.GateProblem, "封条") {
		t.Errorf("GateProblem 该自己说清楚，得到 %q", j.GateProblem)
	}
}

// ④ 反臂：负数。封条是重算的，一个负数照样能和它自己的封条对得上，所以值的范围也得有人守。
func TestNegativeGateCountIsRefused(t *testing.T) {
	g := sampleGate()
	g.Rejected = -1
	b, err := MarshalJournalWithGate(journalFixture(t).Docs(), PayloadIncluded, g)
	if err != nil {
		t.Fatalf("编码被拒（负数在编码这一步不该拦，拦在读侧）：%v", err)
	}
	if _, _, _, err = LoadLedger(b); err == nil {
		t.Fatal("拒收次数是负的竟被收下")
	} else if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("拒因该点名是哪一格（不能只说「有负数」），得到：%v", err)
	}
}

// ⑤ 反臂：两行计数 —— 不知道信哪个时，不许"取最后一行"这种静默裁决。
func TestTwoGateLinesAreRefused(t *testing.T) {
	docs := journalFixture(t).Docs()
	a, err := MarshalJournalWithGate(docs, PayloadIncluded, sampleGate())
	if err != nil {
		t.Fatalf("编码被拒：%v", err)
	}
	_, gate := splitGateLines(t, a)
	doubled := append(append([]byte(nil), a...), []byte(gate+"\n")...)
	_, j, _, err := LoadLedger(doubled)
	if err == nil {
		t.Fatal("两行计数竟被收下")
	}
	if !strings.Contains(j.GateProblem, "不止一行") {
		t.Errorf("拒因该说「不止一行」，得到：%v", err)
	}
}

// ⑥ 反臂：计数行夹在链行中间 —— 它必须排在所有链行之后，否则"最后一行"这类读者
// 会把它当一条账，而它带的六个数一格都不属于 49 格。
func TestGateLineMustComeAfterTheChain(t *testing.T) {
	docs := journalFixture(t).Docs()
	b, err := MarshalJournalWithGate(docs, PayloadIncluded, sampleGate())
	if err != nil {
		t.Fatalf("编码被拒：%v", err)
	}
	chain, gate := splitGateLines(t, b)
	moved := gate + "\n" + strings.Join(chain, "\n") + "\n"
	if _, _, _, err = LoadLedger([]byte(moved)); err == nil {
		t.Fatal("计数行排在链行之前也被收下了")
	} else if !strings.Contains(err.Error(), "之后") {
		t.Errorf("拒因该说清是位置不对（不是内容不对），得到：%v", err)
	}
}

// ⑦ 三态的第三态：从没有计数栏的账本接着记 ⇒ Partial（下界），
// 既不许留在 Unknown（白数了），也不许升成 Live（少算了前面那一段）。
func TestCountingAfterAnUnknownLedgerBecomesPartial(t *testing.T) {
	docs := journalFixture(t).Docs()
	old, err := MarshalJournal(docs, PayloadIncluded)
	if err != nil {
		t.Fatalf("编码被拒：%v", err)
	}
	l, _, _, err := LoadLedger(old)
	if err != nil {
		t.Fatalf("载入被拒：%v", err)
	}
	if n := l.NotePrecheckRejected(); n != 1 {
		t.Errorf("预检拒收该正好计一次，得到 %d", n)
	}
	if st := l.GateStatus(); st != GatePartial {
		t.Fatalf("这该是 Partial（本程数到的 + 前面不可知），得到 %v", st)
	}
	if !strings.Contains(GatePartial.String(), "只覆盖这一程") {
		t.Errorf("Partial 这句话要让读的人知道它是下界，得到：%s", GatePartial.String())
	}
	// 落账那一路的拒收同样要升态：两处各写一套就是两个 owner。
	l2, _, _, err := LoadLedger(old)
	if err != nil {
		t.Fatalf("载入被拒：%v", err)
	}
	if _, err := l2.Append(Record{Session: "s", Actor: "a", Kind: "k", Basis: BasisUnset}); err == nil {
		t.Fatal("没带 basis 的写入该被拒")
	}
	if l2.GateStatus() != GatePartial {
		t.Errorf("Append 那条拒收路也要升态，得到 %v", l2.GateStatus())
	}
}

// ⑧ 封条的算法只有一个 owner：外壳/第三方都得能按同一规则重算，
// 所以这里把 GateSeal 的输入形状钉死 —— 改字段顺序必须让这条测试红。
func TestGateSealIsRecomputableByOutsiders(t *testing.T) {
	g := sampleGate()
	head := chainHeadOf(journalFixture(t).Docs())
	want := sum([]byte(strings.Join([]string{
		"gate" + strconv.Itoa(gateVersion),
		strconv.Itoa(g.Rejected), strconv.Itoa(g.PrecheckRejected), strconv.Itoa(g.PrecheckDoor),
		strconv.Itoa(g.DoorRefused), strconv.Itoa(g.BadRequests), strconv.Itoa(g.TransportRefused),
		strconv.Itoa(int(g.State)),
		head,
	}, "\n")))
	if got := GateSeal(g, head); got != want {
		t.Errorf("封条重算不符：第三方按注释里那份规则重算会得到 %s，内核印的是 %s", want[:12], got[:12])
	}
	if got := GateSeal(g, "另一个链尾"); got == want {
		t.Fatal("换了链尾封条还一样 —— 那「把计数接到另一本账上」就检不出来")
	}
	gPartial := g
	gPartial.State = GatePartial
	if GateSeal(gPartial, head) == want {
		t.Fatal("把下界（Partial）改成全量（Live）封条竟不变 —— v1 的盖法就是这样，" +
			"两路互审各独立撞到的那条洗白路径靠这一格堵住")
	}
}

// ⑨ 反臂：空账也能带计数行（新进程一次都没拦 ⇒ 六笔真 0，且状态是 Live）。
// 这一条盯的是"0 与不可知长得一样"那一族：这里的 0 必须是量到的 0。
func TestEmptyLedgerCanCarryKnownZeros(t *testing.T) {
	b, err := MarshalJournalWithGate(nil, PayloadOmitted, Gate{State: GateLive})
	if err != nil {
		t.Fatalf("编码被拒：%v", err)
	}
	l, j, _, err := LoadLedger(b)
	if err != nil {
		t.Fatalf("读回被拒：%v", err)
	}
	if !j.HasGate || l.GateStatus() != GateLive {
		t.Errorf("空账带计数行时该是 Live，得到 HasGate=%v、状态=%v", j.HasGate, l.GateStatus())
	}
	if l.Rejected() != 0 || l.PrecheckRejected() != 0 {
		t.Errorf("六笔真 0 里读出了非零：%+v", j.Gate)
	}
	// 这句是给"0 与不可知"上最后一道锁：量到的 0 必须被印成「有」，不可知的才许印「不可知」。
	if s := j.String(); !strings.Contains(s, "拒落账 0") || strings.Contains(s, "不可知") {
		t.Errorf("量到的 0 没被印成「有」，得到：%s", s)
	}
}

// ⑩ 证红（M2 互审两路各独立撞到的同一条，10-18 深夜）：**下界要活过一次往返**。
// 原先口径不落盘 —— 一本「只覆盖这一程」的账编出去再读回来，就被硬置成「这一程完整可数」，
// 一次往返把下界洗成全量，而这正是这次改动声称要防的形状。它藏在一片绿针背后：
// ⑦ 那条只测内存，从来没 re-encode → re-load。
func TestPartialSurvivesOneRoundTrip(t *testing.T) {
	docs := journalFixture(t).Docs()
	noGate, err := MarshalJournal(docs, PayloadIncluded)
	if err != nil {
		t.Fatalf("老格式编码被拒：%v", err)
	}
	l, _, _, err := LoadLedger(noGate)
	if err != nil {
		t.Fatalf("载入被拒：%v", err)
	}
	if l.GateStatus() != GateUnknown {
		t.Fatalf("起点该是不可知，得到 %v", l.GateStatus())
	}
	l.NotePrecheckRejected()
	l.noteRejected()
	if st := l.GateStatus(); st != GatePartial {
		t.Fatalf("本程数到东西后该是下界，得到 %v", st)
	}
	b, err := MarshalJournalWithGate(l.Docs(), PayloadIncluded, l.Gate())
	if err != nil {
		t.Fatalf("下界那本编不出去（Unknown 不许落盘，Partial 必须能）：%v", err)
	}
	l2, j2, _, err := LoadLedger(b)
	if err != nil {
		t.Fatalf("读回被拒：%v", err)
	}
	if l2.GateStatus() != GatePartial {
		t.Errorf("下界被洗成了 %v —— 口径没跟着落盘，六笔数就成了一句没法核的话", l2.GateStatus())
	}
	if j2.Gate.PrecheckRejected != 1 || j2.Gate.Rejected != 1 {
		t.Errorf("数本身没跟着回来：%+v", j2.Gate)
	}
	if !strings.Contains(j2.String(), "只覆盖这一程") {
		t.Errorf("String 要把口径一起说出去，得到：%s", j2.String())
	}
}

// ⑪ 反臂：不可知**不许写成一行**。写出一行自称不可知，是让同一件事有两种互相矛盾的记录。
func TestUnknownStateRefusesToBeWritten(t *testing.T) {
	for _, st := range []GateState{GateUnknown, GateState(9)} {
		_, err := MarshalJournalWithGate(journalFixture(t).Docs(), PayloadIncluded, Gate{State: st})
		if err == nil {
			t.Errorf("口径 %d 竟能落盘 —— 那读侧就没法分辨这是下界还是全量了", int(st))
			continue
		}
		if !strings.Contains(err.Error(), "不可知") && !strings.Contains(err.Error(), "只认") {
			t.Errorf("拒因该说清口径的合法取值，得到：%v", err)
		}
	}
	// 手写一行 state=0 也要被读侧拒（不落盘那道闸挡不住手改的文件）。
	g := sampleGate()
	g.State = GateUnknown
	b, err := MarshalJournalWithGate(journalFixture(t).Docs(), PayloadIncluded, sampleGate())
	if err != nil {
		t.Fatalf("夹具编码被拒：%v", err)
	}
	edited := strings.Replace(string(b), `"state":1`, `"state":0`, 1)
	if edited == string(b) {
		t.Fatal("夹具那行里找不到 state，下面这条判据就是空的")
	}
	if _, j, _, err := LoadLedger([]byte(edited)); err == nil {
		t.Errorf("state=0 的计数行被收下了（GateProblem=%q）", j.GateProblem)
	} else if !strings.Contains(err.Error(), "不可知") {
		t.Errorf("拒因该点名「不可知不许落盘」，得到：%v", err)
	}
}

// ⑫ 反臂：一行**读不动**的计数行不许双计。
// 内核侧互审（10-18）抓的原形状：它同时进 GateLines 与 BadLines，于是 String 印出
// 「计数栏：有（拒落账 0、拒预检 0）」—— 一行根本没解开的字节被念成"数在手、都是 0"。
func TestBadGateLineIsNotCountedAsPresent(t *testing.T) {
	docs := journalFixture(t).Docs()
	good, err := MarshalJournalWithGate(docs, PayloadIncluded, sampleGate())
	if err != nil {
		t.Fatalf("编码被拒：%v", err)
	}
	// 造一行"看着是计数栏、但解不开"的字节：整行仍是合法 JSON（probe 认得出 gate 键），
	// 而 rejected 装的是字符串 —— gateLine 解不开。把整行写成坏 JSON 会走普通链行那条岔路，
	// 测的就是"解不开的行"而不是"解不开的计数栏"了。
	broken := strings.Replace(string(good), `"rejected":3`, `"rejected":"三"`, 1)
	j, err := ReadJournal([]byte(broken))
	if err == nil {
		t.Fatal("一行解不开的计数行被当成读成功了")
	}
	if j.GateLines != 0 {
		t.Errorf("解不开的行被计成了 GateLines=%d（它没解出任何东西）", j.GateLines)
	}
	if j.BadGateLines != 1 {
		t.Errorf("BadGateLines 该是 1，得到 %d", j.BadGateLines)
	}
	if len(j.BadLines) != 1 || !strings.Contains(j.BadLines[0], "计数栏") {
		t.Errorf("BadLines 该点名是计数栏那一行，得到 %v", j.BadLines)
	}
	if s := j.String(); strings.Contains(s, "计数栏：有（") {
		t.Errorf("String 把读不动的一行印成了「有（数在手）」：%s", s)
	} else if !strings.Contains(s, "读不动") {
		t.Errorf("String 该说这一行读不动 ⇒ 不可知，得到：%s", s)
	}
}
