package kernel

import (
	"errors"
	"strings"
	"testing"
)

// 每一条判据都配两头：不填/填错必须红，正常填必须绿。
// 只有后一头的测试等于"永远报警也算通过"。

func ok(t *testing.T, r Record) Entry {
	t.Helper()
	e, err := NewLedger().Append(r)
	if err != nil {
		t.Fatalf("这条该收：%v", err)
	}
	return e
}

// ① 漏填 basis ⇒ 拒收，且拒收看得见；填了 ⇒ 收。
func TestMissingBasisIsRefusedAndCounted(t *testing.T) {
	l := NewLedger()
	if _, err := l.Append(Record{Actor: "a", Kind: "claim", Claim: "覆盖 232/232"}); !errors.Is(err, ErrNoBasis) {
		t.Fatalf("没填 basis 必须被拒，得到 err=%v", err)
	}
	if l.Rejected() != 1 {
		t.Errorf("被拒也要计数（否则将来读成「没发生」），得到 %d", l.Rejected())
	}
	if n := len(l.Docs()); n != 0 {
		t.Errorf("被拒的写入不许留下半条，账上有 %d 条", n)
	}
	// 反向：正常填必须进得来，且不是靠默认值。
	if _, err := l.Append(Record{Actor: "a", Kind: "claim", Claim: "覆盖 232/232", Basis: BasisMeasured}); err != nil {
		t.Fatalf("填了 measured 该收：%v", err)
	}
	if l.Rejected() != 1 || len(l.Docs()) != 1 {
		t.Errorf("两头之后应是 1 收 1 拒，得到 收 %d 拒 %d", len(l.Docs()), l.Rejected())
	}
}

// ② 转述/估算必须带出处；带了才收。
func TestRelayAndEstimateNeedSource(t *testing.T) {
	for _, b := range []Basis{BasisRelayed, BasisEstimated} {
		if _, err := NewLedger().Append(Record{Actor: "g", Kind: "verdict", Basis: b}); err == nil {
			t.Errorf("basis=%s 不带 source 竟被收了", b)
		}
		e := ok(t, Record{Actor: "g", Kind: "verdict", Basis: b, Source: "grader:verify.py"})
		if e.Source == "" {
			t.Errorf("basis=%s 的出处没留在账上", b)
		}
	}
}

// ③ 链：改一格必须被发现；什么都不改必须过。
func TestChainRecomputesAndCatchesTampering(t *testing.T) {
	l := NewLedger()
	for i, r := range []Record{
		{Actor: "a", Kind: "claim", Basis: BasisMeasured},
		{Actor: "b", Kind: "claim", Basis: BasisModelSays, Claim: "我说我列全了"},
		{Actor: "k", Kind: "verdict", Basis: BasisRelayed, Source: "grader", Claim: "fail 漏报 4/15"},
	} {
		r.Step = i + 1
		if _, err := l.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Verify(); err != nil {
		t.Fatalf("没动过的账必须能重算通过：%v", err)
	}

	// 三处独立破坏，逐个都必须红（一处不漏，才叫"看着它变红"）。
	cases := []struct {
		name string
		poke func(*Ledger)
		want string
	}{
		{"改正文", func(x *Ledger) { x.docs[1].Claim = "我说我一条没漏" }, "整格指纹重算不符"},
		{"换 payload", func(x *Ledger) { x.docs[2].Payload = []byte("改了") }, "payload 与它自己记的指纹不符"},
		// 抽走中间一条：序号立刻接不上 —— 这是它该有的第一句（比"prev 对不上"更准，别把断言写成后者）
		{"抽走中间一条", func(x *Ledger) { x.docs = append(x.docs[:1], x.docs[2:]...) }, "不连续"},
		// 只把某条的 prev 改错（序号与内容都自洽）：这一格才轮到 prev 链说话
		{"重接前驱", func(x *Ledger) { x.docs[2].PrevHash = x.docs[0].Hash }, "prev_hash 对不上"},
	}
	for _, c := range cases {
		cp := NewLedger()
		cp.docs = l.Docs()
		c.poke(cp)
		err := cp.Verify()
		if err == nil {
			t.Errorf("破坏『%s』之后 Verify 竟然过了 —— 这条判据是空针", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("破坏『%s』之后报的不是那一格（想要 %q，得到 %q）", c.name, c.want, err)
		}
	}
}

// ④ 分桶计数：同一条记录不能同时进两个桶（那正是"红绿共用一个 +1"的形状）。
func TestTallyBucketsAreExclusive(t *testing.T) {
	l := NewLedger()
	for _, r := range []Record{
		{Actor: "a", Kind: "c", Basis: BasisMeasured},
		{Actor: "a", Kind: "c", Basis: BasisMeasured},
		{Actor: "b", Kind: "c", Basis: BasisModelSays},
	} {
		if _, err := l.Append(r); err != nil {
			t.Fatal(err)
		}
	}
	tt := l.Tally()
	if tt.Total != 3 || tt.ByBasis["measured"] != 2 || tt.ByBasis["model_says"] != 1 {
		t.Fatalf("分桶错了：%+v", tt)
	}
	sum := 0
	for _, v := range tt.ByBasis {
		sum += v
	}
	if sum != tt.Total {
		t.Errorf("各桶相加应等于总数（有记录被数了两遍或没数）：%d vs %d", sum, tt.Total)
	}
	if _, bad := tt.ByBasis["unset"]; bad {
		t.Errorf("unset 不该出现在账里（它应当在门口被拒）")
	}
}

// ⑤ 拿出去的是副本 —— 调用方改了返回值不能污染账（否则链就白算了）。
func TestDocsReturnsCopy(t *testing.T) {
	l := NewLedger()
	if _, err := l.Append(Record{Actor: "a", Kind: "c", Claim: "原话", Basis: BasisMeasured}); err != nil {
		t.Fatal(err)
	}
	l2 := NewLedger()
	l2.docs = l.Docs()
	if len(l2.docs) != 1 {
		t.Fatalf("取回来的该是 1 条，得到 %d —— 那这条测试什么都没测", len(l2.docs))
	}
	l2.docs[0].Claim = "被外面改了"
	if got := l.Docs()[0].Claim; got != "原话" {
		t.Errorf("Docs() 交出去的是活引用，外面的改动画进了账里：%q", got)
	}
}
