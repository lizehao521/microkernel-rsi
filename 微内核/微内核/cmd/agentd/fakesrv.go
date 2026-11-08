package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentkernel"
)

// 本机假端点：真开 socket、真发 HTTP、真数字节，但**它不是模型**。
// 为什么要它：剧本那一路压根不发网，于是「过网线字节」只能记缺席；
// 有了这一路，transport / http / config / protocol / malformed_action 五类
// 才有真的管路可分 —— 而凭据那一档仍然 NOT_RUN，不许拿本机假端点冒充真跑过模型。

type fakeEndpoint struct {
	ln  net.Listener
	srv *http.Server
	q   []proposal
	mu  sync.Mutex
}

func newFakeEndpoint(script string) (*fakeEndpoint, error) {
	q, err := scriptFor(script)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("agentd: 本机假端点开不了口：%v", err)
	}
	f := &fakeEndpoint{ln: ln, q: q}
	f.srv = &http.Server{Handler: http.HandlerFunc(f.serve)}
	go func() { _ = f.srv.Serve(ln) }()
	return f, nil
}

func (f *fakeEndpoint) peek() (proposal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.q) == 0 {
		return proposal{}, false
	}
	return f.q[0], true
}

func (f *fakeEndpoint) URL() string { return "http://" + f.ln.Addr().String() + "/v1/chat/completions" }

func (f *fakeEndpoint) Close() error { return f.srv.Close() }

func (f *fakeEndpoint) next() (proposal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.q) == 0 {
		return proposal{}, false
	}
	p := f.q[0]
	f.q = f.q[1:]
	return p, true
}

type chatFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatToolCall struct {
	ID       string       `json:"id"`
	Function chatFunction `json:"function"`
}

type chatMessage struct {
	Content   string         `json:"content,omitempty"`
	ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
}

type chatUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

type chatError struct {
	Message string `json:"message"`
}

