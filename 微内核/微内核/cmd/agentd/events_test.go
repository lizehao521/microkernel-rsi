package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// E1 要证的几件事，一件一支：
//   序号在同一发内严格递增，且判据是从**可达**的那条路径（读回整本日志）走的；
//   absent 没带 why、档名不认识、state 乱写 —— 三种不合格都当场拒且不落盘；
//   append-only 是真的：第二趟接在同一本日志后面，序号不重来；
//   开不开事件层，stdout 那一份正文逐字相同（诊断一律走 stderr）；
//   事件里不许出现原话正文（默认不显示 payload，与轨迹页同一条禁区）；
//   落在哪 / 落了几条 / 为什么没落成 —— 三态都要说得出来。

func TestEventSeqMonotonicWithinEachTurn(t *testing.T) {
	dir := t.TempDir()
	// happy 剧本 = 会走到「跑完」那一种停；full 剧本按 POLICY 提前停，那种停在下面第二支里单独判。
	r := newRun(t, "happy", true, "included", 20)
	evPath := filepath.Join(dir, "run.events.jsonl")
	r.journalPath = filepath.Join(dir, "journal.jsonl")
	r.events = newEventBus(evPath)
	r.loop()
	r.closeOut()

	evs, err := ReadEvents(evPath)
	if err != nil {
		t.Fatalf("事件读不回来：%v", err)
	}
	if len(evs) == 0 {
		t.Fatalf("事件层开了却一条都没落")
	}
	if len(evs) != r.events.written {
		t.Fatalf("文件里 %d 条，总线自印 %d 条 —— 两个数不许并存", len(evs), r.events.written)
	}
	// 判据走可达路径：ValidateLog 判的是**读回来的整本日志**。
	// 早先这条判据写在 Emit 里，next 恒等于 prev+1，是不可达死牙（M2 两路都点到）。
	if err := ValidateLog(evs); err != nil {
		t.Fatalf("读回来的日志序号不合格：%v", err)
	}
	seen := map[string]bool{}
	for _, e := range evs {
		if e.Session != r.session {
			t.Fatalf("事件缺 session：%+v", e)
		}
		seen[e.Kind] = true
		if e.Evidence == "absent" && e.Why == "" {
			t.Fatalf("落盘的 absent 事件没带 why：%+v", e)
		}
	}
	for _, k := range []string{"run_started", "shot_started", "precheck", "recorded", "run_settled"} {
		if !seen[k] {
			t.Fatalf("事件种类 %q 一条都没有 —— 挂点没生效（现有：%v）", k, keysOf(seen))
		}
	}
	settled := lastSettle(t, evs)
	if settled.Progress == nil || settled.Progress.Total == nil || settled.Progress.Limit == nil {
		t.Fatalf("收口事件缺 done/total/limit：%+v", settled)
	}
	// full 剧本这一趟是「跑完」那一种停：total 必须等于 done。
	// 拿额度当 total 会把跑完的停在流里读成 45%，那是给界面灌假进度。
	if r.stop != stopScript {
		t.Fatalf("这一趟本该按剧本跑完，实际停在 %q —— 下面两条断言的现场没了", r.stop)
	}
	if settled.Progress.Done != r.shot || *settled.Progress.Total != r.shot {
		t.Fatalf("跑完那一趟的进度：done=%d total=%d，都应等于发数 %d",
			settled.Progress.Done, *settled.Progress.Total, r.shot)
	}
	if *settled.Progress.Limit != r.maxTurns {
		t.Fatalf("额度另放这一格没对上：limit=%d 应 %d", *settled.Progress.Limit, r.maxTurns)
	}
	if settled.State != "settled" {
		t.Fatalf("收口事件的 state=%q —— 六态整句话不该塞进 state（要按档归并的字段塞了散文）", settled.State)
	}
	if settled.Stop != r.stop.Token() || settled.StopWhy != string(r.stop) {
		t.Fatalf("收口事件的 stop=%q / stop_why=%q 与运行态 %q（token %q）不符 —— 散文该走 stop_why",
			settled.Stop, settled.StopWhy, r.stop, r.stop.Token())
	}
	// 收口是 run 级：不属于任何一发，也不是某一步。
	// 发号塞进 step.no 会让 (turn, step.no) 同时表示「第 N 步」与「共发了 N 发」。
	if settled.Turn != 0 || settled.Step != nil {
		t.Fatalf("收口那条带了发级坐标（turn=%d step=%+v）—— run 级事件不该有 step", settled.Turn, settled.Step)
	}
	// elapsed_ms 要有个消费者，否则它是个没人对的装饰（§5.3 时延预算由它起步）。
	for i := 1; i < len(evs); i++ {
		if evs[i].ElapsedMS < evs[i-1].ElapsedMS {
			t.Fatalf("elapsed_ms 回退了（第 %d 条 %d < 第 %d 条 %d）", i+1, evs[i].ElapsedMS, i, evs[i-1].ElapsedMS)
		}
	}
}

