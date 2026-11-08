package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// E3 要证的几件事，一件一支：
//   每一发恰好结一次算（走到尾、被拒、回帧坏、被打断走同一个 settleTurn）；
//   结算交三样：停在哪个相、走过哪些相、哪些相根本没执行；counts 由这本账现数并与账对得上；
//   被打断的那一发 1s 内出现结算（针4），且已完成计数与 KPI 活过打断；
//   补步针：说「没执行」的相不许在流里出现，说「走过」的相必须有事件（针5）；
//   停止入口只有一个：进程内叫停与 SIGINT 走同一条路，重复叫停不炸、也不覆盖首发原因。

func TestEveryTurnSettlesExactlyOnce(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	path := filepath.Join(t.TempDir(), "s.events.jsonl")
	r.journalPath = filepath.Join(t.TempDir(), "journal.jsonl")
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()

	evs := mustReadEvents(t, path)
	if err := ValidateLog(evs); err != nil {
		t.Fatalf("序号不合格：%v", err)
	}
	if err := ValidateAccepts(evs); err != nil {
		t.Fatalf("回执判据红：%v", err)
	}
	if err := ValidateSettlement(evs); err != nil {
		t.Fatalf("结算判据红：%v", err)
	}
	if r.turns == 0 {
		t.Fatal("一发都没跑，这支针就是空转")
	}
	per := map[int][]Event{}
	for _, e := range evs {
		if e.Turn > 0 {
			per[e.Turn] = append(per[e.Turn], e)
		}
	}
	for turn := 1; turn <= r.turns; turn++ {
		n := 0
		for _, e := range per[turn] {
			if e.Kind == "turn_settled" || e.Kind == "turn_interrupted" {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("第 %d 发结算 %d 条，应为 1（多发就是两套收口代码，0 条就是 running 孤儿）", turn, n)
		}
	}
}

// 针4：打断后 1s 内必须有结算，且「已完成计数」与打断前一致、KPI 不许被清零。
func TestInterruptSettlesWithinSecond(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	path := filepath.Join(t.TempDir(), "i.events.jsonl")
	r.journalPath = filepath.Join(t.TempDir(), "journal.jsonl")
	r.events = newEventBus(path)
	r.abort = make(chan struct{})
	r.shotDelay = 250 * time.Millisecond
	// 不靠墙钟对撞：等事件文件里出现第一条 turn_accepted 再叫停 ——
	// 上一版用 time.Sleep(300ms) 去赌延迟窗口，单独 -run 这一支时就飘了（控制不绿）。
	r.shotDelay = 5 * time.Second
	done := make(chan struct{})
	go func() { r.loop(); close(done) }()
	if !waitForKind(path, "turn_accepted", 2, 6*time.Second) {
		t.Fatal("三条秒内没等到第一条回执 —— 延迟窗口没演成")
	}
	stopAt := time.Now()
	r.Stop("test:进程内叫停")
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("叫停之后循环没回来 —— 停止入口没接上")
	}
	r.closeOut()

	if r.stop != stopAbort || exitOf(r.stop) != rcAbort {
		t.Fatalf("打断那一路的结束原因/退出码不对：%q / rc=%d", r.stop, exitOf(r.stop))
	}
	evs := mustReadEvents(t, path)
	if err := ValidateSettlement(evs); err != nil {
		t.Fatalf("结算判据红：%v", err)
	}
	var last Event
	found := false
	for _, e := range evs {
		if e.Kind == "turn_interrupted" {
			last, found = e, true
		}
	}
	if !found {
		t.Fatalf("跑到了打断窗口却没落 turn_interrupted：%s", kindsOf(evs))
	}
	// 针4 的口径：叫停之后 1s 内必须出现结算 —— 量的是**事件自己落盘的那一刻**，
	// 不是「循环返回」那一刻（两件事可以差出去很远）。
	at, err := time.Parse(time.RFC3339Nano, last.TS)
	if err != nil {
		t.Fatalf("结算那条的 ts 读不懂：%q", last.TS)
	}
	if took := at.Sub(stopAt); took > time.Second {
		t.Fatalf("叫停后过了 %v 才落结算，针4 要的是 1s 内", took)
	}
	if last.Settle == nil || last.Settle.Kind != "interrupted" {
		t.Fatalf("打断结算载荷不对：%+v", last.Settle)
	}
	if last.Settle.DoneBefore != r.shot {
		t.Fatalf("结算说已完成 %d 发，运行态 %d 发 —— 计数没活过打断", last.Settle.DoneBefore, r.shot)
	}
	if r.kpi == 0 {
		t.Fatal("走到打断时 KPI=0 —— 负载没进账，这支钉不住「计数活过打断」（臂要指到走得到那条分支的负载）")
	}
	if last.Settle.KPI != r.kpi {
		t.Fatalf("结算里的 KPI=%d 与运行态 %d 不符 —— 打断路径上把账清零了", last.Settle.KPI, r.kpi)
	}
	if got := last.Settle.Phase; !inPhaseTable(got) {
		t.Fatalf("结算记的相位 %q 不在相位表里 —— 相位表与产物必须同源", got)
	}
	settles := map[int]Event{}
	for _, e := range evs {
		if e.Kind == "run_settled" {
			settles[0] = e
		}
	}
	if settles[0].Stop != "abort" {
		t.Fatalf("run 级结算的 stop=%q，应为 token %q（整句话走 stop_why）", settles[0].Stop, "abort")
	}
}

