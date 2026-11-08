package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// E2 要证的几件事，一件一支：
//   每一发的第一条恒为 turn_accepted（先回执再干活 —— 落点在装配/驱动/内核调用之前）；
//   回执带得出载荷：余量、上限、停止入口、能不能续；
//   ack_ms 是从「请求入口那一刻」到落盘的**真实延迟**，不是恒 0 的装饰；
//   resumable 写成 false 时必须给为什么，且这条流的任何一处不许出现没兑现的「可续」声称；
//   判据是可达的：坏值从公开路径（读回来的整本日志）喂进去就能红。

// rawAccept 造一条**盘上形状**的回执：字段名、指针、omitempty 都按 JSON 走一遍。
// 早先的 mkAccept 是往内存切片里 append Event 结构体 —— 那样六族坏值一条都量不到序列化层，
// 而 M2 路 B 的 P0-1 与 P1-1 正好都藏在序列化这一族（纪律退回一步，这次还债）。
func rawAccept(seq int, mut map[string]any) map[string]any {
	f := map[string]any{
		"v": 1, "session": "arm", "turn": 1, "seq": seq, "ts": "2026-10-22T00:00:00Z",
		"kind": "turn_accepted", "state": "running", "evidence": "measured",
		"elapsed_ms": 0, "why": "断开不能续：没做 SSE",
		"accept": map[string]any{"planned_shots": 3, "turn_limit": 20, "stop_via": "signal:SIGINT",
			"resumable": false, "ack_ms": 5},
	}
	for k, v := range mut {
		if v == nil {
			delete(f, k) // 「那一格根本不在盘上」这种坏法要能造出来：nil 不是「填了 null」
			continue
		}
		if k == "accept" {
			acc := map[string]any{}
			for kk, vv := range f["accept"].(map[string]any) {
				acc[kk] = vv
			}
			for kk, vv := range v.(map[string]any) {
				acc[kk] = vv
			}
			f["accept"] = acc
			continue
		}
		f[k] = v
	}
	return f
}

func rawShot(seq int) map[string]any {
	return map[string]any{"v": 1, "session": "arm", "turn": 1, "seq": seq, "ts": "2026-10-22T00:00:00Z",
		"kind": "shot_started", "state": "running", "evidence": "measured", "elapsed_ms": 0,
		"step": map[string]any{"no": 1, "name": "assemble"}}
}

// writeRawJudgeable 把 map 序列化成 JSONL 落盘，再读回来喂判据 —— 中间那一趟序列化是目的不是路过。
func writeRawJudgeable(t *testing.T, name string, rows ...map[string]any) []Event {
	t.Helper()
	var b strings.Builder
	for _, row := range rows {
		line, err := json.Marshal(row)
		if err != nil {
			t.Fatalf("序列化坏值：%v", err)
		}
		b.Write(append(line, 10))
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("写坏日志：%v", err)
	}
	return mustReadEvents(t, path)
}

func TestTurnAcceptedLeadsEveryTurn(t *testing.T) {
	dir := t.TempDir()
	r := newRun(t, "full", true, "included", 20)
	path := filepath.Join(dir, "acc.events.jsonl")
	r.journalPath = filepath.Join(dir, "journal.jsonl")
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()

	evs := mustReadEvents(t, path)
	if err := ValidateLog(evs); err != nil {
		t.Fatalf("序号不合格：%v", err)
	}
	if err := ValidateTurnShape(evs); err != nil {
		t.Fatalf("轴混了：%v", err)
	}
	// 这一支要判的是顺序与载荷；ack 那一格给宽松限度（满载时绝对阈值会假红，见 ValidateAcceptsWithin）
	if err := ValidateAcceptsWithin(evs, 60000); err != nil {
		t.Fatalf("回执判据红：%v", err)
	}
	accepts, shots := 0, 0
	for _, e := range evs {
		switch e.Kind {
		case "turn_accepted":
			accepts++
		case "shot_started":
			shots++
		}
	}
	// 回执条数必须**等于**发数：一发可以先回执后失败，但不许有发没回执。
	// （原来写的是 accepts >= shots，被 ValidateAccepts 完全支配 = 零独立承载，M2 路 B 的 P2-1）
	if accepts != r.turns || accepts == 0 {
		t.Fatalf("回执 %d 条，进入循环体 %d 发 —— 对不上就是有的发没被受理", accepts, r.turns)
	}
	if accepts < shots {
		t.Fatalf("回执 %d 少于干活 %d —— 「没受理就干活」", accepts, shots)
	}
	if got := r.events.written; got != len(evs) {
		t.Fatalf("自印 %d 条，文件 %d 条", got, len(evs))
	}
}