// 额度到那一种停：total 保持额度，done 小于 total —— 这才叫没跑完。
func TestSettleProgressKeepsQuotaWhenNotFinished(t *testing.T) {
	r := newRun(t, "full", true, "included", 3)
	r.events = newEventBus(filepath.Join(t.TempDir(), "q.events.jsonl"))
	r.loop()
	r.closeOut()
	if r.stop != stopTurns {
		t.Fatalf("这一趟该停在额度上，实际 %q", r.stop)
	}
	settled := lastSettle(t, mustReadEvents(t, r.events.path))
	if settled.Progress.Done != r.shot || *settled.Progress.Total != r.maxTurns {
		t.Fatalf("额度停的进度读错了：done=%d total=%d（应 %d / %d）",
			settled.Progress.Done, *settled.Progress.Total, r.shot, r.maxTurns)
	}
}

func TestAbsentWithoutWhyIsRefusedAndNotWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.events.jsonl")
	b := newEventBus(path)
	defer b.Close()
	if err := b.Emit(Event{Kind: "step_finished", State: "running", Turn: 1, Evidence: "absent"}); err == nil {
		t.Fatalf("absent 没带 why 居然落了 —— 这正是「把没量到写成 0」那一族")
	}
	if err := b.Emit(Event{Kind: "step_finished", State: "running", Turn: 1, Evidence: "guessed"}); err == nil {
		t.Fatalf("出处档不认识也落了：五档之外的词不该进这本账")
	}
	if err := b.Emit(Event{Kind: "", State: "running", Turn: 1, Evidence: "measured"}); err == nil {
		t.Fatalf("没有 kind 的事件也落了")
	}
	// 五档有门禁而 state 没有，是不对称的：下游按 state 归并会被 "运行中"/"running " 这类散写打散。
	if err := b.Emit(Event{Kind: "step_finished", State: "跑完了", Turn: 1, Evidence: "measured"}); err == nil {
		t.Fatalf("state 乱写也落了：白名单没生效")
	}
	if b.written != 0 {
		t.Fatalf("四条全不合格，却自印落了 %d 条", b.written)
	}
	if err := b.Emit(Event{Kind: "step_finished", State: "running", Turn: 1, Evidence: "absent", Why: "这一发没走到读回"}); err != nil {
		t.Fatalf("带 why 的 absent 该被收：=%v", err)
	}
	if b.written != 1 {
		t.Fatalf("合格那条没计数：%d", b.written)
	}
}

