// kresearch 是内核的**第一个真调用方**：它自己发 HTTP、自己在通道上数字节、
// 自己掐首帧与帧间的时间，然后把两侧的账写进 agentkernel 那道哈希链。
//
// 它是「框架的替身」，不是内核的一部分：这一份文件在 cmd/ 下面，它 import net/http。
// README 第 6 节那句「不联网」说的是**包**（package kernel 那一侧），不是这个样例。
// 这一点必须写在这里而不是留给下一个人猜：隔离闸把 net/http 归成标准库，
// 所以如果谁把 net/http 引进 package kernel，**没有任何一根针会红** ——
// 那一格是散文在守，不是机器在守。
//
// 为什么要真跑一次：contract.md 第 4 节列了 10 格「实测一轮真实调用后从未被填过」，
// 给的原因是「采集点不存在」。而这一段代码本身就是一个采集点：
// 它专挑那 10 格里属于传输层与流式的部分去量，量不到的仍然记 absent+理由（不是 0）。
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"agentkernel"
)

const base = "http://123.56.218.60:18000"

// call 是一次 HTTP 往来的**实测记录**。
// 字段的口径都写在名字里：Wire 是通道上的字节（含框架），Answer 是端点说的话（不含框架）。
// 这两个数必须分开，否则「模型说了多少话」和「线路上过了多少字节」又会长成一个 ——
// 那正是本包为 `bytes_out` 付过账的那一族。
type call struct {
	method    string
	url       string
	reqBody   []byte
	wireRaw   []byte // 通道上收到的全部字节
	answerRaw []byte // 只含「原话」：非流式是整个响应体，流式是 report_section 的正文
	status    int
	err       error

	streaming   bool
	firstByteMs int64
	events      int   // SSE 事件块数（空行分隔）
	maxGapMs    int64 // 相邻事件块之间最长的一次静默
	answered    bool  // 收到 task_done
}

func main() {
	query := "append-only 日志如何证明没有漏记"
	if len(os.Args) > 1 {
		query = os.Args[1]
	}

	c1 := doHTTP("POST", base+"/api/research/start", mustJSON(query), false)
	taskID := taskIDOf(c1.wireRaw)
	if taskID == "" {
		fmt.Printf("✗ 第 1 发没拿到 taskId（状态 %d，回复 %q）—— 这一趟在采集点之前就断了\n",
			c1.status, trim(string(c1.wireRaw)))
		os.Exit(1)
	}
	c2 := doHTTP("GET", base+"/api/research/stream?taskId="+taskID, nil, true)

	l := kernel.NewLedger()
	in1, out1 := book(c1, query, 1)
	in2, out2 := book(c2, query, 2)

	n1, err1 := l.RecordCall("kresearch", "kresearch", in1, out1)
	n2, err2 := l.RecordCall("kresearch", "kresearch", in2, out2)

	fmt.Println("== 真接口两发，落进内核的账")
	fmt.Printf("   第 1 发（非流式）%s\n     → %d 条，拒收=%v\n", describe(c1), n1, err1)
	fmt.Printf("   第 2 发（流式）  %s\n     → %d 条，拒收=%v\n", describe(c2), n2, err2)
	fmt.Printf("   %s，拒收计数 %d\n", l, l.Rejected())
	if err1 != nil || err2 != nil {
		fmt.Println("✗ 有一发整发被拒 —— 上面的条数是 0，先修账再谈落盘")
		os.Exit(1)
	}
	if vErr := l.Verify(); vErr != nil {
		fmt.Printf("✗ 链不符：%v\n", vErr)
		os.Exit(1)
	}
	fmt.Println("✓ 链自洽")

	fmt.Println("\n== contract.md 第 4 节那 10 格空洞，现在每一格是什么出处")
	empty10 := []string{"fixed_overhead", "tokens", "chunks", "first_byte_ms", "max_gap_ms",
		"retry_after_ms", "usage_in", "usage_out", "wire_bytes_in", "wire_bytes_out"}
	byKey := map[string]kernel.RecordedCell{}
	for _, c := range kernel.CellsOf(l.Docs(), "kresearch", 2, kernel.KindModelInput) {
		byKey[c.Key] = c
	}
	for _, c := range kernel.CellsOf(l.Docs(), "kresearch", 2, kernel.KindModelOutput) {
		byKey[c.Key] = c
	}
	filled, absentStill := 0, []string{}
	for _, key := range empty10 {
		c, ok := byKey[key]
		if !ok {
			absentStill = append(absentStill, key+"（这一侧压根没这一格）")
			continue
		}
		if c.Basis == kernel.BasisAbsent {
			absentStill = append(absentStill, key)
			fmt.Printf("   %-16s %-8s %s\n", key, "仍缺席", trim(c.Claim))
			continue
		}
		filled++
		fmt.Printf("   ✓ %-15s %-9s %s\n", key, c.Basis, trim(c.Claim))
	}
	fmt.Printf("   —— 10 格里今天真量到或转述的 %d 格，仍是缺席的 %d 格（%v）\n",
		filled, len(absentStill), absentStill)

	fmt.Println("\n== 出处按档分桶（谁算的）")
	t := l.Tally()
	buckets := []string{}
	for _, b := range []kernel.Basis{kernel.BasisMeasured, kernel.BasisRelayed, kernel.BasisEstimated,
		kernel.BasisModelSays, kernel.BasisAbsent} {
		buckets = append(buckets, b.String()+"="+strconv.Itoa(t.ByBasis[b.String()]))
	}
	fmt.Printf("   共 %d 条：%s\n", t.Total, strings.Join(buckets, " "))

	jb, mErr := kernel.MarshalJournal(l.Docs(), kernel.PayloadIncluded)
	fmt.Println("\n== 落盘：本轮做的格式层，第一次吃真数据")
	if mErr != nil {
		fmt.Printf("✗ 编码失败：%v\n", mErr)
		os.Exit(1)
	}
	reloaded, j, rep, lErr := kernel.LoadLedger(jb)
	if lErr != nil {
		fmt.Printf("✗ 读回来被拒：%v\n", lErr)
		os.Exit(1)
	}
	fmt.Printf("   编出去 %d 字节 → %s\n", len(jb), j)
	fmt.Printf("   %s\n", rep)
	if !rep.Decidable() {
		fmt.Println("✗ 真账的往返没做到「全部带原像且链自洽」—— 卖点没兑现")
		os.Exit(1)
	}
	_, again := reloaded.RecordCall("kresearch", "kresearch", in1, out1)
	fmt.Printf("   读回来后重记第 1 发：被拒=%v（拒因点名去重闸=%v）\n",
		again != nil, again != nil && strings.Contains(again.Error(), "已经记过"))

	const out = "报告/样例-真接口两发.jsonl"
	if wErr := os.WriteFile(out, jb, 0o644); wErr != nil {
		fmt.Printf("✗ 写盘失败：%v\n", wErr)
		os.Exit(1)
	}
	fmt.Printf("✓ 落盘：%s（%d 条）—— 这一步是**调用方**做的，内核只交字节\n", out, j.Parsed)
}

