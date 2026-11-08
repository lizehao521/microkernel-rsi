package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// M5 融合 DAG · P0 修复端 —— 给「被拒/失败那一族」补真负载并钉住。
//
// 交接件 §3 明写：loop.go 里 11 处 settleTurn「没人统计过命中」，其中若干处没有任何测试
// 或二进制负载走过。本文件只补「之前没人走」的那几处收口，每支针都：
//   ① 走公开路径（真 r.loop() → 真落盘 → ReadEvents → 喂判据），不拿内存切片喂判据；
//   ② 自带现场自证：这一趟没演到目标那一形就 t.Fatal（别拿没造出来的现场当证据）。
//
// 命中表（**按符号认，不写 loop.go 的行号** —— 这一族的号在 E5/E7/E8/E9 落码后整体漂了约 +97 行，
// 写死的号今天指到 `}` 上；要现数就 `grep -n 'r.settleTurn(' loop.go`，本文件写时数到 11 处）：
//   已有人走：interrupted「等待端点回话时被叫停」（TestInterruptSettlesWithinSecond）·
//             refused「这本账已到上限，门口挡下第 N 次」（TestSettlesCoverRefusedShape）·
//             completed「账上这一格指向「停」」与「这一发做完了：剧本的这一步走到收口」（TestEveryTurnSettlesExactlyOnce / 多支）。
//   本文件新钉：failed「驱动这一路今天跑不了：NOT_RUN」· failed「端点没有下一条了」·
//             refused「门口挡下这一发：预检拒收，一条都不写」。
//   产码里走不到（无负载、且公开路径不可达，见报告精确分析）：failed「装不出这一发的输入」·
//             failed「预检回帧坏了」/「落账回帧坏了」（进程内 RPC 永不返回传输错）·
//             refused「内核拒了这一发的落账」（check_call 的 checkBoth 已在副本上跑过 RecordCall，
//             任何 record 拒都先被「门口挡下这一发：预检拒收」那一支抓到）。

// findSettlesByWhy 把读回来的整本日志里、结算载荷 kind 与 why 都对得上的那几条挑出来。
// 为什么用 why：settleTurn 每一处的 why 是唯一签名（loop.go 里 11 处各不相同），
// 拿它定位「这一处到底走没走过」比拿 phase 稳（多处同 phase）。
func findSettlesByWhy(evs []Event, wantKind, wantWhy string) []Event {
	out := []Event{}
	for _, e := range evs {
		if e.Settle == nil {
			continue
		}
		if e.Settle.Kind == wantKind && strings.Contains(e.Settle.Why, wantWhy) {
			out = append(out, e)
		}
	}
	return out
}

// errorDriver 是一支公开路径驱动：不是 *scripted（所以循环顶部那句
// `if s, ok := r.drv.(*scripted)` 的提前收口不会拦它），Propose 直接回一个 error。
// 错误文本决定 loop.go 走哪一支：带 NOT_RUN 前缀 → 「驱动这一路今天跑不了」；否则 → 「端点没有下一条了」。
type errorDriver struct{ msg string }

func (errorDriver) Name() string     { return "error-driver（测试用，不是新驱动）" }
func (errorDriver) Autonomous() bool { return false }
func (d errorDriver) Propose(turn int, obs string, body []byte) (proposal, error) {
	return proposal{}, errors.New(d.msg)
}

