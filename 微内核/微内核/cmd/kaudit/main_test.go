// 这些测试管的是**外壳**的判定，不是内核的：内核那 104 条测试一个字都碰不到这里。
// 之前吃过一次教训 —— 全部测试只走 Go API，于是「`check_call` 比 `record_call` 松」
// 那一类缺陷（同一条数、两个入口给两种答案）在测试面上完全不存在。
// 这份文件就是为了让"远程只读审计"这几条承诺变成可失败的判定。
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"agentkernel"
)

func sampleLedger(t *testing.T) []byte {
	t.Helper()
	p := filepath.Join("..", "..", "报告", "样例-agent.jsonl")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读不到样例账本 %s：%v —— 这条测试不许因为文件不在就「跳过」，那等于没测", p, err)
	}
	return raw
}

// TestRealLedgerLoadsGreen：真实落在盘上的那本账，经外壳读回来必须是绿的，
// 而且必须**真的比对过原像**（entries>0 且 checked>0）。只判 state==green 不够，
// 一份"没原像所以什么都否不了"的账也会是 green 以外的答案 —— 那就正是下面那条测试。
func TestRealLedgerLoadsGreen(t *testing.T) {
	s, err := newServerBytes("sample", sampleLedger(t))
	if err != nil {
		t.Fatalf("这份账本该被载入：%v", err)
	}
	if s.state() != "green" {
		t.Errorf("样例账本的链该是绿的，实际 %s：%s", s.state(), s.report.String())
	}
	if s.report.Entries == 0 || s.report.Checked == 0 {
		t.Errorf("一条都没比对过原像却报了绿：entries=%d checked=%d", s.report.Entries, s.report.Checked)
	}
}

// TestNoPreimageIsNotGreen：同一本账，用 PayloadOmitted 重新序列化（链仍自洽、但第三方重算不出原值）。
// 它必须是**第三种答案**：既不许是 green，也不许喊成篡改。
func TestNoPreimageIsNotGreen(t *testing.T) {
	s, err := newServerBytes("sample", sampleLedger(t))
	if err != nil {
		t.Fatalf("装载失败：%v", err)
	}
	omit, err := kernel.MarshalJournal(s.led.Docs(), kernel.PayloadOmitted)
	if err != nil {
		t.Fatalf("重新序列化失败：%v", err)
	}
	s2, err := newServerBytes("omit", omit)
	if err != nil {
		t.Fatalf("没有原像不是拒收的理由，它该被载入：%v", err)
	}
	if s2.state() != "undecidable" {
		t.Errorf("没原像的那份该报 undecidable，实际 %s：%s", s2.state(), s2.report.String())
	}
	if strings.Contains(s2.report.String(), "✓") {
		t.Errorf("「核不了」被打成了 ✓（第三种答案不许长得像绿）：%s", s2.report.String())
	}
}

// TestTamperedLedgerRefused：改掉一格的人话，外壳必须**拒绝起来**，而不是起来后给一份漂亮读数。
func TestTamperedLedgerRefused(t *testing.T) {
	raw := sampleLedger(t)
	i := strings.Index(string(raw), "model_output")
	if i < 0 {
		t.Fatal("样例账本里找不到 model_output，这条测试的形状变了")
	}
	tampered := []byte(strings.Replace(string(raw), `"claim":"`, `"claim":"改了`, 2))
	if string(tampered) == string(raw) {
		t.Fatal("没改到任何东西")
	}
	if _, err := newServerBytes("tampered", tampered); err == nil {
		t.Errorf("被改过的账本竟然被外壳接受了（那就是把篡改当正常供出去）")
	}
}

// TestHeaderLineIsSkippedNotCountedBad：encode_journal 的第一行是给人读的表头。
// 外壳必须剥掉它 —— 否则它变成一条 BadLine，而「解开了 46/47」会被读成「46 条账」。
func TestHeaderLineIsSkippedNotCountedBad(t *testing.T) {
	raw := sampleLedger(t)
	withHeader := append([]byte("这本账由 kaudit 供出，共 47 条往来\n"), raw...)
	s, err := newServerBytes("with-header", withHeader)
	if err != nil {
		t.Fatalf("带了表头就该照常载入：%v", err)
	}
	if !s.skippedHeader {
		t.Errorf("没认出第一行是表头（那它会进 BadLines）")
	}
	if len(s.journal.BadLines) != 0 {
		t.Errorf("表头被当成解不开的一行：%v", s.journal.BadLines)
	}
}

