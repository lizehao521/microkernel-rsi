package kernel

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// fullShares 是六类段的份额，相加正好 100。
// 这份表是判据的**基准夹具**，只此一处；下面另有两份是刻意改过它的对照夹具
// （sharesShort 少 5 个百分点、sharesNoMemory 换掉了记忆那一档），三份都能被 grep 到。
func fullShares() map[PartKind]int {
	return map[PartKind]int{
		KindSystem: 8, KindMemory: 18, KindHistory: 32,
		KindObserved: 25, KindUser: 12, KindNotice: 5,
	}
}

// sharesShort 相加 95：份额之和**可以**小于 100，省下来的归最旧的内容段。
func sharesShort() map[PartKind]int {
	return map[PartKind]int{
		KindSystem: 8, KindMemory: 18, KindHistory: 32,
		KindObserved: 25, KindUser: 12,
	}
}

// sharesNoMemory 换掉了记忆那一档，用来造「发了一类段却没给它份额」的现场。
func sharesNoMemory() map[PartKind]int {
	return map[PartKind]int{
		KindSystem: 8, KindHistory: 40, KindObserved: 30, KindUser: 15, KindNotice: 7,
	}
}

// validInput 造一份处处自洽的输入侧账。每条判据都从它出发只改一格 ——
// 一次改两格的话，报红的那一条说不清是哪一格造成的。
func validInput(t *testing.T) InputMeta {
	t.Helper()
	b, err := DeriveBudget(24000, 25, fullShares(), EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节"))
	if err != nil {
		t.Fatalf("夹具自己就不合法：%v", err)
	}
	return InputMeta{
		Call:  1,
		Order: []PartKind{KindSystem, KindUser, KindHistory, KindObserved, KindObserved, KindNotice},
		Parts: []PartCount{
			{Kind: KindSystem, Parts: 1, Bytes: 500},
			{Kind: KindUser, Parts: 1, Bytes: 1200},
			{Kind: KindObserved, Parts: 2, Bytes: 4000},
			{Kind: KindHistory, Parts: 1, Bytes: 800},
			{Kind: KindNotice, Parts: 1, Bytes: 60},
		},
		ContentBytes: 6560,
		Tokens:       EstimatedOf(1600, "按段字节数估：CJK 一字一 token、其余四字节一 token"),
		Budget:       b,
		Cuts: []Cut{{
			Kind: KindObserved, Ref: "step3:read_file:docs/a.md", Role: RoleTool,
			Squeeze: SqueezeFolded, FromBytes: 5000, ToBytes: 124,
			SHA256: sha("被折掉的那一段原文"),
		}},
		Notice: Notice{Sent: true, Text: "本节被裁过：观察 1 段折成了桩"},
	}
}

// ① 预算公式只有一个 owner。份额之和**可以**小于 100（省下来的归最旧的内容段），
// 但不许大于 —— 那等于给了一个兑现不了的承诺。这一条是照着证据源写的：
// 早先这里要求「恰好 100」，把证据源明确放宽的不变量收紧了。
func TestDeriveBudgetIsTheOnlyOwnerOfTheFormula(t *testing.T) {
	b, err := DeriveBudget(24000, 25, fullShares(), EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节"))
	if err != nil {
		t.Fatalf("合法的一份竟被拒：%v", err)
	}
	// 期望值从入参重算，不抄一个常量：改夹具里任一个数，这条不会以「算错了」的口吻误报。
	if want := int64(24000) * int64(100-25) / 100; b.ContentCeiling != want {
		t.Errorf("内容额度该是 %d（24000 扣 25%% 传输余量），得到 %d", want, b.ContentCeiling)
	}
	if b.EffectiveCeiling != b.ContentCeiling || b.ClampRounds != 0 {
		t.Errorf("没夹过的时候生效额度该等于公式算出来的那个：%d vs %d（%d 刀）",
			b.EffectiveCeiling, b.ContentCeiling, b.ClampRounds)
	}
	if b.LeftoverPct != 0 {
		t.Errorf("份额相加正好 100，剩余额度该是 0%%，得到 %d%%", b.LeftoverPct)
	}
	// 份额之和小于 100 是合法的，且剩下的那部分要被记下来。
	short, err := DeriveBudget(24000, 25, sharesShort(), EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节"))
	if err != nil {
		t.Errorf("份额相加 95 该被收（省下来的归最旧的内容段）：%v", err)
	}
	if short.LeftoverPct != 5 {
		t.Errorf("剩余额度该是 5%%，得到 %d%%", short.LeftoverPct)
	}
	// 三头都要红，且红出来的话要说得出是哪个数不对。
	over := map[PartKind]int{KindSystem: 50, KindHistory: 60}
	if _, err := DeriveBudget(24000, 25, over, EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节")); err == nil {
		t.Errorf("份额相加 110 竟被收了 —— 那是一个兑现不了的承诺")
	} else if !strings.Contains(err.Error(), "110") {
		t.Errorf("那句话没报出它数到的和：%v", err)
	}
	if _, err := DeriveBudget(0, 25, fullShares(), EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节")); err == nil {
		t.Errorf("单发上限 0 竟被收了")
	}
	if _, err := DeriveBudget(24000, 100, fullShares(), EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节")); err == nil {
		t.Errorf("传输余量 100%% 会把内容额度算成 0，竟被收了")
	}
	if _, err := DeriveBudget(24000, 25, map[PartKind]int{KindHistory: -5}, EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节")); err == nil {
		t.Errorf("负的份额竟被收了")
	}
	// 固定开销是我们估的或量的，不是对端说的。
	if _, err := DeriveBudget(24000, 25, fullShares(), RelayedOf(1200, "endpoint")); err == nil {
		t.Errorf("固定开销记成转述竟被收了")
	}
	if _, err := DeriveBudget(24000, 25, fullShares(), AbsentBecause("这一发没算")); err != nil {
		t.Errorf("固定开销缺席该收（它本来就是估算，估不出就是估不出）：%v", err)
	}
}

// ② 手搭一份自相矛盾的预算必须被 Check 挡下。
// 「公式只有一个 owner」这句话不能只写在注释里：Budget 是导出结构体，谁都能手搭一个。
func TestHandBuiltBudgetIsRefused(t *testing.T) {
	// 内容额度与公式对不上 —— 这是「第二个 owner」最直接的形状。
	m := validInput(t)
	m.Budget.ContentCeiling = 999999
	m.Budget.EffectiveCeiling = 999999
	err := m.Check()
	if err == nil {
		t.Fatalf("手搭一个 ContentCeiling=999999 的预算竟被收了 —— 那条公式就有了第二个 owner")
	}
	for _, want := range []string{"999999", "18000", "第二个 owner"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("那句话少了 %q：%v", want, err)
		}
	}
	// 全零的预算。
	m = validInput(t)
	m.Budget = Budget{}
	if err := m.Check(); err == nil {
		t.Errorf("一份全零的预算竟被收了")
	}
	// 生效额度比公式算出来的还大 —— 回夹只会收小。
	m = validInput(t)
	m.Budget.EffectiveCeiling = m.Budget.ContentCeiling + 1
	if err := m.Check(); err == nil {
		t.Errorf("生效额度大于公式额度竟被收了")
	} else if !strings.Contains(err.Error(), "不会放大") {
		t.Errorf("那句话没说清方向：%v", err)
	}
	// 夹过却没记账 / 记了账却没夹过 —— 两个方向都要拦。
	m = validInput(t)
	m.Budget.EffectiveCeiling = 17000
	if err := m.Check(); err == nil {
		t.Errorf("生效额度已经收小、却记着 0 刀回夹，竟被收了")
	} else if !strings.Contains(err.Error(), "没记账") {
		t.Errorf("那句话没说出真凶：%v", err)
	}
	m = validInput(t)
	m.Budget.ClampRounds = 2
	if err := m.Check(); err == nil {
		t.Errorf("记着 2 刀回夹、生效额度却与公式一样，竟被收了")
	}
	// 剩余额度与份额之和对不上。
	m = validInput(t)
	m.Budget.LeftoverPct = 7
	if err := m.Check(); err == nil {
		t.Errorf("剩余额度 7%% 而份额相加是 100，竟被收了")
	} else if !strings.Contains(err.Error(), "各算了一遍") {
		t.Errorf("那句话没说这是两个数各算了一遍：%v", err)
	}
}

// ③ 每一刀回夹必须真收小。一刀没夹小还继续夹，就是拿循环换一个不会到的结论。
func TestClampMustActuallyShrink(t *testing.T) {
	b, err := DeriveBudget(24000, 25, fullShares(), EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节"))
	if err != nil {
		t.Fatal(err)
	}
	c1, err := b.Clamp(17000)
	if err != nil {
		t.Fatalf("第一刀该收：%v", err)
	}
	if c1.EffectiveCeiling != 17000 || c1.ClampRounds != 1 {
		t.Errorf("第一刀之后该是 17000/1 刀，得到 %d/%d", c1.EffectiveCeiling, c1.ClampRounds)
	}
	c2, err := c1.Clamp(16000)
	if err != nil {
		t.Fatalf("第二刀该收：%v", err)
	}
	if c2.ClampRounds != 2 || c2.ContentCeiling != b.ContentCeiling {
		t.Errorf("第二刀之后该是 2 刀、公式额度不变，得到 %d 刀 / %d", c2.ClampRounds, c2.ContentCeiling)
	}
	// 份额按**生效**额度算，不按公式额度算 —— 夹过之后每一档都跟着变小。
	before, _ := b.ShareOf(KindObserved)
	after, _ := c2.ShareOf(KindObserved)
	if after >= before {
		t.Errorf("夹过两刀之后观察档该变小：%d → %d", before, after)
	}
	if want := int64(16000) * 25 / 100; after != want {
		t.Errorf("观察档该是生效额度的 25%% = %d，得到 %d", want, after)
	}
	// 没收小就不是一刀；夹到 0 就该报「额度太紧」而不是夹过去。
	if _, err := c2.Clamp(16000); err == nil {
		t.Errorf("夹到同一个数竟被当成一刀收了")
	}
	if _, err := c2.Clamp(20000); err == nil {
		t.Errorf("夹大竟被收了")
	}
	if _, err := c2.Clamp(0); err == nil {
		t.Errorf("夹到 0 竟被收了")
	} else if !strings.Contains(err.Error(), "太紧") {
		t.Errorf("那句话没指出去路：%v", err)
	}
}

// ④ 「这一类不按份额管」与「这一类的份额是零」必须分得开。
func TestShareOfSeparatesNoShareFromZeroShare(t *testing.T) {
	b, err := DeriveBudget(24000, 25, fullShares(), EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节"))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := b.ShareOf(KindObserved); !ok || got != b.ContentCeiling*25/100 {
		t.Errorf("观察档该是生效额度的 25%%，得到 %d（登记了没有：%v）", got, ok)
	}
	// 工具清单没登记份额。返回 0 与 false，而不是 0 与 true ——
	// 后者会被读成「它有一份份额，只是这次是零」。
	if got, ok := b.ShareOf(KindToolSpec); ok || got != 0 {
		t.Errorf("没登记份额的那一类该返回 0 与 false，得到 %d / %v", got, ok)
	}
	if got, ok := b.ObservationShare(); !ok || got == 0 {
		t.Errorf("ObservationShare 该与 ShareOf(observation) 给出同一个数：%d / %v", got, ok)
	}
}

// ⑤ 反函数与正函数必须互相吻合。它必须与正函数住在同一处 ——
// 比例抄到第二处就不再是同一个数了。
func TestObservationShareAndItsReverseAgree(t *testing.T) {
	b, err := DeriveBudget(24000, 25, fullShares(), EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节"))
	if err != nil {
		t.Fatal(err)
	}
	share, ok := b.ObservationShare()
	if !ok || share <= 0 {
		t.Fatalf("夹具里观察档该有份额：%d / %v", share, ok)
	}
	// 装下正好一整档，需要的单发上限不该超过现在这个。
	need, err := BytesToHoldObservations(b, share)
	if err != nil {
		t.Fatalf("反算该成功：%v", err)
	}
	if need > b.CallBytesCeiling {
		t.Errorf("装下 %d 字节要 %d 的上限，而现在就给了 %d —— 正反两个函数不吻合", share, need, b.CallBytesCeiling)
	}
	// 再多一个字节就该要一个更大的上限。这一头不测的话，
	// 一个「永远返回 0」的反函数也能通过上面那条。
	need1, err := BytesToHoldObservations(b, share+1)
	if err != nil {
		t.Fatal(err)
	}
	if need1 <= b.CallBytesCeiling {
		t.Errorf("装下 %d 字节只要 %d 的上限（现在给的是 %d）—— 反函数算少了，它会让人以为宽预算够用",
			share+1, need1, b.CallBytesCeiling)
	}
	// 反函数与正函数要在一段范围上互相吻合，不能只在两个点上对。
	// 只测两点的话，一个取整方向写反的反函数（向上取整写成向下）能溜过去，
	// 而它的后果是「以为宽预算够用」—— 一个装不下的上限被算成了装得下。
	for n := int64(1); n <= 400; n++ {
		need, err := BytesToHoldObservations(b, n)
		if err != nil {
			t.Fatalf("n=%d 反算失败：%v", n, err)
		}
		got, err := DeriveBudget(need, 25, fullShares(), EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节"))
		if err != nil {
			t.Fatalf("n=%d 反算出 %d，拿它造预算却失败：%v", n, need, err)
		}
		share, _ := got.ObservationShare()
		if share < n {
			t.Fatalf("n=%d：反算说单发上限给到 %d 就够，而按那个上限观察档只有 %d 字节 —— 反函数算少了",
				n, need, share)
		}
		// 也不许算多：少给一个字节就该真的装不下，否则这个反函数只是在浪费预算。
		if need > 1 {
			tight, err := DeriveBudget(need-1, 25, fullShares(), EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节"))
			if err != nil {
				t.Fatalf("n=%d 用 %d 造预算失败：%v", n, need-1, err)
			}
			if s, _ := tight.ObservationShare(); s >= n {
				t.Fatalf("n=%d：单发上限 %d 就已经装得下（观察档 %d），反函数却说要 %d —— 它算多了",
					n, need-1, s, need)
			}
		}
	}
	// 没登记观察档份额的预算反算不出来，且要说清是反算不出来而不是答案是 0。
	noObs, err := DeriveBudget(24000, 25, map[PartKind]int{KindHistory: 100}, EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BytesToHoldObservations(noObs, 100); err == nil {
		t.Errorf("没有观察档份额竟反算出了一个数")
	}
}

// ⑤b 那张分档表自己也要能被看着变红。它是包级变量，所以这一条临时改它、再用 defer 还原 ——
// 一个只被正面断言过的表，等于一条写在注释里的承诺。
func TestCutListsTableCanBeCaughtBeingWrong(t *testing.T) {
	saved := CutLists
	defer func() { CutLists = saved }()

	// 少一档：那一档发生了也不会在账上出现。
	CutLists = saved[:len(saved)-1]
	if err := CheckCutLists(); err == nil {
		t.Errorf("少了一档竟没被核出来")
	} else if !strings.Contains(err.Error(), "dropped-whole") {
		t.Errorf("那句话没点名是哪个取值没落到档里：%v", err)
	}
	// 同一份账现在也该被 Check 拒（Check 会先核那张表）。
	m := validInput(t)
	if err := m.Check(); err == nil {
		t.Errorf("分档表少了一档，Check 竟还收账")
	}

	// 一个取值落两档：同一段会被数两遍。
	CutLists = []struct {
		Key   string
		Label string
		Has   []Squeeze
	}{
		{"folded", "折成桩", []Squeeze{SqueezeFolded}},
		{"clipped", "裁尾", []Squeeze{SqueezeTailDropped, SqueezeFolded}},
		{"dropped", "整段没进", []Squeeze{SqueezeDroppedWhole, SqueezeBudgetExhausted}},
	}
	if err := CheckCutLists(); err == nil {
		t.Errorf("folded 落了两档竟没被核出来")
	} else if !strings.Contains(err.Error(), "两档") {
		t.Errorf("那句话没说出真凶：%v", err)
	}
	// 收了「没被挤」当一档：没被挤的段不该出现在裁剪表里。
	CutLists = append(append([]struct {
		Key   string
		Label string
		Has   []Squeeze
	}{}, saved...), struct {
		Key   string
		Label string
		Has   []Squeeze
	}{"none", "没被挤", []Squeeze{SqueezeNone}})
	if err := CheckCutLists(); err == nil {
		t.Errorf("把「没被挤」当成一档竟没被核出来")
	}
	// 一档空着：那一档永远是空的。
	CutLists = []struct {
		Key   string
		Label string
		Has   []Squeeze
	}{
		{"folded", "折成桩", []Squeeze{SqueezeFolded}},
		{"clipped", "裁尾", []Squeeze{SqueezeTailDropped}},
		{"dropped", "整段没进", []Squeeze{SqueezeDroppedWhole, SqueezeBudgetExhausted}},
		{"empty", "空的", nil},
	}
	if err := CheckCutLists(); err == nil {
		t.Errorf("一档一个取值都没收竟没被核出来")
	}
	CutLists = saved
	if err := CheckCutLists(); err != nil {
		t.Errorf("还原之后那张表该合法：%v", err)
	}
}

// ⑥ 各类段相加必须等于内容总字节。这一条是「同一个量由两处各算一遍」的探针。
func TestPartBytesMustSumToContentBytes(t *testing.T) {
	m := validInput(t)
	if err := m.Check(); err != nil {
		t.Fatalf("夹具自己就该合法：%v", err)
	}
	m.ContentBytes = 6561
	err := m.Check()
	if err == nil {
		t.Fatalf("段表相加 6560、总字节写 6561，竟被收了")
	}
	// 报出来的话必须同时带上两个数：只说「对不上」的话，下一个人不知道该信哪一个。
	for _, want := range []string{"6560", "6561"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("那句话少了 %s：%v", want, err)
		}
	}
}

// ⑦ 段表里一类段一行；0 段的行是噪音，重复的行会被数两遍。
func TestPartRowsAreOnePerKindAndNonEmpty(t *testing.T) {
	m := validInput(t)
	m.Parts = append(m.Parts, PartCount{Kind: KindObserved, Parts: 1, Bytes: 100})
	m.ContentBytes += 100
	m.Order = append(m.Order, KindObserved)
	if err := m.Check(); err == nil {
		t.Errorf("观察段占了两行竟被收了 —— 相加时它会被数两遍")
	} else if !strings.Contains(err.Error(), "两次") {
		t.Errorf("报的不是「重复」那一格：%v", err)
	}

	m = validInput(t)
	m.Parts = append(m.Parts, PartCount{Kind: KindMemory, Parts: 0, Bytes: 0})
	if err := m.Check(); err == nil {
		t.Errorf("0 段的行竟被收了")
	}
}

// ⑧ 发送顺序表与段表必须数的是同一批段。顺序就是花钱顺序，也是可复用前缀的判据。
func TestOrderMustMatchPartCounts(t *testing.T) {
	m := validInput(t)
	if len(m.Order) != 6 {
		t.Fatalf("夹具该有 6 项顺序，得到 %d —— 这条判据的前提先不成立了", len(m.Order))
	}
	m.Order = m.Order[:5]
	if err := m.Check(); err == nil {
		t.Errorf("顺序表少一项竟被收了")
	} else if !strings.Contains(err.Error(), "同一批段") {
		t.Errorf("那句话没说这是两个数得同源：%v", err)
	}
	m = validInput(t)
	m.Order[2] = KindMemory // 顶掉 history 那一项：项数不变，但这一类段表里没有
	if err := m.Check(); err == nil {
		t.Errorf("顺序表里多出一类段表里没有的段，竟被收了")
	} else if !strings.Contains(err.Error(), "memory") {
		t.Errorf("那句话没点名是哪一类：%v", err)
	}
	// 反方向：同一类段在两张表里的个数对不上。
	m = validInput(t)
	m.Order[2] = KindNotice // history 换成 notice：项数仍是 6，但两类段的个数都错了
	err := m.Check()
	if err == nil {
		t.Errorf("段表与顺序表数出的个数不一致，竟被收了")
	}
	for _, want := range []string{"history", "两张表数的是同一批段"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("那句话少了 %q（它该点名对不上的那一类）：%v", want, err)
		}
	}
}

// ⑨ 空段表与空顺序表都不许被收：一发请求至少有一段（协议或题面）。
func TestEmptyPartsOrOrderIsRefused(t *testing.T) {
	m := validInput(t)
	m.Parts, m.Order, m.ContentBytes, m.Cuts = nil, nil, 0, nil
	m.Notice = Notice{}
	if err := m.Check(); err == nil {
		t.Errorf("一份什么都没装的账竟被收了 —— 它会被读成「这一发装配出了 0 字节」，而不是「这一份账是空的」")
	} else if !strings.Contains(err.Error(), "段表是空的") {
		t.Errorf("那句话没说出真凶：%v", err)
	}
	m = validInput(t)
	m.Order = nil
	// 这里必须断言**是哪一条规则**报的红：段表非空而顺序表空时，
	// 「顺序表是空的」与「两个数得同源」两条都会成立，只断 err != nil 的话，
	// 把前一条整条删掉，后一条会替它挡着，这条判据就成了永久假绿（探针 m51 抓到的就是这个）。
	if err := m.Check(); err == nil {
		t.Errorf("顺序表空了竟被收了")
	} else if !strings.Contains(err.Error(), "顺序表是空的") {
		t.Errorf("报的不是「顺序表是空的」那一条，而是别的规则替它挡着了：%v", err)
	}
}

// ⑩ 每一类要发的段都必须登记过份额，**不留豁免**。
// 早先这里豁免了协议/说明书/工具清单三类，而证据源对每一类都先查份额、没登记就装配失败 ——
// 于是内核收得下的一份账，框架压根装配不出来。「协议不按份额封顶」是另一件事，
// 它住在 MayShrink 里（整段发或整段不发），不住在这里。
func TestEverySentKindNeedsAShare(t *testing.T) {
	b, err := DeriveBudget(24000, 25, sharesNoMemory(), EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节"))
	if err != nil {
		t.Fatalf("夹具份额相加是 100，该合法：%v", err)
	}
	m := validInput(t)
	m.Budget = b
	m.Parts = append(m.Parts, PartCount{Kind: KindMemory, Parts: 1, Bytes: 300})
	m.ContentBytes += 300
	m.Order = append(m.Order, KindMemory)
	if err := m.Check(); err == nil {
		t.Errorf("记忆段发了却没有份额，竟被收了")
	} else if !strings.Contains(err.Error(), "memory") {
		t.Errorf("那句话没点名是哪一类段：%v", err)
	}
	// 工具清单也不豁免：没给它份额却要发它，就是一份装配不出来的账。
	m = validInput(t)
	m.Parts = append(m.Parts, PartCount{Kind: KindToolSpec, Parts: 1, Bytes: 900})
	m.ContentBytes += 900
	m.Order = append(m.Order, KindToolSpec)
	if err := m.Check(); err == nil {
		t.Errorf("工具清单没份额却要发它，竟被收了")
	}
	// 给了份额就该收。
	withSpec := fullShares()
	withSpec[KindToolSpec] = 6
	withSpec[KindMemory] = 12 // 从 18 降到 12，腾出 6 个百分点给工具清单，加总仍是 100
	b2, err := DeriveBudget(24000, 25, withSpec, EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节"))
	if err != nil {
		t.Fatalf("夹具份额相加该是 100：%v", err)
	}
	m.Budget = b2
	if err := m.Check(); err != nil {
		t.Errorf("给了工具清单份额之后就该收：%v", err)
	}
}

// ⑪ 内容超过**生效**额度 ⇒ 这一发本该被回夹。那句话要把公式额度与刀数一起说出来，
// 否则读的人不知道是额度算小了还是内容装多了。
func TestContentOverEffectiveCeilingIsRefused(t *testing.T) {
	m := validInput(t)
	m.Budget, _ = m.Budget.Clamp(6000)
	m.Parts[2].Bytes = 5000
	m.ContentBytes = 7560
	err := m.Check()
	if err == nil {
		t.Fatalf("内容 7560 超过生效额度 6000，竟被收了")
	}
	for _, want := range []string{"7560", "6000", "18000", "1 刀"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("那句话少了 %s（该同时带公式额度与刀数）：%v", want, err)
		}
	}
	m = validInput(t)
	if err := m.Check(); err != nil {
		t.Errorf("没夹过、内容也没超额，该收：%v", err)
	}
}

// ⑫ 只折内容。协议、题面、记忆、说明书、工具清单各有各的「不许折」理由。
func TestFoldOnlyAppliesToContent(t *testing.T) {
	for _, k := range []PartKind{KindObserved, KindHistory} {
		m := validInput(t)
		// 历史段必须指向"在它之前的一发"，所以夹具得真有第 2 发可指（第 1 发无从指）。
		m.Call = 2
		m.Cuts[0].RefCall = 1
		m.Cuts[0].Kind = k
		if err := m.Check(); err != nil {
			t.Errorf("%s 是内容，折它该被收：%v", k, err)
		}
		if !k.MayFold() {
			t.Errorf("%s 该在可折白名单里", k)
		}
	}
	for _, k := range []PartKind{KindSystem, KindUser, KindMemory, KindSkill, KindToolSpec, KindNotice} {
		m := validInput(t)
		m.Cuts[0].Kind = k
		m.Cuts[0].Role = RoleUser
		if err := m.Check(); err == nil {
			t.Errorf("%s 被折成桩竟被收了", k)
		}
		if k.MayFold() {
			t.Errorf("%s 不该在可折白名单里", k)
		}
	}
}

// ⑬ 桩是整段的：塞不下就记成「整段没进」，不许发半截桩。
func TestStubIsNeverHalfSent(t *testing.T) {
	m := validInput(t)
	m.Cuts[0].ToBytes = 0
	if err := m.Check(); err == nil {
		t.Errorf("折过之后 0 字节竟被收了 —— 那就是发了半截桩，或者根本没折")
	}
	m = validInput(t)
	m.Cuts[0].ToBytes = m.Cuts[0].FromBytes
	if err := m.Check(); err == nil {
		t.Errorf("折过之后与原文一样大竟被收了 —— 它没被折")
	}
	m = validInput(t)
	m.Cuts[0].FromBytes, m.Cuts[0].ToBytes = 5000, 4000
	if err := m.Check(); err != nil {
		t.Errorf("只要真变小、且不超过这一类真发出去的字节，就该收：%v", err)
	}
}

// ⑭ 整段没进 / 预算花完 ⇒ 发出去的必须是 0 字节；裁尾 ⇒ 必须留下一个非空的真前缀。
func TestDroppedCarriesNoBytesAndClipKeepsAPrefix(t *testing.T) {
	m := validInput(t)
	m.Cuts[0] = Cut{Kind: KindObserved, Ref: "step1", Role: RoleTool, Squeeze: SqueezeDroppedWhole,
		FromBytes: 900, ToBytes: 5, SHA256: sha("x")}
	if err := m.Check(); err == nil {
		t.Errorf("说是整段没进、却记着发出去 5 字节，竟被收了")
	}
	m.Cuts[0].ToBytes = 0
	if err := m.Check(); err != nil {
		t.Errorf("整段没进就该记 0 字节：%v", err)
	}
	// 预算花完时一个空段没轮到，原文 0 字节是 runloop 真会产出的形状，不许拒。
	m = validInput(t)
	m.Cuts[0] = Cut{Kind: KindObserved, Ref: "step1", Role: RoleTool, Squeeze: SqueezeBudgetExhausted,
		FromBytes: 0, ToBytes: 0, SHA256: sha("")}
	if err := m.Check(); err != nil {
		t.Errorf("空段在预算花完时被记成「没轮到」，该收：%v", err)
	}

	m = validInput(t)
	m.Cuts[0] = Cut{Kind: KindUser, Ref: "task:1", Role: RoleUser, Squeeze: SqueezeTailDropped,
		FromBytes: 3000, ToBytes: 1200, SHA256: sha("y")}
	if err := m.Check(); err != nil {
		t.Errorf("题面裁尾留前缀该被收：%v", err)
	}
	m.Cuts[0].ToBytes = 0
	if err := m.Check(); err == nil {
		t.Errorf("裁尾留下 0 字节竟被收了 —— 那是整段没进，不是裁尾")
	}
	m.Cuts[0].ToBytes = 3000
	if err := m.Check(); err == nil {
		t.Errorf("裁尾留了个全长前缀竟被收了 —— 那它没被裁")
	}
}

// ⑮ 只有协议是整段的。通知**可以**被裁尾 —— 这不是放宽，是照着证据写的：
// 框架装配那一句通知时走的就是通用那把裁刀，裁过之后另记一格「通知被裁了」。
// 早先这里把通知也写成整段的，于是「通知被裁尾」这个真形状在内核里无处可放，
// 而本包自己的结构体上就带着一格 Trimmed。
func TestOnlySystemIsWholeOrNothing(t *testing.T) {
	m := validInput(t)
	m.Cuts[0] = Cut{Kind: KindSystem, Ref: "system", Role: RoleSystem, Squeeze: SqueezeTailDropped,
		FromBytes: 500, ToBytes: 100, SHA256: sha("z")}
	if err := m.Check(); err == nil {
		t.Errorf("协议被裁尾竟被收了 —— 它该是整段发或整段不发")
	}
	if KindSystem.MayShrink() {
		t.Errorf("协议不该在可裁名单里")
	}
	// 通知被裁尾必须能被收下，且它的逐项留痕（原来多大、留下多大、拿什么核回原文）不许丢。
	m = validInput(t)
	m.Cuts = append(m.Cuts, Cut{Kind: KindNotice, Ref: "truncation-notice", Role: RoleUser,
		Squeeze: SqueezeTailDropped, FromBytes: 200, ToBytes: 60, SHA256: sha("那句通知的原文")})
	m.Notice.Trimmed = true
	if err := m.Check(); err != nil {
		t.Errorf("通知被裁尾是真形状，该被收（且留痕要逐项）：%v", err)
	}
	if !KindNotice.MayShrink() {
		t.Errorf("通知该在可裁名单里")
	}
	if n := len(m.Clipped()); n != 1 {
		t.Errorf("裁尾那一档该有 1 条，得到 %d", n)
	}
	for _, k := range []PartKind{KindSkill, KindToolSpec} {
		if k.MayShrink() {
			t.Errorf("%s 不该在可裁名单里（框架压根不会把它当段发）", k)
		}
	}
	for _, k := range []PartKind{KindObserved, KindHistory, KindMemory, KindUser, KindNotice} {
		if !k.MayShrink() {
			t.Errorf("%s 该在可裁名单里", k)
		}
	}
}

// ⑯ 裁剪记录没有 ref、没有说话人、或指纹不完整 ⇒ 事后核不回原文，等于没留痕。
func TestCutNeedsRefRoleAndAFullFingerprint(t *testing.T) {
	m := validInput(t)
	m.Cuts[0].Ref = "   "
	if err := m.Check(); err == nil {
		t.Errorf("没有 ref 竟被收了 —— 事后谁也说不清被挤掉的是哪一段")
	}
	m = validInput(t)
	m.Cuts[0].SHA256 = sha("被折掉的那一段原文")[:12]
	if err := m.Check(); err == nil {
		t.Errorf("12 位指纹竟被收了 —— 拿它核不回原文")
	} else if !strings.Contains(err.Error(), "64") {
		t.Errorf("那句话没说该是几位：%v", err)
	}
	m = validInput(t)
	m.Cuts[0].SHA256 = strings.Repeat("z", 64)
	if err := m.Check(); err == nil {
		t.Errorf("64 个 z 不是十六进制，竟被收了")
	}
	m = validInput(t)
	m.Cuts[0].FromBytes = 0
	if err := m.Check(); err == nil {
		t.Errorf("折一个 0 字节的段竟被收了 —— 0 字节的段无从折起")
	}
	// 说话人：被裁的段丢了说话人，事后就分不清裁掉的是模型自述还是人的指令。
	// 这一格背后是一次真事故：裁过的段一律被现搭成 user 说的话。
	m = validInput(t)
	m.Cuts[0].Role = Role("")
	if err := m.Check(); err == nil {
		t.Errorf("没有说话人竟被收了")
	} else if !strings.Contains(err.Error(), "说话人") {
		t.Errorf("那句话没说清缺的是哪一格：%v", err)
	}
	m = validInput(t)
	m.Cuts[0].Role = Role("narrator")
	if err := m.Check(); err == nil {
		t.Errorf("陌生的说话人 narrator 竟被收了")
	}
	for _, r := range []Role{RoleSystem, RoleUser, RoleAssistant, RoleTool} {
		if !r.Known() {
			t.Errorf("%s 该在四种说话人里", r)
		}
		m = validInput(t)
		m.Cuts[0].Role = r
		if err := m.Check(); err != nil {
			t.Errorf("说话人 %s 该被收：%v", r, err)
		}
	}
}

// ⑰ squeeze 只许四种取值。第五种是证据源注释里写着、代码从不产出的 head-kept ——
// 一个谁也填不进去的取值就是死格，而一个被填进来的陌生取值是一个新形状正在被当成旧的读。
func TestUnknownSqueezeIsRefused(t *testing.T) {
	m := validInput(t)
	m.Cuts[0].Squeeze = Squeeze("head-kept")
	if err := m.Check(); err == nil {
		t.Errorf("陌生的 squeeze 取值竟被收了")
	} else if !strings.Contains(err.Error(), "head-kept") {
		t.Errorf("那句话没把它收到的值说出来：%v", err)
	}
	m = validInput(t)
	m.Cuts[0].Squeeze = SqueezeNone
	if err := m.Check(); err == nil {
		t.Errorf("没被挤的段出现在裁剪表里竟被收了")
	}
	// 四种合法取值都要能过。
	for _, s := range []Squeeze{SqueezeFolded, SqueezeTailDropped, SqueezeDroppedWhole, SqueezeBudgetExhausted} {
		m = validInput(t)
		m.Cuts[0].Squeeze = s
		switch s {
		case SqueezeTailDropped:
			m.Cuts[0].ToBytes = 1200
		case SqueezeFolded:
		default:
			m.Cuts[0].ToBytes = 0
		}
		if err := m.Check(); err != nil {
			t.Errorf("%s 是四种合法取值之一，该被收：%v", s, err)
		}
	}
}

// ⑱ 那张分档表自己也要被核：除「没被挤」之外，每个取值恰好落在一档里。
// 落两档 ⇒ 同一段被数两遍；一档都没落 ⇒ 它发生了也不会在账上出现。
func TestCutListsCoverEverySqueezeExactlyOnce(t *testing.T) {
	if err := CheckCutLists(); err != nil {
		t.Fatalf("分档表自己不合法：%v", err)
	}
	covered := map[Squeeze]int{}
	for _, l := range CutLists {
		for _, s := range l.Has {
			covered[s]++
		}
	}
	for _, s := range []Squeeze{SqueezeFolded, SqueezeTailDropped, SqueezeDroppedWhole, SqueezeBudgetExhausted} {
		if covered[s] != 1 {
			t.Errorf("%s 落了 %d 档，该正好 1 档", s, covered[s])
		}
	}
	if covered[SqueezeNone] != 0 {
		t.Errorf("「没被挤」不该落在任何一档里")
	}
	// 拼错档名不许静静返回一个空名单 —— 那会被读成「这一档没发生」。
	m := validInput(t)
	if got := m.CutList("folding"); got != nil {
		t.Errorf("拼错的档名该返回 nil，得到 %v", got)
	}
	if got := m.CutList("folded"); len(got) != 1 {
		t.Errorf("正确的档名该取到 1 条，得到 %d", len(got))
	}
}

// ⑲ 裁过了却不告诉模型，也不说为什么不告诉 ⇒ 拒收。
// 对应「通知只写进账本、没真发出去」那一族：收件人没收到就是零。
func TestCutsWithoutNoticeMustSayWhy(t *testing.T) {
	m := validInput(t)
	m.Notice = Notice{}
	m.Parts[4].Parts, m.Parts[4].Bytes = 0, 0
	m.Parts = m.Parts[:4]
	m.Order = m.Order[:5]
	m.ContentBytes -= 60
	if err := m.Check(); err == nil {
		t.Errorf("挤掉了一段却没发通知、也没说为什么，竟被收了")
	} else if !strings.Contains(err.Error(), "1 段") {
		t.Errorf("那句话没带分母（挤了几段）：%v", err)
	}
	m = validInput(t)
	m.Notice = Notice{Why: "这一发的份额装不下那句通知"}
	m.Parts = m.Parts[:4]
	m.Order = m.Order[:5]
	m.ContentBytes -= 60
	if err := m.Check(); err != nil {
		t.Errorf("说清了为什么没发，就该收：%v", err)
	}
	m = validInput(t)
	m.Cuts = nil
	m.Notice = Notice{}
	m.Parts = m.Parts[:4]
	m.Order = m.Order[:5]
	m.ContentBytes -= 60
	if err := m.Check(); err != nil {
		t.Errorf("什么都没挤，不发通知是对的：%v", err)
	}
}

// ⑳ 通知那一格的三种说法不许互相矛盾，也不许「说了却没有正文」。
func TestNoticeCannotContradictItself(t *testing.T) {
	m := validInput(t)
	m.Notice = Notice{Sent: false, Trimmed: true, Why: "装不下"}
	if err := m.Check(); err == nil {
		t.Errorf("没发出去又同时被裁了半句，竟被收了")
	}
	m = validInput(t)
	m.Notice = Notice{Sent: true, Trimmed: true, Text: "本节被裁"}
	if err := m.Check(); err != nil {
		t.Errorf("发了半句通知是可能的形状，该收：%v", err)
	}
	// 说了却没有正文：账上会留下一格「通知已送达」，而收件人收到的是空。
	m = validInput(t)
	m.Notice = Notice{Sent: true, Text: "   "}
	if err := m.Check(); err == nil {
		t.Errorf("通知报「发出去了」却没有正文，竟被收了")
	}
}

// ㉑ token 那一格只能是估算或转述：分词器在对端，我们数不出来。
func TestTokensCannotClaimToBeMeasured(t *testing.T) {
	m := validInput(t)
	m.Tokens = MeasuredOf(1600)
	if err := m.Check(); err == nil {
		t.Errorf("token 记成实测竟被收了 —— 我们并没有对端的分词器")
	}
	m = validInput(t)
	m.Tokens = Measure{N: 1600, Basis: BasisModelSays}
	if err := m.Check(); err == nil {
		t.Errorf("token 记成模型自述竟被收了")
	}
	m = validInput(t)
	m.Tokens = AbsentBecause("这一发没估")
	if err := m.Check(); err != nil {
		t.Errorf("缺席该收：%v", err)
	}
	m = validInput(t)
	m.Tokens = RelayedOf(1587, "endpoint")
	if err := m.Check(); err != nil {
		t.Errorf("对端回 usage 时，转述该收：%v", err)
	}
}

// ㉒ 三类挤压是三张分开的名单，刻意没有「一共挤了几段」那个总数。
func TestThreeSqueezeListsStaySeparate(t *testing.T) {
	m := validInput(t)
	m.Call = 3 // 历史那条要指向第 2 发：第 1 发之前没有发可指
	m.Cuts = append(m.Cuts,
		Cut{Kind: KindHistory, Ref: "step2", RefCall: 2, Role: RoleAssistant, Squeeze: SqueezeTailDropped,
			FromBytes: 900, ToBytes: 400, SHA256: sha("a")},
		Cut{Kind: KindMemory, Ref: "mem", Role: RoleUser, Squeeze: SqueezeDroppedWhole,
			FromBytes: 700, ToBytes: 0, SHA256: sha("b")},
		Cut{Kind: KindObserved, Ref: "step9", Role: RoleTool, Squeeze: SqueezeBudgetExhausted,
			FromBytes: 600, ToBytes: 0, SHA256: sha("c")},
	)
	if err := m.Check(); err != nil {
		t.Fatalf("四条各自合法的裁剪记录，合起来该被收：%v", err)
	}
	if n := len(m.Folded()); n != 1 {
		t.Errorf("折过的该是 1 条，得到 %d", n)
	}
	if n := len(m.Clipped()); n != 1 {
		t.Errorf("裁尾的该是 1 条，得到 %d", n)
	}
	// 整段没进与预算花完合成一档是有意的：两者在模型那边同为「彻底看不见」，
	// 区别只在成因，而成因留在每条 Cut 自己的 Squeeze 里，没有丢。
	if n := len(m.Dropped()); n != 2 {
		t.Errorf("没进去的该是 2 条，得到 %d", n)
	}
	if len(m.Folded())+len(m.Clipped())+len(m.Dropped()) != len(m.Cuts) {
		t.Errorf("三档相加 %d 不等于裁剪总数 %d —— 有记录落在三档之外，或被数了两遍",
			len(m.Folded())+len(m.Clipped())+len(m.Dropped()), len(m.Cuts))
	}
	// 三档必须各自在键名表里有一格，且档名不许重（重了就又变回三格共用一个身份）。
	keys := map[string]bool{}
	for _, c := range m.Cells() {
		keys[c.Key] = true
	}
	for _, l := range CutLists {
		if !keys[l.Key] {
			t.Errorf("分档 %s（%s）在键名表里没有格子", l.Key, l.Label)
		}
	}
}

// ㉓ 裁剪表与段表要对得上：某一类段真发出去的字节，不可能比它那些裁剪记录里
// 「留下来」的字节还少。这一条盯的是「Parts 记的是真发出去的那一份」那句口径 ——
// 一句写在注释里的口径，如果没有任何判据把两张表对过，那它就是散文。
func TestCutsCannotOutliveTheirPartRow(t *testing.T) {
	m := validInput(t)
	m.Cuts[0].FromBytes, m.Cuts[0].ToBytes = 9000, 5000
	if err := m.Check(); err == nil {
		t.Errorf("观察段的裁剪记录说留下了 5000 字节，而段表说这一类一共才发了 4000，竟被收了")
	} else if !strings.Contains(err.Error(), "两张表对不上") {
		t.Errorf("那句话没说出这是两张表对不上：%v", err)
	}
	// 有留痕却在段表里没有那一行（且留下的字节非 0）。
	m = validInput(t)
	m.Cuts = append(m.Cuts, Cut{Kind: KindMemory, Ref: "mem", Role: RoleUser,
		Squeeze: SqueezeTailDropped, FromBytes: 900, ToBytes: 400, SHA256: sha("m")})
	if err := m.Check(); err == nil {
		t.Errorf("记忆段有裁剪记录、段表里却没它那一行，竟被收了")
	}
	// 反方向：留下的字节为 0（整段没进）时，段表里没有那一行是对的。
	m = validInput(t)
	m.Cuts = append(m.Cuts, Cut{Kind: KindMemory, Ref: "mem", Role: RoleUser,
		Squeeze: SqueezeDroppedWhole, FromBytes: 900, ToBytes: 0, SHA256: sha("m")})
	if err := m.Check(); err != nil {
		t.Errorf("整段没进、段表里没那一行是对的：%v", err)
	}
}

// ㉔ 发号从 1 起：0 会被读成「还没发」。
func TestCallNumberStartsAtOne(t *testing.T) {
	m := validInput(t)
	m.Call = 0
	if err := m.Check(); err == nil {
		t.Errorf("第 0 发竟被收了")
	}
	m.Call = 1
	if err := m.Check(); err != nil {
		t.Errorf("第 1 发该收：%v", err)
	}
}

// TestNoticeSentIsPinnedToThePartTable 判的是④：「通知发出去了」必须钉在真发出去的段表上。
// 实例不是设想：驱动的折叠逻辑排在附通知之前，于是有一发 Notice.Sent=true 而段里没有通知，
// 内核当时整发照收（报告/挤历史-判生死.md 第 7 节）。
func TestNoticeSentIsPinnedToThePartTable(t *testing.T) {
	m := validInput(t)
	if err := m.Check(); err != nil {
		t.Fatalf("夹具自己就该合法：%v", err)
	}
	// 反一：说发了，段表里却没有那一段 —— 这才是这一条要挡的形状。
	m.Notice.Sent = true
	m.Notice.Text = "本节被裁过：观察 1 段折成了桩"
	m.Order = []PartKind{KindSystem, KindUser, KindHistory, KindObserved, KindObserved}
	m.Parts = []PartCount{
		{Kind: KindSystem, Parts: 1, Bytes: 500},
		{Kind: KindUser, Parts: 1, Bytes: 1200},
		{Kind: KindObserved, Parts: 2, Bytes: 4000},
		{Kind: KindHistory, Parts: 1, Bytes: 800},
	}
	m.ContentBytes = 6500
	if err := m.Check(); err == nil {
		t.Errorf("Notice.Sent=true 而段表里没有 truncation-notice，竟被收了 —— 发不出去的东西不算通知过")
	}
	// 反二：段表里有那一段，通知却报「没发出去」。
	m = validInput(t)
	m.Notice.Sent = false
	m.Notice.Text = ""
	m.Notice.Why = "这一发挤掉了 1 段，但通知没发出去（我们自己决定不通知）"
	if err := m.Check(); err == nil {
		t.Errorf("段表里明明有 truncation-notice 那一段，通知却说不发，竟被收了")
	}
	// 正：没挤过的那些发，通知与段表都干净，必须收 —— 否则这一条会误伤全场。
	m = validInput(t)
	m.Notice = Notice{Sent: false, Why: "这一发没折叠、没裁尾、没整段丢弃"}
	m.Order = []PartKind{KindSystem, KindUser, KindHistory, KindObserved, KindObserved}
	m.Parts = []PartCount{
		{Kind: KindSystem, Parts: 1, Bytes: 500},
		{Kind: KindUser, Parts: 1, Bytes: 1200},
		{Kind: KindObserved, Parts: 2, Bytes: 4000},
		{Kind: KindHistory, Parts: 1, Bytes: 800},
	}
	m.ContentBytes = 6500
	m.Cuts = nil
	if err := m.Check(); err != nil {
		t.Errorf("没挤过的正常一发不该被这一条误伤：%v", err)
	}
}

// TestHistoryCutMustNameItsCall 判的是⑤：被挤掉的历史段必须说得出它来自第几发。
// 三种形状：没指（0）、指向未来（>=当前发）、指向过去且合法（必须收，不误伤）。
func TestHistoryCutMustNameItsCall(t *testing.T) {
	m := validInput(t)
	m.Call = 3
	hist := Cut{Kind: KindHistory, Ref: "第 2 发模型那句回答", Role: RoleAssistant,
		Squeeze: SqueezeTailDropped, FromBytes: 800, ToBytes: 300, SHA256: sha("那句原话")}
	m.Cuts = []Cut{hist}
	if err := m.Check(); err == nil {
		t.Errorf("挤掉一段历史却没说来自第几发（RefCall=0），竟被收了")
	}
	hist.RefCall = 5
	m.Cuts = []Cut{hist}
	if err := m.Check(); err == nil {
		t.Errorf("第 3 发的裁剪记录指向第 5 发（未来），竟被收了")
	}
	hist.RefCall = 2
	m.Cuts = []Cut{hist}
	if err := m.Check(); err != nil {
		t.Errorf("指向第 2 发（在它之前）的合法引用不该被这一条挡：%v", err)
	}
	// 非历史段不受这条约束：观察段折桩本来就没有"第几发"的来源。
	m.Cuts = []Cut{{Kind: KindObserved, Ref: "step3:read_file:docs/a.md", Role: RoleTool,
		Squeeze: SqueezeFolded, FromBytes: 5000, ToBytes: 124, SHA256: sha("被折掉的那一段原文")}}
	if err := m.Check(); err != nil {
		t.Errorf("观察段的裁剪记录不该被这条误伤：%v", err)
	}
}
