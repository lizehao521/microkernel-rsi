package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 事件投送（E4）：把已经落盘的那本 append-only 日志接成 SSE 推给界面。
//
// 一条设计立场先说清楚：**投送层只读日志，不自带第二本账**。
// 游标就是事件自己的身份四元组 `<session>:<run>:<turn>:<seq>` —— 与日志同一套身份，
// 所以「重放两次得到同一序列」是这本账的性质，不是这层代码的性质。
// 另起一份内存队列就是双库分叉：断线一次就两边各数各的。
//
// 为什么游标要带 run：日志是 append-only 跨趟接续的（E1），同一个 session 连跑两趟
// 就会有两条 turn=1；而 seq 是**发内**序号（Emit 按 turn 分开数），全局也不唯一。
// 所以唯一能定「哪儿之后」的全序只有日志自己的行序 —— 三元组游标在这里不够用。

const sseCursorSep = ":"

// cursor 是游标的解析结果：一句字符串背后是四个字段。坏游标当场报错，
// 不当 0 号位处理 —— 把坏游标当「从头来」会让断线重连把已经看过的一步再播一遍。
type cursor struct {
	session string
	run     string
	turn    int
	seq     int
}

// formatCursor 造 SSE 的 id 字段：`<session>:<run>:<turn>:<seq>`。
func formatCursor(e Event) string {
	return fmt.Sprintf("%s%s%s%s%d%s%d", e.Session, sseCursorSep, e.Run, sseCursorSep, e.Turn, sseCursorSep, e.Seq)
}

