package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// E8：事件流与轨迹页同源 —— 一份账、两个投影。
// 这条针唯一要证的事：轨迹页上的**发数与逐发条数**是从那本事件日志读的，不是 trace 侧自己再算一遍。
// 两个数本来就分得开（`r.shot` 数的是**落账的 shot**，被拒/装不出的发不算），
// 所以负载必须真让它们不相等 —— 相等的话这支针是空的，先叫出来而不是默默通过。

// turnsByRun 从盘上那本日志按 run 数回执条数：测试自己数，不调实现里的 helper（否则循环论证）。
func turnsByRun(t *testing.T, path string, session string) (map[string]int, []string) {
	t.Helper()
	per, order := map[string]int{}, []string{}
	for _, e := range mustReadEvents(t, path) {
		if e.Session != session || e.Kind != "turn_accepted" {
			continue
		}
		if _, has := per[e.Run]; !has {
			order = append(order, e.Run)
		}
		per[e.Run]++
	}
	return per, order
}

func tracePage(t *testing.T, r *run) string {
	t.Helper()
	r.loop()
	r.closeOut()
	return string(mustRead(t, r.tracePath))
}

// 1) 开事件层：页上的发数 = 日志数出来的，且与 r.shot 不同（现场分得开）。
// 拉开这两个数用的是「驱动这一路 NOT_RUN」那一形：回执已经落进流里（发数 1），
// 而一发都没落账（r.shot=0）。拿「有被拒发的剧本」是拉不开的 —— 实测 r.shot 与发数都为 9。
func TestTraceTurnCountComesFromEventLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "e8.events.jsonl")
	drv, err := buildDriver("session", "")
	if err != nil {
		t.Fatalf("造驱动：%v", err)
	}
	r := newRun(t, "happy", true, "included", 20)
	r.drv = drv
	r.events = newEventBus(path)
	r.tracePath = filepath.Join(dir, "trace.html")
	h := tracePage(t, r)

	per, order := turnsByRun(t, path, r.session)
	if len(order) != 1 {
		t.Fatalf("这发负载本该只有一趟，日志里出现 %d 趟：%v", len(order), order)
	}
	turns := per[order[0]]
	if turns == r.shot {
		t.Fatalf("现场分不开：发数 %d == 落账 shot %d —— 这支针在此负载下抓不到任何东西", turns, r.shot)
	}
	want := fmt.Sprintf("事件日志数出来的这一趟共 %d 发", turns)
	if !strings.Contains(h, want) {
		t.Fatalf("页上没出现 %q（r.shot=%d）—— 发数还是 trace 侧自己算的那一份", want, r.shot)
	}
	if strings.Contains(h, fmt.Sprintf("共 %d 发", r.shot)) {
		t.Errorf("页上仍印着内存里的 shot 数 %d 当发数", r.shot)
	}
	if !strings.Contains(h, fmt.Sprintf("每发 49 格 × %d 发 = %d", turns, turns*49)) {
		t.Errorf("那句「每发 49 格 × 发数」没用日志里的发数（应 %d×%d=%d）", turns, 49, turns*49)
	}
	// 逐发条数也要在页上，并且每一发都对得上盘上的数
	counted := map[int]int{}
	for _, e := range mustReadEvents(t, path) {
		if e.Session == r.session && e.Run == order[0] && e.Turn > 0 {
			counted[e.Turn]++
		}
	}
	for turn, n := range counted {
		wantPair := fmt.Sprintf("%d:%d", turn, n)
		if !strings.Contains(h, wantPair) {
			t.Errorf("第 %d 发盘上 %d 条，页上的逐发条数里没有这一对（%s）", turn, n, wantPair)
		}
	}
	// 总条数那一格也不许是另一份账
	if !strings.Contains(h, fmt.Sprintf("这一趟在事件日志里共 %d 条", lenAll(t, path, r.session, order[0]))) {
		t.Error("页上的总条数与日志对不上")
	}
}

func lenAll(t *testing.T, path, session, run string) int {
	t.Helper()
	n := 0
	for _, e := range mustReadEvents(t, path) {
		if e.Session == session && e.Run == run {
			n++
		}
	}
	return n
}

// 2) 没开事件层：不许退回内存里那个数顶 —— 这一格是「没量过」。
func TestTraceWithoutEventLogSaysNotMeasured(t *testing.T) {
	dir := t.TempDir()
	r := newRun(t, "happy", true, "included", 20)
	r.tracePath = filepath.Join(dir, "trace.html")
	h := tracePage(t, r)
	if r.shot == 0 {
		t.Fatal("负载本身没落账，这一支不作数")
	}
	if !strings.Contains(h, "共几发：没量过") {
		t.Errorf("事件层关着却印了一个发数 —— 那是把没量过写成有数（r.shot=%d）", r.shot)
	}
	if strings.Contains(h, fmt.Sprintf("共 %d 发", r.shot)) {
		t.Errorf("还是拿 shot 数当了发数：%d", r.shot)
	}
	if !strings.Contains(h, "每发 49 格 × 几发") || !strings.Contains(h, "只说没量过") {
		t.Error("那句乘法在没日志时仍然给了一个积 —— 应该只说没量过")
	}
}

// 3) 同一对路径上追加过两趟：这一页只数**这一趟**，上一趟不算进来。
func TestTraceTurnCountIsPerRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "two.events.jsonl")
	r1 := newRun(t, "happy", true, "included", 20)
	r1.session = "two-runs"
	r1.events = newEventBus(path)
	r1.loop()
	r1.closeOut() // 第一趟只落账，不出页：这一支要看的是第二趟那页数到几发

	r2 := newRun(t, "happy", true, "included", 20)
	r2.session = "two-runs"
	r2.events = newEventBus(path) // append-only：同一本日志第二趟
	r2.tracePath = filepath.Join(dir, "trace2.html")
	h := tracePage(t, r2)

	per, order := turnsByRun(t, path, "two-runs")
	if len(order) != 2 {
		t.Fatalf("本该两趟，日志里 %d 趟：%v —— 跨趟现场没造出来", len(order), order)
	}
	// 第二趟的 run 号从盘上取（最后一条回执带的就是它），不依赖实现的 accessor
	last := order[len(order)-1]
	own, union := per[last], 0
	for _, k := range order {
		union += per[k]
	}
	if own == union {
		t.Fatal("两趟发数一样？union==own 时这支针抓不到混列")
	}
	if strings.Contains(h, fmt.Sprintf("事件日志数出来的这一趟共 %d 发", union)) {
		t.Fatalf("页上把上一趟的发数也算进来了（共 %d 发应为 %d 发）", union, own)
	}
	if !strings.Contains(h, fmt.Sprintf("事件日志数出来的这一趟共 %d 发", own)) {
		t.Errorf("页上没印这一趟自己的发数 %d（应只数 run=%s）", own, last)
	}
	// 逐发条数：这一页只出现自己那一趟的发号（1..own），上一趟那条列不该混进来
	for turn := 1; turn <= own; turn++ {
		if !strings.Contains(h, fmt.Sprintf("%d:%d", turn, per2ForRun(t, path, "two-runs", last, turn))) {
			t.Errorf("第 %d 发的逐发条数没和盘上对上（这一趟 run=%s）", turn, last)
		}
	}
}

// per2ForRun 盘上某一趟某一发的条数（测试自己数，不调实现）。
func per2ForRun(t *testing.T, path, session, run string, turn int) int {
	t.Helper()
	n := 0
	for _, e := range mustReadEvents(t, path) {
		if e.Session == session && e.Run == run && e.Turn == turn {
			n++
		}
	}
	return n
}