// 臂（叶子）：序号判据必须接得住「重复」与「回退」两种坏值。
// 判据本体只管坏值，坏值必须由**公开路径**喂进去 —— 所以这一支喂的是 crafted 日志，
// 不是总线自己自增出来的那份理想序列（那种序列恒过，等于没牙）。
func TestValidateLogArmRejectsDuplicateAndRegress(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string, seqs []int) string {
		p := filepath.Join(dir, name)
		var buf bytes.Buffer
		for i, s := range seqs {
			line, err := json.Marshal(Event{V: 1, Session: "arm", Turn: 1, Seq: s, Kind: "precheck",
				State: "ok", Evidence: "measured", TS: fmt.Sprintf("t%d", i)})
			if err != nil {
				t.Fatalf("造坏日志：%v", err)
			}
			buf.Write(append(line, '\n'))
		}
		if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("写坏日志：%v", err)
		}
		return p
	}
	dup := mk("dup.events.jsonl", []int{1, 2, 2, 3})
	if err := ValidateLog(mustReadEvents(t, dup)); err == nil {
		t.Fatalf("重复序号 2→2 没被抓到 —— 这根针是恒绿的")
	}
	reg := mk("reg.events.jsonl", []int{1, 2, 3, 2})
	if err := ValidateLog(mustReadEvents(t, reg)); err == nil {
		t.Fatalf("回退序号 3→2 没被抓到 —— 这根针是恒绿的")
	}
	if err := ValidateLog(mustReadEvents(t, mk("ok.events.jsonl", []int{1, 2, 3, 4}))); err != nil {
		t.Fatalf("正常递增被误伤：%v", err)
	}
	// 判据本体单独再咬一次：坏值直接喂 seqCanFollow。
	if err := seqCanFollow(3, 3); err == nil {
		t.Fatal("重复 3→3 没被抓到")
	}
	if err := seqCanFollow(3, 2); err == nil {
		t.Fatal("回退 3→2 没被抓到")
	}
}

// P0 的那一条：旗标文案写 append-only，实现却是 O_TRUNC —— 把「断线续传要读的那本底」抹了。
// 这一支就是它的臂：同一本日志开两次，第二趟必须接在后面、序号不重来。
func TestEventLogAppendsAcrossRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "same.events.jsonl")
	first := newEventBus(path)
	for i := 0; i < 3; i++ {
		if err := first.Emit(Event{Kind: "precheck", State: "ok", Evidence: "measured", Turn: 1}); err != nil {
			t.Fatalf("第一趟落不去：%v", err)
		}
	}
	first.Close()
	second := newEventBus(path)
	for i := 0; i < 2; i++ {
		if err := second.Emit(Event{Kind: "precheck", State: "ok", Evidence: "measured", Turn: 1}); err != nil {
			t.Fatalf("第二趟落不去：%v", err)
		}
	}
	second.Close()

	evs := mustReadEvents(t, path)
	if len(evs) != 5 {
		t.Fatalf("两趟共 5 条，读回来 %d 条 —— 第二趟把第一趟覆盖了（append-only 是假的）", len(evs))
	}
	if err := ValidateLog(evs); err != nil {
		t.Fatalf("跨趟的序号不连续（续传会指向两条）：%v", err)
	}
	if second.written != 2 {
		t.Fatalf("第二趟自印 %d 条，应 2 条（既有那 3 条不算它落的）", second.written)
	}
}

// 既有日志读不回来时不许闷着续写：那会写出一本序号重复的账。
// 这一支同时是「静默降级」那一族的臂 —— 降级必须留下说得出来的理由。
func TestUnreadableExistingLogDegradesLoudly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "torn.events.jsonl")
	if err := os.WriteFile(path, []byte("{\"v\":1,\"turn\":1,\"seq\":1}\n{\" 这条是撕裂的\n"), 0o644); err != nil {
		t.Fatalf("造撕裂日志：%v", err)
	}
	b := newEventBus(path)
	defer b.Close()
	if _, _, degraded := b.stats(); degraded == "" {
		t.Fatalf("既有日志读不回却照常落盘 —— 这本账会出现重复序号，而降级没喊")
	}
	if b.path != "" {
		t.Fatalf("降级之后还留着落点 %q：那叫半落盘", b.path)
	}
	if err := b.Emit(Event{Kind: "precheck", State: "ok", Evidence: "measured"}); err != nil {
		t.Fatalf("降级成内存数序后这条该收下（只是不落盘）：%v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读原文件：%v", err)
	}
	if strings.Contains(string(raw), "precheck") {
		t.Fatalf("降级那趟还是往那本坏账里写了")
	}
}

