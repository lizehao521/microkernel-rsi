package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentkernel"
)

type stopReason string

// Token 把六态整句压成一个可分档的 token —— 下游要按结束原因分支，
// 不该拿中文字符串去匹配（E1 我判 state 塞散文是错的，同一个错不能换个格子再犯一次）。
func (s stopReason) Token() string {
	switch s {
	case stopScript:
		return "script"
	case stopTurns:
		return "turns"
	case stopPolicy:
		return "policy"
	case stopDriver:
		return "driver"
	case stopCapped:
		return "capped"
	case stopAbort:
		return "abort"
	}
	return ""
}

const (
	stopScript stopReason = "跑完：端点剧本走到最后一发"
	stopTurns  stopReason = "停下：轮次额度到（--max-turns）"
	stopPolicy stopReason = "停下：账上的 fail_kind 指向「停」这一类，按 POLICY 主动停"
	stopDriver stopReason = "停下：驱动这一路跑不了（NOT_RUN 或剧本没下一条）"
	stopCapped stopReason = "停下：这本账已到上限，再发也只会被门口挡回来"
	stopAbort  stopReason = "停下：外部打断（Ctrl-C / SIGINT）—— 先把账交出去，再走"
)

// decision 是账上那一格与「接下来干什么」之间唯一那条边。
// Basis 要么是 read_back 真读回来的格名，要么是「本地信息，没问内核」—— 二者不许混写。
type decision struct {
	Shot   int
	Basis  string
	Value  string
	Action string
	Why    string
}

type run struct {
	// turnFloor 是续跑时的发号基线：r.turns 只数本进程跑了几发（额度、停止判断看它），
	// 落进事件流的那一格是 turnNo()=turnFloor+turns。少这一步，同一本日志里会出现
	// turn=1 却写着 done_before=7 —— 身份轴与载荷轴各说各话（M5 扫端抓到）。
	turnFloor       int
	rpc             *kernelRPC
	drv             Driver
	session         string
	ask             string // 任务输入面；空 = 内置例子（老剧本那一路）
	maxTurns        int
	checkpointEvery int
	readBack        bool
	declare         bool
	abort           chan struct{} // 关闭它 = 外部打断；循环每一发开头看一眼
	stopMu          sync.Mutex    // 叫停入口的幂等锁：check-then-act 会双关
	abortClosed     bool          // 关过就没有第二次，与 close 同锁判
	stopWhy         string        // 谁、为什么叫的停（Stop 那里写，结算与横幅都读它）
	phase           string        // 这一发走到哪一相了；结算的 reached/not_reached 由它算
	tRequest        time.Time     // 循环入口 = 第 1 发的「请求入口」
	turnEnter       time.Time     // 本发进入循环体那一刻 = 后面几发的「请求入口」
	settled         bool          // 一发只结一次：重复调用不改账（两套收口的复发点就在这）
	shotDelay       time.Duration // 每发之前等这么久（真端点本来就慢；也用来验 OS 级信号）
	tickIdle        time.Duration // E5：距上一条落盘事件多久就补一条进度/解释；<=0 = 关（关要说出来）
	tickStop        chan struct{} // 本发看门狗的开关；结算与收口都先关它再写自己那一行
	journalPath     string
	payload         string
	events          *eventBus // 宿主侧事件层；nil = 这一趟不带事件（内核调用与数值逻辑一律不变）

	log        io.Writer
	errLog     io.Writer // 横幅与诊断走这里；nil 时 errf 兜到 os.Stderr（测试里直接给 buffer）
	decisions  []decision
	traces     []shotTrace
	kpi        int
	caps       int
	drift      int
	shot       int
	turns      int
	stop       stopReason
	lastTally  string
	lastVerify string
	resumed    int
	// refuseWhy 非空 = 续跑被拒：这一趟一条本机新账都没有，收口不许写 --journal-out。
	// 不拦的话，-resume 与 -journal-out 指同一本时，会把被篡改的输入拿空账覆盖掉。
	refuseWhy string
	caveat    string
	tracePath string

	historyText string
	historyOrig string
	foldNext    bool
	expectFold  bool
	rephrase    bool
	obs         string
	closed      bool
	declared    map[string]bool
	lastChain   string
}

const (
	systemPrompt = "你是一个只用工具的计算器。你手里只有一个 ledger_add，一次只能加两个数。" +
		"要算一组数的和，就一步步提议加法，等我把每次的结果作为工具结果回给你，最后把总和写成一句话。不要一次编出结果。"
	toolSpec = `{"type":"function","function":{"name":"ledger_add","description":"把两个整数相加，返回它们的和"}}`
	userAsk  = "把 27、15、8、34 加起来。"
	nums     = "27+15+8+34"
)

func (r *run) say(f string, a ...any) { fmt.Fprintf(r.log, f+"\n", a...) }

// errf 印横幅与诊断：落点与 stdout 分开，所以事件层自己坏了不会把「两趟正文相同」那根针弄红。
func (r *run) errf(f string, a ...any) {
	w := r.errLog
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, f+"\n", a...)
}