func mustJSON(query string) []byte {
	b, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		fmt.Printf("✗ 请求体编不出来：%v\n", err)
		os.Exit(1)
	}
	return b
}

// doHTTP 发一次请求，并且**在通道上逐块数**：
// 分帧数按 SSE 事件块（空行）算，不按 read 的次数算 ——
// 后者会把「一次 read 拿到三个事件」算成 0 等待，那是假的好消息。
func doHTTP(method, url string, body []byte, streaming bool) *call {
	c := &call{method: method, url: url, reqBody: body, streaming: streaming}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		c.err = err
		return c
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	started := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.err = err
		return c
	}
	defer resp.Body.Close()
	c.status = resp.StatusCode

	br := bufio.NewReaderSize(resp.Body, 64*1024)
	var wire, answer bytes.Buffer
	prev, first := started, true
	event := ""
	for {
		line, rErr := br.ReadString('\n')
		got := time.Now()
		if len(line) > 0 {
			if first {
				c.firstByteMs = got.Sub(started).Milliseconds()
				first = false
			}
			wire.WriteString(line)
			t := strings.TrimRight(line, "\r\n")
			if streaming {
				if t == "" {
					c.events++
					if g := got.Sub(prev).Milliseconds(); g > c.maxGapMs {
						c.maxGapMs = g
					}
					prev = got
				}
				switch {
				case strings.HasPrefix(t, "event: "):
					event = strings.TrimPrefix(t, "event: ")
					if event == "task_done" {
						c.answered = true
					}
				case strings.HasPrefix(t, "data: ") && event == "report_section":
					answer.WriteString(strings.TrimPrefix(t, "data: "))
				}
			}
		}
		if rErr == io.EOF {
			break
		}
		if rErr != nil {
			c.err = rErr
			break
		}
	}
	c.wireRaw = wire.Bytes()
	// 「原话」的口径只在这里定一次（规矩三：一个定义一个 owner）。
	// 非流式那一发回复就是一个 JSON —— 那**就是**它说的话，别再另数一遍。
	if streaming {
		c.answerRaw = answer.Bytes()
	} else {
		c.answerRaw = c.wireRaw
	}
	return c
}

func taskIDOf(raw []byte) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	s, _ := m["taskId"].(string)
	return s
}

