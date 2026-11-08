package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseOperandsReadsTheTaskNotTheDefault(t *testing.T) {
	cases := []struct {
		in   string
		want []int64
	}{
		{"把 27、15、8、34 加起来。", []int64{27, 15, 8, 34}},
		{"27+15+8+34", []int64{27, 15, 8, 34}},
		{"把 100 加 1", []int64{100, 1}},
	}
	for _, c := range cases {
		got, err := parseOperands(c.in)
		if err != nil {
			t.Fatalf("任务 %q 读不出来：%v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("任务 %q 读成 %v，该是 %v", c.in, got, c.want)
		}
	}

	// 三族拒收：数不够、负数读不准、int64 装不下 —— 每一族都要自己说为什么。
	if _, err := parseOperands("把 5 加起来"); err == nil || !strings.Contains(err.Error(), "至少要两个") {
		t.Fatalf("只给一个数也该拒收，实得 %v", err)
	}
	if _, err := parseOperands("把 -3 和 5 加起来"); err == nil || !strings.Contains(err.Error(), "负数") {
		t.Fatalf("负数该明说读不了，实得 %v", err)
	}
	if _, err := parseOperands("把 99999999999999999999 和 1 加起来"); err == nil || !strings.Contains(err.Error(), "装不下") {
		t.Fatalf("超 int64 该拒收，实得 %v", err)
	}
}

func TestOracleSumCatchesOverflow(t *testing.T) {
	if _, err := oracleSum([]int64{1 << 62, 1 << 62}); err == nil {
		t.Fatal("和超出 int64 时 oracle 仍然给了答案 —— 那就是编数")
	}
	if v, err := oracleSum([]int64{27, 15, 8, 34}); err != nil || v != 84 {
		t.Fatalf("oracle 求和错：%d / %v", v, err)
	}
}

func newTaskRun(t *testing.T, task string, maxTurns int) (*run, *ledgerDriver) {
	t.Helper()
	drv, err := newLedgerDriver(task)
	if err != nil {
		t.Fatalf("造执行器：%v", err)
	}
	r := &run{
		rpc:         newKernelRPC(0, false),
		drv:         drv,
		session:     "task-" + strings.ReplaceAll(task, " ", ""),
		ask:         task,
		maxTurns:    maxTurns,
		readBack:    true,
		declare:     true,
		journalPath: filepath.Join(t.TempDir(), "journal.jsonl"),
		payload:     "included",
		log:         &bytes.Buffer{},
		errLog:      &bytes.Buffer{},
		declared:    map[string]bool{},
	}
	return r, drv
}

// 任务输入面 + 执行器 + oracle 的主路径：任务原文进账，两两相加真算，oracle 独立求和对上。
func TestLedgerTaskRunsAndOraclePasses(t *testing.T) {
	r, drv := newTaskRun(t, "把 27、15、8、34 加起来", 12)
	r.loop()
	if r.stop != stopScript {
		t.Fatalf("执行器把任务走完，结束原因该是跑完，实得 %q", r.stop)
	}
	if r.shot != 4 { // 3 次 ledger_add + 1 次收口那句
		t.Fatalf("4 个数该走 3 次加法 + 1 次收口 = 4 发，实得 %d 发", r.shot)
	}
	rd := applyOracle(r, drv)
	if rd.Verdict != "ok" || rd.Expected != 84 || rd.Got != 84 || rd.Calls != 3 || rd.CallsExpected != 3 {
		t.Fatalf("oracle 读数不对：%+v", rd)
	}
	if rd.ExitCode != 0 || r.stop != stopScript {
		t.Fatalf("ok 那一趟不该改结束原因/退出码：%q / %d", r.stop, rd.ExitCode)
	}
	// 任务原文真的进了装配（不是拿内置例子顶）。
	if got := r.assemble(r.shot + 1).user; got != "把 27、15、8、34 加起来" {
		t.Fatalf("装配用的任务原文是 %q —— 任务输入面没接进循环", got)
	}
	if r.decisions[0].Basis != "fail_kind" && r.decisions[0].Basis != "folded（输入侧）" {
		t.Fatalf("第一发的依据格是 %q —— 账没接上", r.decisions[0].Basis)
	}
}

// 反臂一：执行器算错（累计值与 oracle 不符）⇒ 不许算跑完，退出码回到「这一路跑不了」。
func TestLedgerOracleArmWrongAnswerIsRed(t *testing.T) {
	r, drv := newTaskRun(t, "27+15+8", 12)
	r.loop()
	drv.acc++ // 造一个「算错了」的现场
	rd := applyOracle(r, drv)
	if rd.Verdict != "mismatch" {
		t.Fatalf("答案差 1，oracle 却判 %q —— 恒绿的尺子", rd.Verdict)
	}
	if r.stop != stopDriver || rd.ExitCode != rcNotRun {
		t.Fatalf("算错的那一趟仍报 %q / rc=%d", r.stop, rd.ExitCode)
	}
	if !strings.Contains(r.stopWhy, "oracle") {
		t.Fatalf("停下来的理由没点名 oracle：%q", r.stopWhy)
	}
}

// 反臂二：少调一次 ledger_add（步数口径）⇒ 同样要红，而不是只看数值。
func TestLedgerOracleArmSkippedCallIsRed(t *testing.T) {
	r, drv := newTaskRun(t, "1+2+3", 12)
	r.loop()
	drv.calls--
	rd := applyOracle(r, drv)
	if rd.Verdict != "mismatch" || !strings.Contains(rd.Why, "次 ledger_add") {
		t.Fatalf("少调一次却判 %q（%s）", rd.Verdict, rd.Why)
	}
}

// 反臂三：轮次上限把计划截断 ⇒ 只能是 incomplete，结束原因仍是轮次额度（3），不许被 oracle 改写。
func TestLedgerOracleIncompleteWhenTurnsCapCutsThePlan(t *testing.T) {
	r, drv := newTaskRun(t, "1+2+3+4+5", 2)
	r.loop()
	if r.stop != stopTurns {
		t.Fatalf("上限 2 发该停在轮次额度，实得 %q", r.stop)
	}
	rd := applyOracle(r, drv)
	if rd.Verdict != "incomplete" {
		t.Fatalf("没走到收口却判 %q", rd.Verdict)
	}
	if rd.ExitCode != rcTurns || r.stop != stopTurns {
		t.Fatalf("oracle 不该改写被截断那一趟的结束原因：%q / rc=%d", r.stop, rd.ExitCode)
	}
}

// 读数落盘：整份 JSON 能读回、字节数自印、.part 不留在盘上。
func TestWriteOracleFileLandsWholeJSON(t *testing.T) {
	r, drv := newTaskRun(t, "27+15+8+34", 12)
	r.loop()
	rd := applyOracle(r, drv)
	path := filepath.Join(t.TempDir(), "oracle.json")
	n, err := writeOracleFile(path, rd)
	if err != nil {
		t.Fatalf("写 oracle 读数：%v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回 oracle 读数：%v", err)
	}
	if len(b) != n {
		t.Fatalf("自印 %d 字节，盘上 %d 字节 —— 自印那份不算数", n, len(b))
	}
	var back oracleReading
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("读回来的不是 JSON：%v", err)
	}
	if back.Got != 84 || back.Verdict != "ok" || back.ExitCode != 0 {
		t.Fatalf("读回的读数与交出去的不一致：%+v", back)
	}
	if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
		t.Fatalf("正式名写完了 .part 还留着：%v", err)
	}
}

// 任务输入面接在驱动选择上：不给 -task 用内置例子，给了就用那句话；ledger 永远不是模型自主。
func TestBuildDriverLedgerTakesTheTask(t *testing.T) {
	d, err := buildDriver("ledger", "", "把 1 和 2 加起来")
	if err != nil {
		t.Fatalf("造 ledger 驱动：%v", err)
	}
	ld, ok := d.(*ledgerDriver)
	if !ok || ld.task != "把 1 和 2 加起来" {
		t.Fatalf("任务没接上：%#v", d)
	}
	if d.Autonomous() || driverAutonomous("ledger", "", "把 1 和 2 加起来") {
		t.Fatal("本机执行器被写成了模型自主 —— 那一格是假的")
	}
	d2, err := buildDriver("ledger", "")
	if err != nil {
		t.Fatalf("不给任务该退回内置例子，实得错误：%v", err)
	}
	if d2.(*ledgerDriver).task != userAsk {
		t.Fatalf("不给任务时的原文不是内置例子：%q", d2.(*ledgerDriver).task)
	}
	if _, err := buildDriver("ledger", "", "只有一个数 5"); err == nil {
		t.Fatal("读不动的一句话仍造出了驱动 —— 任务输入面在骗人")
	}
}
