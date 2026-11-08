package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// E4 的针：投送层只读日志，游标就是事件自己的身份四元组。
//
// 这一族针全部走公开路径：真 Emit 落盘（或 raw JSONL 落盘）→ ReadEvents / HTTP 读回来 →
// 喂判据或直接比响应体。不在内存里造 Event 再判自己 —— 那族恒真的判据在 E2 已经被点过一次。

// sseRow 造一条盘上形状的事件行（run 轴要能显式写，跨趟那一族才有得造）。
func sseRow(seq int, session, run string, turn int, kind string, acc map[string]any) map[string]any {
	f := map[string]any{
		"v": 1, "session": session, "run": run, "turn": turn, "seq": seq,
		"ts": "2026-10-22T00:00:00Z", "kind": kind, "state": "running",
		"evidence": "measured", "elapsed_ms": 0,
	}
	if acc != nil {
		f["why"] = "断开不能续：拿来验判据的落盘形状"
		f["accept"] = acc
	}
	return f
}

func sseAccept(resumable any) map[string]any {
	a := map[string]any{"planned_shots": 3, "turn_limit": 20, "stop_via": "signal:SIGINT|in-proc:run.Stop(why)", "ack_ms": 5}
	if resumable != nil {
		a["resumable"] = resumable
	}
	return a
}

func httpGet(t *testing.T, url string, cursor string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("造请求：%v", err)
	}
	if cursor != "" {
		req.Header.Set("Last-Event-ID", cursor)
	}
	cl := &http.Client{Timeout: 3 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		t.Fatalf("GET %s：%v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应体：%v", err)
	}
	return resp.StatusCode, string(body)
}

// 针6 本体：同一个游标连打两次，必须得到逐条相同的 id 序列；且游标自己那条不许再播。
func TestReplayTwiceGivesSameSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.events.jsonl")
	r := newRun(t, "happy", true, "included", 20)
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()

	evs := mustReadEvents(t, path)
	if len(evs) < 6 {
		t.Fatalf("这一趟只落了 %d 条，造不出「游标之后还有东西」的现场", len(evs))
	}
	mid := evs[3]
	cur := formatCursor(mid)
	addr, stop, err := serveEvents(path)
	if err != nil {
		t.Fatalf("起投送：%v", err)
	}
	t.Cleanup(stop)
	url := "http://" + addr + "/events"

	code1, body1 := httpGet(t, url, cur)
	code2, body2 := httpGet(t, url, cur)
	if code1 != 200 || code2 != 200 {
		t.Fatalf("接得上却给了非 200：%d / %d", code1, code2)
	}
	ids1, ids2 := idsOf(body1), idsOf(body2)
	if strings.Join(ids1, "|") != strings.Join(ids2, "|") {
		t.Fatalf("重放两次序列不同：\n first=%v\n second=%v", ids1, ids2)
	}
	want := []string{}
	for _, e := range evs[4:] {
		want = append(want, formatCursor(e))
	}
	if strings.Join(ids1, "|") != strings.Join(want, "|") {
		t.Fatalf("重放尾部与日志自己的尾部不一致：\n 给的=%v\n 应得=%v", ids1, want)
	}
	for _, id := range ids1 {
		if id == cur {
			t.Fatalf("游标那条又被播了一遍：%s", cur)
		}
	}
	if len(ids1) == 0 {
		t.Fatal("游标之后明明还有事件，重放却一条都没给")
	}
}

