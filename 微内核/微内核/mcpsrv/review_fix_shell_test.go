package mcpsrv

// review_fix_shell_test.go 钉住 M2 外壳侧留下的三处：
// ① 口径必须是**六笔合起来**那一份（只有门口拒收的进程不许说"不可知"）；
// ② 那道新闸对**带计数栏**的文件不许谎称"变成不可知"；
// ③ 账本容量这道闸：设了才拦、默认不拦、拒收有数且与"整块没给"分得开。

import (
	"encoding/json"
	"strings"
	"testing"

	"agentkernel"
)

func TestAggregateGateStateCoversShellCounters(t *testing.T) {
	s := New()
	// 起点：从一份**没有计数栏**的文件读回来 ⇒ 内核那两格是 0/不可知。
	plain, err := kernel.MarshalJournal(nil, kernel.PayloadOmitted)
	if err != nil {
		t.Fatalf("编码被拒：%v", err)
	}
	body, _ := json.Marshal(map[string]any{"text": string(plain)})
	if got, isErr := toolText(t, s, 1, "kernel.load_journal", string(body)); isErr {
		t.Fatalf("空账载入被拒：%s", got)
	}
	// 只走**门口**那一路（整块没给）：内核判据一次都没拒，所以 Rejected/PrecheckRejected 都是 0。
	for i := 0; i < 3; i++ {
		if _, isErr := toolText(t, s, 10+i, "kernel.check_call", legalCall(t, "input", "Notice")); !isErr {
			t.Fatal("Notice 整块没给竟被收下")
		}
	}
	txt := tallyOf(t, s)
	if strings.Contains(txt, "这几笔数合起来的口径：不可知") {
		t.Errorf("门口数到 3 笔却报「不可知」—— 那就是把本程实数到的东西白扔掉：%s", txt)
	}
	if !strings.Contains(txt, "只覆盖这一程") {
		t.Errorf("口径应当是下界（只覆盖这一程），得到：%s", txt)
	}
	if n := grabCount(t, txt, "预检在门口被挡下"); n != 3 {
		t.Errorf("门口那一栏没数到 3：%d", n)
	}
}

func TestNewGateDoesNotLieAboutCountersWhenFileCarriesThem(t *testing.T) {
	s := New()
	for i := 0; i < 2; i++ {
		if _, isErr := toolText(t, s, 10+i, "kernel.check_call", badContentBytes(t)); !isErr {
			t.Fatal("该拒的没拒")
		}
	}
	if _, isErr := toolText(t, s, 40, "kernel.record_call", legalCall(t, "", "")); isErr {
		t.Fatalf("落账被拒：%v", isErr)
	}
	enc, isErr := toolText(t, s, 41, "kernel.encode_journal", `{"payload":"omitted"}`)
	if isErr {
		t.Fatalf("编码被拒：%s", enc)
	}
	// 链上已有一条 ⇒ 不带 acknowledge 必须被拦（这是**保护链**，不是保护计数）。
	bodyNoAck, _ := json.Marshal(map[string]any{"text": enc})
	got, isErr := toolText(t, s, 42, "kernel.load_journal", string(bodyNoAck))
	if !isErr {
		t.Fatalf("链上有条还能无痕替换，那道闸退化了：%s", got)
	}
	if !strings.Contains(got, "带着计数栏") || !strings.Contains(got, "不会丢") {
		t.Errorf("手里这份明明带着计数栏，拒因却该说清「只有链上那些条会换掉」：%s", got)
	}
	if !strings.Contains(got, "拒预检 2") {
		t.Errorf("拒因该逐栏列出本程的数（不许相加成一个数）：%s", got)
	}
	// 点名之后就放行，六笔数原样回来。
	bodyAck, _ := json.Marshal(map[string]any{"text": enc, "acknowledge": "replace"})
	if got2, isErr2 := toolText(t, s, 43, "kernel.load_journal", string(bodyAck)); isErr2 {
		t.Fatalf("点名替换后被拒：%s", got2)
	}
	if n := grabCount(t, tallyOf(t, s), "被内核判据拒"); n != 2 {
		t.Errorf("带计数栏的文件载入后那一栏变成 %d（应当原样是 2）", n)
	}
}