// 针5 的反面：被打断那一发里，not_reached 的相一条事件都不许出现；轨迹页也不许多出这一发。
func TestInterruptDoesNotFabricateSteps(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	path := filepath.Join(t.TempDir(), "f.events.jsonl")
	trace := filepath.Join(t.TempDir(), "trace.html")
	r.journalPath = filepath.Join(t.TempDir(), "journal.jsonl")
	r.tracePath = trace
	r.events = newEventBus(path)
	r.abort = make(chan struct{})
	r.shotDelay = 5 * time.Second
	done := make(chan struct{})
	go func() { r.loop(); close(done) }()
	if !waitForKind(path, "turn_accepted", 2, 6*time.Second) {
		t.Fatal("没等到第一条回执，演不出打断那一发")
	}
	r.Stop("test:叫停")
	<-done
	r.closeOut()

	evs := mustReadEvents(t, path)
	byTurn := map[int][]Event{}
	for _, e := range evs {
		byTurn[e.Turn] = append(byTurn[e.Turn], e)
	}
	var interrupted *Event
	for i, e := range evs {
		if e.Kind == "turn_interrupted" {
			interrupted = &evs[i]
		}
	}
	if interrupted == nil {
		t.Fatalf("没演成打断：%s", kindsOf(evs))
	}
	group := byTurn[interrupted.Turn]
	for _, p := range interrupted.Settle.NotReached {
		k := phaseKind[p]
		for _, e := range group {
			if e.Kind == k {
				t.Fatalf("第 %d 发结算说 %q 根本没执行，流里却来了 %s", interrupted.Turn, p, k)
			}
		}
	}
	if len(interrupted.Settle.NotReached) == 0 {
		t.Fatal("打断那一发的 not_reached 是空的 —— 「没走到的步骤」这一样没交")
	}
	h := string(mustRead(t, trace))
	lastCall := r.shot + 1 // 被打断那一发没占发号，它的 call 号不该出现在轨迹页里
	if strings.Contains(h, "第 "+strconv.Itoa(lastCall)+" 发") {
		t.Fatalf("轨迹页里出现了根本没执行的那一发（call=%d）—— 补步补到页面上去了", lastCall)
	}
}