// parseCursor 解开客户端递回来的游标。
func parseCursor(s string) (cursor, error) {
	parts := strings.Split(strings.TrimSpace(s), sseCursorSep)
	if len(parts) != 4 {
		return cursor{}, fmt.Errorf("游标 %q 不是 <session>:<run>:<turn>:<seq> 四段", s)
	}
	t, err := strconv.Atoi(parts[2])
	if err != nil {
		return cursor{}, fmt.Errorf("游标第三段不是数：%q", parts[2])
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil {
		return cursor{}, fmt.Errorf("游标第四段不是数：%q", parts[3])
	}
	return cursor{session: parts[0], run: parts[1], turn: t, seq: n}, nil
}

// replayTail 取出游标**之后**的事件：从游标那条所在的行号往下切。
//
// 不按 seq、也不按 (turn,seq) 定序 —— seq 是发内的，跨发跨趟都会重复；日志的行序才是
// 这本账唯一的全序（append-only，E1 的 TestEventLogAppendsAcrossRuns 钉的就是它）。
func replayTail(log []Event, idx int) []Event {
	if idx < 0 || idx >= len(log) {
		return []Event{}
	}
	return log[idx+1:]
}

// locate 找游标声称的那一条：四元组（session,run,turn,seq）全对才算找到。
// 返回 (行号, 命中次数)。命中次数 >1 就是身份撞车 —— 两条长得一模一样，
// 重放会指错，这不是投送层能兜的事，得先修这本账。
func locate(log []Event, cur cursor) (int, int) {
	idx, hits := -1, 0
	for i, e := range log {
		if e.Session == cur.session && e.Run == cur.run && e.Turn == cur.turn && e.Seq == cur.seq {
			hits++
			if idx < 0 {
				idx = i
			}
		}
	}
	return idx, hits
}

// acceptOf 找出游标那一发的受理事件（同一 session+run+turn）。
func acceptOf(log []Event, cur cursor) *Event {
	var found *Event
	for i := range log {
		e := log[i]
		if e.Kind == "turn_accepted" && e.Session == cur.session && e.Run == cur.run && e.Turn == cur.turn {
			found = &log[i]
		}
	}
	return found
}

// ValidateReplay 是针6 的判据，吃读回来的整本日志 + 一个游标：
//
//	游标那条必须在这本账里**唯一**存在（0 条=接不上，>1 条=身份撞车，两种都别想续）；
//	接不上时，那一发的受理事件**必须**明写 resumable=false —— nil 是没量过，true 是声称能续却续不了，
//	  两种都算谎（研究件 §5.2.6 那条红线）；
//	接得上时，尾部就是游标之后的那些行，游标那条不许再播一遍。
//
// 「重放两次得到同一序列」不在这里判：判据吃的是同一份切片，比它自己是恒真的。
// 那一族由 events_sse_test.go 连打两次 HTTP 来钉（走盘、走网线）。
func ValidateReplay(log []Event, c string) error {
	if c == "" {
		return nil // 没带 Last-Event-ID：首连，不做续传判断
	}
	cur, err := parseCursor(c)
	if err != nil {
		return err
	}
	idx, hits := locate(log, cur)
	if hits > 1 {
		return fmt.Errorf("游标 %q 在日志里有 %d 条同身份记录 —— 身份四元组不唯一，重放会指错", c, hits)
	}
	if hits == 1 {
		// 尾部不许再把游标那条播一遍（重放幂等的那一半）。
		for _, e := range replayTail(log, idx) {
			if e.Session == cur.session && e.Run == cur.run && e.Turn == cur.turn && e.Seq == cur.seq {
				return fmt.Errorf("游标 %q 之后又冒出同身份的一条 —— 这本账的身份四元组不唯一", c)
			}
		}
		return nil
	}
	// 接不上：只看游标那一发自己有没有老实说不可续。老实说了，接不上是诚实的结果。
	acc := acceptOf(log, cur)
	if acc == nil {
		return fmt.Errorf("游标 %q 在日志里不存在，且那一发连受理事件都没有 —— 没得续", c)
	}
	if acc.Accept == nil || acc.Accept.Resumable == nil {
		return fmt.Errorf("游标 %q 接不上；受理事件又没写 resumable（nil=没量过）—— 界面只能猜", c)
	}
	if *acc.Accept.Resumable {
		return fmt.Errorf("游标 %q 接不上；受理事件却写了 resumable=true —— 声称能续却续不了", c)
	}
	return nil
}

// sseHandler 把一本日志接成 text/event-stream 推出去，并支持 Last-Event-ID 重放。
//
// 边界三条，都写死在响应里：
//   - 日志读不动（文件不在/坏了）：503，不假装能续；
//   - 续传判断没过：409，并把判据那句话原样递出去；
//   - 断线之后能不能续，由**受理事件那格 resumable** 说了算，这里不改口。
func sseHandler(path string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log, err := ReadEvents(path)
		if err != nil {
			http.Error(w, "事件日志读不动："+err.Error(), http.StatusServiceUnavailable)
			return
		}
		c := r.Header.Get("Last-Event-ID")
		if c == "" {
			c = r.URL.Query().Get("after")
		}
		if err := ValidateReplay(log, c); err != nil {
			http.Error(w, "续传判断没过："+err.Error(), http.StatusConflict)
			return
		}
		// 走到这里游标要么为空（首连），要么解得开且在这本账里唯一存在。
		// 不在这里「解不开就当从头播」—— 那会把断线重连变成重播已经看过的一步。
		tail := log // 首连：整本都是「游标之后」
		lost := ""
		if c != "" {
			cur, _ := parseCursor(c)
			idx, hits := locate(log, cur)
			tail = replayTail(log, idx)
			if hits == 0 {
				// 判据放了这一支（那一发自己老实写了不可续），但**零帧不等于追平**：
				// 客户端要能分辨「你要的都在我这儿了」与「你断的那段我接不上」（M5 P1-1）。
				lost = "游标 " + c + " 接不上；那一发明说不可续，缺的那段不再补"
			}
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "这一路不支持流式（要 http.Flusher）", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		if lost != "" {
			fmt.Fprintf(w, "event: resume-lost\ndata: %s\n\n", lost)
			fl.Flush()
		}
		for _, e := range tail {
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "id: %s\ndata: %s\n\n", formatCursor(e), b)
			fl.Flush()
		}
	})
}

// serveEvents 在 127.0.0.1 的**随机空闲端口**上起投送层，返回监听地址与关停函数。
// 端口不写死、只绑回环 —— 别的会话在同一台机器上跑，占谁的端口要先验 owner。
func serveEvents(path string) (addr string, stop func(), err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{Handler: eventsMux(sseHandler(path)), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), func() { _ = srv.Close() }, nil
}

// eventsMux 认两条路：/events 播帧，/ 出那张三行页（E7）。别的路径仍旧 404 ——
// 加一条路是把计划里的那一页接上，不是把「任何路径都回东西」当成路由表。
func eventsMux(h http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/events", h)
	mux.Handle("/", pageHandler())
	return mux
}

// idsOf 把一段 SSE 响应体里的 `id:` 序列抽出来（测两次重放是否同一序列）。
func idsOf(body string) []string {
	out := []string{}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "id: ") {
			out = append(out, strings.TrimPrefix(line, "id: "))
		}
	}
	return out
}
