package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 退出码各管一种结束原因（这一条是收口那块的要求，不是给好看用的）：
//
//	0 跑完剧本 / 2 驱动这一路跑不了 / 3 轮次额度到 / 4 账上 fail_kind 指向「停」/ 5 这本账已到上限 / 6 外部打断
const (
	rcScript = 0
	rcNotRun = 2
	rcTurns  = 3
	rcPolicy = 4
	rcCapped = 5
	rcAbort  = 6
)

func main() {
	var (
		driverName  = flag.String("driver", "scripted", "驱动：scripted（本地剧本，不开 socket）/ fake（本机假端点，真过网线）/ real / session / ledger（本机执行器：按 -task 真算 ledger_add，不联网）")
		task        = flag.String("task", userAsk, "任务输入面：要算的加法题（例：把 27、15、8、34 加起来）；只认十进制非负整数")
		oracleOut   = flag.String("oracle-out", "", "算术 oracle 的判读写到这个文件（UTF-8 JSON）；空 = 只印在 stdout")
		shotDelay   = flag.Duration("shot-delay", 0, "每发之前等多久（真端点本来就慢；也让外部打断来得及被看见）")
		tickIdle    = flag.Duration("progress-tick", 10*time.Second, "距上一条落盘事件多久就补一条进度/解释（E5 的 10s 兜底；填 0 = 关，关会说出来）")
		script      = flag.String("script", "full", "剧本：full（跑到按账停）/ happy（跑到剧本完）")
		session     = flag.String("session", "agentd-"+strconv.Itoa(os.Getpid()), "session 轴：一发由 session+发号+侧三个轴定身份")
		maxTurns    = flag.Int("max-turns", 12, "最多发几发；到就停（这是「额度到」那一种结束原因）")
		ckptEvery   = flag.Int("checkpoint-every", 0, "每几发编一次账交出去；0 = 只在收口编")
		journal     = flag.String("journal-out", defaultJournalPath(), "宿主把编出的字节写到这个文件（内核不写盘）；空 = 只编不写")
		payload     = flag.String("payload", "included", "编账时带不带原像：included / omitted")
		readBack    = flag.Bool("read-back", true, "关掉它 = 那条回边断开（反臂：KPI 必须回到 0）")
		declare     = flag.Bool("declare-readers", true, "读到的格顺手声明读者（框架侧那一半）")
		entryCap    = flag.Int("entry-cap", 0, "这本账最多写多少条；0 = 不拦")
		traceOut    = flag.String("trace-out", filepath.Join(os.TempDir(), "agentd-轨迹.html"), "循环跑完吐一份能点开的轨迹解剖；空 = 不写")
		resumePath  = flag.String("resume", "", "读回上一次 checkpoint 出来的账接着跑（先核链，不符就不往下写）")
		eventsOn    = flag.Bool("events", true, "把每一发的过程落成 append-only 事件日志（宿主侧，不改内核调用、不改 stdout 正文）")
		eventsPath  = flag.String("events-out", "", "事件日志落哪；空 = 用 --journal-out 同位置加 .events.jsonl")
		ackLimit    = flag.Int64("ack-limit-ms", int64(ackLimitMS), "回执预算毫秒数（默认取契约那个数）；放宽它只影响这一次判读，读数行里会写明用的哪个")
		eventsServe = flag.Bool("events-serve", false, "把事件日志接成 SSE 推出去（127.0.0.1 随机空闲端口，这一趟跑完随进程关）")
		checkLog    = flag.String("check-log", "", "只读判据模式：拿一本事件日志跑四支判据，一行一个读数交出去再退出；空=正常跑循环")
		skipOut     = flag.Int("skip-outcomes", 0, "剧本：跳过前 N 个结局（配合 --resume 演「重启后接着跑」）")
	)
	flag.Parse()

	if *checkLog != "" {
		// 判据口径只有这一份：尺子（冒烟、别的会话）不许再自己数一遍。
		os.Exit(checkLogAgainst(os.Stdout, *checkLog, *ackLimit))
	}

	// 横幅只走 stderr：stdout 留给能被逐字比对的运行正文。
	fmt.Fprintf(os.Stderr, "agentd：驱动=%s（自主=%v）；session=%s；读回=%v；声明读者=%v\n",
		*driverName, driverAutonomous(*driverName, *script, *task), *session, *readBack, *declare)
	if *driverName == "scripted" {
		fmt.Fprint(os.Stderr, "  注意：这一路是剧本走出来的绿，不是模型自主决定的绿。\n")
	}
	if *driverName == "ledger" {
		fmt.Fprint(os.Stderr, "  注意：这一路是本机执行器（不是模型）：答案由它自己两两相加算出来，另有算术 oracle 核。\n")
	}

	drv, err := buildDriver(*driverName, *script, *task)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentd:", err)
		os.Exit(rcNotRun)
	}
	if s, ok := drv.(*scripted); ok && *skipOut > 0 {
		if *skipOut > len(s.q) {
			fmt.Fprintf(os.Stderr, "agentd: 剧本只有 %d 个结局，跳不过 %d 个\n", len(s.q), *skipOut)
			os.Exit(rcNotRun)
		}
		s.i = *skipOut
	}
	r := &run{
		rpc:             newKernelRPC(*entryCap, false),
		drv:             drv,
		session:         *session,
		ask:             *task,
		maxTurns:        *maxTurns,
		checkpointEvery: *ckptEvery,
		readBack:        *readBack,
		declare:         *declare,
		journalPath:     *journal,
		payload:         *payload,
		log:             os.Stdout,
		declared:        map[string]bool{},
		tracePath:       *traceOut,
		caveat: "KPI 数的是「账驱动过几步决定」，不是「模型有多聪明」；" +
			"这一份是循环自己的轨迹解剖，不是 kaudit 那块只读面板（那三件决定还没圈）。",
	}
	abort := make(chan struct{})
	r.abort = abort
	r.errLog = os.Stderr
	if *eventsOn {
		p, note := eventLogPath(*journal, *eventsPath)
		if note != "" {
			fmt.Fprintln(os.Stderr, "agentd:", note)
		}
		r.events = newEventBus(p)
	}
	if *eventsServe {
		// 投送层只读那本日志，不自带第二本账；所以它起不起来，全看落点可不可读。
		// 起不来也不改退出码 —— 落盘与投送是两件事，投送失败不该把一趟跑完的账判成没跑完。
		path, _, degraded := r.events.stats()
		switch {
		case path == "" || degraded != "":
			why := "事件层没有落点（只在内存数序）"
			if degraded != "" {
				why = "事件层降级：" + degraded
			}
			fmt.Fprintf(os.Stderr, "agentd: --events-serve 要的是盘上那本日志，可 %s ⇒ 投送不起，resumable 那格照实写 false\n", why)
		default:
			addr, _, err := serveEvents(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "agentd: 事件投送起不来（%v）⇒ 只落盘，不推流\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "agentd: 事件投送 http://%s/events（游标=session:run:turn:seq，这一趟跑完随进程关）\n", addr)
			}
		}
	}
	r.shotDelay = *shotDelay
	r.tickIdle = *tickIdle
	// 看门狗开没开必须上屏：关了之后那条流只是安静，读的人分不清「快」与「没人报」——
	// 三态不许塌，旗标也要留第三态。
	switch {
	case r.events == nil:
		fmt.Fprintln(os.Stderr, "agentd: 没有事件层（没给 --events-log）⇒ 10s 兜底进度没有可补的那本账，不开")
	case r.tickIdle <= 0:
		fmt.Fprintln(os.Stderr, "agentd: 10s 兜底进度已关（-progress-tick 0）⇒ 静默期不会有任何一条说明")
	default:
		fmt.Fprintf(os.Stderr, "agentd: 10s 兜底进度开：静默过 %v 补 progress_tick，total 算不出补 why_late\n", r.tickIdle)
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	go func() {
		select {
		case <-sigs:
			fmt.Fprintln(os.Stderr, "agentd: 收到打断信号 —— 先把这一段已经落进账的东西交出去再走")
			r.Stop("signal:SIGINT") // 与进程内叫停同一个入口：两套停止入口就是两套收口代码
		case <-abort:
		}
	}()
	r.tRequest = time.Now() // 请求入口：进程拿到这个任务的那一刻（针1 的锚点就钉在这里）
	r.say("任务：%s（只用一个两数相加的工具，一步步来）", *task)
	if *resumePath != "" {
		if !r.resume(*resumePath) {
			r.closeOut()
			printEventBanner(r)
			if c, ok := drv.(Closer); ok {
				_ = c.Close()
			}
			os.Exit(exitOf(r.stop))
		}
	}
	r.loop()

	// 算术 oracle：必须在收口之前判 —— 判定要进结束原因与事件流，
	// 不许「账照收、答案错了还退 0」。只对交了答案的驱动（ledger）跑；
	// 别的驱动只在给了 --oracle-out 时写一份 absent + 理由，不冒充量过。
	if _, ok := drv.(Answerer); ok || *oracleOut != "" {
		rd := applyOracle(r, drv)
		if *oracleOut != "" {
			if n, err := writeOracleFile(*oracleOut, rd); err != nil {
				r.say("  oracle 读数没写出去：%v", err)
			} else {
				r.say("  oracle 读数：%s（%d 字节，先写 .part 再换名）", *oracleOut, n)
			}
		}
		r.say("  算术 oracle：期望=%d 实得=%d 调用=%d/%d ⇒ %s", rd.Expected, rd.Got, rd.Calls, rd.CallsExpected, rd.Verdict)
		if rd.Verdict == "mismatch" {
			r.say("  为什么不算跑成：%s", rd.Why)
		}
	}
	r.closeOut()
	printEventBanner(r)
	if c, ok := drv.(Closer); ok {
		_ = c.Close() // 本机假端点收口：这一趟开的那只耳朵要关掉
	}
	os.Exit(exitOf(r.stop))
}

