package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// 任务输入面：把一句话里的非负整数按出现顺序取出来。
// 只认十进制非负整数 —— 负数、全角数字这类它读不准的写法宁可当场拒收，
// 也不许静默按正数算（读错号比读不到更难查）。
func parseOperands(task string) ([]int64, error) {
	rs := []rune(task)
	var out []int64
	for i := 0; i < len(rs); {
		if rs[i] < '0' || rs[i] > '9' {
			if rs[i] == '-' && i+1 < len(rs) && rs[i+1] >= '0' && rs[i+1] <= '9' {
				return nil, fmt.Errorf("任务里有负数（%q）：这一版只认非负整数，不猜负号的归属", string(rs[i:i+2]))
			}
			i++
			continue
		}
		j := i
		var v int64
		for j < len(rs) && rs[j] >= '0' && rs[j] <= '9' {
			d := int64(rs[j] - '0')
			if v > (math.MaxInt64-d)/10 {
				return nil, fmt.Errorf("任务里的数太大，int64 装不下：%q", string(rs[i:j+1]))
			}
			v = v*10 + d
			j++
		}
		out = append(out, v)
		i = j
	}
	if len(out) < 2 {
		return nil, fmt.Errorf("任务里只找到 %d 个非负整数：ledger_add 一次只加两个，至少要两个数才走得动", len(out))
	}
	return out, nil
}

// oracleSum 是算术 oracle：一趟把所有数加起来。
// 它与执行器（两两相加、每步经账）是两条独立的路子 —— 对不上就说明执行器那一路丢了步或算错了，
// 不是把执行器自己的答案再抄一遍。
func oracleSum(xs []int64) (int64, error) {
	var s int64
	for _, x := range xs {
		if x < 0 || s > math.MaxInt64-x {
			return 0, fmt.Errorf("这组数的和超出 int64：算术 oracle 交不出答案，也不许编一个")
		}
		s += x
	}
	return s, nil
}

// ledgerDriver 是本机执行器：任务进来，它把「两两相加」的下一步提议出来，
// 并在本地真的执行 ledger_add。它不是模型 —— Autonomous 永远是 false，
// 运行横幅和账本说明里都不许写成「模型自主决定」。
type ledgerDriver struct {
	task   string
	ops    []int64
	i      int // 下一个要并进来的操作数
	acc    int64
	calls  int
	closed bool // 已经交过收口那一发
}

func newLedgerDriver(task string) (*ledgerDriver, error) {
	ops, err := parseOperands(task)
	if err != nil {
		return nil, err
	}
	if _, err := oracleSum(ops); err != nil {
		return nil, err
	}
	return &ledgerDriver{task: task, ops: ops, acc: ops[0], i: 1}, nil
}

func (d *ledgerDriver) Name() string     { return "ledger" }
func (d *ledgerDriver) Autonomous() bool { return false }
func (d *ledgerDriver) Done() bool       { return d.closed }

func (d *ledgerDriver) Propose(turn int, obs string, body []byte) (proposal, error) {
	if d.i < len(d.ops) {
		a, b := d.acc, d.ops[d.i]
		d.acc = a + b // 执行器本体：两数相加，结果并进累计
		d.i++
		d.calls++
		剩 := ""
		if left := len(d.ops) - d.i; left > 0 {
			剩 = fmt.Sprintf("；还剩 %d 个数没加", left)
		}
		return proposal{
			Text:         fmt.Sprintf("第 %d 次调用 ledger_add：%d + %d = %d%s。", d.calls, a, b, d.acc, 剩),
			ToolCalls:    1,
			FinishSaid:   true,
			FinishReason: "tool_calls",
			ReportsModel: true,
		}, nil
	}
	if d.closed {
		return proposal{}, fmt.Errorf("任务已经收口，执行器没有下一条了")
	}
	d.closed = true
	return proposal{
		Text:         fmt.Sprintf("总和是 %d。", d.acc),
		FinishSaid:   true,
		FinishReason: "stop",
		ReportsModel: true,
	}, nil
}

type oracleReading struct {
	Task          string  `json:"task"`
	Operands      []int64 `json:"operands"`
	Expected      int64   `json:"expected"`
	Got           int64   `json:"got"`
	Calls         int     `json:"calls"`
	CallsExpected int     `json:"calls_expected"`
	Verdict       string  `json:"verdict"`
	Why           string  `json:"why"`
	Stop          string  `json:"stop"`
	StopWhy       string  `json:"stop_why"`
	ExitCode      int     `json:"exit_code"`
}

// Answerer 是「自己的答案能被核」的那一路驱动；没有它的驱动（剧本/真端点）交不出答案，
// 读数只能写 absent + 理由。
type Answerer interface {
	Oracle() oracleReading
}

// Oracle 报的是执行器自己的账（累计值、调了几次）与另一条求和路径的对照。
// verdict 三态各有自己的话：ok / mismatch（跑了但答案不对）/ incomplete（没走到收口那一发）。
func (d *ledgerDriver) Oracle() oracleReading {
	expected, err := oracleSum(d.ops)
	if err != nil {
		return oracleReading{Task: d.task, Operands: d.ops, Got: d.acc, Calls: d.calls,
			CallsExpected: len(d.ops) - 1, Verdict: "absent", Why: err.Error()}
	}
	want := len(d.ops) - 1
	rd := oracleReading{Task: d.task, Operands: d.ops, Expected: expected, Got: d.acc,
		Calls: d.calls, CallsExpected: want}
	switch {
	case !d.closed:
		rd.Verdict = "incomplete"
		rd.Why = fmt.Sprintf("执行器没走到收口那一发：只执行了 %d/%d 次 ledger_add", d.calls, want)
	case d.acc != expected:
		rd.Verdict = "mismatch"
		rd.Why = fmt.Sprintf("执行器交出 %d，算术 oracle 说该是 %d：差 %d ⇒ 执行器那一路丢了步或算错了", d.acc, expected, d.acc-expected)
	case d.calls != want:
		rd.Verdict = "mismatch"
		rd.Why = fmt.Sprintf("执行器调了 %d 次 ledger_add，%d 个数该是 %d 次", d.calls, len(d.ops), want)
	default:
		rd.Verdict = "ok"
	}
	return rd
}

// applyOracle 在收口之前把判定落进结束原因：答案不符的这一趟不许顶着「跑完」退 0。
// 六态退出码是冻结的，所以借「驱动这一路跑不了」那一态（rc=2）并写明为什么。
func applyOracle(r *run, drv Driver) oracleReading {
	var rd oracleReading
	if a, ok := drv.(Answerer); ok {
		rd = a.Oracle()
	} else {
		rd = oracleReading{Task: r.askText(), Verdict: "absent",
			Why: "这一路没有可核的执行器（只有 -driver ledger 自己算答案）"}
	}
	if rd.Verdict == "mismatch" && r.stop == stopScript {
		r.stop = stopDriver
		r.stopWhy = "算术 oracle 与执行器不符：" + rd.Why
	}
	rd.Stop = r.stop.Token()
	rd.StopWhy = r.stopWhy
	rd.ExitCode = exitOf(r.stop)
	return rd
}

// writeOracleFile 先把读数写进 .part 再换名：跑到一半被打断时，正式名上不该出现半份 JSON。
func writeOracleFile(path string, rd oracleReading) (int, error) {
	b, err := json.MarshalIndent(rd, "", "  ")
	if err != nil {
		return 0, err
	}
	b = append(b, '\n')
	tmp := path + ".part"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return len(b), nil
}