// Stop 是 E3 的停止入口：进程内任何一处叫停，与 SIGINT 走同一个口子（不分两套）。
// 重复叫停不炸 —— channel 已关就看一眼；why 只记第一次，后面谁来叫的都覆盖不了首发原因。
func (r *run) Stop(why string) {
	// 「关没关过」与 close 必须在同一把锁里：SIGINT 协程与干活那一路可能同时叫停，
	// select-default 那种 check-then-act 两头都走 default → close of closed channel
	// （M2 路 A 的 P1-④，400 轮并发里复现过；崩掉的是整个进程，收口一条都不执行）。
	r.stopMu.Lock()
	defer r.stopMu.Unlock()
	if r.stopWhy == "" {
		r.stopWhy = why
	}
	if r.abort == nil || r.abortClosed {
		return
	}
	r.abortClosed = true
	close(r.abort)
}

// ackAnchor 是针1 的锚点：第 1 发取循环入口（宿主拿到这个任务那一刻），
// 后面每发取该发进入循环体那一刻。
//
// M2 两路**各自独立**撞到同一处：锚点原先取在 acceptedEv 里，而它就是被 emit 的那一瞬调用 ——
// 窗口里根本没有可延迟的工作，实测 42/42 与 16/16 条 ack_ms 恒 0，
// `--shot-delay 600ms` 那一趟第一发距总线建立 625ms 而 ack_ms 仍是 0、判据照绿。
// 挪到入口之后，任何排在回执之前的工作（等待、装配、驱动、内核调用）都会自己把 ack_ms 顶起来。
func (r *run) ackAnchor() time.Time {
	switch {
	case r.turns <= 1 && !r.tRequest.IsZero():
		return r.tRequest
	case !r.turnEnter.IsZero():
		return r.turnEnter
	}
	return time.Now()
}

// settleTurn 收这一发，三样都交：停在哪个相、走过哪些相、哪些相**根本没执行**。
// startTick 给这一发挂上看门狗（E5）：距上一条**落盘**事件超过 -progress-tick 还没动静，
// 就补一条 progress_tick（带 done/total/limit）；total 算不出就补 why_late（absent 且必带理由）。
// 超时在这里是汇报不是报错：沉默与「沉默但我说了我还在等」是两种不同的账。
//
// 三条现场量（静默时长 / 最后落的一条 / 这一发已落几条）在总线的一把锁里一次取走；
// 发号、done、limit、total 在启动那一刻算好当参数带走 —— 看门狗与循环不同 goroutine，
// 去读活的 r.turns/r.phase/r.drv 内部游标就是竞态，那些数也不该跨 goroutine 现取。
func (r *run) startTick() {
	if r.events == nil || r.tickIdle <= 0 {
		return
	}
	r.stopTick()
	stop := make(chan struct{})
	r.tickStop = stop
	turn, done, limit := r.turnNo(), r.turnFloor+r.turns, r.turnFloor+r.maxTurns
	total := r.plannedTotal()
	clamped := false
	if total != nil && *total > limit {
		// 剧本比额度长：这一趟到额度就停，预计落点不可能是 6 发（真负载抓到过：done=1/total=6/limit=2）。
		// 与收口同一口径 —— total 越过 limit 就是给界面画一条永远走不完进度条。
		total, clamped = intp(limit), true
	}
	session := r.session
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(r.tickIdle):
			}
			idle, lastKind, landed, seen := r.events.idleView(session, turn)
			if !seen || idle < r.tickIdle {
				continue // 期间有人落过盘（或这本账一条还没落）—— 不抢话
			}
			ms := idle.Milliseconds()
			over := ""
			if clamped {
				over = "；剧本比额度长，落点夹到额度"
			}
			ev := Event{
				Kind: "progress_tick", State: "running", Evidence: "measured", Turn: turn,
				Why:      fmt.Sprintf("静默 %dms：这一发落了 %d 条，最后一条是 %q；额度到 %d 发为止%s", ms, landed, lastKind, limit, over),
				Progress: &Progress{Done: done, Total: total, Limit: intp(limit)},
				idleOnly: true,
			}
			if total == nil {
				ev.Kind = "why_late"
				ev.Evidence = "absent"
				ev.Why = fmt.Sprintf("静默 %dms，最后一条是 %q：这一趟不是剧本驱动，本发之后还要走几步算不出 —— 不编一个 total（额度 %d 发是另一格）", ms, lastKind, limit)
			}
			ev.Session = session
			if err := r.events.Emit(ev); err != nil {
				// 失败不在这里印：errLog 是循环的单线程财产。攒进总线，收口时由循环说出来。
				r.events.noteIdleFail(fmt.Sprintf("第 %d 发 kind=%s：%v", turn, ev.Kind, err))
			}
		}
	}()
}

// stopTick 关掉本发的看门狗：关过就不第二次（与 abortClosed 那族同一写法），置 nil 让重复调用无害。
func (r *run) stopTick() {
	if r.tickStop == nil {
		return
	}
	select {
	case <-r.tickStop:
	default:
		close(r.tickStop)
	}
	r.tickStop = nil
}

// plannedTotal 是「这一趟预计落到第几发」：只有剧本驱动知道还剩几发，别的驱动给 nil。
// 与 Accept.Planned 同一口径 —— 不知道就 nil，绝不写 0（0 会被界面读成「一步也没有」）。
//
// 减一是必须的：进来这一发已经计进 r.turns，而 s.Left() 含当前这一发的提案，
// 两遍一起加会让最后一发读成 done=3 / total=4 —— 跑完的那趟在流里永远只有 75%。
func (r *run) plannedTotal() *int {
	if s, ok := r.drv.(*scripted); ok {
		return intp(r.turnFloor + r.turns - 1 + s.Left())
	}
	return nil
}