// 跨趟那一族：同一个 session 连跑两趟，游标落在第一趟中间 —— 尾部必须按日志自己的全局序号排，
// 不能按 (turn,seq) 排（第二趟的 turn=1 会插到第一趟的 turn=2 之前，顺序就错了）。
func TestCrossRunReplayKeepsLogOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "xrun.events.jsonl")
	for i := 0; i < 2; i++ {
		r := newRun(t, "happy", true, "included", 20)
		r.session = "cross-run"
		r.events = newEventBus(path)
		r.loop()
		r.closeOut()
	}
	evs := mustReadEvents(t, path)
	runs := map[string]bool{}
	for _, e := range evs {
		runs[e.Run] = true
	}
	if len(runs) < 2 {
		t.Fatalf("两趟跑完只见到 %d 个 run 轴，跨趟现场没造出来", len(runs))
	}
	firstRunEnd := -1
	for i, e := range evs {
		if e.Run == evs[0].Run {
			firstRunEnd = i
		}
	}
	if firstRunEnd < 1 || firstRunEnd == len(evs)-1 {
		t.Fatalf("第一趟的尾部位置不对（%d / %d 条）", firstRunEnd, len(evs))
	}
	cur := formatCursor(evs[1]) // 落在第一趟里面
	addr, stop, err := serveEvents(path)
	if err != nil {
		t.Fatalf("起投送：%v", err)
	}
	t.Cleanup(stop)
	code, body := httpGet(t, "http://"+addr+"/events", cur)
	if code != 200 {
		t.Fatalf("跨趟日志上的合法游标被拒：%d %s", code, body)
	}
	got := idsOf(body)
	want := []string{}
	for _, e := range evs[2:] {
		want = append(want, formatCursor(e))
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("跨趟重放不是日志顺序（说明还按 (turn,seq) 排）：\n 给的=%v\n 应得=%v", got, want)
	}
	// 第二趟的 turn=1 必须排在第一趟的高 turn 之后 —— 直接点名这一族。
	secondRunStart := -1
	for i, e := range evs {
		if e.Run != evs[0].Run {
			secondRunStart = i
			break
		}
	}
	if secondRunStart > 2 {
		if got[secondRunStart-2] != formatCursor(evs[secondRunStart]) {
			t.Fatalf("第二趟第一条排错了位置：应给 %s，实给 %s", formatCursor(evs[secondRunStart]), got[secondRunStart-2])
		}
	}
}

// 游标必须带 run：三段形状（没有 run 轴）不许被当成首连从头播。
func TestCursorCarriesRunAxis(t *testing.T) {
	e := Event{Session: "s", Run: "r1", Turn: 2, Seq: 9}
	cur := formatCursor(e)
	if got := strings.Count(cur, sseCursorSep); got != 3 {
		t.Fatalf("游标 %q 不是四段（分隔符 %d 次）—— run 轴没进去", cur, got)
	}
	c, err := parseCursor(cur)
	if err != nil {
		t.Fatalf("自己造的游标解不开：%v", err)
	}
	if c.session != "s" || c.run != "r1" || c.turn != 2 || c.seq != 9 {
		t.Fatalf("游标解析回去不对：%+v", c)
	}
	if _, err := parseCursor("s:2:9"); err == nil {
		t.Fatal("三段游标（旧形状，没有 run）被接了 —— 那会把跨趟的两发当成一发")
	}
	if _, err := parseCursor(""); err == nil {
		t.Fatal("空游标不该由 parseCursor 负责，判据里另有首连那一支")
	}
}

// 坏游标不当「从头来」：接不上就是接不上，响应里一条事件都不许播。
func TestBadCursorIsNotTreatedAsHead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "badcur.events.jsonl")
	r := newRun(t, "happy", true, "included", 20)
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()

	addr, stop, err := serveEvents(path)
	if err != nil {
		t.Fatalf("起投送：%v", err)
	}
	t.Cleanup(stop)
	for _, bad := range []string{"垃圾", "s:1:2:3:4", "s:r1:x:2", "s:r1:1:y"} {
		code, body := httpGet(t, "http://"+addr+"/events", bad)
		if code != http.StatusConflict {
			t.Fatalf("坏游标 %q 给了 %d，应 409：%s", bad, code, body)
		}
		if strings.Contains(body, "data: {") {
			t.Fatalf("坏游标 %q 被当成首连，把整本日志播了一遍", bad)
		}
	}
	// 不带游标 = 首连，必须播全量（对照，别让 409 那一路冒充成「一切正常」）。
	code, body := httpGet(t, "http://"+addr+"/events", "")
	if code != 200 || len(idsOf(body)) == 0 {
		t.Fatalf("首连应当播全量：%d，%d 条 id", code, len(idsOf(body)))
	}
	if n := len(mustReadEvents(t, path)); len(idsOf(body)) != n {
		t.Fatalf("首连播了 %d 条，日志里 %d 条 —— 对不上", len(idsOf(body)), n)
	}
}

// 日志读不动就是读不动：503，不假装能续。
func TestUnreadableLogGives503(t *testing.T) {
	dir := t.TempDir()
	addr, stop, err := serveEvents(filepath.Join(dir, "根本没有这个文件.jsonl"))
	if err != nil {
		t.Fatalf("起投送：%v", err)
	}
	t.Cleanup(stop)
	code, body := httpGet(t, "http://"+addr+"/events", "")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("日志不在却给了 %d：%s", code, body)
	}
	if strings.Contains(body, "data: {") {
		t.Fatalf("读不动还在播事件：%s", body)
	}
}

