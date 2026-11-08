package kernel

// review_fix_kernel_test.go 把 M2 内核侧审查留下的两条「口径待定」钉成有形状的判据，
// 不再靠读代码口头解释：一条是真会拒的，一条是设计上等价的两写法。

import (
	"strings"
	"testing"
)

// ① 手写的一条**链行**里带了顶层 `gate` 键 ⇒ 不许"静默掉出 Docs"。
// 审查员点的是：这一行会被当成计数栏，链上就少了一条真账，而读的人可能毫无察觉。
// 现在钉住它的实际形状：要么封条/位置红（拒载），要么双行红 —— 总之不可能无声。
func TestDocLineWithGateKeyCannotVanishSilently(t *testing.T) {
	docs := journalFixture(t).Docs()
	b, err := MarshalJournalWithGate(docs, PayloadIncluded, sampleGate())
	if err != nil {
		t.Fatalf("编码被拒：%v", err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	// 把第一条链行改造成"带顶层 gate 键"的样子（合法 JSON，键名撞上探测）
	malformed := `{"seq":1,"session":"sess-j","actor":"assembler","kind":"x","gate":{"rejected":1},"chain_head":"",` +
		`"gate_sha256":"deadbeef","gate_version":2}`
	replaced := append([]string{malformed}, lines[1:]...)
	j, _ := ReadJournal([]byte(strings.Join(replaced, "\n") + "\n"))
	// 审查员点的现象是真的：那一行确实**掉出了 Docs**（探测只认顶层 `gate` 键，不看内容）。
	// 所以要钉的是后半句 —— 它**不能无声**：`LoadLedger` 必须拒载，拒因要点名是计数栏那一栏。
	if j.Parsed >= len(docs) {
		t.Fatalf("那一行没掉出 Docs（Parsed=%d，写出去 %d 条）—— 题面前提变了，这条测试要重写", j.Parsed, len(docs))
	}
	_, j2, _, err := LoadLedger([]byte(strings.Join(replaced, "\n") + "\n"))
	if err == nil {
		t.Fatal("带顶层 gate 键的账行既少了一条真账、又没人报错 —— 这正是「静默掉出」")
	}
	if !strings.Contains(err.Error(), "计数") && j2.GateProblem == "" {
		t.Errorf("拒因应当点名是计数栏坏了（而不是让人去查链）：%v ｜ GateProblem=%q", err, j2.GateProblem)
	}
	if j2.HasGate {
		t.Error("被劫持的那一行居然还算作有效计数栏")
	}
}

// ② 空链时 `chain_head` 省略与写成空串**同义**，这是设计；
// 反臂：链非空时省略 `chain_head` 必须红 —— 否则就是"把计数接到一本空账上"的通路。
func TestMissingChainHeadEqualsEmptyOnlyOnEmptyChain(t *testing.T) {
	g := sampleGate()
	bEmpty, err := MarshalJournalWithGate(nil, PayloadOmitted, g)
	if err != nil {
		t.Fatalf("空链编码被拒：%v", err)
	}
	line := strings.TrimRight(string(bEmpty), "\n")
	omitted := strings.Replace(line, `"chain_head":"",`, ``, 1)
	if omitted == line {
		t.Fatal("夹具那行里找不到 chain_head，下面两条判据都是空的")
	}
	if _, j, _, err := LoadLedger([]byte(omitted)); err != nil {
		t.Errorf("空链上省略 chain_head 与写空串应当同义，却拒了：%v", err)
	} else if !j.HasGate {
		t.Errorf("空链省略后 HasGate=false —— 那封条到底盖的是谁？")
	}

	// 链非空：把 chain_head 抹掉 ⇒ 必须红（它接的是"空链尾"，与这本账的链尾不符）。
	docs := journalFixture(t).Docs()
	bReal, err := MarshalJournalWithGate(docs, PayloadIncluded, g)
	if err != nil {
		t.Fatalf("编码被拒：%v", err)
	}
	dropFromReal := strings.Replace(strings.TrimRight(string(bReal), "\n"), `"chain_head":"`+
		strings.Trim(chainHeadOf(docs), `"`)+`",`, ``, 1)
	if _, _, _, err := LoadLedger([]byte(dropFromReal)); err == nil ||
		!strings.Contains(err.Error(), "链尾") && !strings.Contains(err.Error(), "封条") {
		t.Errorf("链非空时省略 chain_head 竟被收下，或拒因没点名是链尾/封条：%v", err)
	}
}