// eventLogPath 定事件层的落点。两种「没落点」要分开说：本来就没给（--journal-out 与
// --events-out 都空），和给了但撞车 —— 后者撞的是账本，MkdirAll 会把 j.jsonl 变成目录，
// 账写不出去而退出码照旧 0。撞车这一族宁可当场不落事件，也不许动账本。
func eventLogPath(journalPath, eventsPath string) (path, note string) {
	if eventsPath != "" {
		clean := filepath.Clean(eventsPath)
		if journalPath != "" {
			j := filepath.Clean(journalPath)
			if clean == j || filepath.Dir(clean) == j {
				return "", "事件落点与 --journal-out 撞车（" + eventsPath + " 会把账本路径当目录建）⇒ 这一趟不落事件，也不动账本"
			}
		}
		return clean, ""
	}
	if journalPath != "" {
		return strings.TrimSuffix(journalPath, ".jsonl") + ".events.jsonl", ""
	}
	return "", "事件层开了但没有落点（--journal-out 与 --events-out 都空）⇒ 只在内存里数序，一条都不落盘"
}

// printEventBanner 把事件层这一趟的三态印在 stderr：落了 / 没落点 / 坏了。
// 塌成一态（只报条数）就是「报落了 20 条却 path 为空」那一族假账。
func printEventBanner(r *run) {
	if r.events == nil {
		fmt.Fprint(os.Stderr, "agentd: 事件层没开（--events=false），这一趟一条都不落\n")
		return
	}
	path, written, degraded := r.events.stats()
	if degraded != "" {
		fmt.Fprintf(os.Stderr, "agentd: 事件层降级（%s）⇒ 这一条不落盘，只在内存数了 %d 条\n", degraded, written)
		return
	}
	if path == "" {
		fmt.Fprintf(os.Stderr, "agentd: 事件层没有落点 ⇒ 只在内存数了 %d 条，盘上一条都没有\n", written)
		return
	}
	fmt.Fprintf(os.Stderr, "agentd: 事件层 %d 条 → %s\n", written, path)
}

func exitOf(s stopReason) int {
	switch s {
	case stopScript:
		return rcScript
	case stopTurns:
		return rcTurns
	case stopPolicy:
		return rcPolicy
	case stopCapped:
		return rcCapped
	case stopAbort:
		return rcAbort
	default:
		return rcNotRun
	}
}

func defaultJournalPath() string {
	return filepath.Join(os.TempDir(), "agentd-journal.jsonl")
}

func driverAutonomous(name, script string, task ...string) bool {
	d, err := buildDriver(name, script, task...)
	if err != nil {
		return false
	}
	return d.Autonomous()
}