// A3 的结构性缓解：只增模式下服务面不提供换账本这条路（`load_journal` 一律拒），
// 但 `encode_journal`（交字节出去归档）照旧可用 —— 否则"先归档再换账"这条唯一正路也被堵死。
func TestAppendOnlyRefusesLoadButKeepsEncode(t *testing.T) {
	// 反臂（现状）：默认关 ⇒ load 照旧能用。
	plain := New()
	encDefault, isErr := toolText(t, plain, 1, "kernel.encode_journal", `{"payload":"omitted"}`)
	if isErr {
		t.Fatalf("编码被拒：%s", encDefault)
	}
	body, _ := json.Marshal(map[string]any{"text": encDefault})
	if got, e2 := toolText(t, plain, 2, "kernel.load_journal", string(body)); e2 {
		t.Fatalf("默认（只增关着）时载入被拒，现状漂了：%s", got)
	}

	// 证红：开了只增 ⇒ 载入拒、链上一条没动、门口那一桶计上一笔，且编码仍可用。
	ao := New()
	ao.SetAppendOnly(true)
	if got, e2 := toolText(t, ao, 3, "kernel.record_call", legalCall(t, "", "")); e2 {
		t.Fatalf("只增不许影响正常落账：%s", got)
	}
	got, isErr := toolText(t, ao, 4, "kernel.load_journal", string(body))
	if !isErr || !strings.Contains(got, "只增") {
		t.Errorf("只增开着却提供了换账本这条路：%s", got)
	}
	if e := len(ao.ledger.Docs()); e != 49 {
		t.Errorf("被拒的载入动了链（现有 %d 条，应当还是那一发的 49 条）", e)
	}
	tr := tallyOf(t, ao)
	if n := grabCount(t, tr, "另有"); n != 1 {
		t.Errorf("只增挡下的这一笔没进门口那一桶（拒了必须看得见）：%d ｜ %s", n, tr)
	}
	if enc2, e3 := toolText(t, ao, 5, "kernel.encode_journal", `{"payload":"included"}`); e3 ||
		!strings.Contains(enc2, "行链上条目") {
		t.Errorf("只增模式下归档这条路必须还在：%s", firstLine(enc2))
	}
}

func TestEntryCapRefusesAndCountsSeparately(t *testing.T) {
	capped := New()
	capped.SetEntryCap(49) // 一发恰好 49 条 ⇒ 第二发就该被挡
	if _, isErr := toolText(t, capped, 1, "kernel.record_call", legalCall(t, "", "")); isErr {
		t.Fatalf("第一发被拒（夹具没落账，后面没得判）")
	}
	txt, isErr := toolText(t, capped, 2, "kernel.record_call", legalCall(t, "", ""))
	if !isErr {
		t.Fatalf("已到上限 49 条还能再写：容量这道闸没生效（%s）", firstLine(txt))
	}
	if !strings.Contains(txt, "上限") || strings.Contains(txt, "内核") && strings.Contains(txt, "账不自洽") {
		t.Errorf("拒因要写清是外壳按容量挡的，不是内核判据拒的：%s", txt)
	}
	if e := len(capped.ledger.Docs()); e != 49 {
		t.Errorf("被容量挡下却多写了 %d 条（应当一条都不写）", e)
	}
	tr := tallyOf(t, capped)
	if n := grabCount(t, tr, "其中容量挡下"); n != 1 {
		t.Errorf("容量挡下那一笔没单独数出来（子集披露）：%d ｜ %s", n, tr)
	}

	// 反臂一：默认（0）不拦。
	open := New()
	if _, isErr := toolText(t, open, 1, "kernel.record_call", legalCall(t, "", "")); isErr {
		t.Fatal("默认上限为 0 却挡下了第一发 —— 「0 = 不拦」被读成了「0 = 全拦」")
	}
	if strings.Contains(tallyOf(t, open), "上限 0 条") {
		t.Error("横幅与 tally 要把 0 说成「没设」，不能印成「上限 0 条」（那是另一种假话）")
	}
	// 反臂二：一份比上限还长的文件不许被换进来。
	over := New()
	over.SetEntryCap(49)
	big := New()
	for i := 0; i < 3; i++ {
		p := legalCall(t, "", "")
		var m map[string]any
		if err := json.Unmarshal([]byte(p), &m); err != nil {
			t.Fatal(err)
		}
		m["session"] = "over-cap"
		m["input"].(map[string]any)["Call"] = 50 + i
		m["output"].(map[string]any)["Call"] = 50 + i
		raw, _ := json.Marshal(m)
		if _, isErr := toolText(t, big, 60+i, "kernel.record_call", string(raw)); isErr {
			t.Fatalf("夹具第 %d 发落账被拒", i+1)
		}
	}
	encBig, _ := toolText(t, big, 70, "kernel.encode_journal", `{"payload":"omitted"}`)
	body, _ := json.Marshal(map[string]any{"text": encBig})
	got, isErr := toolText(t, over, 71, "kernel.load_journal", string(body))
	if !isErr || !strings.Contains(got, "超过这本账的上限") {
		t.Errorf("比上限长的文件被换进来了（载入是替换，换进来就压不回去）：%s", got)
	}
}