// reached/not_reached 由相位表与 r.phase 算出来（不靠人手写），counts 由总线从这本账现数。
// 走到尾、被拒、回帧坏、被打断都调这一个函数 —— 分叉出两套收口代码就是「两个数并排上屏」的复发点。
func (r *run) settleTurn(kind, why string) {
	if r.events == nil || r.settled || r.turns == 0 {
		return
	}
	r.settled = true
	// 先关看门狗，再抄 counts、再写结算行：反过来会留一条挤在 counts 与结算行中间的补位，
	// 那两个数从此不一样（总线里还有一道 idleOnly+quiet 的兜底，两处都拆才漏）。
	r.stopTick()
	at := -1
	for i, p := range turnPhases {
		if p == r.phase {
			at = i
		}
	}
	reached, not := []string{}, []string{}
	for i, p := range turnPhases {
		if i <= at {
			reached = append(reached, p)
		} else {
			not = append(not, p)
		}
	}
	e := Event{
		Kind: "turn_settled", State: "settled", Evidence: "measured", Turn: r.turnNo(), Why: why,
		Progress: &Progress{Done: r.turnNo(), Limit: intp(r.turnFloor + r.maxTurns)},
		Settle: &Settle{Phase: r.phase, Reached: reached, NotReached: not, Kind: kind, Why: why,
			Counts: r.events.countsFor(r.session, r.turnNo()), DoneBefore: r.shot, KPI: r.kpi},
	}
	if kind == "interrupted" {
		e.Kind = "turn_interrupted"
		e.State = "interrupted"
	}
	r.emit(e)
}

// askText 是这一趟的用户原文：没给任务输入面就用内置例子（老剧本那一路）。
func (r *run) askText() string {
	if r.ask == "" {
		return userAsk
	}
	return r.ask
}

// assemble 装这一发要发的东西。foldNext / rephrase 都是上一发从账上读回来的决定造成的，
// 不是这里自己拍的 —— 那两个旗标是回边的落点。
func (r *run) assemble(call int) *assembly {
	// 旗标在这里就被吃掉：它们属于「上一发的账决定的下一发」，装配过一次就作废。
	// 先取到局部变量再清 —— 清早了会让下面折历史那一段看不到自己刚读到的决定。
	fold := r.foldNext
	rephrase := r.rephrase
	r.rephrase = false
	r.foldNext = false

	ask := r.askText()
	a := &assembly{system: systemPrompt, toolspec: toolSpec, user: ask, observation: r.obs}
	if rephrase {
		a.user = ask + "（换个说法：请只说这一个加法，两个操作数写全。）"
	}
	if r.historyOrig != "" {
		a.historyOrig = r.historyOrig
		r.say("    装配第 %d 发：往轮 %d 字节，折不折=%v", call, len(r.historyOrig), fold)
		if fold && len(r.historyOrig) > 120 {
			// 桩要么整段发、要么记成整段没进；折完还比原文长就不是折，是变大。
			a.history = fmt.Sprintf(historyStub, sha(r.historyOrig), len(r.historyOrig))
			a.foldHistory = true
			a.historyRefCal = r.shot - 1 // 引用的是上一发（内核要求它严格小于这一发且大于 0）
			r.expectFold = true          // 下一发要回账上确认这条折叠真落了，不是我以为它落了
		} else {
			a.history = r.historyOrig
		}
	}
	return a
}

