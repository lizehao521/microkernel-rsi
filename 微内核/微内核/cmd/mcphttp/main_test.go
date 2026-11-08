// 这一份测的是**传输层**的判定：405/401/400/413/202 各归谁、口令漏不漏内容、
// 一条请求有没有上限。协议内容本身由 mcpsrv 的测试管，不在这里重测一遍
// （两处各测一遍 = 两套「我以为服务端长什么样」）。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentkernel/mcpsrv"
)

func newTestServer(t *testing.T, token string) http.Handler {
	t.Helper()
	h := &httpServer{srv: mcpsrv.New()}
	t.Setenv("AGENTKERNEL_TOKEN", token)
	return h.handler()
}

func post(h http.Handler, body, token, altHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader([]byte(body)))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if altHeader != "" {
		req.Header.Set("X-Agentkernel-Token", altHeader)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func resultText(t *testing.T, body []byte) (string, bool) {
	t.Helper()
	var rep struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatalf("回来的不是 JSON：%v（%.160s）", err, body)
	}
	if len(rep.Result.Content) == 0 {
		t.Fatalf("回话里没有 content 段：%.160s", body)
	}
	return rep.Result.Content[0].Text, rep.Result.IsError
}

// TestRPCOverHTTP：握手、清单、格位表三趟都走真的 net/http 路径（不是直接调函数）。
// 这一条存在的意义是：URL 那一面如果连不上，用户看到的就只是"填了没反应"。
func TestRPCOverHTTP(t *testing.T) {
	h := newTestServer(t, "")
	rec := post(h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize 该 200，实际 %d：%.120s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type 该是 application/json，实际 %q", ct)
	}
	var init struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &init); err != nil {
		t.Fatalf("initialize 回来的不是 JSON：%v", err)
	}
	if init.Result.ServerInfo.Name != "agentkernel" || init.Result.ProtocolVersion != mcpsrv.ProtocolVersion {
		t.Errorf("握手报了别的东西：name=%q pv=%q", init.Result.ServerInfo.Name, init.Result.ProtocolVersion)
	}

	rec = post(h, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "", "")
	var tl struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tl); err != nil {
		t.Fatalf("tools/list 不是 JSON：%v", err)
	}
	if len(tl.Result.Tools) != mcpsrv.ToolCount() {
		t.Errorf("HTTP 这面列出 %d 个工具，mcpsrv 自己说 %d 个（同一个 owner 不该两个数）",
			len(tl.Result.Tools), mcpsrv.ToolCount())
	}

	rec = post(h, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"kernel.celltable","arguments":{}}}`, "", "")
	txt, isErr := resultText(t, rec.Body.Bytes())
	if isErr {
		t.Fatalf("celltable 被拒：%s", txt)
	}
	if !strings.HasPrefix(txt, "当前 49 格（输入侧 20 + 输出侧 29）；唯一键名 48 个") {
		t.Errorf("URL 那一面报的格位表与封板值不符：第一行 %q", strings.SplitN(txt, "\n", 2)[0])
	}
}

// TestNonPostIs405RegardlessOfAuth：非 POST 一律 405，**与口令无关**（与只读外壳同一条规矩）。
// GET 尤其要说清"不支持 SSE 流"，否则客户端会挂在一个永远不返回的 GET 上等流。
func TestNonPostIs405RegardlessOfAuth(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodOptions} {
		for _, token := range []string{"", "sekret"} {
			h := newTestServer(t, "sekret")
			req := httptest.NewRequest(method, "/mcp", nil)
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s（Authorization=%q）该 405，实际 %d：%.100s", method, token, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Allow"); got != "POST" {
				t.Errorf("%s 的 405 没告知只许 POST，Allow=%q", method, got)
			}
			if method == http.MethodGet && !strings.Contains(rec.Body.String(), "SSE") {
				t.Errorf("GET 的拒绝没说清「不给 SSE 流」，客户端会一直等：%s", rec.Body.String())
			}
		}
	}
}

// TestGarbageBodyIs400：体不像 JSON-RPC 时要当场 400 并点名为什么，
// 而且**不许把送来的字节原样回显**（那是把请求体变成响应体的一条反射）。
func TestGarbageBodyIs400(t *testing.T) {
	h := newTestServer(t, "")
	rec := post(h, `hello-kernel`, "", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("一句 helloworld 该 400，实际 %d：%.120s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "method") {
		t.Errorf("400 没点名缺的是什么（要说出 method）：%s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "hello-kernel") {
		t.Errorf("响应体里回显了请求内容：%s", rec.Body.String())
	}
}

// TestOverLimitBodyRefused：一条请求有上限，超了要当场拒并说清是多少。
// 不设上限等于让任何人用一条 100 MB 的 load_journal 把这台只剩几百 MB 的机器打死。
func TestOverLimitBodyRefused(t *testing.T) {
	h := newTestServer(t, "")
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kernel.load_journal","arguments":{"text":"` +
		strings.Repeat("x", maxBody+1024) + `"}}}`
	rec := post(h, body, "", "")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("超过 %d 字节的请求该 413，实际 %d：%.120s", maxBody, rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), fmt.Sprintf("%d", maxBody)) {
		t.Errorf("413 没告知上限是多少：%s", rec.Body.String())
	}
}