type chatChoice struct {
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatResponse struct {
	Object  string       `json:"object"`
	Model   string       `json:"model"`
	Usage   *chatUsage   `json:"usage,omitempty"`
	Choices []chatChoice `json:"choices"`
	Error   *chatError   `json:"error,omitempty"`
}

func (f *fakeEndpoint) serve(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	p, ok := f.next()
	if !ok {
		http.Error(w, `{"error":{"message":"剧本跑完，端点没有下一条了"}}`, http.StatusNotImplemented)
		return
	}
	switch p.Fail {
	case kernel.FailTransport:
		// 管路没通：接管连接然后直接关掉，一个字节响应都不回。
		hj, is := w.(http.Hijacker)
		if !is {
			http.Error(w, "hijack 不可用", 500)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
		return
	case kernel.FailNone:
		if p.Stream {
			// SSE：真分帧、帧之间真等待，于是 chunks / first_byte_ms / max_gap_ms
			// 这三格第一次有**量得到**的东西可量。
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				_ = f
			}
			parts := splitChunks(p.Text)
			for _, s := range parts {
				chunk := chatResponse{Object: "chat.completion.chunk", Model: fakeModelID,
					Choices: []chatChoice{{Message: chatMessage{Content: s}, FinishReason: ""}}}
				_, _ = fmt.Fprintf(w, "data: %s\n\n", mustJSON(chunk))
				_ = flush(w)
				time.Sleep(12 * time.Millisecond)
			}
			last := chatResponse{Object: "chat.completion.chunk", Model: fakeModelID,
				Choices: []chatChoice{{Message: chatMessage{}, FinishReason: p.FinishReason}}}
			if p.UsageIn > 0 || p.UsageOut > 0 {
				last.Usage = &chatUsage{PromptTokens: p.UsageIn, CompletionTokens: p.UsageOut}
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", mustJSON(last))
			_ = flush(w)
			return
		}
		body := chatResponse{
			Object: "chat.completion", Model: fakeModelID,
			Choices: []chatChoice{{Message: chatMessage{Content: p.Text}, FinishReason: p.FinishReason}},
		}
		if p.UsageIn > 0 || p.UsageOut > 0 {
			body.Usage = &chatUsage{PromptTokens: p.UsageIn, CompletionTokens: p.UsageOut}
		}
		writeJSON(w, http.StatusOK, body)
	case kernel.FailMalformedAction:
		msg := chatMessage{Content: p.Text, ToolCalls: []chatToolCall{
			{ID: "call_bad", Function: chatFunction{Name: toolName, Arguments: badArgs}},
		}}
		body := chatResponse{Object: "chat.completion", Model: fakeModelID,
			Choices: []chatChoice{{Message: msg, FinishReason: "tool_calls"}}}
		writeJSON(w, http.StatusOK, body)
	case kernel.FailProtocol:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{这截不是 JSON`))
	default:
		code := p.Status
		if code == 0 {
			code = http.StatusInternalServerError
		}
		if p.RetryAfterMs >= 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(p.RetryAfterMs/1000)+1))
		}
		writeJSON(w, code, chatResponse{Error: &chatError{Message: p.Detail}})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// fakeDriver 真的把请求发出去，也真的在通道上数字节。
type fakeDriver struct {
	f    *fakeEndpoint
	turn int
}

func (fakeDriver) Name() string     { return "fake" }
func (fakeDriver) Autonomous() bool { return false }

func (d *fakeDriver) Propose(turn int, obs string, body []byte) (proposal, error) {
	d.turn = turn
	// 预算那一类是我们自己掐的闸，压根不该发出去：先看一眼下一发是什么。
	if p, ok := d.f.peek(); ok && p.Fail == kernel.FailBudget {
		d.f.next()
		return proposal{
			Fail: kernel.FailBudget, Sent: false, BytesIn: -1, RetryAfterMs: -1,
			Detail: p.Detail + "（客户端在发之前就掐了，一个字节都没过网线）",
		}, nil
	}
	return d.do(body)
}

func (d *fakeDriver) do(body []byte) (proposal, error) {
	start := proposal{Sent: true, BytesIn: -1, RetryAfterMs: -1, Status: 0}
	client := &http.Client{}
	resp, err := client.Post(d.f.URL(), "application/json", bytes.NewReader(body))
	if err != nil {
		start.Fail = kernel.FailTransport
		start.Detail = "管路没通（本机假端点把连接直接关了）：" + shortErr(err)
		return start, nil
	}
	defer func() { _ = resp.Body.Close() }()
	start.Status = resp.StatusCode
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "text/event-stream") {
		return readSSE(resp, start)
	}
	raw, rerr := io.ReadAll(resp.Body)
	if rerr != nil {
		start.Fail = kernel.FailTransport
		start.Detail = "响应读到一半断了：" + shortErr(rerr)
		return start, nil
	}
	start.BytesIn = int64(len(raw))
	var cr chatResponse
	if json.Unmarshal(raw, &cr) != nil {
		start.Fail = kernel.FailProtocol
		start.Detail = "收到 200 但体读不出来（前 40 字节：" + cut(string(raw), 40) + "）"
		start.ReportsModel = false
		return start, nil
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		start.Fail = kernel.FailHTTP
		start.Detail = "对端回 " + strconv.Itoa(resp.StatusCode)
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if sec, e := strconv.Atoi(ra); e == nil {
				start.RetryAfterMs = sec * 1000
			}
		}
		if msg := errMsg(cr); msg != "" {
			start.Detail += "：" + msg
		}
	case resp.StatusCode >= 400:
		start.Fail = kernel.FailConfig
		start.Detail = "对端回 " + strconv.Itoa(resp.StatusCode)
		if msg := errMsg(cr); msg != "" {
			start.Detail += "：" + msg
		}
	default:
		start.ReportsModel = cr.Model != ""
		start.Text, start.FinishReason, start.FinishSaid = pickReply(&cr)
		if cr.Usage != nil {
			start.UsageIn, start.UsageOut = cr.Usage.PromptTokens, cr.Usage.CompletionTokens
		}
		start.ToolCalls = len(cr.Choices[0].Message.ToolCalls)
		for _, tc := range cr.Choices[0].Message.ToolCalls {
			if tc.Function.Name == "" || !json.Valid([]byte(tc.Function.Arguments)) {
				start.Malformed++
			}
		}
		if start.Malformed > 0 {
			start.Fail = kernel.FailMalformedAction
			start.Streak = 1
			start.Detail = "端点回得通，但那个提议执行不了（参数不是合法 JSON 或没名字）"
		}
	}
	return start, nil
}

func errMsg(cr chatResponse) string {
	if cr.Error != nil {
		return cr.Error.Message
	}
	return ""
}

func pickReply(cr *chatResponse) (text, reason string, said bool) {
	if len(cr.Choices) == 0 {
		return "", "", false
	}
	return cr.Choices[0].Message.Content, cr.Choices[0].FinishReason, cr.Choices[0].FinishReason != ""
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.Index(s, ":"); i > 0 && len(s) > 90 {
		return cut(s, 90)
	}
	return cut(s, 90)
}

const fakeModelID = "fake-endpoint-127（本机，不是模型）"

const (
	toolName = "ledger_add"
	badArgs  = `{"a":` // 故意截断：这不是合法 JSON，端点回得通但那个提议执行不了
)

// readSSE 在通道上一帧一帧地数：帧数、首帧等待、最大帧间间隔都是这里量出来的，
// 不是对端报的 —— 所以这三格记 measured 站得住。
func readSSE(resp *http.Response, p proposal) (proposal, error) {
	br := bufio.NewReader(resp.Body)
	t0 := time.Now()
	var last time.Time
	p.Stream = true
	p.WireFormat = "chat.completion.chunk"
	var sb strings.Builder
	gap := time.Duration(0)
	recv := 0
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			recv += len(line)
			if now := time.Now(); strings.HasPrefix(strings.TrimSpace(line), "data: ") {
				p.Chunks++
				if p.Chunks == 1 {
					p.FirstMS = now.Sub(t0).Milliseconds()
				} else if !last.IsZero() {
					if d := now.Sub(last); d > gap {
						gap = d
					}
				}
				last = now
			}
			payload := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "data: "))
			if payload != "" && payload != "[DONE]" {
				var ch chatResponse
				if json.Unmarshal([]byte(payload), &ch) == nil {
					if len(ch.Choices) > 0 {
						sb.WriteString(ch.Choices[0].Message.Content)
						if r := ch.Choices[0].FinishReason; r != "" {
							p.FinishReason, p.FinishSaid = r, true
						}
					}
					if ch.Usage != nil {
						p.UsageIn, p.UsageOut = ch.Usage.PromptTokens, ch.Usage.CompletionTokens
					}
					if ch.Model != "" {
						p.Model, p.ReportsModel = ch.Model, true
					}
				} else {
					p.Fail = kernel.FailProtocol
					p.Detail = "流式帧读得出来但体不是 JSON：" + cut(payload, 40)
				}
			}
		}
		if err != nil {
			break
		}
	}
	p.BytesIn = int64(recv)
	p.MaxGap = gap.Milliseconds()
	p.Text = sb.String()
	if p.Fail == kernel.FailNone && p.Chunks == 0 {
		p.Fail = kernel.FailProtocol
		p.Detail = "说是流式，一帧都没收到（这一格在真端点上就是协议错）"
	}
	return p, nil
}

func splitChunks(s string) []string {
	out := []string{}
	r := []rune(s)
	for i := 0; i < len(r); i += 6 {
		end := i + 6
		if end > len(r) {
			end = len(r)
		}
		out = append(out, string(r[i:end]))
	}
	return out
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func flush(w http.ResponseWriter) error {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