// 幂等守卫要有消费者：一条退出路径上被叫两次（或被两条路径同时叫到），
// 落出去的结算必须还是一条 —— 否则「一套收口代码」只是嘴上说说。
func TestSettleTurnIsIdempotent(t *testing.T) {
	r := newRun(t, "happy", true, "included", 20)
	path := filepath.Join(t.TempDir(), "idem.events.jsonl")
	r.events = newEventBus(path)
	r.loop() // 跑完：每一发已被 settleTurn 结过一次
	r.settleTurn("completed", "再叫一次也不该多落一条")
	r.settleTurn("interrupted", "第三次")
	r.closeOut()

	n := map[int]int{}
	for _, e := range mustReadEvents(t, path) {
		if e.Kind == "turn_settled" || e.Kind == "turn_interrupted" {
			n[e.Turn]++
		}
	}
	for turn, c := range n {
		if c != 1 {
			t.Fatalf("第 %d 发结算 %d 条，应为 1 —— 幂等守卫没生效就是两套收口代码", turn, c)
		}
	}
	if len(n) != r.turns {
		t.Fatalf("有结算的发数 %d 与实际发数 %d 不符", len(n), r.turns)
	}
	if err := ValidateSettlement(mustReadEvents(t, path)); err != nil {
		t.Fatalf("判据红：%v", err)
	}
	r.events.Close()
}

// 被拒那一形也要有结算：门口挡下的那一发，phase 停在 record_call / check_call，
// 「没走到的步骤」必须还在 —— 全跑完那种负载走不到这条分支，拿它当证据就是数空气。
func TestSettlesCoverRefusedShape(t *testing.T) {
	drv, err := buildDriver("scripted", "full")
	if err != nil {
		t.Fatalf("造驱动：%v", err)
	}
	r := &run{
		rpc: newKernelRPC(98, false), drv: drv, session: "test-ev-settle", maxTurns: 20,
		readBack: true, declare: true, payload: "included",
		journalPath: filepath.Join(t.TempDir(), "journal.jsonl"),
		log:         &bytes.Buffer{}, errLog: &bytes.Buffer{}, declared: map[string]bool{},
	}
	path := filepath.Join(t.TempDir(), "r.events.jsonl")
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()

	evs := mustReadEvents(t, path)
	if err := ValidateSettlement(evs); err != nil {
		t.Fatalf("被拒那一形的结算判据红：%v", err)
	}
	refused := 0
	for _, e := range evs {
		if e.Settle != nil && e.Settle.Kind == "refused" {
			refused++
			if len(e.Settle.Reached)+len(e.Settle.NotReached) != len(turnPhases) {
				t.Fatalf("被拒那一发的相位没凑齐整张表：%+v", e.Settle)
			}
		}
	}
	if refused == 0 {
		t.Fatal("这一趟没演到被拒那一形 —— 负载不对，别拿它当证据")
	}
	if r.stop != stopCapped {
		t.Fatalf("该停在账满上，实际 %q", r.stop)
	}
}

// 停止入口只有一条：进程内叫停与 SIGINT 共用 Stop；重复叫停不炸，也不覆盖首发原因。
func TestStopEntryIsSingleAndIdempotent(t *testing.T) {
	r := newRun(t, "happy", true, "included", 20)
	r.abort = make(chan struct{})
	r.Stop("first")
	r.Stop("second")
	select {
	case <-r.abort:
	default:
		t.Fatal("叫停没生效：abort 没关")
	}
	if r.stopWhy != "first" {
		t.Fatalf("首发原因被覆盖了：%q —— 谁先停的这件事不能被后面改写", r.stopWhy)
	}
	// 没有 abort 的运行（单元测试里常见）也不能炸。
	(&run{}).Stop("没人听")
}