func (r *run) loop() {
	// 请求入口由 main 在拿到任务那一刻钉；测试也可以预先把这一刻往前拨，
	// 用来验「排在回执之前的工作会被 ack_ms 量到」—— 所以这里只补零、不覆盖。
	if r.tRequest.IsZero() {
		r.tRequest = time.Now()
	}
	r.emit(Event{Kind: "run_started", State: "running", Evidence: "measured"})
	for {
		r.turnEnter = time.Now()
		// 先看一眼有没有已经叫过停：这一条在**受理之前**，
		// 所以「一开局就已被打断」不会往流里写一条没干过活的回执。
		select {
		case <-r.abort:
			// 被打断不是「跑完了」：这一趟的收口与交账仍然要做，
			// 否则按 Ctrl-C 就等于把这几发的账扔掉 —— 那正是「断开即失」原样复现。
			r.stop = stopAbort
			return
		default:
		}
		// Done 是「这一路还有没有下一条」的接缝：剧本看队列，本机执行器看任务收没收口。
		// 只认这一条，不按具体类型分支 —— 换驱动不该改循环体。
		if d, ok := r.drv.(interface{ Done() bool }); ok && d.Done() {
			r.stop = stopScript
			return
		}
		if r.turns >= r.maxTurns {
			r.stop = stopTurns
			return
		}
		r.turns++
		r.phase = "accept"
		r.settled = false
		call := r.shot + 1
		// 先回执再干活：这一发的「收到了」必须在装配、驱动耗时与第一次内核调用之前落进流里。
		// 落点错了（挪到 check_call 之后），ValidateAccepts 的「第一条」判据当场红。
		r.emit(r.acceptedEv())

		// 回执之后就有了一条「这一发在途中」的时间窗：看门狗从这里起、到结算前停。
		// 放在回执之前会量不到「收到但还没干活」那一段，而那正是 10s 兜底要说话的地方。
		r.startTick()

		// 延迟放在**回执之后**：它模拟的是端点耗时（真端点本来就慢），
		// 放在之前就没有「一发正在途中」这个窗口，打断路径只能演 run 级收口、演不了发级结算。
		if r.shotDelay > 0 {
			select {
			case <-time.After(r.shotDelay):
			case <-r.abort:
				r.settleTurn("interrupted", "等待端点回话时被叫停（-shot-delay 期间）")
				r.stop = stopAbort
				return
			}
		}

		asm := r.assemble(call)
		in, body, err := asm.buildInput(call)
		if err != nil {
			r.say("    装配就散了（第 %d 发）：%v", call, err)
			r.noteLocal(call, "装配失败", "停 —— 装不出这一发")
			r.settleTurn("failed", "装不出这一发的输入")
			r.stop = stopDriver
			return
		}
		p, err := r.drv.Propose(r.turnNo(), r.obs, body)
		if err != nil {
			r.say("    驱动回话（第 %d 发）：%v", call, err)
			if strings.HasPrefix(err.Error(), "NOT_RUN") {
				r.noteLocal(call, "驱动 NOT_RUN", "停 —— 这一路今天跑不了，也不许用剧本冒充")
				r.settleTurn("failed", "驱动这一路今天跑不了：NOT_RUN")
				r.stop = stopDriver
				return
			}
			r.noteLocal(call, "端点没有下一条", "停")
			r.settleTurn("failed", "端点没有下一条了")
			r.stop = stopDriver
			return
		}
		r.shot = call // 只有真要发出去的那一发才占发号
		r.emit(r.stepEv("shot_started", "running", "measured", "", "assemble", call))
		// 相位记的是「最后一条落进流里的相」—— 设在这行之后，
		// 否则结算会说「work 走到了」而流里根本没有 shot_started，那叫补出来的步。
		r.phase = "work"
		out := buildOutput(call, p, body)
		t := shotTrace{Call: call, Assemble: describeShot(asm, r.askText()), Cells: 0}

		args := sides(r.session, in, out)
		pre, err := r.rpc.call("kernel.check_call", args)
		if err != nil {
			r.say("    预检回帧坏了（第 %d 发）：%v", call, err)
			t.Precheck = "回帧坏了：" + err.Error()
			r.traces = append(r.traces, t)
			r.settleTurn("failed", "预检回帧坏了："+err.Error())
			r.stop = stopDriver
			return
		}
		t.Precheck = firstLine(pre.Text)
		if pre.IsErr {
			r.emit(r.stepEv("precheck", "refused", "measured", firstLine(pre.Text), "kernel.check_call", call))
			r.phase = "check_call"
			r.say("    预检被拒（第 %d 发）：%s", call, firstLine(pre.Text))
			r.noteLocal(call, "预检拒收", "不发 —— 这一发一条都不写")
			t.BasisFlag, t.Basis, t.Value, t.Action = "本地", "本地信息，没问内核", "预检拒收", "不发 —— 这一发一条都不写"
			r.traces = append(r.traces, t)
			r.settleTurn("refused", "门口挡下这一发：预检拒收，一条都不写")
			continue
		}
		r.emit(r.stepEv("precheck", "ok", "measured", "", "kernel.check_call", call))
		r.phase = "check_call"
		rec, err := r.rpc.call("kernel.record_call", args)
		if err != nil {
			r.say("    落账回帧坏了（第 %d 发）：%v", call, err)
			t.Record = "回帧坏了：" + err.Error()
			r.traces = append(r.traces, t)
			r.settleTurn("failed", "落账回帧坏了："+err.Error())
			r.stop = stopDriver
			return
		}
		t.Record = firstLine(rec.Text)
		if rec.IsErr {
			// 「被拒但继续」与「被拒且账满要停」在流里必须是两条不同的事件：
			// 界面据此决定是重试还是收摊，一模一样就没法投影。
			refusedWhy := firstLine(rec.Text)
			capped := r.ledgerCapped()
			if capped != "" {
				refusedWhy += " ｜ 容量栏挡下 " + capped + " 次：这本账吃不下了，再发也只会被挡"
			}
			r.emit(r.stepEv("recorded", "refused", "measured", refusedWhy, "kernel.record_call", call))
			r.phase = "record_call"
			r.say("    落账被拒（第 %d 发）：%s", call, firstLine(rec.Text))
			if capped != "" {
				// 这一笔**不计进 KPI**：它来自外壳的容量那一栏，不是 49 格里的一格。
				// 两个数分开数，与内核把「门口挡的」和「内核拒的」分开数是同一条纪律。
				r.caps++
				r.decisions = append(r.decisions, decision{
					Shot: call, Basis: "tally 的容量栏（不是 49 格里的一格）", Value: capped,
					Action: "停 —— 这本账吃不下了，再发还是挡", Why: "继续发只会把发号烧光",
				})
				r.say("    决定：第 %d 发 依据=%-14s 值=%-18q 动作=%s", call, "容量栏", capped, "停 —— 这本账吃不下了")
				t.BasisFlag, t.Basis, t.Value, t.Action = "本地", "tally 的容量栏", capped, "停 —— 这本账吃不下了"
				r.traces = append(r.traces, t)
				r.settleTurn("refused", "这本账已到上限，门口挡下第 "+capped+" 次")
				r.stop = stopCapped
				return
			}
			r.noteLocal(call, "落账拒收", "问内核为什么，再决定下一发")
			t.BasisFlag, t.Basis, t.Value, t.Action = "本地", "本地信息，没问内核", "落账拒收", "问内核为什么，再决定下一发"
			r.traces = append(r.traces, t)
			r.settleTurn("refused", "内核拒了这一发的落账：先问为什么，再决定下一发")
			continue
		}
		t.Cells = cellsFromRecordLine(rec.Text)
		r.emit(r.stepEv("recorded", "ok", "measured", "", "kernel.record_call", call))
		r.phase = "record_call"
		r.say("  第 %d 发落账：%s", call, firstLine(rec.Text))
		if r.expectFold {
			r.expectFold = false
			r.confirmFold(call, &t)
		}

		basis, value, ok := r.readBackCell(call)
		if !ok {
			// 读不到就是读不到：这一发的依据只能记「本地」，KPI 不加。
			// 这条边虚接的时候，这个数字必须自己掉回 0，那是这一支唯一要证的事。
			kernelFail := p.Fail
			act := noFailureAction
			if rule, has := POLICY[kernelFail]; has {
				act = rule.Action
			}
			r.noteLocal(call, string(kernelFail), act)
			t.BasisFlag, t.Basis, t.Value, t.Action, t.Why = "本地", "本地信息，没问内核", string(kernelFail), act, "这一发的下一步不是从账上读回来的"
			r.traces = append(r.traces, t)
		} else {
			act, why := noFailureAction, "账上这一格没有故障：接着算下一发"
			fail := normalizeFail(value)
			if driftOf(value, p.Fail) {
				// 账上读回来的故障类别与驱动当场给的不一味 —— 只可能是读错了发、
				// 错了侧或错了 session。这一眼不分大小，先记下来再说。
				r.drift++
				r.say("    漂移：第 %d 发账上是 %q，驱动当场给的是 %q —— 这一发的依据不能算从账上读回来的", call, fail, p.Fail)
			}
			if rule, has := POLICY[fail]; has {
				act, why = rule.Action, rule.Why
				r.decide(call, fail)
			}
			r.decisions = append(r.decisions, decision{
				Shot: call, Basis: basis, Value: strings.TrimSpace(value), Action: act, Why: why,
			})
			if !driftOf(value, p.Fail) {
				r.kpi++ // 漂移那一发不计：账上说的与当场给的不一样，这一步是谁决定的就说不清了
			}
			r.say("    决定：第 %d 发 依据=%-14s 值=%-18q 动作=%s", call, basis, strings.TrimSpace(value), act)
			t.BasisFlag, t.Basis, t.Value, t.Action, t.Why = "账上", basis, strings.TrimSpace(value), act, why
			t.LocalFail = string(p.Fail)
			t.Drift = driftOf(value, p.Fail)
			t.BasisCells = append(t.BasisCells, basis)
			t.OutCells = r.cellsOf(call, "output")
			t.InCells = r.cellsOf(call, "input")
			r.traces = append(r.traces, t)
			if strings.HasPrefix(act, "停") {
				r.settleTurn("completed", "账上这一格指向「停」：这一发做完了，整趟按 POLICY 收")
				r.stop = stopPolicy
				return
			}
		}

		// 这一发的四相都落进流里了，结在这一行。
		// **打断与正常走同一个 settleTurn** —— 分叉出两套收口代码就是「两个数并排上屏」的复发点。
		r.settleTurn("completed", "这一发做完了：剧本的这一步走到收口，下一发接着来")

		if p.Text != "" {
			r.historyOrig += p.Text + "\n"
			r.obs = "上一次结果：" + p.Text
			r.appendTranscript(call, p.Text)
		}

		if r.checkpointEvery > 0 && r.shot%r.checkpointEvery == 0 {
			r.checkpoint("每 " + strconv.Itoa(r.checkpointEvery) + " 发")
		}
		if r.turns >= r.maxTurns {
			r.stop = stopTurns
			return
		}
	}
}

