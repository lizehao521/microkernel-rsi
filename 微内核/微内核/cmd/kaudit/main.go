// kaudit —— 只读的远程审计入口：把一份已经落盘的往来账（`encode_journal` 的字节格式）
// 用 HTTP 供出去，让**别的人、别的机器**能查三件事：链自洽吗、每格的出处是什么、
// 某一发某一侧到底记了哪些话。
//
// 它为什么住在 cmd/ 而不是内核里：`package kernel` 不 import `net/http`、不写磁盘
// （contract.md 第 1 节的依赖方向）。这里只做外壳该做的事：读文件、鉴权、渲染。
//
// 它刻意**没有任何写入口**。理由不是"暂时没写"，而是这条产品定位：
// 审计者看的是证据，不该能改证据 —— 任何 POST/PUT 一律 405，`TestReadOnly` 钉着这一点。
//
// 三态不许被压扁：链「核不了」（手里没有原像）是一种**独立的**答案，
// 渲染成 `state:"undecidable"`，既不是 `green` 也不是 `mismatch`（`TestNoPreimageIsNotGreen`）。
//
// 用法：
//
//	$env:KAUDIT_TOKEN="…"            # 非本机监听时必须给
//	go run ./cmd/kaudit -listen 127.0.0.1:8788 -journal 报告/样例-agent.jsonl
//	curl -H "Authorization: Bearer $KAUDIT_TOKEN" localhost:8788/v1/verify
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"agentkernel"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8788", "监听地址；非本机地址必须给 KAUDIT_TOKEN")
	path := flag.String("journal", "", "要供出去的账本文件（JSONL，可含 encode_journal 的人读表头）")
	flag.Parse()
	if *path == "" {
		log.Fatalf("kaudit: 要 -journal 指一份账本文件；没有文件就没有可审计的东西")
	}
	srv, err := newServer(*path)
	if err != nil {
		log.Fatalf("kaudit: %v", err)
	}
	if err := srv.checkListen(*listen); err != nil {
		log.Fatalf("kaudit: %v", err)
	}
	// 起不来就别说起来过：装载失败已经在上面 fatal 了，这里连上就报实际地址。
	log.Printf("kaudit 只读服务：%s（账本 %s，%d 条，链：%s）", *listen, *path, srv.report.Entries, srv.state())
	if err := http.ListenAndServe(*listen, srv.handler()); err != nil {
		log.Fatalf("kaudit: %v", err)
	}
}

type server struct {
	path          string
	led           *kernel.Ledger
	journal       kernel.Journal
	report        kernel.ChainReport
	skippedHeader bool
}

// newServer 读盘并**当场**验链：`LoadLedger` 载入即逐条比对指纹，被改过的文件在这里就拒。
// 人读表头（`encode_journal` 的第一行）不是账，剥掉并记下这件事 —— 不剥会多出一条 BadLine，
// 而「解开了 46/47」被读成「46 条账」正是这个包最恨的那一族。
func newServer(path string) (*server, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读不到账本：%w", err)
	}
	return newServerBytes(path, raw)
}

// newServerBytes 是同一个装载过程的入口，给测试留着：让"被改过的账""没有原像的账"
// 这些形状能在不碰真文件的情况下被装配出来 —— 否则这几条判定就只能靠人肉起服务去验。
func newServerBytes(path string, raw []byte) (*server, error) {
	s := &server{path: path}
	body, header := splitHeader(string(raw))
	s.skippedHeader = header
	led, j, rep, err := kernel.LoadLedger([]byte(body))
	if err != nil {
		return nil, fmt.Errorf("这份账本不肯被载入（链不符或字节不对）：%w", err)
	}
	s.led, s.journal, s.report = led, j, rep
	return s, nil
}

func splitHeader(text string) (string, bool) {
	i := strings.IndexByte(text, '\n')
	if i < 0 {
		return text, false
	}
	if strings.HasPrefix(strings.TrimSpace(text[:i]), "{") {
		return text, false
	}
	return text[i+1:], true
}

// state 是三态里的那一个词。它由内核的报告算出来，不在这里另判一遍。
func (s *server) state() string {
	if s.report.Mismatch != "" {
		return "mismatch"
	}
	if s.report.Entries == 0 {
		// 空账不是 green：审计者看到的 `state:"green"` 会被读成"这本合同核过了"。
		// 它与"核不了"也不同 —— 核不了是手里字节不够，这里是压根没有条。第四个词，
		// 但**不是新的判据格**：它是同一条三态结论在"没什么可判"时的另一种说法。
		return "nothing_to_audit"
	}
	if !s.report.Decidable() {
		return "undecidable"
	}
	return "green"
}