// Close 之后再 Emit 必须拒 —— 这一支补的是「已关闭」那条守卫原来的空档：没人测过它。
func TestClosedBusRefusesNewEvents(t *testing.T) {
	b := newEventBus(filepath.Join(t.TempDir(), "c.events.jsonl"))
	b.Close()
	if err := b.Emit(Event{Kind: "precheck", State: "ok", Evidence: "measured"}); err == nil {
		t.Fatalf("关了之后还能落 —— 收口之后的写会把 Close 变成装饰")
	}
	if !strings.Contains(b.Emit(Event{Kind: "precheck", State: "ok", Evidence: "measured"}).Error(), "已关闭") {
		t.Fatalf("拒是拒了，没说为什么拒")
	}
}

// 这一支是 E1 的硬约束：事件层不许改写 stdout 那份可逐字比对的正文。
// 两趟各用自己那份 journal 路径（共用会让往轮轨迹文件跨趟累加，那是一颗延时雷）；
// 也不能直接比字节 —— stdout 里印了账本字节数，而 encode_journal 内嵌时间戳，
// 同一趟跑两次那个数也会变（现场老规矩：跨进程别比字节，时钟刻度就是抖动源）。
// 掩码只管「数字宽度」这一类抖动，它管不了的方向写死在下面那条臂与注释里，不许吹。
func TestEventLayerLeavesStdoutUnchanged(t *testing.T) {
	dir := t.TempDir()

	on := newRun(t, "happy", true, "included", 20)
	on.journalPath = filepath.Join(dir, "on-journal.jsonl")
	on.events = newEventBus(filepath.Join(dir, "on.events.jsonl"))
	on.loop()
	on.closeOut()

	off := newRun(t, "happy", true, "included", 20)
	off.journalPath = filepath.Join(dir, "off-journal.jsonl")
	off.loop()
	off.closeOut()

	// 两趟各用自己那份 journal 路径（共用会让往轮轨迹文件跨趟累加，那是一颗延时雷），
	// 但那份路径本身会印进 stdout —— 先各自换成同一个记号，否则比的是文件名不是正文。
	gotOn := normVolatile(strings.ReplaceAll(on.log.(*bytes.Buffer).String(), on.journalPath, "<journal>"))
	gotOff := normVolatile(strings.ReplaceAll(off.log.(*bytes.Buffer).String(), off.journalPath, "<journal>"))
	if gotOn != gotOff {
		i := 0
		for i < len(gotOn) && i < len(gotOff) && gotOn[i] == gotOff[i] {
			i++
		}
		lo, hi := i-60, i+90
		if lo < 0 {
			lo = 0
		}
		if hi > len(gotOn) {
			hi = len(gotOn)
		}
		if hi > len(gotOff) {
			hi = len(gotOff)
		}
		t.Fatalf("事件层改变了 stdout（掩掉时钟类读数后仍不同）｜第一处差异在第 %d 字节：\n  开：%q\n  关：%q",
			i, gotOn[lo:hi], gotOff[lo:hi])
	}
	if on.events.written == 0 {
		t.Fatalf("这一趟事件层根本没落东西，那「两趟相同」是空对空")
	}
	if on.kpi != off.kpi || on.shot != off.shot {
		t.Fatalf("开事件层改变了账上的数：kpi %d vs %d，发数 %d vs %d", on.kpi, off.kpi, on.shot, off.shot)
	}
	// 诊断一律不许上 stdout：上了就会把上面这条针弄成「分不清是谁在响」。
	if strings.Contains(on.log.(*bytes.Buffer).String(), "事件层") {
		t.Fatalf("事件层的字出现在 stdout 正文里 —— 诊断该走 stderr")
	}
}