// readBackCell 是那条回边本身：值只能从 read_back 的字节里读出来。
// --no-read-back 就是这个开关关掉 —— 反臂靠它把 KPI 打回 0。
func (r *run) readBackCell(call int) (basis, value string, ok bool) {
	if !r.readBack {
		return "", "", false
	}
	rb, err := r.rpc.call("kernel.read_back", map[string]any{
		"session": r.session, "call": call, "side": "output",
	})
	if err != nil || rb.IsErr {
		return "", "", false
	}
	basisTxt, claim, found := cellFromReadBack(rb.Text, "fail_kind")
	if !found {
		return "", "", false
	}
	r.declareKey("output", "fail_kind")
	_ = basisTxt
	return "fail_kind", stripBasisWord(claim), true
}

func stripBasisWord(claim string) string {
	i := strings.IndexAny(claim, " ")
	if i < 0 {
		return claim
	}
	return claim[:i]
}

func (r *run) decide(call int, fail kernel.FailKind) {
	switch fail {
	case kernel.FailBudget:
		r.foldNext = true
	case kernel.FailMalformedAction:
		r.rephrase = true
	case kernel.FailTransport, kernel.FailHTTP:
		// 原样再发：这两个动作在循环里的落点就是「下一发不折历史、不换说法，照装」。
	default:
	}
}