// checkListen 是这台机器上唯一一条"已经观测到"的风险：绑到非回环地址就等于把账本给全网看。
// 没有 token 就拒绝启动，而不是"先起来再说"。
func (s *server) checkListen(listen string) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("监听地址看不懂：%w", err)
	}
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return nil
	}
	if os.Getenv("KAUDIT_TOKEN") == "" {
		return fmt.Errorf("要监听 %s（非本机回环）必须先给 KAUDIT_TOKEN —— 没有口令的只读审计口=任何人可下载全部往来原话", listen)
	}
	return nil
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if !s.auth(w, r) {
			return
		}
		s.json(w, http.StatusOK, map[string]any{"ok": true, "state": s.state(), "entries": s.report.Entries})
	})
	mux.HandleFunc("/v1/verify", func(w http.ResponseWriter, r *http.Request) {
		if !s.auth(w, r) {
			return
		}
		s.json(w, http.StatusOK, map[string]any{
			"state": s.state(), "text": s.report.String(),
			"entries": s.report.Entries, "checked": s.report.Checked,
			"no_preimage": s.report.NoPreimage,
		})
	})
	mux.HandleFunc("/v1/tally", func(w http.ResponseWriter, r *http.Request) {
		if !s.auth(w, r) {
			return
		}
		t := s.led.Tally()
		s.json(w, http.StatusOK, map[string]any{
			"text": s.led.String(), "by_basis": t.ByBasis, "total": t.Total,
			// 计数这一栏是三态的：归档文件里没写过这几格时**不许报 0**。
			// 报 0 会让第三方把"这份文件里没这个数"读成"闸门一次都没拦过"，
			// 而 kaudit 存在的全部意义就是让第三方自己读（缺口 #51 的原话就是这个 0）。
			"rejected_writes": rejectedOrNull(s.led),
			"gate_state":      s.led.GateStatus().String(),
			"gate_line":       s.journal.HasGate,
			// 没有计数栏时整格印 null。原先这里无脑印 `s.journal.Gate`，
			// 于是同一个响应里出现「rejected_writes: null」+「gate.rejected: 0」两句话 ——
			// 外壳侧互审（10-18）点的就是这一条：新加的格子把 #51 那个形状重新造了一遍，
			// 而且造在**六格**上，比原来那一格更容易被读走。
			"gate": gateOrNull(s.journal),
			"journal": map[string]any{"lines": s.journal.Lines, "parsed": s.journal.Parsed,
				"with_payload": s.journal.WithPayload, "no_payload": s.journal.NoPayload,
				"bad_lines": s.journal.BadLines, "header_skipped": s.skippedHeader,
				"gate_lines": s.journal.GateLines, "gate_problem": s.journal.GateProblem},
		})
	})
	mux.HandleFunc("/v1/celltable", func(w http.ResponseWriter, r *http.Request) {
		if !s.auth(w, r) {
			return
		}
		s.json(w, http.StatusOK, map[string]any{"sides": celltable()})
	})
	mux.HandleFunc("/v1/turns", func(w http.ResponseWriter, r *http.Request) {
		if !s.auth(w, r) {
			return
		}
		s.json(w, http.StatusOK, map[string]any{"turns": s.turns()})
	})
	mux.HandleFunc("/v1/read_back", func(w http.ResponseWriter, r *http.Request) {
		if !s.auth(w, r) {
			return
		}
		session, step, kind, err := axes(r)
		if err != nil {
			s.json(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		docs := s.led.Docs()
		cells := kernel.CellsOf(docs, session, step, kind)
		out := make([]map[string]any, 0, len(cells))
		for _, c := range cells {
			out = append(out, map[string]any{"key": c.Key, "claim": c.Claim,
				"basis": c.Basis.String(), "source": c.Source, "payload_sha": c.PayloadSHA})
		}
		s.json(w, http.StatusOK, map[string]any{
			"session": session, "call": step, "side": kind, "cells": out,
			"absent": kernel.AbsentKeys(docs, session, step, kind),
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.json(w, http.StatusNotFound, map[string]any{"endpoints": []string{
			"/healthz", "/v1/verify", "/v1/tally", "/v1/celltable", "/v1/turns", "/v1/read_back"}})
	})
	// 只读与鉴权各管各的：写请求必须先吃到 405。若让它先吃到 401，
	// 审计者读到的就是"配上口令就能写"——那正是这个服务最不想被读出来的意思。
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			s.json(w, http.StatusMethodNotAllowed, map[string]any{
				"error": "这个服务只读：没有任何写入口（record/check 请走本机 stdio 外壳 kernel.record_call）"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// auth：缺 token ⇒ 401，且**不回任何账本内容**（401 的响应体里不许有 claim）。
func (s *server) auth(w http.ResponseWriter, r *http.Request) bool {
	want := os.Getenv("KAUDIT_TOKEN")
	if want == "" {
		return true // 只绑回环时允许无口令；checkListen 已经拦掉别的绑法
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	if got != want {
		s.json(w, http.StatusUnauthorized, map[string]any{"error": "没给 Bearer token（或不对）—— 账本内容一个字都不给"})
		return false
	}
	return true
}

// axes 一发的身份是三个轴（session + 发号 + 侧），少一个就会把两本不同 session 的
// 同号同侧并成一次回答 —— 与写侧 alreadyRecorded 同一份定义。
// rejectedOrNull 把"不可知"落成 JSON 的 null，而不是 0。
// 为什么值得为它开一个函数：`"rejected_writes": 0` 与 `"rejected_writes": null` 在
// 审计方那里是两句话（前者=量到一次都没拒，后者=这份归档里没写过这一栏），
// 而 10-17 那次实测印出来的正是前者，真值是后者（缺口 #51）。
func rejectedOrNull(l *kernel.Ledger) any {
	if l.GateStatus() == kernel.GateUnknown {
		return nil
	}
	return l.Rejected()
}

// gateOrNull 与 rejectedOrNull 同一条判据、同一个 owner：没有计数栏就整格 null，
// 不许印一张六个格子都填着 0 的表 —— 那张表长得像"量到了，全是 0"。
func gateOrNull(j kernel.Journal) any {
	if !j.HasGate {
		return nil
	}
	return j.Gate
}

func axes(r *http.Request) (string, int, string, error) {
	q := r.URL.Query()
	session, side := q.Get("session"), q.Get("side")
	call, err := strconv.Atoi(q.Get("call"))
	if err != nil {
		return "", 0, "", fmt.Errorf("call 要是一个发号（不是 %q）", q.Get("call"))
	}
	if session == "" {
		return "", 0, "", fmt.Errorf("session 不能空：空的读侧入口会把所有 session 的同一发并成一份")
	}
	var kind string
	switch side {
	case "input":
		kind = "model_input"
	case "output":
		kind = "model_output"
	default:
		return "", 0, "", fmt.Errorf("side 要 input 或 output（给的是 %q）", side)
	}
	return session, call, kind, nil
}

// turns 把账本按 (session, 发号) 聚合，顺带数每发两侧的格数 —— 审计的人第一问就是"有哪几发"。
func (s *server) turns() []map[string]any {
	type key struct {
		session string
		step    int
	}
	seen := map[key]map[string]int{}
	order := []key{}
	for _, e := range s.led.Docs() {
		k := key{e.Session, e.Step}
		if _, ok := seen[k]; !ok {
			seen[k] = map[string]int{}
			order = append(order, k)
		}
		side := "other"
		if i := strings.IndexByte(e.Kind, ':'); i >= 0 {
			side = e.Kind[:i]
		}
		seen[k][side]++
	}
	out := make([]map[string]any, 0, len(order))
	for _, k := range order {
		out = append(out, map[string]any{"session": k.session, "call": k.step, "cells_by_side": seen[k]})
	}
	return out
}

func celltable() []map[string]any {
	out := []map[string]any{}
	for _, side := range []struct {
		name  string
		cells []kernel.Cell
	}{{"input", kernel.InputMeta{}.Cells()}, {"output", kernel.OutputMeta{}.Cells()}} {
		for _, c := range side.cells {
			out = append(out, map[string]any{"side": side.name, "key": c.Key, "field": c.Field})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i]["side"] != out[j]["side"] {
			return out[i]["side"].(string) < out[j]["side"].(string)
		}
		return out[i]["key"].(string) < out[j]["key"].(string)
	})
	return out
}

func (s *server) json(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("kaudit: 写响应失败：%v", err)
	}
}