// book 把一次实测折成两侧的账。
// 每一格的出处都由「这个数究竟是谁算的」决定，不由方便决定：
// 通道字节是我们数的 ⇒ measured；分词在对端 ⇒ 只能 estimated 或 relayed；没报的 ⇒ absent+理由。
func book(c *call, query string, step int) (kernel.InputMeta, kernel.OutputMeta) {
	qb := int64(len(query))
	budget, err := kernel.DeriveBudget(32768, 12,
		map[kernel.PartKind]int{kernel.KindUser: 90},
		kernel.EstimatedOf(300, "请求行+头+JSON 骨架的粗估"))
	if err != nil {
		fmt.Printf("✗ 第 %d 发的预算造不出来：%v\n", step, err)
		os.Exit(1)
	}
	in := kernel.InputMeta{
		Call:         step,
		Order:        []kernel.PartKind{kernel.KindUser},
		Parts:        []kernel.PartCount{{Kind: kernel.KindUser, Parts: 1, Bytes: qb}},
		ContentBytes: qb,
		Tokens:       kernel.EstimatedOf(qb/3, "按 1 token ≈ 3 字节的粗估（中文偏小）"),
		Budget:       budget,
	}
	out := kernel.OutputMeta{
		Call:         step,
		Streaming:    c.streaming,
		WireBytesOut: kernel.MeasuredOf(int64(len(c.reqBody))),
		WireBytesIn:  kernel.MeasuredOf(int64(len(c.wireRaw))),
		Says: kernel.Says{
			Bytes:  int64(len(c.answerRaw)),
			Kept:   int64(len(c.answerRaw)),
			SHA256: shaHex(c.answerRaw),
		},
		UsageIn:  kernel.AbsentBecause("端点响应里没有 usage 字段，我们数不出对端的分词"),
		UsageOut: kernel.AbsentBecause("端点响应里没有 usage 字段，我们数不出对端的分词"),
		Failure:  kernel.Failure{RetryAfterMs: kernel.AbsentBecause("这一发没故障，对端没让等")},
	}
	if c.streaming {
		out.Chunks = kernel.MeasuredOf(int64(c.events))
		out.FirstByteMs = kernel.MeasuredOf(c.firstByteMs)
		out.MaxGapMs = kernel.MeasuredOf(c.maxGapMs)
	} else {
		out.Chunks = kernel.AbsentBecause("这一发非流式：响应一次读完，结构上没有「帧」")
		out.FirstByteMs = kernel.AbsentBecause("这一发非流式：没有「首帧」这个量")
		out.MaxGapMs = kernel.AbsentBecause("这一发非流式：没有帧间间隔")
	}
	switch {
	case c.err != nil:
		out.Failure = kernel.Failure{
			Kind:         kernel.FailTransport,
			Detail:       trim(c.err.Error()),
			RetryAfterMs: kernel.AbsentBecause("管路失败，对端没机会说要等多久"),
			Downgrade:    c.streaming,
		}
		out.UsageIn = kernel.AbsentBecause("管路失败，这一发没拿到完整响应")
		out.UsageOut = kernel.AbsentBecause("管路失败，这一发没拿到完整响应")
	case c.status >= 400:
		out.Failure = kernel.Failure{
			Kind:         kernel.FailHTTP,
			Status:       kernel.MeasuredOf(int64(c.status)),
			Detail:       "HTTP " + strconv.Itoa(c.status) + "：" + trim(string(c.answerRaw)),
			RetryAfterMs: kernel.AbsentBecause("响应头里没有 Retry-After"),
			Downgrade:    c.streaming,
		}
	case c.streaming && !c.answered:
		// 流读完了却没有 task_done：这既不是正常收尾也不是管路断开，是形状对不上。
		// 这一类**收到过**对端响应（200 都在），所以状态码是量到的，不是缺席 ——
		// 以前它被迫记成 0，那是拿类别名冒充「收到没有」那根轴逼出来的假数。
		out.Failure = kernel.Failure{
			Kind:         kernel.FailProtocol,
			Status:       kernel.MeasuredOf(int64(c.status)),
			Detail:       "流正常结束但没收到 task_done 事件，收尾形状对不上",
			RetryAfterMs: kernel.AbsentBecause("协议形状问题，原样重发不会变好"),
		}
	}
	if c.answered {
		out.Finish = kernel.Finish{Reason: "task_done", Said: true}
	}
	return in, out
}

func shaHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func describe(c *call) string {
	return fmt.Sprintf("%s %s → 状态 %d、发 %d 字节、通道收到 %d 字节、原话 %d 字节、%d 个事件块、首帧 %dms、最长静默 %dms、err=%v",
		c.method, strings.Replace(c.url, base, "", 1), c.status, len(c.reqBody), len(c.wireRaw),
		len(c.answerRaw), c.events, c.firstByteMs, c.maxGapMs, c.err)
}

func trim(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}