// TestTokenGate：给了口令就强制；两种头都认；401 的响应体里不许有链上的东西。
// 这一面与只读外壳不同 —— 它**能写**，所以 401 必须一个字节都不给。
func TestTokenGate(t *testing.T) {
	h := newTestServer(t, "sekret")
	for _, c := range []struct {
		hdr, alt string
		want     int
	}{
		{"", "", http.StatusUnauthorized},
		{"nope", "", http.StatusUnauthorized},
		{"sekret", "", http.StatusOK},
		{"", "sekret", http.StatusOK},
	} {
		rec := post(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kernel.tally","arguments":{}}}`, c.hdr, c.alt)
		if rec.Code != c.want {
			t.Errorf("Authorization=%q X-Agentkernel-Token=%q 该 %d，实际 %d：%.120s",
				c.hdr, c.alt, c.want, rec.Code, rec.Body.String())
		}
		if c.want == http.StatusUnauthorized {
			for _, leak := range []string{"链自洽", "被拒的写入", "content"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Errorf("401 的响应体里漏出了 %q：%s", leak, rec.Body.String())
				}
			}
		}
	}
}

// TestNotificationIs202NoBody：通知处理但不回话，回 202 空体。
// 空体的 200 会被读成"回了一条空结果"，那是另一种假绿。
func TestNotificationIs202NoBody(t *testing.T) {
	h := newTestServer(t, "")
	rec := post(h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "", "")
	if rec.Code != http.StatusAccepted {
		t.Errorf("通知该 202，实际 %d：%.120s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "" {
		t.Errorf("202 不该带体，实际 %.120s", rec.Body.String())
	}
}

// TestCheckListenRefusesPublicBindWithoutToken：三条都要判。
// 只判"该拒"那一支的话，把判据写成"永远拒"也照样绿。
func TestCheckListenRefusesPublicBindWithoutToken(t *testing.T) {
	h := &httpServer{srv: mcpsrv.New()}
	cases := []struct {
		listen, token, wantMsg string
		wantErr                bool
		why                    string
	}{
		{"127.0.0.1:8797", "", "", false, "只绑回环可以无口令"},
		{"localhost:8797", "", "", false, "localhost 同回环"},
		{"0.0.0.0:8797", "", "写入口", true, "全网监听没口令必须拒，并点名这一面能写"},
		{"192.168.1.9:8797", "", "AGENTKERNEL_TOKEN", true, "网段地址没口令必须拒，并说要给哪个变量"},
		{"0.0.0.0:8797", "sekret", "", false, "给了口令就放行"},
		{"not-an-address", "", "监听地址看不懂", true, "地址形状不对要单独说，不许混进口令那一类"},
	}
	for _, c := range cases {
		t.Setenv("AGENTKERNEL_TOKEN", c.token)
		err := h.checkListen(c.listen)
		if (err != nil) != c.wantErr {
			t.Errorf("%s：listen=%s token=%q 该 err=%v，实际 %v", c.why, c.listen, c.token, c.wantErr, err)
		}
		if c.wantMsg != "" {
			if err == nil {
				t.Errorf("%s：该有报错好点名 %q，结果没报错", c.why, c.wantMsg)
				continue
			}
			if !strings.Contains(err.Error(), c.wantMsg) {
				t.Errorf("%s：报错没点名「%s」：%v", c.why, c.wantMsg, err)
			}
		}
	}
}

// TestLoadJournalThenVerifyOverHTTP：拿盘上真落过的那本账经 HTTP 载进来，再验链。
// 这一条同时管三件事：URL 那面能写（load 是写）、跨请求共享同一本账（不是每次新建）、
// 以及 verify 的 ✓ 必须**真的比对过原像**（checked>0）。
func TestLoadJournalThenVerifyOverHTTP(t *testing.T) {
	p := filepath.Join("..", "..", "报告", "样例-agent.jsonl")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读不到样例账本 %s：%v —— 这条不许因为文件不在就跳过，那等于没测", p, err)
	}
	esc, err := json.Marshal(string(raw))
	if err != nil {
		t.Fatalf("账本没法塞进 JSON：%v", err)
	}
	h := newTestServer(t, "")
	rec := post(h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kernel.load_journal","arguments":{"text":`+
		string(esc)+`}}}`, "", "")
	txt, isErr := resultText(t, rec.Body.Bytes())
	if isErr {
		t.Fatalf("带 acknowledge 之外的情况都不该拒（这本本来是空的）：%s", txt)
	}
	if !strings.Contains(txt, "读回") {
		t.Errorf("载入没回报数：%s", txt)
	}
	rec = post(h, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kernel.verify","arguments":{}}}`, "", "")
	txt, isErr = resultText(t, rec.Body.Bytes())
	if isErr {
		t.Fatalf("刚读回来的链该是自洽的：%s", txt)
	}
	if !strings.Contains(txt, "✓") || strings.Contains(txt, "其中 0 条比对过原像") {
		t.Errorf("verify 这一面要么没打勾、要么一条原像都没比对过：%s", txt)
	}
	if strings.Contains(txt, "共 0 条") {
		t.Errorf("载入了账却验了个空链（跨请求没共享同一本账）：%s", txt)
	}
	// 拒收的那句理由必须**原样传到 URL 这一面**：传输层不许把它吞成一条通用 500，
	// 也不许替内核归因。（去重门那一半由 mcpsrv 与内核的测试管，这里不重测。）
	rec = post(h, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"kernel.record_call","arguments":{}}}`, "", "")
	txt, isErr = resultText(t, rec.Body.Bytes())
	if !isErr || !strings.Contains(txt, "发号从 1 起") {
		t.Errorf("空一发的拒收理由没传到 URL 这一面：isErr=%v %s", isErr, txt)
	}
}

// TestServerTimeoutsAreSet：服务端四个超时必须是**非零**。
// 这条测的是配置而不是计时——在慢机器上"等一个慢连接被切断"的测试是运气测试，
// 它会把"没设超时"偶尔读成通过。配置能当场判，就当场判。
func TestServerTimeoutsAreSet(t *testing.T) {
	s := newHTTPServer(nil)
	for _, c := range []struct {
		name string
		d    time.Duration
	}{{"ReadHeaderTimeout", s.ReadHeaderTimeout}, {"ReadTimeout", s.ReadTimeout},
		{"WriteTimeout", s.WriteTimeout}, {"IdleTimeout", s.IdleTimeout}} {
		if c.d <= 0 {
			t.Errorf("%s 没设（%v）—— 一个不动的连接能永久占着 goroutine", c.name, c.d)
		}
	}
	if s.ReadHeaderTimeout >= s.ReadTimeout {
		t.Errorf("ReadHeaderTimeout(%v) 不该不短于 ReadTimeout(%v)：慢着头发就能占满读时限",
			s.ReadHeaderTimeout, s.ReadTimeout)
	}
	// IdleTimeout **「非零」不够**。120 秒这种非零会把挂载的连接杀掉，而且从外面看不出来：
	// 现场两次独立观测（10-16）—— 一把子代理报 `ECONNRESET`；我自己撞上会话里回
	// `No connected MCP server offers a tool named …`，而**同一个进程还在听**（`UP pid=…`）、直连 HTTP 正常。
	// 对照实验（同一根 keep-alive 连接、闲置后发第二发）：闲置 30 秒照常 200，
	// 闲置 130 秒 `ConnectionAbortedError: [WinError 10053]`。
	// 这一面的闲置时长是"两次对话之间的间隔"，那是几分钟到十几分钟级 —— 界必须**远大于**它，
	// 否则我们守住的不是慢连接，是我们自己的挂载。
	if s.IdleTimeout < 15*time.Minute {
		t.Errorf("IdleTimeout=%v 太短：跨轮次闲置会杀掉客户端的 keep-alive 连接（120 秒时实测闲置 130 秒就断），至少要 %v 量级",
			s.IdleTimeout, 15*time.Minute)
	}
}

// TestQueueTimeoutIs503AndCounted：锁被占住时，请求必须**在等待上限后拿到 503**，
// 而且这次拒绝要留下数（"被拒也要看得见"是同一条理由，不因它在传输层就免）。
// 反面对照同样要判：锁空着时必须照常 200 —— 否则把判据写成"永远 503"也照样绿。
func TestQueueTimeoutIs503AndCounted(t *testing.T) {
	h := &httpServer{srv: mcpsrv.New(), wait: 1500 * time.Millisecond}
	t.Setenv("AGENTKERNEL_TOKEN", "")
	handler := h.handler()
	rpc := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kernel.tally","arguments":{}}}`
	rec := post(handler, rpc, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("没人排队时该 200，实际 %d：%.120s", rec.Code, rec.Body.String())
	}
	release, ok := h.srv.Hold()
	if !ok {
		t.Fatal("Hold 拿不到锁——测试自己的前提就不成立，别往下判")
	}
	rec = post(handler, rpc, "", "")
	release()
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("排不到队该 503，实际 %d：%.160s", rec.Code, rec.Body.String())
	}
	if n := h.overloads.Load(); n != 1 {
		t.Errorf("排队超时没计数（被拒读成没发生过）：overloads=%d 该 1", n)
	}
	for _, want := range []string{"一次只处理一条", "第 1 次"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("503 的理由少了 %q：%.200s", want, rec.Body.String())
		}
	}
	rec = post(handler, rpc, "", "")
	if rec.Code != http.StatusOK {
		t.Errorf("交还锁之后必须能做作业务，实际 %d", rec.Code)
	}
}