// 针 A：failed「驱动这一路今天跑不了：NOT_RUN」 —— 之前只有 TestRealDriverStaysNotRunAndWritesNothing 走过这一支，
// 但那一支 r.events 是 nil（settleTurn 一进门就 return，从没落过一条 failed 结算事件）。
// 真 exe 的 -driver real 能演（见报告 S_real：phase=accept、not_reached=work|check_call|record_call），
// 但 Go 测试树里这一族的 failed 结算是零负载。这一支把它钉进盘上日志。
func TestRefusedNotRunBranchSettlesAsFailed(t *testing.T) {
	os.Unsetenv("AGNES_KEY")
	os.Unsetenv("AGENTNES_KEY")
	drv, err := buildDriver("real", "full")
	if err != nil {
		t.Fatalf("造 real 驱动：%v", err)
	}
	path := filepath.Join(t.TempDir(), "notrun.events.jsonl")
	r := newRun(t, "full", true, "included", 20)
	r.drv = drv
	r.journalPath = filepath.Join(t.TempDir(), "journal.jsonl")
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()

	evs := mustReadEvents(t, path)
	// 现场自证：这一趟没演到 failed+NOT_RUN 那一形，这支就不作数。
	got := findSettlesByWhy(evs, "failed", "NOT_RUN")
	if len(got) == 0 {
		t.Fatal("现场没造出来，这一支不作数（没演到 287 failed-NOT_RUN 那一形）")
	}
	if len(got) != 1 {
		t.Fatalf("NOT_RUN 结算应有 1 条，实得 %d —— 一发只结一次", len(got))
	}
	s := got[0]
	if r.stop != stopDriver || exitOf(r.stop) != rcNotRun {
		t.Fatalf("NOT_RUN 那一路结束原因/退出码不对：%q / rc=%d", r.stop, exitOf(r.stop))
	}
	// 停在 accept 那一相：驱动还没回话就散，后面三相根本没执行。
	if s.Settle.Phase != "accept" {
		t.Fatalf("NOT_RUN 结算停在 %q 相，应为 accept（还没装出这一发就散了）", s.Settle.Phase)
	}
	want := map[string]bool{"work": true, "check_call": true, "record_call": true}
	if len(s.Settle.NotReached) != 3 {
		t.Fatalf("not_reached 应点名 3 相（work/check_call/record_call），实得 %v", s.Settle.NotReached)
	}
	for _, p := range s.Settle.NotReached {
		if !want[p] {
			t.Fatalf("not_reached 里冒出 %q —— 不是那没走的三相", p)
		}
	}
	// counts 只数得出这一发落出去的那条 running（turn_accepted），且判据从盘上读回来必须绿。
	if s.Settle.Counts["running"] != 1 || len(s.Settle.Counts) != 1 {
		t.Fatalf("NOT_RUN 那一发的 counts=%v，应为 {running:1}（只有回执落了盘）", s.Settle.Counts)
	}
	if err := ValidateSettlement(evs); err != nil {
		t.Fatalf("failed-NOT_RUN 真负载的结算判据红：%v", err)
	}
	if err := ValidateAcceptsWithin(evs, 60000); err != nil {
		t.Fatalf("failed-NOT_RUN 真负载的回执判据红：%v", err)
	}
}

// 针 B：failed「端点没有下一条了」 —— 之前零负载（scripted 被循环顶部的 Done() 提前收口挡在门外，
// 从没让 Propose 真回过这个非-NOT_RUN 错误；real 走的是「驱动这一路今天跑不了」那一支）。
// 用一支「不是 scripted」的 errorDriver 走公开 r.loop() 把它落到盘上。
func TestRefusedEndpointExhaustedSettlesAsFailed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "noNext.events.jsonl")
	r := newRun(t, "full", true, "included", 20)
	r.drv = errorDriver{msg: "端点没有下一条了：这一路的剧本走到头，收口不该由驱动编"}
	r.journalPath = filepath.Join(t.TempDir(), "journal.jsonl")
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()

	evs := mustReadEvents(t, path)
	got := findSettlesByWhy(evs, "failed", "端点没有下一条")
	if len(got) == 0 {
		t.Fatal("现场没造出来，这一支不作数（没演到 292 failed-端点没有下一条 那一形）")
	}
	if len(got) != 1 {
		t.Fatalf("端点没下一条的结算应有 1 条，实得 %d", len(got))
	}
	s := got[0]
	if r.stop != stopDriver || exitOf(r.stop) != rcNotRun {
		t.Fatalf("端点没下一路结束原因/退出码不对：%q / rc=%d", r.stop, exitOf(r.stop))
	}
	if s.Settle.Phase != "accept" || len(s.Settle.NotReached) != 3 {
		t.Fatalf("相位/not_reached 不对：phase=%q not_reached=%v", s.Settle.Phase, s.Settle.NotReached)
	}
	if err := ValidateSettlement(evs); err != nil {
		t.Fatalf("failed-端点没下一条 真负载的结算判据红：%v", err)
	}
}

