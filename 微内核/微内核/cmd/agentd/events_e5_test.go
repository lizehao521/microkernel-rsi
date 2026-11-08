package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// E5：10s 兜底进度。规矩是「超时是汇报，不是报错」——
// 静默到点就要往流里落一条可核对的进度（progress_tick 带 done/total/limit），
// total 算不出就落 why_late（absent 且必须带为什么）。两形都有对照绿形，判据也不许把合法形状判红。

// slowRun 造一趟「回执之后静默一段」的负载：阈值调小、延迟放在回执之后（那正是发在途中的窗口）。
func slowRun(t *testing.T, script string, drvName string, idle time.Duration, delay time.Duration, maxTurns int) (string, []Event) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "e5.events.jsonl")
	drv, err := buildDriver(drvName, script)
	if err != nil {
		t.Fatalf("造驱动：%v", err)
	}
	r := newRun(t, script, true, "included", maxTurns)
	r.drv = drv
	r.events = newEventBus(path)
	r.tickIdle = idle
	r.shotDelay = delay
	r.loop()
	r.closeOut()
	evs := mustReadEvents(t, path)
	if p, n, degraded := r.events.stats(); n != len(evs) {
		t.Fatalf("盘上 %d 条与判据吃到的 %d 条不同源（落点 %s，降级 %q）", n, len(evs), p, degraded)
	}
	return path, evs
}

func countTicks(evs []Event) (tick, late int) {
	for _, e := range evs {
		switch e.Kind {
		case "progress_tick":
			tick++
		case "why_late":
			late++
		}
	}
	return
}

// 1) 剧本驱动（知道还剩几发）⇒ 静默到点补 progress_tick，且三条数都在同一根轴上。
// 额度故意给成 2 而剧本有 6 发：真负载就是这么一趟（happy + -max-turns 2），
// 补位若照抄计划的 6，流里就出现 done=1 / total=6 / limit=2 三个数并存。
func TestIdleTickCarriesCheckableProgress(t *testing.T) {
	_, evs := slowRun(t, "happy", "scripted", 60*time.Millisecond, 500*time.Millisecond, 2)
	n, _ := countTicks(evs)
	if n == 0 {
		t.Fatal("静默 500ms（阈值 60ms）一条补位都没落 —— 超时这一维在流里是沉默的")
	}
	for _, e := range evs {
		if e.Kind != "progress_tick" {
			continue
		}
		if e.Evidence != "measured" || e.State != "running" {
			t.Errorf("补位条的档/态不对：evidence=%s state=%s（量的应是静默本身，所以是 measured；事没结束所以是 running）", e.Evidence, e.State)
		}
		if e.Turn == 0 || e.Progress == nil {
			t.Fatalf("补位条 %q 没挂发号或没带进度格 —— 下游没法归列", e.Kind)
		}
		p := e.Progress
		if p.Total == nil {
			t.Errorf("第 %d 发补位条在剧本驱动下居然算不出 total —— 剧本知道还剩几发", e.Turn)
		} else if *p.Total <= 0 {
			t.Errorf("第 %d 发的 total=%d —— 算不出是 nil，不许拿 0 顶（0 会被读成「一步也没有」）", e.Turn, *p.Total)
		}
		if p.Done < 1 || (p.Total != nil && p.Done > *p.Total) {
			t.Errorf("第 %d 发 done=%d total=%v —— done 越过 total，就是那根轴不同源", e.Turn, p.Done, p.Total)
		}
		if p.Limit == nil || *p.Limit < p.Done {
			t.Errorf("第 %d 发额度格缺失或小于已完成：%v vs %d", e.Turn, p.Limit, p.Done)
		}
		if p.Total != nil && p.Limit != nil && *p.Total > *p.Limit {
			t.Errorf("第 %d 发 total=%d 越过额度 limit=%d —— 到额度就停的趟，落点不可能是更大的数（真负载抓到过 1/6/2）",
				e.Turn, *p.Total, *p.Limit)
		}
		if !strings.Contains(e.Why, "静默") {
			t.Errorf("补位条没说自己静默了多久：%q", e.Why)
		}
	}
	// 「额度夹住计划」这一支到底被这发负载走到了没有，从收口那一格读，不靠人记：
	// stop=turns 就是「剧本比额度长」。负载走不到就不许让这条针挂着当已证。
	capped := false
	for _, e := range evs {
		if e.Kind == "run_settled" && e.Stop == "turns" {
			capped = true
		}
	}
	if !capped {
		t.Fatal("这一趟的收口不是 turns（额度到）—— 夹额那一支没被负载走到，换个走得到的负载再判")
	}
	for _, e := range evs {
		if e.Kind == "progress_tick" && !strings.Contains(e.Why, "夹到额度") {
			t.Errorf("落点是额度决定的，why 里却不说哪一侧决定：%q", e.Why)
		}
	}
	// 顺序针：补位只能在结算之前。结算之后再落一条 running，
	// 结算自印的 counts 就比账上少一条 —— 那是判据只能事后喊「两个数不许并存」的假账。
	settledAt := map[int]int{}
	for _, e := range evs {
		if e.Kind == "turn_settled" || e.Kind == "turn_interrupted" {
			if _, has := settledAt[e.Turn]; !has {
				settledAt[e.Turn] = e.Seq
			}
			continue
		}
		if at, has := settledAt[e.Turn]; has && (e.Kind == "progress_tick" || e.Kind == "why_late") && e.Seq > at {
			t.Fatalf("第 %d 发结算在 seq=%d，之后还补了 %q（seq=%d）—— counts 与账差一条", e.Turn, at, e.Kind, e.Seq)
		}
	}
	// 对照：带了补位条的这本真账，三道判据都得绿（牙不许乱咬）
	if err := ValidateLog(evs); err != nil {
		t.Fatalf("补位条把读回路径判红了：%v", err)
	}
	if err := ValidateSettlement(evs); err != nil {
		t.Fatalf("补位条把结算判据判红了：%v", err)
	}
	if err := ValidateAcceptsWithin(evs, 60000); err != nil {
		t.Fatalf("补位条把回执判据判红了：%v", err)
	}
}