// resumable 是算出来的，不是约定：三种落盘形态各给各的话。
func TestResumableIsMeasuredNotAssumed(t *testing.T) {
	// ① 落点在盘上、读得回来 → true
	dir := t.TempDir()
	good := filepath.Join(dir, "ok.events.jsonl")
	r := newRun(t, "happy", true, "included", 20)
	r.events = newEventBus(good)
	defer r.events.Close() // 句柄不还，TempDir 清不掉（Windows 上会咬）
	r.turns = 1
	if ev := r.acceptedEv(); ev.Accept.Resumable == nil || !*ev.Accept.Resumable {
		t.Fatalf("日志可读却不算成能续：%v / %q", ev.Accept.Resumable, ev.Why)
	}
	// ② 事件层没开（nil 总线）→ false 且说为什么
	r2 := newRun(t, "happy", true, "included", 20)
	r2.turns = 1
	if ev := r2.acceptedEv(); ev.Accept.Resumable == nil || *ev.Accept.Resumable || ev.Why == "" {
		t.Fatalf("事件层没开却给了 %v（why=%q）", ev.Accept.Resumable, ev.Why)
	}
	// ③ 既有日志读不回 → 总线降级、落点清空 → false，且理由点名是这本账的问题。
	// （不用「跑一半把文件删了」造这一形：Windows 上总线还握着句柄，删不动 —— 那种形状本机造不出来，
	// 真要钉得靠跨进程，记 NOT_RUN，不在这里冒充成已验。）
	torn := filepath.Join(dir, "torn.events.jsonl")
	if err := os.WriteFile(torn, []byte("{\"v\":1,\"turn\":1,\"seq\":1}\n{\" 这条是撕裂的\n"), 0o644); err != nil {
		t.Fatalf("造撕裂日志：%v", err)
	}
	b3 := newEventBus(torn)
	if b3.degraded == "" {
		t.Fatal("撕裂日志没让总线降级 —— 后面那半支不作数")
	}
	r3 := newRun(t, "happy", true, "included", 20)
	r3.events = b3
	r3.turns = 1
	ev3 := r3.acceptedEv()
	if ev3.Accept.Resumable == nil || *ev3.Accept.Resumable {
		t.Fatalf("降级的账还写能续：%v", ev3.Accept.Resumable)
	}
	if !strings.Contains(ev3.Why, "降级") {
		t.Fatalf("理由没点名是这本账降级了：%q", ev3.Why)
	}
	// ④ 内存模式（没落点）→ false，且不许冒充「可续」字样
	memOnly := newRun(t, "happy", true, "included", 20)
	memOnly.events = newEventBus("")
	memOnly.turns = 1
	ev4 := memOnly.acceptedEv()
	if ev4.Accept.Resumable == nil || *ev4.Accept.Resumable {
		t.Fatalf("内存模式写成了能续：%v", ev4.Accept.Resumable)
	}
	if reCanResume.MatchString(ev4.Why) || reCanResume.MatchString(ev4.Stop) {
		t.Fatalf("不可续那一路的流里出现了「可续」字样：%q —— 判据会把它当谎", ev4.Why)
	}
	// 这一支不喂 ValidateAccepts：内存模式没有可读回的落点，喂进去的只能是自己造的指针，
	// ack_ms 那一格本来就由 Emit 填 —— 拿它判「内存模式诚实」是恒红，不叫判据。
	// 走盘那一半由 TestReceiptPayloadIsHonest 与 TestResumableFieldSurvivesFalse 钉。
	if ev4.Accept.AckMS != nil {
		t.Fatalf("ack_ms 应由 Emit 落盘时填，构造期不许带：%v", *ev4.Accept.AckMS)
	}
}

