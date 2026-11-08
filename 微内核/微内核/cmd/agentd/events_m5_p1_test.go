package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// M5 扫端那五条 P1 的针（#119）。规矩：坏形从**盘上**喂进来；每种都有对照绿形；
// 判据不许拿读不到的东西判（那是假牙），也不许把合法形状判红（今天刚为这件事修过一条）。

func realLogRows(t *testing.T) (string, []Event) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "base.events.jsonl")
	r := newRun(t, "happy", true, "included", 20)
	r.events = newEventBus(path)
	r.loop()
	r.closeOut()
	evs := mustReadEvents(t, path)
	if err := ValidateLog(evs); err != nil {
		t.Fatalf("对照日志不合格，后面每支都白测：%v", err)
	}
	if err := ValidateSettlement(evs); err != nil {
		t.Fatalf("对照日志结算判据红：%v", err)
	}
	return path, evs
}

// 1) 读回路径的三查（原来只有写侧有，checklog 的注释却一直声称读回也管 —— P1-5）。
func TestReadPathRejectsTamperedRows(t *testing.T) {
	cases := []struct {
		name string
		row  map[string]any
		want string
	}{
		{"状态散写", map[string]any{"state": "banana"}, "不在白名单里"},
		{"出处档散写", map[string]any{"evidence": "guessed"}, "不在五档里"},
		{"absent 没带理由", map[string]any{"evidence": "absent", "why": ""}, "absent 却没带"},
		{"没有 kind", map[string]any{"kind": ""}, "没有 kind"},
	}
	for _, tc := range cases {
		base := sseRow(1, "readpath", "rA", 1, "turn_accepted", sseAccept(false))
		for k, v := range tc.row {
			base[k] = v
		}
		if _, ok := tc.row["why"]; ok {
			base["why"] = ""
		}
		acc, _ := base["accept"].(map[string]any)
		if acc != nil {
			delete(base, "accept") // 这一支只判档名与状态，别把回执载荷卷进来
		}
		evs := writeRows(t, "tampered.events.jsonl", []map[string]any{base, sseRow(2, "readpath", "rA", 1, "shot_started", nil)})
		err := ValidateLog(evs)
		if err == nil {
			t.Errorf("%s：读回路径没挡（判据只活在写侧）", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s：红了但指错地方 want=%q got=%q", tc.name, tc.want, err.Error())
		}
	}
	// 对照：真负载落盘的那本必须绿（不然这条牙就是在乱咬）
	good, _ := realLogRows(t)
	if err := ValidateLog(mustReadEvents(t, good)); err != nil {
		t.Fatalf("真日志被读回路径判红：%v", err)
	}
}

// 2) 相位不只数条数：reached 必须是前缀、not_reached 必须是剩下那段、phase 必须等于走得最远那一相（P1-2）。
func TestSettlementPhaseShapeJudges(t *testing.T) {
	path, _ := realLogRows(t)
	settleIdx := -1
	for i, e := range mustReadEvents(t, path) {
		if e.Settle != nil {
			settleIdx = i
			break
		}
	}
	if settleIdx < 0 {
		t.Fatal("真日志里没有结算载荷 —— 这一族针不作数")
	}
	cases := []struct {
		name string
		mut  func(s map[string]any)
		want string
	}{
		{"phase 谎报成没走到的那一相", func(s map[string]any) { s["phase"] = "accept" }, "停下那一相"},
		{"reached 内容错配（条数对但不是前缀）", func(s map[string]any) {
			s["reached"] = []any{"accept", "check_call", "work", "record_call"}
			s["not_reached"] = []any{}
			s["phase"] = "record_call"
		}, "前缀"},
		{"两格拼不起来（not_reached 与 reached 交错）", func(s map[string]any) {
			s["reached"] = []any{"accept", "work"}
			s["not_reached"] = []any{"record_call", "check_call"}
			s["phase"] = "work"
		}, "那一段"},
	}
	for _, tc := range cases {
		rows := rowsOf(t, path)
		sv, ok := rows[settleIdx]["settle"].(map[string]any)
		if !ok {
			t.Fatal("结算行没有 settle 格")
		}
		tc.mut(sv)
		evs := writeRows(t, "phase-bad.events.jsonl", rows)
		err := ValidateSettlement(evs)
		if err == nil {
			t.Errorf("%s：判据没红（条数对了就放过内容）", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s：红了但指错地方 want=%q got=%q", tc.name, tc.want, err.Error())
		}
	}
}

// 3) 开了账（有 run_started）却没有 run_settled 必须红；手写的残缺形（没开账）不该被它误伤（P1-3）。
func TestRunSettledPresence(t *testing.T) {
	path, evs := realLogRows(t)
	rows := rowsOf(t, path)
	hadStart := false
	kept := []map[string]any{}
	for _, r := range rows {
		if r["kind"] == "run_started" {
			hadStart = true
		}
		if r["kind"] == "run_settled" {
			continue
		}
		kept = append(kept, r)
	}
	if !hadStart {
		t.Fatal("真日志里没有 run_started —— 这一支的现场没造出来")
	}
	if !strings.Contains(kindsOf(mustReadEvents(t, path)), "run_settled") {
		t.Fatal("真日志本来就没有 run_settled，那删它这一步没意义")
	}
	err := ValidateSettlement(writeRows(t, "nostop.events.jsonl", kept))
	if err == nil {
		t.Fatal("开了账却没有 run_settled：判据没红（半路被杀的账能被当跑完的账读）")
	} else if !strings.Contains(err.Error(), "没有 run_settled") {
		t.Errorf("红了但指错地方：%v", err)
	}
	// 对照一：完整真日志必须绿
	if err := ValidateSettlement(evs); err != nil {
		t.Errorf("完整真日志被自己判红：%v", err)
	}
	// 对照二：没开账的手作残缺形（只有发级事件）不该被这一条误伤
	crafted := []map[string]any{
		{"v": 1, "session": "noRun", "run": "rZ", "turn": 1, "seq": 1, "ts": "2026-10-22T00:00:00Z",
			"kind": "shot_started", "state": "running", "evidence": "measured", "elapsed_ms": 0},
	}
	if err := ValidateSettlement(writeRows(t, "norun.events.jsonl", crafted)); err != nil &&
		strings.Contains(err.Error(), "没有 run_settled") {
		t.Errorf("拿没开账的残缺手作形去判「缺收口」，误伤了：%v", err)
	}
}

// 4) 「接不上但老实」与「已经追平」必须分得开：前者给 resume-lost，后者安静零帧（P1-1）。
func TestResumeLostIsDistinguishable(t *testing.T) {
	rows := []map[string]any{
		sseRow(1, "lost", "rA", 1, "turn_accepted", sseAccept(false)),
		sseRow(2, "lost", "rA", 1, "shot_started", nil),
	}
	evs := writeRows(t, "lost.events.jsonl", rows)
	path := filepath.Join(t.TempDir(), "served.events.jsonl")
	if err := os.WriteFile(path, rawOf(t, rows), 0o644); err != nil {
		t.Fatalf("写日志：%v", err)
	}
	if len(mustReadEvents(t, path)) != len(evs) {
		t.Fatal("盘上与判据吃的不是同一本账")
	}
	addr, stop, err := serveEvents(path)
	if err != nil {
		t.Fatalf("起投送：%v", err)
	}
	defer stop()
	url := "http://" + addr + "/events"

	// ① 游标指向不存在的那条，而那一发老实写了不可续 ⇒ 判据放过（绿），但投送层必须说清接不上
	code, body := httpGet(t, url, "lost:rA:1:99")
	if code != http.StatusOK {
		t.Fatalf("老实说不可续的游标被给了 %d（应 200 + resume-lost）：%s", code, body)
	}
	if !strings.Contains(body, "event: resume-lost") {
		t.Fatalf("接不上与追平逐字相同，客户端分不出来：%q", body)
	}
	if strings.Contains(body, "data: {") {
		t.Fatalf("接不上却把整本播了一遍：%q", body)
	}
	// ② 游标指向最后一条（真的追平）⇒ 零帧且**不许**有 resume-lost
	code2, body2 := httpGet(t, url, formatCursor(evs[len(evs)-1]))
	if code2 != http.StatusOK {
		t.Fatalf("追平的游标被给了 %d", code2)
	}
	if strings.Contains(body2, "resume-lost") {
		t.Fatalf("已经追平却报接不上（会把界面推到假错误态）：%q", body2)
	}
	if strings.Contains(body2, "data: {") {
		t.Fatalf("追平还多播了东西：%q", body2)
	}
}

// 5) 每一行读数都要带上用的哪个上限，否则放宽过的 GREEN 能冒充契约预算下的（P1-6）。
func TestCheckLogEveryLineCarriesTheLimit(t *testing.T) {
	path, _ := realLogRows(t)
	for _, lim := range []int64{100, 60000} {
		var buf bytes.Buffer
		rc := checkLogAgainst(&buf, path, lim)
		tag := "ack_limit_ms=" + strings.TrimSpace(strings.ReplaceAll(itoa64(lim), " ", ""))
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		if len(lines) < 5 {
			t.Fatalf("上限 %d 只印了 %d 行", lim, len(lines))
		}
		for _, l := range lines {
			cols := strings.Split(l, "\t")
			if len(cols) < 4 || cols[0] != "check-log" {
				t.Fatalf("行形状不对：%q", l)
			}
			if cols[3] == "GREEN" || cols[3] == "RED" {
				if !strings.Contains(l, tag) {
					t.Fatalf("上限 %d 下这行没带口径身份：%q", lim, l)
				}
			}
		}
		if rc != rcScript && rc != rcPolicy {
			t.Fatalf("上限 %d 给了退出码 %d", lim, rc)
		}
		// 两档读数必须**不能**逐字相同：至少有一行（或末行）带上不同的上限
		if lim == 60000 && strings.Contains(buf.String(), "ack_limit_ms=100\t") {
			t.Fatal("60000 那趟的读数里混进了 100 的身份")
		}
	}
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	if neg {
		return "-" + digits
	}
	return digits
}

// rawOf 把行序列化成 JSONL 字节：盘只由一处造，别让判据与投送层各读一份不同的账。
func rawOf(t *testing.T, rows []map[string]any) []byte {
	t.Helper()
	var b []byte
	for _, r := range rows {
		line, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("序列化：%v", err)
		}
		b = append(b, line...)
		b = append(b, 10)
	}
	return b
}

var _ = http.StatusOK