func TestReceiptPayloadIsHonest(t *testing.T) {
	r := newRun(t, "happy", true, "included", 20)
	path := filepath.Join(t.TempDir(), "p.events.jsonl")
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()

	first := 0
	for _, e := range mustReadEvents(t, path) {
		if e.Kind != "turn_accepted" {
			continue
		}
		first++
		a := e.Accept
		if a == nil {
			t.Fatalf("回执没带载荷：%+v", e)
		}
		if a.StopVia == "" {
			t.Fatal("回执没说停止入口在哪 —— 界面拿着这条流没法喊停")
		}
		if a.Limit == nil || *a.Limit != r.maxTurns {
			t.Fatalf("回执的上限 %v 应与 --max-turns %d 一致", a.Limit, r.maxTurns)
		}
		// 剧本驱动知道余量；不知道必须给 nil，不许给 0。
		if a.Planned == nil {
			t.Fatal("scripted 驱动应当知道还剩几发，给了 nil")
		}
		// 语义也要钉：第 k 条回执的余量必须 = 剧本全长 − (k−1)。
		// 只判「在区间里」的话，恒报全长（intp(len(q))）也算过（M2 路 B 的 P2-2/C15 实测全包绿）。
		want := len(r.drv.(*scripted).q) - (first - 1)
		if *a.Planned != want {
			t.Fatalf("第 %d 条回执 planned_shots=%d，应为余量 %d（只钉区间钉不住语义）", first, *a.Planned, want)
		}
		if a.Resumable == nil {
			t.Fatal("resumable 不许留 nil：能不能续要当场给个话")
		}
		// E4 之后这一格是算出来的，不再是「恒 false 的约定」：这一趟日志就在 path 上、读得回来，
		// 所以必须写 true；写成 false 就是把已经兑现的能力藏了（对照在 events_sse_test.go）。
		if !*a.Resumable {
			t.Fatalf("日志落在 %s 上，resumable 却写 false：%s", path, e.Why)
		}
		if e.Why == "" {
			t.Fatal("resumable 给了值却没给凭什么 —— 界面读不出这格是哪来的")
		}
		if a.AckMS == nil {
			t.Fatal("回执没有 ack_ms —— 针1 无从判断")
		}
	}
	if first == 0 {
		t.Fatal("一条回执都没有")
	}
}

// 叶子臂：ack_ms 必须真反映「入口时刻 → 落盘」这段延迟。
// 把入口时刻往前拨 250ms 再落盘，判据必须红 —— 若 Emit 是「落盘时才现取时刻」，
// ack_ms 恒为 0，这条臂就永远不红，针1 是装饰。
func TestLateReceiptGoesRed(t *testing.T) {
	b := newEventBus(filepath.Join(t.TempDir(), "late.events.jsonl"))
	defer b.Close()
	e := Event{Kind: "turn_accepted", State: "running", Evidence: "measured", Turn: 1,
		Why: "断开不能续：没做 SSE", Accept: &Accept{Planned: intp(1), Limit: intp(20),
			StopVia: "signal:SIGINT", Resumable: boolp(false)}}
	e.enteredAt = time.Now().Add(-250 * time.Millisecond)
	if err := b.Emit(e); err != nil {
		t.Fatalf("迟到那条该收下（判据在读回侧）：%v", err)
	}
	evs := mustReadEvents(t, b.path)
	if len(evs) == 0 {
		t.Fatal("一条都没落 —— 这条臂是靠崩报红的，不算判据红")
	}
	if evs[0].Accept.AckMS == nil || *evs[0].Accept.AckMS < 200 {
		t.Fatalf("ack_ms=%v，没量到那 250ms —— 说明时刻是落盘时现取的", evs[0].Accept.AckMS)
	}
	if err := ValidateAccepts(evs); err == nil {
		t.Fatal("回执距入口 250ms（上限 100ms）判据却没红 —— 针1 恒绿")
	}
}

// 回执没带入口时刻就必须被拒 —— 放过去等于让 ack_ms 变成自证的 0。
func TestReceiptWithoutEntryInstantIsRefused(t *testing.T) {
	b := newEventBus(filepath.Join(t.TempDir(), "noentry.events.jsonl"))
	defer b.Close()
	err := b.Emit(Event{Kind: "turn_accepted", State: "running", Evidence: "measured", Turn: 1,
		Why: "断开不能续：没做 SSE", Accept: &Accept{Limit: intp(20), StopVia: "signal:SIGINT", Resumable: boolp(false)}})
	if err == nil || !strings.Contains(err.Error(), "入口时刻") {
		t.Fatalf("缺入口时刻的回执居然落了（err=%v）—— ack_ms 会是恒 0 的假数", err)
	}
}

