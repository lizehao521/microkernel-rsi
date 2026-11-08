package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// M2 路 B 报回来的三条，先按现场规矩**自己复现**再修：
// 这一支现在是红的 —— 红就是「缺陷成立」的凭据，修完它必须转绿。
// 三条都在序列化/落盘这一族，而 E2 那六族坏值全走内存切片，所以整族量不到（路 B 的 P1-3）。

// 先要一个能落盘的构造口：走公开路径（Emit → 文件 → ReadEvents），不再对着自己造的指针判。
func emitAccept(t *testing.T, b *eventBus, turn int, enteredAt time.Time, preset *int64) {
	t.Helper()
	resumable := false
	a := &Accept{Planned: intp(3), Limit: intp(20), StopVia: "signal:SIGINT", Resumable: &resumable}
	if preset != nil {
		a.AckMS = preset
	}
	e := Event{Kind: "turn_accepted", State: "running", Evidence: "measured", Turn: turn,
		Why: "断开不能续：没做 SSE", Accept: a}
	e.enteredAt = enteredAt
	if err := b.Emit(e); err != nil {
		t.Fatalf("落这条：%v", err)
	}
}

// 路 B P1-1：Emit 浅拷贝共享 *Accept，同一份对象复用一次，ack_ms 就串到第一条的值上。
func TestAckMSDoesNotLeakAcrossReuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alias.events.jsonl")
	b := newEventBus(path)
	defer b.Close()

	old := time.Now().Add(-400 * time.Millisecond)
	// 同一个 Event 值复用两次：第二次入口是「现在」，ack 就不该还是 400。
	ev := Event{Kind: "turn_accepted", State: "running", Evidence: "measured", Turn: 1,
		Why: "断开不能续：没做 SSE", Accept: &Accept{Planned: intp(3), Limit: intp(20),
			StopVia: "signal:SIGINT", Resumable: func() *bool { v := false; return &v }()}}
	ev.enteredAt = old
	if err := b.Emit(ev); err != nil {
		t.Fatalf("第一条：%v", err)
	}
	ev.enteredAt = time.Now()
	if err := b.Emit(ev); err != nil {
		t.Fatalf("第二条：%v", err)
	}
	evs := mustReadEvents(t, path)
	if len(evs) != 2 {
		t.Fatalf("落了 %d 条，应 2 条", len(evs))
	}
	first, second := *evs[0].Accept.AckMS, *evs[1].Accept.AckMS
	if second >= first {
		t.Fatalf("第二条 ack_ms=%d 没比第一条 %d 小 —— Emit 在往调用方那份 Accept 上写（浅拷贝串值）", second, first)
	}
}

// 路 B P1-2：调用方自填 ack_ms=0 就能绕过针1；注释写明了这个风险，代码照收。
func TestCallerCannotFakeAckLatency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake.events.jsonl")
	b := newEventBus(path)
	defer b.Close()
	zero := int64(0)
	entered := time.Now().Add(-500 * time.Millisecond)
	emitAccept(t, b, 1, entered, &zero)
	evs := mustReadEvents(t, path)
	if len(evs) != 1 {
		t.Fatalf("落了 %d 条，应 1 条", len(evs))
	}
	if got := *evs[0].Accept.AckMS; got < 400 {
		t.Fatalf("自填 0 被照收：ack_ms=%d，入口明明是 500ms 前 —— 写侧一行赋值就能抹平针1", got)
	}
	if err := ValidateAccepts(evs); err == nil {
		t.Fatal("一条被自填成 0 的迟到回执判据还绿 —— 针1 是能被写的")
	}
}

// 路 B P1-5：「不知道就写 nil，绝不写 0」在 planned_shots 上没有针。
// 这一支不需要修代码就能过（现在行为是对的），但把它钉死：变异成 intp(0) 必须红。
func TestNonScriptDriverLeavesPlannedNil(t *testing.T) {
	for _, drv := range []string{"real", "session"} {
		d, err := buildDriver(drv, "full")
		if err != nil {
			t.Fatalf("造驱动 %s：%v", drv, err)
		}
		r := newRun(t, "happy", true, "included", 20)
		r.drv = d
		if p := r.plannedLeft(); p != nil {
			t.Fatalf("驱动 %s 不知道余量却给了 %d —— 量不到要写 nil，不能写个数", drv, *p)
		}
		s := newRun(t, "happy", true, "included", 20)
		if p := s.plannedLeft(); p == nil {
			t.Fatalf("scripted 明明知道余量却给了 nil")
		} else if *p != len(s.drv.(*scripted).q) {
			t.Fatalf("scripted 的余量 %d 不等于剧本全长 %d（还没发过任何一发）", *p, len(s.drv.(*scripted).q))
		}
	}
}