// confirmFold 是回边的另一半：不是「我决定折历史」就算折了，而是回账上把那一格读回来。
// 读回来是空的（内核那句「没交这一档的名单」）就当没折过 —— 意图与效果分开数，
// 否则 KPI 数的是我们打了几个旗标，而不是账认了几步。
func (r *run) confirmFold(call int, t *shotTrace) {
	if !r.readBack {
		return
	}
	rb, err := r.rpc.call("kernel.read_back", map[string]any{
		"session": r.session, "call": call, "side": "input",
	})
	if err != nil || rb.IsErr {
		t.Extra = "折历史的回读没走通（不算进 KPI）"
		return
	}
	_, claim, found := cellFromReadBack(rb.Text, "folded")
	if !found || strings.Contains(claim, "没交") || strings.Contains(claim, "空表") {
		t.Extra = "我以为折了，账上这一格是空的 —— 不计进 KPI"
		r.say("    回读确认：第 %d 发 input/folded 没有真记录（旗标 ≠ 效果）", call)
		return
	}
	r.declareKey("input", "folded")
	r.kpi++
	t.BasisCells = append(t.BasisCells, "folded")
	r.decisions = append(r.decisions, decision{
		Shot: call, Basis: "folded（输入侧）", Value: claim, Action: "折叠真落了：接着按桩续跑",
		Why: "这一条是从账上读回来的效果，不是我们打的旗标",
	})
	r.say("    回读确认：第 %d 发 input/folded = %s", call, claim)
	t.Extra = "账上确认 input/folded = " + claim
}

// declareKey 声明「我读了这一格」。内核按 读者+格 去重，所以只声明真读到的那一次。
func (r *run) declareKey(side, key string) {
	if !r.declare {
		return
	}
	id := side + "/" + key
	if r.declared[id] {
		return
	}
	d, err := r.rpc.call("kernel.declare_reader", map[string]any{"reader": "agentd", "key": key, "side": side})
	if err != nil {
		r.say("    声明读者回帧坏了：%v", err)
		return
	}
	if d.IsErr {
		r.say("    声明读者被拒：%s", firstLine(d.Text))
		return
	}
	r.declared[id] = true
	r.say("    已声明读者：agentd 读 %s/%s（这一格的话真的被用掉了）", side, key)
}

func (r *run) noteLocal(call int, value, action string) {
	r.decisions = append(r.decisions, decision{
		Shot: call, Basis: "本地信息，没问内核", Value: value, Action: action,
		Why: "这一发的下一步不是从账上读回来的，所以不计进 KPI",
	})
	r.say("    决定：第 %d 发 依据=%-14s 值=%-18q 动作=%s", call, "本地", value, action)
}

// resume 把上一次编出去的账读回来接着跑。顺序不能反：先核链再续写 ——
// 拿着一条不符的账往下写，是把错往下传给下一个读账的人。
func (r *run) resume(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		r.say("  续跑失败：读不到上一次的账（%s）：%v", path, err)
		r.refuseWhy = "读不到上一次的账"
		r.stop = stopDriver
		return false
	}
	l, err := r.rpc.call("kernel.load_journal", map[string]any{"text": string(b), "acknowledge": "replace"})
	if err != nil || l.IsErr {
		r.say("  续跑被内核拒：%s", firstLine(l.Text))
		r.refuseWhy = "内核拒收：" + firstLine(l.Text)
		r.stop = stopDriver
		return false
	}
	n := r.rpc.srv.Entries()
	shots := n / 49
	v, _ := r.rpc.call("kernel.verify", nil)
	r.say("  续跑：读回 %d 条 = %d 发；先核一遍链：%s", n, shots, firstLine(v.Text))
	if v.IsErr || strings.Contains(v.Text, "不符") {
		r.say("  链不符（或读不出原像）就不往下写 —— 这是拒写，不是照跑")
		r.refuseWhy = "链不符（或读不出原像）"
		r.stop = stopDriver
		return false
	}
	r.shot = shots
	r.turnFloor = shots
	r.resumed = n
	r.say("  发号从第 %d 发接着走", shots+1)
	r.rebuildHistory()
	return true
}

// transcriptPath 是宿主自己的往轮轨迹文件（内核的账里**存不下原文**：
// 那 49 格里只有 model_says_bytes / _kept / _sha256 三格，都是元数据）。
// 所以续跑要带历史，只能宿主自己留着，再用账上那枚指纹对一遍。
func (r *run) transcriptPath() string {
	if r.journalPath == "" {
		return ""
	}
	return r.journalPath + ".transcript"
}

type transcriptLine struct {
	Call int    `json:"call"`
	Sha  string `json:"sha"`
	Text string `json:"text"`
}

