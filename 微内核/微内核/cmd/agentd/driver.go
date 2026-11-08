package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"agentkernel"
)

// Driver 是「下一步问谁」的那一层接缝。三个位可以互换，同一个循环体一行不改：
//
//	scripted —— 本地剧本，不联网、不要凭据；它的绿**只能叫剧本绿**
//	ledger   —— 本机执行器：按 -task 真算 ledger_add，不是模型；答案另有算术 oracle 核
//	real     —— 真端点；本机 env 里没有凭据，所以它现在只会回 NOT_RUN
//	session  —— 把 Qoder 会话当端点；本轮未接，同样回 NOT_RUN
type Driver interface {
	Name() string
	// Autonomous 说的是「这一发的内容是谁决定的」。剧本永远是 false ——
	// 这一句会印进运行横幅和账本说明里，不许在写报告时偷偷升格成「模型自主决定」。
	Autonomous() bool
	Propose(turn int, obs string, body []byte) (proposal, error)
}

// Closer 是给起了本地端点的那一路收口的；没有它的主机不必实现。
type Closer interface{ Close() error }

type scripted struct {
	q []proposal
	i int
}

func (s *scripted) Name() string     { return "scripted" }
func (s *scripted) Autonomous() bool { return false }
func (s *scripted) Done() bool       { return s.i >= len(s.q) }
func (s *scripted) Left() int        { return len(s.q) - s.i }

func (s *scripted) Propose(turn int, obs string, body []byte) (proposal, error) {
	if s.Done() {
		return proposal{}, errors.New("剧本跑完，端点没有下一条了（这一趟的收口不该由我编）")
	}
	p := s.q[s.i]
	p.Stream = false // 剧本不开 socket：分帧那三格在这一路上只能是缺席，不是 0
	s.i++
	return p, nil
}

type realAPI struct {
	task   string
	closed bool
	last   string
}

func (d *realAPI) Name() string     { return "real" }
func (d *realAPI) Autonomous() bool { return true }
func (d *realAPI) Done() bool       { return d.closed }