// 结算判据接坏值：每一族都要红一次。
func TestSettlementArmsCatchEachBadShape(t *testing.T) {
	ok := func() []Event {
		return []Event{
			{V: 1, Session: "arm", Turn: 1, Seq: 1, Kind: "turn_accepted", State: "running", Evidence: "measured",
				Why: "不可续", Accept: &Accept{Limit: intp(20), StopVia: "x", Resumable: boolp(false), AckMS: int64p(5)}},
			{V: 1, Session: "arm", Turn: 1, Seq: 2, Kind: "shot_started", State: "running", Evidence: "measured",
				Step: &EventStep{No: intp(1), Name: "assemble"}},
			{V: 1, Session: "arm", Turn: 1, Seq: 3, Kind: "precheck", State: "ok", Evidence: "measured",
				Step: &EventStep{No: intp(1), Name: "kernel.check_call"}},
			{V: 1, Session: "arm", Turn: 1, Seq: 4, Kind: "turn_settled", State: "settled", Evidence: "measured",
				Settle: &Settle{Phase: "check_call", Why: "test:好样本，停在 check_call 这一相", Reached: []string{"accept", "work", "check_call"},
					NotReached: []string{"record_call"}, Kind: "refused",
					Counts: map[string]int{"running": 2, "ok": 1}, DoneBefore: 1, KPI: 1}},
		}
	}
	if err := ValidateSettlement(ok()); err != nil {
		t.Fatalf("好样本被判据拒了：%v", err)
	}
	cases := []struct {
		name   string
		mutate func(evs []Event) []Event
	}{
		{"说走过 record_call 却没有落账事件", func(e []Event) []Event {
			e[3].Settle.Reached = append(e[3].Settle.Reached, "record_call")
			e[3].Settle.NotReached = nil
			return e
		}},
		{"说没执行 record_call 却来了落账事件", func(e []Event) []Event {
			e[3].Settle.Reached = []string{"accept", "work", "check_call"}
			e[3].Settle.NotReached = []string{"record_call"}
			e[3].Settle.Counts = map[string]int{"running": 2, "ok": 2}
			return append(e, Event{V: 1, Session: "arm", Turn: 1, Seq: 5, Kind: "recorded", State: "ok", Evidence: "measured"})
		}},
		{"结算类别散写", func(e []Event) []Event { e[3].Settle.Kind = "done"; return e }},
		{"打断事件与结算类别不一致", func(e []Event) []Event { e[3].Kind = "turn_interrupted"; return e }},
		{"counts 少算一条", func(e []Event) []Event { e[3].Settle.Counts = map[string]int{"running": 2}; return e }},
		{"两条结算", func(e []Event) []Event {
			second := e[3]
			second.Seq = 5
			return append(e, second)
		}},
		{"结算没带载荷", func(e []Event) []Event { e[3].Settle = nil; return e }},
		{"run 级结算把散文塞进 stop", func(e []Event) []Event {
			return append(e, Event{V: 1, Session: "arm", Turn: 0, Seq: 1, Kind: "run_settled",
				State: "settled", Evidence: "measured", Stop: "跑完：端点剧本走到最后一发"})
		}},
	}
	for _, c := range cases {
		evs := c.mutate(ok())
		if err := ValidateSettlement(evs); err == nil {
			t.Fatalf("坏法 %q 没被抓到 —— 这一族针瞎了", c.name)
		}
	}
}

func kindsOf(evs []Event) string {
	seen := map[string]bool{}
	out := []string{}
	for _, e := range evs {
		if !seen[e.Kind] {
			seen[e.Kind] = true
			out = append(out, e.Kind)
		}
	}
	return strings.Join(out, ",")
}

func int64p(v int64) *int64 { return &v }