func (r *run) appendTranscript(call int, text string) {
	tf := r.transcriptPath()
	if tf == "" {
		return
	}
	line, err := json.Marshal(transcriptLine{Call: call, Sha: sha(text), Text: text})
	if err != nil {
		r.say("    往轮轨迹记不下来（不影响落账）：%v", err)
		return
	}
	f, err := os.OpenFile(tf, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		r.say("    往轮轨迹写不出去（不影响落账）：%v", err)
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

// rebuildHistory 每发都拿账上的 model_says_sha256 对一遍原文指纹：
// 对得上才敢把它当往轮轨迹用；对不上就说「这一段的原文与封好的账不一致」，
// 宁可不带历史，也不拿一句对不上的话去续跑。
func (r *run) rebuildHistory() {
	tf := r.transcriptPath()
	if tf == "" {
		r.say("  往轮轨迹：没给 --journal-out，宿主没处留原文 —— 这一趟不带历史段")
		return
	}
	b, err := os.ReadFile(tf)
	if err != nil {
		r.say("  往轮轨迹：%s 读不到（%v）—— 这一趟不带历史段", tf, err)
		return
	}
	var sb strings.Builder
	matched, checked, bad := 0, 0, []int{}
	for _, raw := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var e transcriptLine
		if json.Unmarshal([]byte(raw), &e) != nil {
			bad = append(bad, -1)
			continue
		}
		checked++
		// 对两遍：账上那枚指纹要对得上**这段原文重算出来的**，
		// 文件里自己写的那枚也要与原文一致 —— 只比文件里那两个字段，改原文就查不出来。
		want := sha(e.Text)
		rb, err := r.rpc.call("kernel.read_back", map[string]any{"session": r.session, "call": e.Call, "side": "output"})
		claim := ""
		if err == nil {
			_, claim, _ = cellFromReadBack(rb.Text, "model_says_sha256")
		}
		if strings.TrimSpace(claim) != want || e.Sha != want {
			bad = append(bad, e.Call)
			continue
		}
		matched++
		sb.WriteString(e.Text)
		sb.WriteString("\n")
	}
	if len(bad) > 0 {
		r.say("  往轮轨迹：**有 %d 发的原文与账上指纹对不上**（发号 %v）—— 不带这些段续跑", len(bad), bad)
		if matched == 0 {
			return
		}
	}
	r.historyOrig = sb.String()
	r.say("  往轮轨迹：从宿主的 %s 重建，%d/%d 发的指纹与账上 model_says_sha256 对上", filepath.Base(tf), matched, checked)
}

// ledgerCapped 问内核的 tally 那一栏：容量挡下的次数不是 49 格里的一格，
// 但它是这本账的真实状态 —— 账吃不下了还一路发，就是把发号烧光换五句同一样的拒绝。
func (r *run) ledgerCapped() string {
	const mark = "其中容量挡下 "
	t, err := r.rpc.call("kernel.tally", nil)
	if err != nil {
		return ""
	}
	i := strings.Index(t.Text, mark)
	if i < 0 {
		return ""
	}
	rest := t.Text[i+len(mark):]
	digits := ""
	for _, ch := range rest {
		if ch < '0' || ch > '9' {
			break
		}
		digits += string(ch)
	}
	if digits == "" || digits == "0" {
		return ""
	}
	return "容量挡下 " + digits + " 次"
}

func (r *run) checkpoint(tag string) {
	if r.refuseWhy != "" {
		r.say("  checkpoint(%s)：不写 --journal-out —— %s；这一趟一条本机新账都没有，不许拿空账盖掉输入那一本", tag, r.refuseWhy)
		return
	}
	args := map[string]any{"payload": r.payload}
	j, err := r.rpc.call("kernel.encode_journal", args)
	if err != nil {
		r.say("  checkpoint(%s) 失败：编字节就散了：%v", tag, err)
		return
	}
	if j.IsErr {
		r.say("  checkpoint(%s) 被拒：%s", tag, firstLine(j.Text))
		return
	}
	if r.journalPath == "" {
		r.say("  checkpoint(%s)：编出 %d 字节，但没给 --journal-out —— 内核不写盘，这一步是我自己省的", tag, len(j.Text))
		return
	}
	if err := os.WriteFile(r.journalPath, []byte(j.Text), 0o644); err != nil {
		r.say("  checkpoint(%s) 写盘失败：%v", tag, err)
		return
	}
	r.say("  checkpoint(%s)：写了 %s（%d 字节；写盘是宿主的事，内核只交字节）",
		tag, r.journalPath, len(j.Text))
	r.verifyRoundTrip(tag)
}

// verifyRoundTrip 把刚写出去的那份字节交给**另一本空账**读回来，看内核说什么。
// 起一本临时的，不是拿这本去 load —— 顶掉自己正在写的账不叫自证，叫自毁。
func (r *run) verifyRoundTrip(tag string) {
	b, err := os.ReadFile(r.journalPath)
	if err != nil {
		r.lastChain = "回读失败：" + err.Error()
		r.say("  回读（%s）：%s", tag, r.lastChain)
		return
	}
	probe := newKernelRPC(0, false)
	l, err := probe.call("kernel.load_journal", map[string]any{"text": string(b), "acknowledge": "replace"})
	if err != nil {
		r.lastChain = "回读没走到内核：" + err.Error()
		r.say("  回读（%s）：%s", tag, r.lastChain)
		return
	}
	verdict := "收下"
	if l.IsErr {
		// 载入就被拒 ⇒ 那本临时账压根没成形，再去 verify 只会得到「空账」那句假绿。
		r.lastChain = "载入被拒 —— " + strings.ReplaceAll(l.Text, "\n", " ⏎ ")
		r.say("  回读核一遍（%s，另起一本空账）：拒收 / %s", tag, r.lastChain)
		return
	}
	// 载入那句只报「读回几条」，链的三态得再问一次 verify —— 这两句不是同一件事，
	// 拿前一句当后一句会把「读得动」读成「核得过」。
	v, err := probe.call("kernel.verify", nil)
	if err != nil {
		r.lastChain = "收下之后核不动：" + err.Error()
	} else {
		r.lastChain = strings.ReplaceAll(v.Text, "\n", " ⏎ ")
	}
	r.say("  回读核一遍（%s，另起一本空账）：%s / %s", tag, verdict, r.lastChain)
}

func (r *run) closeOut() {
	r.stopTick() // 收口之前把看门狗关干净：它要是还在跑，就会往已经关了账的流里补条目
	for _, f := range r.events.takeIdleFails() {
		// 看门狗在别的 goroutine 里，不能直接碰 errLog（那是循环的单线程财产）；
		// 失败先攒在总线（有锁），到这里由循环印 —— 既不静默吞，也不竞态。
		r.errf("    补位进度没落盘：%v", f)
	}
	r.say("")
	r.say("【收口】")
	// 收口也要落一条事件：done/total 给得出来就给，结束原因是哪一态直接照六态写 ——
	// 这一条是「停了断了会怎样」那一句的账本形态，不是给人看的装饰。
	// state 只放可分档的 token（六态那句整话说的是 stop 字段，塞进 state 下游就没法按档归并）。
	// 收口不是某一步，也不属于任何一发：turn=0 且不带 step ——
	// 把发号塞进 step.no 会让 (turn, step.no) 这一对同时表示「第 N 步」和「共发了 N 发」。
	settle := Event{Kind: "run_settled", State: "settled", Evidence: "measured"}
	settle.Turn = 0
	settle.Stop = r.stop.Token() // 六态的 token，下游按档分支用这一格
	settle.StopWhy = string(r.stop)
	settle.Why = r.stopWhy // 谁叫的停（Stop 那条路写的）；没人叫停就是空 —— 不是「没叫停」也别说成「挡住了」
	done, total := r.turnFloor+r.turns, r.turnFloor+r.maxTurns
	if r.stop == stopScript {
		// 剧本走完 = 没有更多要做的了，total 取 done。
		// 拿额度当 total 会把「跑完的那一趟」在流里读成 45%（9/20），那是给界面灌假进度。
		total = done
	}
	settle.Progress = &Progress{Done: done, Total: intp(total), Limit: intp(r.turnFloor + r.maxTurns)}
	r.emit(settle)
	if r.events != nil {
		// 落在哪、落了几条、为什么没落成 —— 一律由 main 的横幅从 stats() 现读现印；
		// 这里不复制一份到 run 上（留两份就要对账，那份对账没人跑就是假账）。
		r.events.Close()
	}
	if v, err := r.rpc.call("kernel.verify", nil); err == nil {
		r.lastVerify = v.Text
		r.say("  verify：%s", firstLine(v.Text))
	}
	if t, err := r.rpc.call("kernel.tally", nil); err == nil {
		r.lastTally = t.Text
		r.say("  tally ：%s", firstLine(t.Text))
	}
	if rr, err := r.rpc.call("kernel.readers_report", nil); err == nil {
		for _, line := range strings.Split(strings.TrimRight(rr.Text, "\n"), "\n") {
			r.say("  读者  ：%s", line)
		}
	}
	r.checkpoint("收口")
	r.say("")
	r.say("账本触发过的决定：%d（共 %d 发，其中依据写「本地」的 %d 发不计）", r.kpi, r.shot, len(r.decisions)-r.kpi-r.caps)
	if r.caps > 0 {
		r.say("  另计（不与上面相加）：由内核容量那一栏促成的停 —— %d 次", r.caps)
	}
	if r.kpi == 0 {
		r.say("  => 这条回边虚接：没有任何一步「接下来干什么」是从账上读回来的。")
	}
	if r.drift > 0 {
		r.say("  漂移：%d 发的格值与当场给的类别不一致 —— 那些发的依据不算从账上读回来", r.drift)
	} else {
		r.say("  漂移：0 发（每一发读回来的故障类别都与当场给的对得上；对不上只可能是读错了发/侧/session）")
	}
	r.say("结束原因：%s", r.stop)
	r.closed = true
	if err := r.writeTrace(r.tracePath); err != nil {
		r.say("  轨迹件没写出去：%v", err)
	} else if r.tracePath != "" {
		r.say("  轨迹解剖：%s（这一趟每一发的依据是哪一格，都在这张表里）", r.tracePath)
	}
}

// describeShot 说的是这一发装了什么 —— 只报段名顺序与两个旗标，不报正文。
// 换说法要看的是「与这一趟的任务原文相比」，拿内置例子当基准会把任务驱动那一路全判成换说法。
func describeShot(a *assembly, ask string) string {
	kinds := []string{}
	for _, s := range a.segs() {
		kinds = append(kinds, string(s.kind))
	}
	return fmt.Sprintf("%s ｜ 折历史=%v ｜ 换说法=%v",
		strings.Join(kinds, " "), a.foldHistory, a.user != ask)
}

// cellsFromRecordLine 从「整发落账 49 条…」那句里取条数；取不到回 0，
// 但页面那一栏会把 0 与「读不到」分开显示，因为这两句话不一样。
func cellsFromRecordLine(s string) int {
	i := strings.Index(s, "整发落账 ")
	if i < 0 {
		return -1
	}
	rest := s[i+len("整发落账 "):]
	digits := ""
	for _, ch := range rest {
		if ch < '0' || ch > '9' {
			break
		}
		digits += string(ch)
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return -1
	}
	return n
}

func firstLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …（共 " + strconv.Itoa(strings.Count(s, "\n")+1) + " 行）"
	}
	return s
}

var _ = io.Discard

func (r *run) bufString() string {
	if b, ok := r.log.(*bytes.Buffer); ok {
		return b.String()
	}
	return ""
}

// turnNo 是落进事件流的那根发号轴：续跑时接着上一本的账数，本进程从头数的是 turns。
// 额度、停止判断看 turns（本进程跑了几发）；身份、counts、进度看 turnNo（第几发这件事）。
func (r *run) turnNo() int { return r.turnFloor + r.turns }