// 判据接坏值：每一族坏值各红一次，一条不许误伤诚实的否定句。
func TestAcceptArmsCatchEachBadShape(t *testing.T) {
	cases := []struct {
		name string
		rows func() []map[string]any
	}{
		{"ack 超限", func() []map[string]any {
			return []map[string]any{rawAccept(1, map[string]any{"accept": map[string]any{"ack_ms": 101}}), rawShot(2)}
		}},
		{"ack 没给", func() []map[string]any {
			return []map[string]any{rawAccept(1, map[string]any{"accept": map[string]any{"ack_ms": nil}}), rawShot(2)}
		}},
		{"停止入口空着", func() []map[string]any {
			return []map[string]any{rawAccept(1, map[string]any{"accept": map[string]any{"stop_via": ""}}), rawShot(2)}
		}},
		{"resumable 留 nil（缺格）", func() []map[string]any {
			return []map[string]any{rawAccept(1, map[string]any{"accept": map[string]any{"resumable": nil}}), rawShot(2)}
		}},
		{"不可续没给为什么", func() []map[string]any {
			return []map[string]any{rawAccept(1, map[string]any{"why": ""}), rawShot(2)}
		}},
		{"谎写在 why 里", func() []map[string]any {
			return []map[string]any{rawAccept(1, map[string]any{"why": "断开可续"}), rawShot(2)}
		}},
		{"谎写在 stop 里（M2 路 B：这一路原来没人走过）", func() []map[string]any {
			return []map[string]any{rawAccept(1, map[string]any{"stop": "断开可续"}), rawShot(2)}
		}},
		{"谎写在 step.name 里（同上）", func() []map[string]any {
			return []map[string]any{rawAccept(1, map[string]any{
				"step": map[string]any{"no": 1, "name": "这里写：可续"}}), rawShot(2)}
		}},
		{"回执没带载荷", func() []map[string]any {
			return []map[string]any{rawAccept(1, map[string]any{"accept": nil, "step": nil}), rawShot(2)}
		}},
		{"回执挂了 step（统一走 ValidateTurnShape 那道）", func() []map[string]any {
			return []map[string]any{rawAccept(1, map[string]any{
				"step": map[string]any{"no": 1, "name": "assemble"}}), rawShot(2)}
		}},
		{"第一条不是回执", func() []map[string]any {
			return []map[string]any{rawShot(1), rawAccept(2, nil)}
		}},
		{"一发两条回执", func() []map[string]any {
			return []map[string]any{rawAccept(1, nil), rawAccept(2, nil)}
		}},
		{"一条发级事件都没有（只有 run 级）", func() []map[string]any {
			return []map[string]any{{"v": 1, "session": "arm", "turn": 0, "seq": 1,
				"ts": "t", "kind": "run_started", "state": "running", "evidence": "measured", "elapsed_ms": 0}}
		}},
	}
	for _, c := range cases {
		evs := writeRawJudgeable(t, "bad.events.jsonl", c.rows()...)
		if err := ValidateAccepts(evs); err == nil {
			t.Fatalf("坏法 %q 没被抓到 —— 这一族判据瞎了（而且是走盘喂进去的）", c.name)
		}
	}
	// 不许误伤：诚实的否定句里含「可续」三字，不能当谎抓。
	ok := writeRawJudgeable(t, "ok.events.jsonl",
		rawAccept(1, map[string]any{"why": "断开即取消，不可续：没做 SSE"}), rawShot(2))
	if err := ValidateAccepts(ok); err != nil {
		t.Fatalf("把「不可续」当谎抓了：%v —— 这会把诚实的话挤掉", err)
	}
}

// 落盘形状：resumable 不带 omitempty —— false 与 true 都得在盘上看得见，
// 旧记录（缺那格）读回来是 nil（没量过）而不是 false（真不是）。两半都走盘验。
func TestResumableFieldSurvivesFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resumable.events.jsonl")
	b := newEventBus(path)
	r := newRun(t, "happy", true, "included", 20)
	r.events = b
	r.turns = 1
	ev := r.acceptedEv()
	// E4 之后这一格是算出来的：落点在盘上、读得回来，就该是 true。
	if ev.Accept.Resumable == nil {
		t.Fatal("resumable 留了 nil —— 能不能续当场就该给个话")
	}
	if !*ev.Accept.Resumable {
		t.Fatalf("日志落在 %s 上却算出不可续：%s", path, ev.Why)
	}
	flipped := ev
	na := *ev.Accept
	na.Resumable = boolp(false)
	flipped.Accept = &na
	flipped.Why = "断开不能续：这一发是拿来验落盘形状的"
	r.emit(flipped) // 只验序列化形状，不改生产算出来的那个值
	again := ev
	again.Turn = 2
	tb := *ev.Accept
	tb.Resumable = boolp(true)
	again.Accept = &tb
	again.Why = ev.Why
	r.emit(again)
	b.Close()

	raw := string(mustRead(t, path))
	if !strings.Contains(raw, `"resumable":false`) {
		t.Fatalf("resumable=false 这一格不在盘上（或被折叠掉了）：%s", strings.TrimSpace(raw))
	}
	if !strings.Contains(raw, `"resumable":true`) {
		t.Fatalf("resumable=true 这一格不在盘上：%s", strings.TrimSpace(raw))
	}
	back := mustReadEvents(t, path)
	if len(back) != 2 {
		t.Fatalf("落了 %d 条，应 2 条", len(back))
	}
	if rp := back[0].Accept.Resumable; rp == nil || *rp {
		t.Fatalf("读回来的 resumable 不是 false：%v", rp)
	}
	if rp := back[1].Accept.Resumable; rp == nil || !*rp {
		t.Fatalf("读回来的 resumable 不是 true：%v", rp)
	}
}

func boolp(v bool) *bool { return &v }