// 掩码的牙与它的盲区，两条都写死：
//
//	能抓：事件层（或任何东西）往 stdout 多写一行 —— 数字掩掉了，文字还在，OFF 那趟没这行。
//	不能抓：只改数字宽度那一类（这条针看不见，账本体积那一族由 tally/verify 两行的格数兜）。
//
// 早先注释吹了「单列一条臂证明掩码没把真污染一起掩掉」而那条臂不存在 —— 这次是实的。
func TestMaskArms(t *testing.T) {
	base := "第 1 发落账：ok\n  checkpoint(收口)：写了 x（147840 字节）\n"
	withExtraLine := "第 1 发落账：ok\n  事件层多写了一行（9 字节）\n  checkpoint(收口)：写了 x（148183 字节）\n"
	if normVolatile(withExtraLine) == normVolatile(base) {
		t.Fatalf("整行新增被掩码吞了 —— 那这条针谁都红不了")
	}
	// 同源抖动有三处（现场实测过），漏一处就是「哪一上线都随机红」。
	sample := "  checkpoint(收口)：写了 p（148232 字节；...）\n  checkpoint(x)：编出 147938 字节，但没给\n    装配第 3 发：往轮 1234 字节，折不折=true\n"
	if n := len(reVolatile.FindAllString(sample, -1)); n != 3 {
		t.Fatalf("掩码命中 %d 处，应为 3（三处同源抖动）—— 覆盖面漂了", n)
	}
	if normVolatile(sample) == sample {
		t.Fatalf("掩码一处都没改，那上面那条计数是数空气")
	}
}

// 叶子臂：事件层一旦拒收，必须在 stderr 上喊出来（不许上 stdout，也不许静默）。
func TestEmitFailureIsLoudOnStderr(t *testing.T) {
	r := newRun(t, "happy", true, "included", 20)
	r.events = newEventBus(filepath.Join(t.TempDir(), "arm.events.jsonl"))
	before := r.errLog.(*bytes.Buffer).Len()
	stdoutBefore := r.log.(*bytes.Buffer).Len()
	r.emit(Event{Kind: "step_finished", State: "running", Evidence: "absent"}) // 故意不给 why
	shout := r.errLog.(*bytes.Buffer).String()[before:]
	if !strings.Contains(shout, "事件层拒了") {
		t.Fatalf("拒收没在 stderr 上喊出来：%q —— 事件层坏了会静默", shout)
	}
	if !strings.Contains(shout, "absent 事件没带 why") {
		t.Fatalf("喊是喊了，没说要紧的是哪一条：%q", shout)
	}
	if r.log.(*bytes.Buffer).String()[stdoutBefore:] != "" {
		t.Fatalf("拒收写进了 stdout 正文：%q", r.log.(*bytes.Buffer).String()[stdoutBefore:])
	}
	r.events.Close()
}

// 拒收那两条挂点（预检拒 / 落账拒）必须从 loop() 走过：
// 早先它们一次都没被测过，而「被拒但继续」与「被拒且账满要停」在流里一模一样。
func TestRefusedEventsCarryTheirWhy(t *testing.T) {
	drv, err := buildDriver("scripted", "full")
	if err != nil {
		t.Fatalf("造驱动：%v", err)
	}
	r := &run{
		rpc: newKernelRPC(98, false), drv: drv, session: "test-ev-cap", maxTurns: 20,
		readBack: true, declare: true, payload: "included",
		journalPath: filepath.Join(t.TempDir(), "journal.jsonl"),
		log:         &bytes.Buffer{}, errLog: &bytes.Buffer{}, declared: map[string]bool{},
	}
	path := filepath.Join(t.TempDir(), "cap.events.jsonl")
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()

	refused := []Event{}
	for _, e := range mustReadEvents(t, path) {
		if e.State == "refused" {
			refused = append(refused, e)
		}
	}
	if len(refused) == 0 {
		t.Fatalf("跑到了门口挡那一形，事件流里却一条 refused 都没有")
	}
	for _, e := range refused {
		if e.Why == "" {
			t.Fatalf("refused 事件没带为什么：%+v —— 界面拿不到理由就只能显示「失败了」", e)
		}
	}
	last := refused[len(refused)-1]
	if !strings.Contains(last.Why, "容量栏") {
		t.Fatalf("最后一条 refused 分不出是「继续」还是「账满要停」（两者流里相同）：%q", last.Why)
	}
	if r.stop != stopCapped {
		t.Fatalf("这一趟该停在账满上，实际 %q", r.stop)
	}
}

