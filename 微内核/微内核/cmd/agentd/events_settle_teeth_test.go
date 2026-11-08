package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 结算判据的**牙**：每一形都是真负载读回来的整本日志改一处，写回盘、再读回来喂判据。
// 这一支是 M2 路 A 的 P1-③ 复算件 —— 报告说十四形里八形判不出，先拿自己的数验，不照抄结论。
// 规矩：坏形必须红；对照（没改的那本真日志）必须绿。

func rowsOf(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读原日志：%v", err)
	}
	rows := []map[string]any{}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("解一行：%v", err)
		}
		rows = append(rows, m)
	}
	return rows
}

// writeRows 把改过的行序列化落盘再读回来 —— 坏形必须走公开路径（序列化那一层也要走到）。
func writeRows(t *testing.T, name string, rows []map[string]any) []Event {
	t.Helper()
	var b []byte
	for _, row := range rows {
		line, err := json.Marshal(row)
		if err != nil {
			t.Fatalf("序列化坏形：%v", err)
		}
		b = append(b, line...)
		b = append(b, 10)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("写坏日志：%v", err)
	}
	return mustReadEvents(t, path)
}

// settleIdx 取第一发那条结算所在的行号（生产日志里 turn 从 1 起）。
func settleIdx(rows []map[string]any) int {
	for i, r := range rows {
		if r["turn"] == float64(1) && (r["kind"] == "turn_settled" || r["kind"] == "turn_interrupted") {
			return i
		}
	}
	return -1
}

func TestSettlementJudgeCatchesEachBadShape(t *testing.T) {
	base := filepath.Join(t.TempDir(), "base.events.jsonl")
	r := newRun(t, "happy", true, "included", 20)
	r.events = newEventBus(base)
	r.loop()
	r.closeOut()

	if err := ValidateSettlement(mustReadEvents(t, base)); err != nil {
		t.Fatalf("对照（没改的真日志）就红 —— 后面每一形都白测：%v", err)
	}
	si := settleIdx(rowsOf(t, base))
	if si < 0 {
		t.Fatal("真日志里没找到第一发的结算行 —— 现场没造出来，这一族针不作数")
	}

	cases := []struct {
		name string
		mut  func([]map[string]any) []map[string]any
		want string
	}{
		{"整发缺席：把第一发的结算删掉（半结算）", func(rows []map[string]any) []map[string]any {
			out := []map[string]any{}
			for i, x := range rows {
				if i != si {
					out = append(out, x)
				}
			}
			return out
		}, "却没有结算"},
		{"结算的 phase 是相位表外的名", func(rows []map[string]any) []map[string]any {
			rows[si]["settle"].(map[string]any)["phase"] = "zombie"
			return rows
		}, "phase"},
		{"reached 里塞一个表外相名（散写）", func(rows []map[string]any) []map[string]any {
			s := rows[si]["settle"].(map[string]any)
			s["reached"] = []any{"accept", "work", "check_call", "read_back"}
			s["not_reached"] = []any{}
			return rows
		}, "reached"},
		{"counts 键名散写（state 白名单外）", func(rows []map[string]any) []map[string]any {
			s := rows[si]["settle"].(map[string]any)
			n := s["counts"].(map[string]any)
			v, ok := n["running"]
			if !ok {
				return rows
			}
			delete(n, "running")
			n["Succes"] = v
			return rows
		}, "counts"},
		{"done_before_shots 大于发号（乱数）", func(rows []map[string]any) []map[string]any {
			rows[si]["settle"].(map[string]any)["done_before_shots"] = 99
			return rows
		}, "不可能大于发号"},
		{"结算的 kind 写成 done（结束类别散写）", func(rows []map[string]any) []map[string]any {
			rows[si]["settle"].(map[string]any)["kind"] = "done"
			return rows
		}, "kind"},
		{"结算没带 why（给了类别没给为什么）", func(rows []map[string]any) []map[string]any {
			rows[si]["settle"].(map[string]any)["why"] = ""
			return rows
		}, "why"},
		// 同趟内计数倒退：这一发比前一发说得还少 —— 只有「不许倒退」那一条管得住（F5 测过：没这条负载就没人响）
		{"同趟内 done_before 倒退（第二发比第一发少）", func(rows []map[string]any) []map[string]any {
			// 第一发照旧（说 1，合法），第二发改成 0：既不为负、也不大于自己的发号，
			// 只有「同一趟不许倒退」那一条能抓 —— 前一版我把它写成 0、-1，结果先撞了「负数」那条。
			seen := 0
			for _, r := range rows {
				sv, ok := r["settle"].(map[string]any)
				if !ok {
					continue
				}
				seen++
				if seen == 2 {
					sv["done_before_shots"] = 0
				}
			}
			return rows
		}, "倒退"},
	}
	blind := []string{}
	for _, tc := range cases {
		// 逐形重读：内层 map 是引用，改过一发会串到下一发（与 Emit 的 P1-1 同族）
		evs := writeRows(t, "bad.events.jsonl", tc.mut(rowsOf(t, base)))
		err := ValidateSettlement(evs)
		if err == nil {
			blind = append(blind, tc.name)
			t.Errorf("%s：判据没红（这一形没牙）", tc.name)
			continue
		}
		if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s：红了但指错地方，want=%q got=%q", tc.name, tc.want, err.Error())
		}
	}
	if len(blind) > 0 {
		t.Logf("盲形清单（%d 形）：%v", len(blind), blind)
	}
}