// 判据的坏形状表：每种都从盘上读回来喂（序列化那一层也要走到）。
func TestReplayArmsCatchEachBadShape(t *testing.T) {
	cases := []struct {
		name   string
		rows   []map[string]any
		cursor string
		want   string // "" = 这一支必须绿
	}{
		{
			name:   "游标那条不在，回执却写 resumable=true（声称能续却续不了）",
			rows:   []map[string]any{sseRow(1, "arm", "rA", 1, "turn_accepted", sseAccept(true)), sseRow(2, "arm", "rA", 1, "shot_started", nil)},
			cursor: "arm:rA:1:99",
			want:   "resumable=true",
		},
		{
			name:   "游标那条不在，回执压根没写 resumable（nil=没量过）",
			rows:   []map[string]any{sseRow(1, "arm", "rA", 1, "turn_accepted", sseAccept(nil)), sseRow(2, "arm", "rA", 1, "shot_started", nil)},
			cursor: "arm:rA:1:99",
			want:   "nil=没量过",
		},
		{
			name:   "游标那条不在，但那一发老实写了不可续 —— 接不上是诚实结果",
			rows:   []map[string]any{sseRow(1, "arm", "rA", 1, "turn_accepted", sseAccept(false)), sseRow(2, "arm", "rA", 1, "shot_started", nil)},
			cursor: "arm:rA:1:99",
			want:   "",
		},
		{
			name:   "游标那条不在且那一发连受理都没有",
			rows:   []map[string]any{sseRow(1, "arm", "rA", 1, "shot_started", nil), sseRow(2, "arm", "rA", 1, "step_recorded", nil)},
			cursor: "arm:rA:1:99",
			want:   "连受理事件都没有",
		},
		{
			name: "游标来自另一本账（身份在这本里根本没有，且没有对应受理）",
			rows: []map[string]any{
				sseRow(1, "other", "rB", 7, "turn_accepted", sseAccept(true)),
				sseRow(2, "other", "rB", 7, "shot_started", nil),
			},
			cursor: "arm:rA:1:1",
			want:   "连受理事件都没有",
		},
		{
			name: "身份四元组撞车：两条一模一样，重放指不准是哪条",
			rows: []map[string]any{
				sseRow(1, "arm", "rA", 1, "turn_accepted", sseAccept(true)),
				sseRow(1, "arm", "rA", 1, "turn_accepted", sseAccept(true)),
				sseRow(2, "arm", "rA", 1, "shot_started", nil),
			},
			cursor: "arm:rA:1:1",
			want:   "同身份记录",
		},
		{
			name:   "游标接得上（对照，必须绿）",
			rows:   []map[string]any{sseRow(1, "arm", "rA", 1, "turn_accepted", sseAccept(true)), sseRow(2, "arm", "rA", 1, "shot_started", nil), sseRow(3, "arm", "rA", 2, "turn_accepted", sseAccept(true))},
			cursor: "arm:rA:1:1",
			want:   "",
		},
	}
	for _, tc := range cases {
		evs := writeRawJudgeable(t, "replay-arm.events.jsonl", tc.rows...)
		err := ValidateReplay(evs, tc.cursor)
		if tc.want == "" {
			if err != nil {
				t.Fatalf("%s：绿形被判红了 —— %v", tc.name, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s：判据没红（这根臂没牙）", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s：红了但指错了地方， want=%q got=%q", tc.name, tc.want, err.Error())
		}
	}
}

// 游标解不开时，判据与投送层必须是同一句话 —— 各写一套就会一处绿一处红（E2 的教训）。
func TestReplayJudgeAndHandlerAgreeOnBadCursor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "same.events.jsonl")
	r := newRun(t, "happy", true, "included", 20)
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()
	evs := mustReadEvents(t, path)
	for _, c := range []string{"垃圾", "s:1:2", "s:r1:1:x"} {
		judge := fmt.Sprint(ValidateReplay(evs, c))
		addr, stop, err := serveEvents(path)
		if err != nil {
			t.Fatalf("起投送：%v", err)
		}
		code, body := httpGet(t, "http://"+addr+"/events", c)
		stop()
		if code != http.StatusConflict {
			t.Fatalf("游标 %q：判据红（%s）却给了 %d", c, judge, code)
		}
		if !strings.Contains(body, strings.TrimPrefix(judge, "续传判断没过：")) && !strings.Contains(body, judge) {
			t.Fatalf("游标 %q：投送层与判据说了两句话\n 判据=%s\n 响应=%s", c, judge, body)
		}
	}
}

// 投送层不许自带第二本账：它给的每条 id 都必须在日志里找得到，且载荷逐条能解回同一个身份。
func TestServedFramesAreLogRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "frames.events.jsonl")
	r := newRun(t, "happy", true, "included", 20)
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()
	evs := mustReadEvents(t, path)

	addr, stop, err := serveEvents(path)
	if err != nil {
		t.Fatalf("起投送：%v", err)
	}
	t.Cleanup(stop)
	code, body := httpGet(t, "http://"+addr+"/events", "")
	if code != 200 {
		t.Fatalf("首连给了 %d：%s", code, body)
	}
	frames := 0
	for _, block := range strings.Split(body, "\n\n") {
		if !strings.Contains(block, "data: ") {
			continue
		}
		frames++
		var line string
		var id string
		for _, l := range strings.Split(block, "\n") {
			if strings.HasPrefix(l, "id: ") {
				id = strings.TrimPrefix(l, "id: ")
			}
			if strings.HasPrefix(l, "data: ") {
				line = strings.TrimPrefix(l, "data: ")
			}
		}
		if id == "" || line == "" {
			t.Fatalf("帧不完整（id=%q data=%q）", id, line)
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("帧载荷解不开：%v / %s", err, line)
		}
		if formatCursor(e) != id {
			t.Fatalf("帧的 id 与它自己的载荷不是同一条：%s vs %s", id, formatCursor(e))
		}
		onDisk := false
		for _, d := range evs {
			if formatCursor(d) == id {
				onDisk = true
				break
			}
		}
		if !onDisk {
			t.Fatalf("投送层播了一条日志里没有的：%s（那就是自带第二本账）", id)
		}
	}
	if frames != len(evs) {
		t.Fatalf("播了 %d 帧，日志里 %d 条 —— 投送层在偷偷丢或偷偷加", frames, len(evs))
	}
}