// 2) 不是剧本驱动（不知道还剩几发）⇒ total 是 nil，走 why_late 并写明为什么算不出。
func TestWhyLateWhenTotalIsNotComputable(t *testing.T) {
	_, evs := slowRun(t, "happy", "session", 60*time.Millisecond, 500*time.Millisecond, 20)
	_, n := countTicks(evs)
	if n == 0 {
		t.Fatal("非剧本驱动静默到点却没落 why_late —— 「为什么还没到」这一格在流里是空的")
	}
	for _, e := range evs {
		if e.Kind != "why_late" {
			continue
		}
		if e.Evidence != "absent" {
			t.Errorf("算不出 total 却标成 %q —— 那是没这个数，不是量到的", e.Evidence)
		}
		if e.Why == "" || !strings.Contains(e.Why, "算不出") {
			t.Errorf("absent 没带为什么（或没说清算不出的是哪一格）：%q", e.Why)
		}
		if e.Progress == nil || e.Progress.Total != nil {
			t.Errorf("why_late 却交了 total=%v —— 不许编一个数顶上", e.Progress)
		}
		if e.Progress == nil || e.Progress.Done < 1 || e.Progress.Limit == nil {
			t.Errorf("why_late 把已完成的发号与额度也一起丢了：%+v", e.Progress)
		}
	}
	if err := ValidateLog(evs); err != nil {
		t.Fatalf("why_late 这本账读回判红：%v", err)
	}
	if err := ValidateSettlement(evs); err != nil {
		t.Fatalf("why_late 这本账结算判红：%v", err)
	}
}

// 3) 冻结补位这件事要能定点测：不能靠竞态撞。抄 counts 的那一把锁里就该把这一发标成别再补。
func TestSettleFreezesIdleTicks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "freeze.events.jsonl")
	b := newEventBus(path)
	defer b.Close()
	probe := func(turn int, kind string) Event {
		return Event{Kind: kind, State: "running", Evidence: "measured", Turn: turn, Session: "freeze", idleOnly: true}
	}
	// 开账：一条回执 + 一条干活
	if err := b.Emit(Event{Kind: "turn_accepted", State: "running", Evidence: "measured", Turn: 1, Session: "freeze"}); err != nil {
		t.Fatalf("回执落不出去：%v", err)
	}
	_, n0, _ := b.stats()
	if err := b.Emit(probe(1, "progress_tick")); err != nil {
		t.Fatalf("没结过账时补位该能落：%v", err)
	}
	_, n1, _ := b.stats()
	if n1 != n0+1 {
		t.Fatalf("结算之前补位没落（%d → %d）—— 看门狗这条路根本没接上盘", n0, n1)
	}
	// 抄 counts（= 结算的第一步），之后这条发就不许再收补位
	if c := b.countsFor("freeze", 1); c["running"] != 2 {
		t.Fatalf("抄到的 counts=%v，应是 running 2 条（回执 + 上面那条补位）", c)
	}
	if err := b.Emit(probe(1, "progress_tick")); err != nil {
		t.Fatalf("冻结之后这条本该安静跳过，却报了错：%v", err)
	}
	_, n2, _ := b.stats()
	if n2 != n1 {
		t.Fatalf("结算抄过 counts 之后还落了补位（%d → %d）—— 那两个数从此不一致", n1, n2)
	}
	// 对照：冻结只冻这一发，别的发照收
	if err := b.Emit(Event{Kind: "turn_accepted", State: "running", Evidence: "measured", Turn: 2, Session: "freeze"}); err != nil {
		t.Fatalf("第 2 发回执落不出去：%v", err)
	}
	if err := b.Emit(probe(2, "progress_tick")); err != nil {
		t.Fatalf("第 2 发没结过账，补位被误冻：%v", err)
	}
	_, n3, _ := b.stats()
	if n3 != n2+2 {
		t.Fatalf("第 2 发的回执+补位应落两条，实际落了 %d 条（从 %d 起）—— 冻结漏到了别的发", n3-n2, n2)
	}
	// 静默时长与最后落的那条要同源可取（看门狗就说这两样）
	idle, lastKind, landed, seen := b.idleView("freeze", 2)
	if !seen || idle < 0 || lastKind != "progress_tick" || landed != 2 {
		t.Fatalf("idleView 给的现场不对：seen=%v idle=%v last=%q landed=%d（第 2 发应落了回执 + 一条补位）", seen, idle, lastKind, landed)
	}
	if _, _, _, seen := b.idleView("freeze", 99); seen {
		t.Fatal("没开过的发被说成「有静默可读」—— 那是没量过")
	}
	// 别的发的 counts 不许被第 1 发的冻结带走
	if err := b.Emit(Event{Kind: "turn_accepted", State: "running", Evidence: "measured", Turn: 3, Session: "freeze"}); err != nil {
		t.Fatalf("第 3 发回执落不出去：%v", err)
	}
	if c := b.countsFor("freeze", 3); c["running"] != 1 {
		t.Fatalf("第 3 发抄到的 counts=%v，与第 1/2 发串了", c)
	}
}