// 针 C：refused「门口挡下这一发：预检拒收，一条都不写」—— 之前零负载。
// 「这本账已到上限」那一支有负载，但它是「落账被拒且账满要停」；这一支是「预检拒收就继续」。
// 两者 kind 都是 refused、在流里必须分得开。可达路径：先把 (session,发号=1) 预记进这本账，
// 于是循环第一发的 check_call 会在副本上 RecordCall 撞去重门（mcpsrv.go:913 → checkBoth 返回 bad），
// 走 loop.go 里「门口挡下这一发：预检拒收，一条都不写」那一支。
func TestRefusedPrecheckGateSettlesAsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "precheck.events.jsonl")
	drv, err := buildDriver("scripted", "happy")
	if err != nil {
		t.Fatalf("造 happy 驱动：%v", err)
	}
	r := &run{
		rpc: newKernelRPC(0, false), drv: drv, session: "test-ev-precheck", maxTurns: 20,
		readBack: true, declare: true, payload: "included",
		journalPath: filepath.Join(t.TempDir(), "journal.jsonl"),
		log:         &bytes.Buffer{}, errLog: &bytes.Buffer{}, declared: map[string]bool{},
	}
	// 预记一发（session、发号 1）到这本账：让循环真正跑到的第一发在 check_call 上撞去重门。
	in1, body1, err := (&assembly{system: systemPrompt, toolspec: toolSpec, user: userAsk}).buildInput(1)
	if err != nil {
		t.Fatalf("装配预记那发：%v", err)
	}
	out1 := buildOutput(1, proposal{
		Text: "预记占位一发", UsageIn: 10, UsageOut: 2, FinishSaid: true, FinishReason: "stop", ReportsModel: true,
	}, body1)
	pre, err := r.rpc.call("kernel.record_call", sides(r.session, in1, out1))
	if err != nil || pre.IsErr {
		t.Fatalf("预记第一发就该落进账（err=%v isErr=%v）：%s", err, pre.IsErr, firstLine(pre.Text))
	}
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()

	evs := mustReadEvents(t, path)
	got := findSettlesByWhy(evs, "refused", "门口挡下这一发")
	if len(got) == 0 {
		t.Fatal("现场没造出来，这一支不作数（没演到 322 refused-预检拒收 那一形）")
	}
	s := got[0]
	// 停在 check_call：work 走过了（有 shot_started），record_call 根本没执行。
	if s.Settle.Phase != "check_call" {
		t.Fatalf("预检拒收结算停在 %q 相，应为 check_call", s.Settle.Phase)
	}
	if len(s.Settle.NotReached) != 1 || s.Settle.NotReached[0] != "record_call" {
		t.Fatalf("not_reached 应恰是 record_call，实得 %v", s.Settle.NotReached)
	}
	// 「一条都不写」：这一发（turn=1）不许出现 recorded 事件。
	for _, e := range evs {
		if e.Turn == s.Turn && e.Kind == "recorded" {
			t.Fatalf("预检拒收那一发却落了 recorded（本该一条都不写）：%+v", e)
		}
	}
	// counts 里必须有 refused 那一格（预检被拒的事件），且判据从盘上读回来绿。
	if s.Settle.Counts["refused"] < 1 {
		t.Fatalf("预检拒收那一发的 counts=%v，没有 refused 格", s.Settle.Counts)
	}
	if err := ValidateSettlement(evs); err != nil {
		t.Fatalf("refused-预检拒收 真负载的结算判据红：%v", err)
	}
}