// 投送层要在**跑的过程中**就能读这本账：总线还握着写句柄时，读一侧必须打得开、读得回已落的那些行。
// 这条不验的话，E4 的「实时」就是假的 —— 只能等收口后才能播，界面拿到的永远是事后账。
func TestLogIsReadableWhileBusHoldsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.events.jsonl")
	b := newEventBus(path)
	r := newRun(t, "happy", true, "included", 20)
	r.events = b
	r.turns = 1
	if err := b.Emit(r.acceptedEv()); err != nil {
		t.Fatalf("落第一条：%v", err)
	}
	addr, stop, err := serveEvents(path)
	if err != nil {
		t.Fatalf("起投送：%v", err)
	}
	defer stop() // 这一支要在总线还开着的时候读，所以不能等 closeOut
	got, err := ReadEvents(path)
	if err != nil {
		t.Fatalf("总线握着写句柄时读不回来：%v", err)
	}
	if len(got) != 1 {
		t.Fatalf("读回 %d 条，应 1 条", len(got))
	}
	code, body := httpGet(t, "http://"+addr+"/events", "")
	if code != 200 {
		t.Fatalf("跑的过程中首连给了 %d：%s", code, body)
	}
	if len(idsOf(body)) != 1 {
		t.Fatalf("跑的过程中播了 %d 条，盘上 1 条 —— 投送层跟不上还在写的这本账", len(idsOf(body)))
	}
	// 再落一条，游标取第一条：重放必须只给新落的那一条（边写边续，这才叫实时）。
	second := r.acceptedEv()
	second.Turn = 2
	if err := b.Emit(second); err != nil {
		t.Fatalf("落第二条：%v", err)
	}
	code, body = httpGet(t, "http://"+addr+"/events", formatCursor(got[0]))
	if code != 200 {
		t.Fatalf("边写边续给了 %d：%s", code, body)
	}
	// 游标从**读回来的那条**取：Emit 在盘上才盖 run 轴，调用方那份结构体里 Run 是空的。
	back, err := ReadEvents(path)
	if err != nil || len(back) != 2 {
		t.Fatalf("第二条没读回来：%d 条 / %v", len(back), err)
	}
	ids := idsOf(body)
	if len(ids) != 1 || ids[0] != formatCursor(back[1]) {
		t.Fatalf("游标之后应只播第二条，实播 %v", ids)
	}
	if formatCursor(got[0]) != formatCursor(back[0]) {
		t.Fatalf("同一个游标两次读回来的身份不一样：%s vs %s", formatCursor(got[0]), formatCursor(back[0]))
	}
	b.Close()
}

// 只认 /events 这一条路：别的路径必须 404，不拿「任何路径都播事件」冒充路由。
// 这一支是给 eventsMux 配的针 —— 摘掉 mux 就只有这里会红，否则那一步是没人走的枝。
func TestOtherPathGives404(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mux.events.jsonl")
	r := newRun(t, "happy", true, "included", 20)
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()
	addr, stop, err := serveEvents(path)
	if err != nil {
		t.Fatalf("起投送：%v", err)
	}
	t.Cleanup(stop)
	// "/" 从 E7 起是那张三行页（见 events_e7_page_test.go），所以这里只列**没有**的路径。
	for _, p := range []string{"/event", "/events/extra", "/api/events", "/page", "/index.html"} {
		code, body := httpGet(t, "http://"+addr+p, "")
		if code != http.StatusNotFound {
			t.Fatalf("%s 给了 %d（应 404）：%s", p, code, body)
		}
	}
	if code, _ := httpGet(t, "http://"+addr+"/events", ""); code != 200 {
		t.Fatalf("/events 自己反倒 %d —— 404 那几发是路由没接上，不是判据在管", code)
	}
}