func newAuthedServer(t *testing.T, token string) http.Handler {
	t.Helper()
	s, err := newServerBytes("sample", sampleLedger(t))
	if err != nil {
		t.Fatalf("装载失败：%v", err)
	}
	t.Setenv("KAUDIT_TOKEN", token)
	return s.handler()
}

// TestAuthRequiredAndSilent：没口令/错口令 ⇒ 401，而且**响应体里不许有账本内容**。
// 逐个数过的端点都跑一遍 —— 只测一个端点等于给其余端点发了空白通行证。
func TestAuthRequiredAndSilent(t *testing.T) {
	h := newAuthedServer(t, "sekret")
	endpoints := []string{"/healthz", "/v1/verify", "/v1/tally", "/v1/celltable", "/v1/turns",
		"/v1/read_back?session=agent&call=1&side=output"}
	for _, ep := range endpoints {
		for _, hdr := range []string{"", "Bearer nope", "Bearer"} {
			req := httptest.NewRequest(http.MethodGet, ep, nil)
			if hdr != "" {
				req.Header.Set("Authorization", hdr)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s 上 Authorization=%q 该 401，实际 %d", ep, hdr, rec.Code)
			}
			body := rec.Body.String()
			for _, leak := range []string{"model_output", "fail_status", "\"cells\"", "\"entries\"", "\"turns\"", "✓"} {
				if strings.Contains(body, leak) {
					t.Errorf("%s 的 401 响应体里漏出了账本内容 %q：%.120s", ep, leak, body)
				}
			}
		}
		req := httptest.NewRequest(http.MethodGet, ep, nil)
		req.Header.Set("Authorization", "Bearer sekret")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s 上对的口令该 200，实际 %d：%.120s", ep, rec.Code, rec.Body.String())
		}
	}
}

// TestListenGuardRefusesPublicBindWithoutToken：绑到非回环 = 把全部往来原话交给网段，
// 没有口令就拒绝启动，而不是"先起来再说"。三种组合都得判：
// 回环+无口令（放行）、非回环+无口令（拒）、非回环+有口令（放行）。
// 只测"拒"那一支的话，把判据写成"永远拒"也照样绿。
func TestListenGuardRefusesPublicBindWithoutToken(t *testing.T) {
	s, err := newServerBytes("sample", sampleLedger(t))
	if err != nil {
		t.Fatalf("装载失败：%v", err)
	}
	cases := []struct {
		listen, token string
		wantErr       bool
		why           string
	}{
		{"127.0.0.1:8788", "", false, "只绑回环可以无口令"},
		{"localhost:8788", "", false, "localhost 同回环"},
		{"0.0.0.0:8788", "", true, "全网监听没口令必须拒"},
		{"192.168.1.7:8788", "", true, "网段地址没口令必须拒"},
		{"0.0.0.0:8788", "sekret", false, "给了口令就放行"},
		{"not-an-address", "", true, "监听地址看不懂也要拒"},
	}
	for _, c := range cases {
		t.Setenv("KAUDIT_TOKEN", c.token)
		err := s.checkListen(c.listen)
		if (err != nil) != c.wantErr {
			t.Errorf("%s：listen=%s token=%q 该要 err=%v，实际 err=%v", c.why, c.listen, c.token, c.wantErr, err)
		}
	}
}

// TestReadOnly：这个服务一个字都不写。任何非 GET ⇒ 405，且理由要说清"写走 stdio 外壳"。
// 关键的一条是**它与口令无关**：没带 token 的 POST 不许先吃到 401，
// 否则审计者读到的是"配上口令就能写"，而那是这个服务最不该被读出来的意思。
func TestReadOnly(t *testing.T) {
	h := newAuthedServer(t, "sekret")
	for _, hdr := range []string{"", "Bearer sekret"} {
		for _, p := range []string{"/v1/verify", "/v1/read_back", "/kernel.record_call", "/healthz"} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, p, strings.NewReader("{}"))
			if hdr != "" {
				req.Header.Set("Authorization", hdr)
			}
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s 上 POST（Authorization=%q）该 405（只读服务没有写入口），实际 %d：%.120s",
					p, hdr, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "只读") {
				t.Errorf("%s 的 405 没说清为什么：%.80s", p, rec.Body.String())
			}
			if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
				t.Errorf("%s 的 405 没告知只许 %q，实际 Allow=%q", p, "GET, HEAD", got)
			}
		}
	}
}