// 落点推导与撞车守卫：这块以前整段没测（P0 就藏在这里 —— 账本路径被当目录建，
// 账写不出去而退出码照旧 0）。
func TestEventLogPathDerivationAndCollision(t *testing.T) {
	cases := []struct {
		name            string
		journal, events string
		wantPath        string
		wantNote        bool
	}{
		{"按账本同位置推导", filepath.Join("d", "j.jsonl"), "", filepath.Join("d", "j.events.jsonl"), false},
		{"显式给落点", filepath.Join("d", "j.jsonl"), filepath.Join("d", "e.jsonl"), filepath.Join("d", "e.jsonl"), false},
		{"没账本也没落点", "", "", "", true},
		{"落在账本文件里面", filepath.Join("d", "j.jsonl"), filepath.Join("d", "j.jsonl", "e.jsonl"), "", true},
		{"就是账本自己", filepath.Join("d", "j.jsonl"), filepath.Join("d", "j.jsonl"), "", true},
		// 非 .jsonl 后缀不许装作推导成功——那是盲拼，实测过 weird.log → weird.log.events.jsonl，
		// 现在仍这么落，但落点由这一支钉死，将来改口径会红。
		{"怪后缀", "weird.log", "", "weird.log.events.jsonl", false},
	}
	for _, c := range cases {
		p, note := eventLogPath(c.journal, c.events)
		if p != c.wantPath {
			t.Fatalf("%s：落点=%q 应 %q", c.name, p, c.wantPath)
		}
		if (note != "") != c.wantNote {
			t.Fatalf("%s：该有理由=%v，实得 %q", c.name, c.wantNote, note)
		}
		if c.wantNote && p != "" {
			t.Fatalf("%s：报了理由还给落点，那是半落盘", c.name)
		}
	}
}

// 禁区：事件默认不带原话正文（与轨迹页同一条）。这一支跑完整本剧本 ——
// 早先截在前 6 发，后三发的正文从没进过禁区比对。
func TestEventsCarryNoPayloadText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.events.jsonl")
	r := newRun(t, "full", true, "included", 20)
	r.journalPath = filepath.Join(t.TempDir(), "journal.jsonl")
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()
	evs := mustReadEvents(t, path)
	if len(evs) < 20 {
		t.Fatalf("跑完整本剧本应有 20 条以上事件，实得 %d —— 这条禁区比的是半趟", len(evs))
	}
	raw, err := json.Marshal(evs)
	if err != nil {
		t.Fatalf("序列化事件：%v", err)
	}
	s := string(raw)
	// nums（"27+15+8+34"）全包只在它自己的定义处出现，任何 payload 都不含它 ——
	// 拿它当禁区是一条死断言，所以禁区用的是真正会出现在正文里的那三段。
	for _, banned := range []string{systemPrompt, userAsk, toolSpec, "把 27"} {
		if strings.Contains(s, banned) {
			t.Fatalf("事件里出现了原话正文（%q）—— 默认不显示 payload 这条禁区破了", banned)
		}
	}
}

func lastSettle(t *testing.T, evs []Event) Event {
	t.Helper()
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Kind == "run_settled" {
			return evs[i]
		}
	}
	t.Fatalf("事件流里没有收口那一条")
	return Event{}
}

// 掩掉的是数字宽度，不是整行 —— 三处同源抖动都要盖住（覆盖面由 TestMaskArms 数死）：
//
//	写了 %s（148232 字节；…）/ 编出 %d 字节 / 往轮 %d 字节
//
// RE2 没有前后查，所以把"字节"两字一起吃进匹配再原样写回。
var reVolatile = regexp.MustCompile(`[0-9]+ 字节`)

func normVolatile(s string) string { return reVolatile.ReplaceAllString(s, "N 字节") }

func keysOf(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mustReadEvents(t *testing.T, path string) []Event {
	t.Helper()
	evs, err := ReadEvents(path)
	if err != nil {
		t.Fatalf("读事件：%v", err)
	}
	return evs
}