// waitForEvent 轮询事件文件直到出现某个 kind —— 让打断测试不依赖墙钟对撞。
func waitForEvent(path, kind string, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), "\"kind\":\""+kind+"\"") {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// waitForKind 等第 n 条某类事件落盘：打断那几支必须等到**账上已经有东西**再叫停，
// 否则断的是 0==0（M2 路 A 的 P1-①）。
func waitForKind(path, kind string, n int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		evs, err := ReadEvents(path)
		if err == nil {
			got := 0
			for _, e := range evs {
				if e.Kind == kind {
					got++
				}
			}
			if got >= n {
				return true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// 跨趟那一族的账：同一对落点连跑两趟，第二趟每发的 counts 不许把上一趟同发号的那几条算进来。
// 这是 M2 路 A 的 P0（真 exe 默认落点两跑即复现，五把尺子当时全都看不见这一形）。
func TestCrossRunCountsArePerRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xrun-counts.events.jsonl")
	for i := 0; i < 2; i++ {
		r := newRun(t, "happy", true, "included", 20)
		r.session = "shared-session"
		r.events = newEventBus(path)
		r.loop()
		r.closeOut()
	}
	evs := mustReadEvents(t, path)
	if err := ValidateSettlement(evs); err != nil {
		t.Fatalf("跨趟日志上结算判据红：%v", err)
	}
	// 逐块现数对账：结算里那份 counts 必须等于「这一发（session+run+turn）自己」的条数。
	type blk struct {
		session, run string
		turn         int
	}
	actual := map[blk]map[string]int{}
	order := []blk{}
	settled := map[blk]map[string]int{}
	for _, e := range evs {
		if e.Settle == nil {
			continue
		}
		k := blk{e.Session, e.Run, e.Turn}
		settled[k] = e.Settle.Counts
	}
	for _, e := range evs {
		if e.Settle != nil || settleKinds[e.Kind] {
			continue
		}
		k := blk{e.Session, e.Run, e.Turn}
		if _, ok := actual[k]; !ok {
			actual[k] = map[string]int{}
			order = append(order, k)
		}
		actual[k][e.State]++
	}
	if len(settled) < 2 {
		t.Fatalf("只有 %d 发结算，跨趟现场没造出来", len(settled))
	}
	// 只对有结算的那几发对账：趟级那一行（turn=0 的 run_started）本来就不结算，
	// 「每一发恰好结一次算」由 ValidateSettlement 自己管（它上面那行已经跑过）。
	for _, k := range order {
		if _, ok := settled[k]; !ok {
			continue
		}
		got, want := settled[k], actual[k]
		for st, n := range want {
			if got[st] != n {
				t.Fatalf("%s:%s 第 %d 发的 counts[%s]=%d，账上实际 %d 条 —— counts 把别的趟算进来了",
					k.session, k.run, k.turn, st, got[st], n)
			}
		}
		for st, n := range got {
			if want[st] != n {
				t.Fatalf("%s:%s 第 %d 发的 counts 多报了 %s=%d（账上只有 %d 条）",
					k.session, k.run, k.turn, st, n, want[st])
			}
		}
	}
}

// 叫停入口要经得起**并发**：SIGINT 协程与干活那一路可能同一刻都叫。
// check-then-act 的 select-default 会两头都走 default → close of closed channel → 进程死、收口全不执行。
func TestConcurrentStopClosesAbortExactlyOnce(t *testing.T) {
	for round := 0; round < 40; round++ {
		r := newRun(t, "happy", true, "included", 20)
		r.abort = make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				r.Stop(fmt.Sprintf("test:并发叫停-%d", i))
			}(i)
		}
		wg.Wait()
		select {
		case <-r.abort:
		default:
			t.Fatalf("第 %d 轮：并发叫停没把 abort 关掉 —— 循环收不到停", round)
		}
		if !strings.HasPrefix(r.stopWhy, "test:并发叫停-") {
			t.Fatalf("第 %d 轮：叫停凶手没记下来：%q", round, r.stopWhy)
		}
		// 关掉之后再说一次：不许二次 close，也不许换凶手（先到的那个算）。
		first := r.stopWhy
		r.Stop("test:后到的")
		if r.stopWhy != first {
			t.Fatalf("第 %d 轮：凶手被后发的覆盖了（%q → %q）", round, first, r.stopWhy)
		}
	}
}
