package main

import (
	"path/filepath"
	"testing"
)

// 跨进程续跑那一族的**发号轴**。这一支是 M5 那轮扫端抓到的真东西：
//
//	续跑那趟 stderr 上印着「发号从第 7 发接着走」，结算载荷 done_before 也确实从 7 起，
//	可事件流里的 turn 却从 1 重新开始 —— 同一本日志里两个轴各说各话。
//	新加的判据（done_before 不许大于发号）因此红了，而**红得对**：错的是产物不是尺子。
//	当时 91 支 Test 全绿看不见它，因为「resume × 事件日志 × 结算」这个组合一个负载都没有。
//
// 所以这一支钉两件事：① 续跑的 turn 必须接续；② 合并日志上四支判据必须全绿（假红也算缺陷）。

func TestResumedRunContinuesTurnAxis(t *testing.T) {
	dir := t.TempDir()
	evPath := filepath.Join(dir, "axis.events.jsonl")

	r1 := newRun(t, "happy", true, "included", 6)
	r1.journalPath = filepath.Join(dir, "j1.jsonl")
	r1.events = newEventBus(evPath)
	r1.loop()
	r1.closeOut()
	floor := r1.shot
	if floor == 0 {
		t.Fatalf("第一趟一发都没落账（shot=0）—— 续跑现场没造出来，这一支不作数")
	}

	r2 := newRun(t, "happy", true, "included", 3)
	r2.journalPath = filepath.Join(dir, "j2.jsonl")
	r2.session = "cross-process"
	r2.events = newEventBus(evPath)
	if !r2.resume(r1.journalPath) {
		t.Fatal("续跑被拒 —— 这一支测不了接续，先看 r.resume 为什么返回 false")
	}
	r2.loop()
	r2.closeOut()
	if r2.turns == 0 {
		t.Fatal("续跑那趟一发都没跑 —— 现场没造出来，这一支不作数")
	}

	evs := mustReadEvents(t, evPath)
	runs := map[string]bool{}
	for _, e := range evs {
		runs[e.Run] = true
	}
	if len(runs) < 2 {
		t.Fatalf("一本日志里只有 %d 个趟身份，跨趟现场没造出来", len(runs))
	}

	// ① 续跑那趟的第一发，turn 必须是 floor+1 —— 不是 1
	firstResumed, minTurn := -1, 1<<30
	for _, e := range evs {
		if e.Run != r2.events.run || e.Kind != "turn_accepted" {
			continue
		}
		if e.Turn < minTurn {
			minTurn, firstResumed = e.Turn, e.Seq
		}
	}
	if firstResumed < 0 {
		t.Fatal("续跑那趟一条回执都没落 —— 上面的接续断言会空过")
	}
	if minTurn != floor+1 {
		t.Fatalf("续跑第一发的发号是 %d，应为上一本账 %d 发之后 %d —— turn 轴没接续，同一本日志里会出现 turn=%d 却写着 done_before=%d 的两轴打架", minTurn, floor, floor+1, minTurn, floor+1)
	}

	// ② 合并日志上四支判据必须全绿：假红与假绿一样是缺陷
	if err := ValidateLog(evs); err != nil {
		t.Fatalf("续跑合并日志的序号判据红：%v", err)
	}
	if err := ValidateAcceptsWithin(evs, 60000); err != nil {
		t.Fatalf("续跑合并日志的回执判据红：%v", err)
	}
	if err := ValidateSettlement(evs); err != nil {
		t.Fatalf("续跑合并日志的结算判据红：%v —— 修之前就是这条假红（done_before=%d 对 turn=1）", err, floor+1)
	}
	if err := ValidateReplay(evs, formatCursor(evs[len(evs)-1])); err != nil {
		t.Fatalf("续跑合并日志的尾游标接不上：%v", err)
	}

	// ③ 进度那一格必须在一根轴上：每趟的 limit 是「这一趟的基线 + 本进程额度」，done 不许越过它。

	wantLimit := map[string]int{r1.events.run: r1.maxTurns, r2.events.run: floor + r2.maxTurns}
	checked := 0
	for _, e := range evs {
		if e.Progress == nil || e.Progress.Limit == nil {
			continue
		}
		want, known := wantLimit[e.Run]
		if !known {
			t.Fatalf("日志里出现没登记的趟身份 %s —— 现场构造与期望不同源", e.Run)
		}
		if got := *e.Progress.Limit; got != want {
			t.Fatalf("%s（第 %d 发）的额度 %d，应为基线+本进程额度 = %d —— 分子分母不同源，流里就会出现 done 比总额度还大",
				e.Kind, e.Turn, got, want)
		}
		if e.Progress.Done > want && e.Kind != "run_settled" {
			t.Fatalf("%s（第 %d 发）说已完成 %d 发，本趟额度只到 %d —— done 与 limit 不同一根轴", e.Kind, e.Turn, e.Progress.Done, want)
		}
		checked++
	}
	if checked < 2 {
		t.Fatalf("只核到 %d 条进度格，续跑现场没造出来", checked)
	}
}