// TestReadBackNeedsThreeAxes：一发的身份是 session + 发号 + 侧。
// 少给一个轴，读回来的就会是"别的 session 的同一发"，那比报错更糟。
func TestReadBackNeedsThreeAxes(t *testing.T) {
	h := newAuthedServer(t, "sekret")
	cases := []struct {
		q, want string
	}{
		{"/v1/read_back?call=1&side=output", "session"},
		{"/v1/read_back?session=agent&side=output", "call"},
		{"/v1/read_back?session=agent&call=1", "side"},
		{"/v1/read_back?session=agent&call=1&side=everything", "side"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, c.q, nil)
		req.Header.Set("Authorization", "Bearer sekret")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s 该 400，实际 %d", c.q, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s 的报错没点名缺的那个轴 %q：%.120s", c.q, c.want, rec.Body.String())
		}
	}
}

// TestReadBackEndToEnd：从 /v1/turns 里拿一发，再按那三个轴读回来，必须读到东西，
// 而且每格都带出处 —— 审计的人问的就是"这一格谁算的"。
func TestReadBackEndToEnd(t *testing.T) {
	h := newAuthedServer(t, "sekret")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/turns", nil)
	req.Header.Set("Authorization", "Bearer sekret")
	h.ServeHTTP(rec, req)
	var turns struct {
		Turns []struct {
			Session string         `json:"session"`
			Call    int            `json:"call"`
			BySide  map[string]int `json:"cells_by_side"`
		} `json:"turns"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &turns); err != nil {
		t.Fatalf("/v1/turns 回来的不是 JSON：%v（%.120s）", err, rec.Body.String())
	}
	if len(turns.Turns) == 0 {
		t.Fatal("一本有账的账本，turns 却是空的")
	}
	first := turns.Turns[0]
	q := "/v1/read_back?session=" + first.Session + "&call=" + strconv.Itoa(first.Call) + "&side=output"
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, q, nil)
	req2.Header.Set("Authorization", "Bearer sekret")
	h.ServeHTTP(rec2, req2)
	var back struct {
		Cells []struct {
			Key    string `json:"key"`
			Basis  string `json:"basis"`
			Source string `json:"source"`
			Claim  string `json:"claim"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &back); err != nil {
		t.Fatalf("read_back 回来的不是 JSON：%v", err)
	}
	if len(back.Cells) == 0 {
		t.Fatalf("%s 读回来 0 格（写侧能核不等于读侧能用）", q)
	}
	for _, c := range back.Cells {
		if c.Basis == "" || c.Claim == "" {
			t.Errorf("%s 读回来的格子缺出处或缺人话：%+v", q, c)
		}
	}
}

// TestEmptyBookIsNotGreen：一份**一条都没有**的账供出去时，`state` 必须是第四个词，
// 不许是 `green`，正文也不许戴 ✓。审计者把 `green` 读成"这本合同核过了"，
// 而这里连一合同都没记过 —— 那既不是通过，也不是篡改，也不是"核不了"（那是手里字节不够）。
// 反面对照同样要判：真那本合同仍然必须是 green（否则这条判据只是"永远不给绿"）。
func TestEmptyBookIsNotGreen(t *testing.T) {
	s, err := newServerBytes("empty", []byte(""))
	if err != nil {
		t.Fatalf("空账本该被接受（还没记不是坏字节）：%v", err)
	}
	if st := s.state(); st != "nothing_to_audit" {
		t.Errorf("空账的 state 该是 nothing_to_audit，实际 %q", st)
	}
	if strings.Contains(s.report.String(), "✓") {
		t.Errorf("空账的正文戴了 ✓：%s", s.report.String())
	}
	if !strings.Contains(s.report.String(), "尚无账可核") {
		t.Errorf("空账没点名「尚无账可核」：%s", s.report.String())
	}
	h := newAuthedServer(t, "sekret")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/verify", nil)
	req.Header.Set("Authorization", "Bearer sekret")
	// 这一趟打的是**样例账**（非空），它必须是 green —— 另一头的对照。
	h.ServeHTTP(rec, req)
	var v struct {
		State string `json:"state"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("/v1/verify 不是 JSON：%v", err)
	}
	if v.State != "green" || !strings.Contains(v.Text, "✓ 链自洽") {
		t.Errorf("非空账的绿没回来：state=%q text=%.80s", v.State, v.Text)
	}
}