// TestBrokenArgumentsMustNotCollapseIntoNotification —— 10-16 实测：`arguments` 里断掉的请求回的是
//
//	HTTP 202、Content-Length 0、正文 0 字节
//
// 而 202 在这一面是「通知，没有回话」的合法形状。根因是一处塌缩：`Handle` 把
// 「整条解不开」与「真通知」都回成 nil，传输层照 nil 发 202 —— 于是调用方拿到的是
// "发成功了"，而不是"你这条我看不懂"。这条也不进任何计数，所以连"被拒过"都看不见。
// （同一面在整条请求体连 method 都没有时倒是老实回 400 —— 分叉只补了一条，正是这种洞的形状。）
func TestBrokenArgumentsMustNotCollapseIntoNotification(t *testing.T) {
	h := newTestServer(t, "")
	rec := post(h, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":`+
		`{"name":"kernel.check_call","arguments":{"session":"x","input":{broken}}}}`, "", "")
	if rec.Code == http.StatusAccepted {
		t.Errorf("解不开的请求塌成了 202（读起来像「已经收下了」）：%.80s", rec.Body.String())
	}
	// 状态码也要判：规范（transports 一节）对"收下这条输入"失败写的是 MUST return an HTTP error
	// status code（例如 400 Bad Request）—— 只把理由塞进 200 的正文，是"我看见了"但"没说我拒了"。
	if rec.Code != http.StatusBadRequest {
		t.Errorf("解不开的请求该 400（规范：cannot accept the input ⇒ MUST return an HTTP error code），实际 %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) == "" {
		t.Errorf("回话是空的（HTTP %d）—— 调用方拿不到任何理由", rec.Code)
	} else if !strings.Contains(rec.Body.String(), "解不开") {
		t.Errorf("拒了但没点名是「这条请求本身解不开」：%.160s", rec.Body.String())
	}
	// 第三种坏法分两头，因为它们**不是同一个 owner 拒的**（22 根那轮的 s20 把这点照了出来：
	// 只发 `{"id":5}` 时是「门」先答的，分类那一支根本没被执行，测试却照样绿 —— 那是假证据）。
	// ①门：体里连 "method" 这个键名字符串都没有 ⇒ mcpsrvLooksLikeRPC 在传输层就挡下，走不到协议层。
	nm := post(h, `{"jsonrpc":"2.0","id":5}`, "", "")
	if nm.Code != http.StatusBadRequest {
		t.Errorf("连 method 键都没有的体该被门挡成 400，实际 %d：%.120s", nm.Code, nm.Body.String())
	}
	if !strings.Contains(nm.Body.String(), "method") {
		t.Errorf("门拒了但没点名缺的是 method：%.160s", nm.Body.String())
	}
	// ②分类：**有 "method" 这个键、值为空串** ⇒ 过了门，才轮到 HandleOutcome 判它是不是无效请求。
	// 这一头才是 s20 那根针咬得住的形状（把 `r.Method == ""` 短路掉，这条会掉进 route 的 default）。
	cl := post(h, `{"jsonrpc":"2.0","method":"","id":6}`, "", "")
	if cl.Code != http.StatusBadRequest {
		t.Errorf("method 是空串的消息该 400（它不是通知），实际 %d：%.120s", cl.Code, cl.Body.String())
	}
	if !strings.Contains(cl.Body.String(), "-32600") {
		t.Errorf("该用 JSON-RPC 的「无效请求」码 -32600，正文里却没有：%.200s", cl.Body.String())
	}
	if !strings.Contains(cl.Body.String(), "method") {
		t.Errorf("分类拒了但没点名缺的是 method：%.200s", cl.Body.String())
	}

	// 反面对照：真通知仍然必须 202 空体 —— 只判上面那一支，把判据写成「永远 400」也照样绿。
	ok := post(h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "", "")
	if ok.Code != http.StatusAccepted || strings.TrimSpace(ok.Body.String()) != "" {
		t.Errorf("真通知被误伤了：HTTP %d，体 %.80s", ok.Code, ok.Body.String())
	}
	// 而且这条解不开的请求要**有数**（被拒也要看得见）：tally 里得能看见它没过协议层。
	tally := post(h, `{"jsonrpc":"2.0","id":8,"method":"tools/call",`+
		`"params":{"name":"kernel.tally","arguments":{}}}`, "", "")
	if !strings.Contains(tally.Body.String(), "被协议层判成无效") {
		t.Errorf("tally 里看不见「被协议层判成无效」这一笔：%.200s", tally.Body.String())
	}
}

// TestDoorRefusalIsCounted —— #44：门挡下的那一条必须进某个计数，否则「被拒也要看得见」
// 在第四个桶上没落实。
//
// 10-16 的 22 根那轮由 s20 照出来的形状：`{"jsonrpc":"2.0","id":5}`（体里连 `"method"` 键都没有）
// 被 `mcpsrvLooksLikeRPC` 挡成 400 并给了理由，但它既不在 `doorRefused`（那数的是「整块没给的写请求」），
// 也不在 `badRequests`（那要走到 `mcpsrv.HandleOutcome` 才 +1）—— 当场读数印证：直连发了三种坏形状，
// `kernel.tally` 只印「2 条请求连协议层都没过」。
//
// 这一条刻意**不判那句短语在不在**：那句是格式化字符串里写死的，数字是 0 也照样印得出
// 「连协议层都没过」—— 拿它当证据就是把"没跑成"读成"跑过了"（上面那支测试的注释里记的是同一族错误）。
// 判的是**数**：门挡下两条，那一笔必须动 2。
func TestDoorRefusalIsCounted(t *testing.T) {
	h := newTestServer(t, "")
	before := doorCount(t, h)
	for _, body := range []string{`{"jsonrpc":"2.0","id":5}`, `{"jsonrpc":"2.0","id":9}`} {
		rec := post(h, body, "", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("门该把它挡成 400，实际 %d：%.120s", rec.Code, rec.Body.String())
		}
	}
	if got := doorCount(t, h) - before; got != 2 {
		t.Errorf("门挡下了 2 条请求，tally 那一笔却只动了 %d —— 被拒也要看得见（改前实测：那一笔根本没印）", got)
	}
}

// TestBucketsDoNotMerge —— #49：四桶各说各的层位，不许共用同一句话（共用句话的下一步就是共用那个数）。
//
// 今天会红的是**归因**，不是数：数本来就分开（#44 立第五笔之前连分开的数都没有）。
// 一处协议层的拒绝（`-32700` 解不开、`-32600` 没有 method）被写成「它没走到协议层」，
// 而真正没走到协议层的是传输层那道门 —— 读者按这句话归因就会把两桶读反，
// 而"答得出谁拒的"是这个产品唯一 KPI 的前提。
func TestBucketsDoNotMerge(t *testing.T) {
	h := newTestServer(t, "")
	before := bucketCounts(t, h)

	// 门（第五笔）：体里连 "method" 键都没有 → 传输层挡下，连协议层都没到
	door := post(h, `{"jsonrpc":"2.0","id":5}`, "", "")
	if !strings.Contains(door.Body.String(), "method") {
		t.Errorf("门拒了但没点名缺的是 method：%.160s", door.Body.String())
	}
	mid := bucketCounts(t, h)
	if mid.transport != before.transport+1 {
		t.Errorf("门挡下 1 条，第五笔却动了 %d", mid.transport-before.transport)
	}
	if mid.proto != before.proto {
		t.Errorf("门挡的那条不该进第三笔（它连协议层都没到），第三笔却动了 %d", mid.proto-before.proto)
	}
	if mid.door != before.door {
		t.Errorf("门挡的那条不该进第四笔（那是整块没给／写侧没这个键），第四笔却动了 %d", mid.door-before.door)
	}

	// 协议层（第三笔）：有 "method" 键、整条解不开 → 走到了协议层、被判无效
	bad := post(h, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{broken`, "", "")
	after := bucketCounts(t, h)
	if after.proto != mid.proto+1 {
		t.Errorf("协议层判无效 1 条，第三笔却动了 %d", after.proto-mid.proto)
	}
	if after.transport != mid.transport {
		t.Errorf("协议层判的那条不该进第五笔（它走到了协议层），第五笔却动了 %d", after.transport-mid.transport)
	}
	if after.rejected != before.rejected {
		t.Errorf("这两条都不是内核拒的，内核拒收数却从 %d 动到 %d", before.rejected, after.rejected)
	}

	// 归因要说准层位（这一组今天红）：协议层判的那条不许说"没走到协议层"，
	// 门挡的那条要说"连协议层都没到"。
	if !strings.Contains(bad.Body.String(), "协议层判成无效") {
		t.Errorf("协议层判无效的回话没点出它是在协议层被判的：%.200s", bad.Body.String())
	}
	if strings.Contains(bad.Body.String(), "它没走到协议层") {
		t.Errorf("协议层判的请求被回话说成「没走到协议层」，与第五笔指反了：%.200s", bad.Body.String())
	}
	if !strings.Contains(door.Body.String(), "连协议层都没到") {
		t.Errorf("门挡下的回话没说清它连协议层都没到：%.200s", door.Body.String())
	}
}

// bucketCounts 从 kernel.tally 的回话里数出三笔数（单位各不同：发／写请求／请求）。
// 找不到短语就当场判失败 —— 不许回一个 0 让差值"看起来对得上"。
func bucketCounts(t *testing.T, h http.Handler) (out struct{ rejected, door, proto, transport int }) {
	t.Helper()
	rec := post(h, `{"jsonrpc":"2.0","id":9,"method":"tools/call",`+
		`"params":{"name":"kernel.tally","arguments":{}}}`, "", "")
	body := rec.Body.String()
	grab := func(mark string) int {
		i := strings.Index(body, " "+mark)
		if i < 0 {
			t.Fatalf("tally 里没有这一笔（%q）：%.260s", mark, body)
		}
		j := i
		for j > 0 && body[j-1] >= '0' && body[j-1] <= '9' {
			j--
		}
		if j == i {
			t.Fatalf("那一笔前面没有数字（%q）：%.200s", mark, body)
		}
		n := 0
		for _, c := range body[j:i] {
			n = n*10 + int(c-'0')
		}
		return n
	}
	out.rejected = grab("次 —— 拒收也要看得见")
	out.door = grab("次写请求根本没走到内核")
	out.proto = grab("条请求被协议层判成无效")
	out.transport = grab("条请求连协议层都没到")
	return out
}

// doorCount 从 kernel.tally 的回话里数出「门挡下」那一笔。短语不在时直接判失败，
// 不许回一个 0 让上面的差值"看起来对得上"。
func doorCount(t *testing.T, h http.Handler) int {
	t.Helper()
	const mark = " 条请求连协议层都没到"
	rec := post(h, `{"jsonrpc":"2.0","id":8,"method":"tools/call",`+
		`"params":{"name":"kernel.tally","arguments":{}}}`, "", "")
	body := rec.Body.String()
	i := strings.Index(body, mark)
	if i < 0 {
		t.Fatalf("tally 里没有「门挡下」这一笔（第四个桶今天没人数）：%.300s", body)
	}
	j := i
	for j > 0 && body[j-1] >= '0' && body[j-1] <= '9' {
		j--
	}
	if j == i {
		t.Fatalf("那一笔前面没有数字：%.200s", body)
	}
	n := 0
	for _, c := range body[j:i] {
		n = n*10 + int(c-'0')
	}
	return n
}