func envFirst(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

// Propose 问一次真端点：OpenAI 式非流式 chat/completions。
// 端点、模型、口令全从环境变量来；口令只进请求头，错误与读数里都擦掉。
// 这一路交的是模型的话，不是可复算的执行器答案 —— Oracle 判的是「话里最后一个整数」。
func (d *realAPI) Propose(turn int, obs string, body []byte) (proposal, error) {
	base := envFirst("AGENTNES_BASE_URL", "AGNES_BASE_URL")
	key := envFirst("AGENTNES_KEY", "AGNES_KEY")
	if base == "" {
		return proposal{}, errors.New("NOT_RUN：env 里没有 AGENTNES_BASE_URL / AGNES_BASE_URL；这一路今天跑不了")
	}
	if key == "" {
		return proposal{}, errors.New("NOT_RUN：env 里没有 AGENTNES_KEY / AGNES_KEY；这一路今天跑不了，也不许用剧本冒充它跑过")
	}
	model := envFirst("AGENTNES_MODEL", "AGNES_MODEL")
	if model == "" {
		model = "gpt-4o-mini"
	}
	endpoint := strings.TrimSuffix(base, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	payload, err := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": "你在跑一个记账宿主的任务。直接回答用户的问题；算数题要给出最后的总数，并用一句普通话说明。"},
			{"role": "user", "content": d.task},
		},
		"temperature": 0,
	})
	if err != nil {
		return proposal{}, err
	}
	timeout := 60 * time.Second
	if v := envFirst("AGENTNES_TIMEOUT_MS", "AGNES_TIMEOUT_MS"); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n > 0 {
			timeout = time.Duration(n) * time.Millisecond
		}
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return proposal{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return proposal{}, fmt.Errorf("端点取不回：%s", strings.ReplaceAll(err.Error(), key, "***"))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return proposal{}, fmt.Errorf("端点回了 HTTP %d（正文没抄进读数，防回显口令）", resp.StatusCode)
	}
	var 回 struct {
		Model   string `json:"model"`
		Choices []struct {
			Message      struct{ Content string } `json:"message"`
			FinishReason string                   `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &回); err != nil {
		return proposal{}, fmt.Errorf("端点回的不是那形状：%v", err)
	}
	if len(回.Choices) == 0 {
		return proposal{}, errors.New("端点回里没有 choices ⇒ 这一读没法用")
	}
	text := strings.TrimSpace(回.Choices[0].Message.Content)
	if text == "" {
		return proposal{}, errors.New("端点回了空正文 ⇒ 不拿空话冒充答案")
	}
	d.closed = true
	d.last = text
	return proposal{
		Text:         text,
		FinishSaid:   true,
		FinishReason: 回.Choices[0].FinishReason,
		ReportsModel: true,
		UsageIn:      回.Usage.PromptTokens,
		UsageOut:     回.Usage.CompletionTokens,
		Model:        回.Model,
		Sent:         true,
		BytesIn:      int64(len(raw)),
		WireFormat:   "chat.completion",
	}, nil
}

// Oracle 是文本口径的对照：任务里的数与 model 回话里最后一个整数比。
// 它不复算执行过程（这一路没有执行器），所以只在回话里真出现整数时才有判定。
func (d *realAPI) Oracle() oracleReading {
	ops, err := parseOperands(d.task)
	if err != nil {
		return oracleReading{Task: d.task, Verdict: "absent", Why: "任务里读不出两个非负整数：" + err.Error()}
	}
	expected, err := oracleSum(ops)
	if err != nil {
		return oracleReading{Task: d.task, Operands: ops, Verdict: "absent", Why: err.Error()}
	}
	got, ok := lastInt(d.last)
	if !ok {
		return oracleReading{Task: d.task, Operands: ops, Expected: expected, Verdict: "absent",
			Why: "模型回话里没有整数 ⇒ 文本 oracle 判不了（不是 0）"}
	}
	rd := oracleReading{Task: d.task, Operands: ops, Expected: expected, Got: got,
		Calls: 1, CallsExpected: 1}
	if got == expected {
		rd.Verdict = "ok"
		rd.Why = "模型回话里最后一个整数 = 任务各数之和（文本口径，不是执行器复算）"
	} else {
		rd.Verdict = "mismatch"
		rd.Why = fmt.Sprintf("model 回话最后一个整数是 %d，任务之和是 %d（文本口径）", got, expected)
	}
	return rd
}

// lastInt 取字符串里最后一段连续十进制数字；负号不参与（与任务输入面同一条规矩）。
func lastInt(s string) (int64, bool) {
	var last int64
	found := false
	for i := 0; i < len(s); {
		if s[i] < '0' || s[i] > '9' {
			i++
			continue
		}
		var v int64
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			d := int64(s[j] - '0')
			if v > (1<<62)/10 {
				v = 0
				break
			}
			v = v*10 + d
			j++
		}
		last, found = v, true
		i = j
	}
	return last, found
}

type sessionDriver struct{}

func (sessionDriver) Name() string     { return "session" }
func (sessionDriver) Autonomous() bool { return true }

func (sessionDriver) Propose(turn int, obs string, body []byte) (proposal, error) {
	return proposal{}, errors.New("NOT_RUN：会话驱动这一版没接（接线要动 builtin 会话工具，另一轮的事）")
}

// scriptFor 是这一趟的端点剧本。每一发的出处都按 fixture 自报 / 本地数出来的写，
// 没有一格因为「跑绿了」而升成 measured。
//
//	full  —— 走完六类故障里的四类，最后一发 config 让 POLICY 把整趟停掉（结束原因是「按账停」）
//	happy —— 中途两类可重试的故障，最后一发把总和写成一句话（结束原因是「剧本跑完」）
func scriptFor(name string) ([]proposal, error) {
	ok := func(text string, in, out int64) proposal {
		return proposal{Text: text, UsageIn: in, UsageOut: out, FinishSaid: true, FinishReason: "stop", ReportsModel: true}
	}
	switch name {
	case "full":
		return []proposal{
			{Fail: kernel.FailTransport, Detail: "剧本注入：管路没通，一个字节响应都没收到", Status: 0, RetryAfterMs: -1},
			ok("我把 27 与 15 相加，得到 42；这一步只动了一个加法，剩下的两个数还在题目里等，下一发接着算。", 96, 8),
			{Fail: kernel.FailHTTP, Detail: "剧本注入：对端说忙（429）", Status: 429, RetryAfterMs: 1200},
			ok("接着算：42 加 8 等于 50；这一发同样只动一个加法，题目里还剩最后一个数没进。", 104, 8),
			{Fail: kernel.FailBudget, Detail: "剧本注入：我们自己的预算闸在发之前掐的", Status: 0, RetryAfterMs: -1},
			streamOK(ok("50 加 34 等于 84；四个数到这里全部加完，往轮轨迹我在账上记的是折过的桩。", 71, 9)),
			{
				Text: "我要用 ledger_add 但参数没填全", ToolCalls: 1, Malformed: 1, Streak: 1,
				Fail: kernel.FailMalformedAction, Detail: "剧本注入：这一发的提议执行不了（缺参数）",
				Status: 200, RetryAfterMs: -1, UsageIn: 60, UsageOut: 6, ReportsModel: true,
				FinishSaid: true, FinishReason: "tool_calls",
			},
			ok("总和是 84。这句话是收口用的，不再动工具。", 58, 12),
			{Fail: kernel.FailConfig, Detail: "剧本注入：模型名不存在（404）", Status: 404, RetryAfterMs: -1},
		}, nil
	case "happy":
		return []proposal{
			{Fail: kernel.FailTransport, Detail: "剧本注入：管路没通，一个字节响应都没收到", Status: 0, RetryAfterMs: -1},
			ok("我把 27 与 15 相加，得到 42；这一步只动了一个加法，剩下的两个数还在题目里等，下一发接着算。", 96, 8),
			{Fail: kernel.FailHTTP, Detail: "剧本注入：对端说忙（429）", Status: 429, RetryAfterMs: 1200},
			ok("接着算：42 加 8 等于 50；这一发同样只动一个加法，题目里还剩最后一个数没进。", 104, 8),
			ok("50 加 34 等于 84；四个数到这里全部加完。", 71, 9),
			ok("总和是 84。这句话是收口用的，不再动工具。", 58, 12),
		}, nil
	default:
		return nil, fmt.Errorf("agentd: 没有这份剧本（只有 full / happy）：%q", name)
	}
}

func buildDriver(name, script string, task ...string) (Driver, error) {
	switch name {
	case "scripted":
		q, err := scriptFor(script)
		if err != nil {
			return nil, err
		}
		return &scripted{q: q}, nil
	case "real":
		t := userAsk
		if len(task) > 0 && strings.TrimSpace(task[0]) != "" {
			t = task[0]
		}
		return &realAPI{task: t}, nil
	case "fake":
		f, err := newFakeEndpoint(script)
		if err != nil {
			return nil, err
		}
		return &fakeDriver{f: f}, nil
	case "session":
		return sessionDriver{}, nil
	case "ledger":
		// 任务输入面：不给 -task 就退回内置例子，给了就把那句话当任务。
		t := userAsk
		if len(task) > 0 && strings.TrimSpace(task[0]) != "" {
			t = task[0]
		}
		return newLedgerDriver(t)
	default:
		return nil, fmt.Errorf("agentd: 不认识驱动 %q（只有 scripted / fake / real / session / ledger）", name)
	}
}

// streamOK 标出一发「只有真开 socket 才可能是流式」的结果；剧本那一路会把它抹掉。
func streamOK(p proposal) proposal { p.Stream = true; return p }