// 路 B P0-1（我自己复现过）：同一对路径连跑三趟（E1 的 append-only 设计目标、E4 重放要读的底），
// 判据按「一本日志 = 一趟」假设就恒红。它给的最小修法（改按 (session,turn) 分组）我复算不成立 ——
// 三趟用的是同一个 session，(session,turn) 上是 3 条回执。要有 **run 轴** 才分得开。
func TestCrossRunLogStaysJudgeable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.events.jsonl")
	for i := 0; i < 3; i++ {
		b := newEventBus(path)
		turn := 1 // 每一趟都从第 1 发开始（turn 号是趟内的轴）
		emitAccept(t, b, turn, time.Now(), nil)
		if err := b.Emit(Event{Kind: "recorded", State: "ok", Evidence: "measured", Turn: turn,
			Step: &EventStep{No: intp(turn), Name: "kernel.record_call"}}); err != nil {
			t.Fatalf("落 recorded：%v", err)
		}
		b.Close()
	}
	evs := mustReadEvents(t, path)
	if len(evs) != 6 {
		t.Fatalf("三趟应累加出 6 条，实得 %d —— append-only 破了", len(evs))
	}
	if err := ValidateLog(evs); err != nil {
		t.Fatalf("跨趟的序号判据红：%v", err)
	}
	if err := ValidateAccepts(evs); err != nil {
		t.Fatalf("跨趟合法日志被回执判据判红：%v —— 判据缺一个 run 轴，E4 那天它会挡死重放", err)
	}
	// 这一支的负载是手工造的「回执 + 落账」两条，没有收口那一行 —— 拿它判结算就是判一个不存在的形状。
	// 跨趟日志的结算那一维由生产负载的 TestCrossRunCountsArePerRun 管（它跑的是真 loop + 真 settleTurn）。
}

// 路 B P2-6：「旧记录读回来是 nil（没量过）」这一半无人验，注释却写着它。
func TestOldRecordWithoutResumableReadsAsNil(t *testing.T) {
	line := `{"v":1,"session":"old","turn":1,"seq":1,"ts":"2026-10-22T00:00:00Z","kind":"turn_accepted",` +
		`"state":"running","evidence":"measured","elapsed_ms":1,"accept":{"planned_shots":null,"turn_limit":20,` +
		`"stop_via":"signal:SIGINT","ack_ms":3}}`
	var e Event
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		t.Fatalf("读旧记录：%v", err)
	}
	if e.Accept == nil || e.Accept.Resumable != nil {
		t.Fatalf("缺 resumable 那格的旧记录没读成 nil：%+v —— 「没量过」会被塌成「不是」", e.Accept)
	}
	err := ValidateAccepts([]Event{e})
	if err == nil || !strings.Contains(err.Error(), "resumable") {
		t.Fatalf("判据对旧记录（缺格）没红或没点名：%v", err)
	}
}

// M2 两路独立撞到的那一条（撞车=最高信度）：针1 原先量不到东西 ——
// 锚点取在回执构造那一格，实测 42/42（路 B）与 16/16 + `--shot-delay 600ms` 恒 0（路 A）。
// 这一支走**生产路径**：入口锚往前拨 600ms，让真跑出来的日志自己把 ack_ms 顶起来并被判红。
func TestProductionAckLatencyIsMeasurable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prod.events.jsonl")
	r := newRun(t, "happy", true, "included", 1)
	r.journalPath = filepath.Join(t.TempDir(), "journal.jsonl")
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()

	evs := mustReadEvents(t, path)
	if len(evs) == 0 {
		t.Fatal("一条都没落 —— 这一支要靠崩报红吗")
	}
	var acks []int64
	for _, e := range evs {
		if e.Kind == "turn_accepted" && e.Accept != nil && e.Accept.AckMS != nil {
			acks = append(acks, *e.Accept.AckMS)
		}
	}
	if len(acks) == 0 {
		t.Fatal("生产路径的回执没带 ack_ms")
	}

	// 同一支跑法，只把「请求入口」往前挪 600ms：真出来的 ack 必须跟着涨，判据必须红。
	path2 := filepath.Join(t.TempDir(), "late.events.jsonl")
	r2 := newRun(t, "happy", true, "included", 1)
	r2.journalPath = filepath.Join(t.TempDir(), "journal2.jsonl")
	r2.events = newEventBus(path2)
	r2.tRequest = time.Now().Add(-600 * time.Millisecond) // 把「拿到任务」往前拨：等于回执前压了 600ms 的活
	r2.loop()
	r2.closeOut()
	evs2 := mustReadEvents(t, path2)
	var maxAck int64 = -1
	for _, e := range evs2 {
		if e.Kind == "turn_accepted" && e.Accept != nil && e.Accept.AckMS != nil && *e.Accept.AckMS > maxAck {
			maxAck = *e.Accept.AckMS
		}
	}
	if maxAck < 100 {
		t.Fatalf("回执前压了 600ms，ack_ms 却只有 %d —— 锚点还是没钉在请求入口（针1 量不到东西）", maxAck)
	}
	if err := ValidateAccepts(evs2); err == nil {
		t.Fatalf("ack_ms=%d 超上限 %d，判据却没红", maxAck, ackLimitMS)
	}
	// 顺带把「回执是这一发第一条」在真日志上核一遍（正常那趟必须全绿）。
	// 这一趟用的是宽松上限：100ms 是**契约预算**，不是「这条流的第一条是不是回执」。
	// 整包 -shuffle 时同一对落点被并发压到 235/308ms 是真的（M2 路 B 的 P1-①，9 趟红 2 趟），
	// 拿它判「回执排没排在干活前面」就是判据在量机器。预算那一半由 -check-log 与针1 的专门支管。
	if err := ValidateAcceptsWithin(evs, 60000); err != nil {
		t.Fatalf("正常那趟的回执判据红：%v", err)
	}
	if err := ValidateSettlement(evs); err != nil {
		t.Fatalf("正常那趟的结算判据红：%v", err)
	}
}
