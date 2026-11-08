package main

// A2 那道闸的测试：`AGENTKERNEL_REQUIRE_TOKEN=1` 把"回环免口令"这个信任假设关掉。
// 默认不设 ⇒ 现状一字不变；设了 ⇒ 不带口令的请求连内核的门都到不了。

import (
	"net/http"
	"strings"
	"testing"

	"agentkernel/mcpsrv"
)

const tallyRPC = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kernel.tally","arguments":{}}}`

func TestRequireTokenClosesTheLoopbackHole(t *testing.T) {
	h := &httpServer{srv: mcpsrv.New()}

	// 反臂（现状）：没给口令、也没要求 ⇒ 回环照旧放行。少了这一条，"永远 401" 也算绿。
	t.Setenv("AGENTKERNEL_TOKEN", "")
	t.Setenv("AGENTKERNEL_REQUIRE_TOKEN", "")
	if rec := post(h.handler(), tallyRPC, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("默认行为漂了（现状必须放行），得到 %d：%s", rec.Code, rec.Body.String())
	}

	// 证红：要求口令 + 没带 ⇒ 401；带对 ⇒ 200。
	t.Setenv("AGENTKERNEL_TOKEN", "sekret")
	t.Setenv("AGENTKERNEL_REQUIRE_TOKEN", "1")
	rec := post(h.handler(), tallyRPC, "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("REQUIRE_TOKEN=1 时空手请求竟然 %d —— 回环这一档还是没关上", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "条") {
		t.Errorf("401 的响应体里不许有账本内容：%s", rec.Body.String())
	}
	if rec2 := post(h.handler(), tallyRPC, "sekret", ""); rec2.Code != http.StatusOK {
		t.Errorf("带对口令应当放行，得到 %d", rec2.Code)
	}

	// 自相矛盾的部署：要求口令却没给口令 ⇒ 503，而且要能说清"这道闸等于没上"。
	t.Setenv("AGENTKERNEL_TOKEN", "")
	rec3 := post(h.handler(), tallyRPC, "", "")
	if rec3.Code != http.StatusServiceUnavailable {
		t.Errorf("要求口令但 TOKEN 是空的，竟然回 %d —— 宁可 503 也不要「看着有闸、其实没闸」", rec3.Code)
	}
	// 横幅与判定必须同源：tokenMode() 不许在 auth 已经拒了的场合说"无"。
	if got := tokenMode(); !strings.Contains(got, "503") {
		t.Errorf("横幅该把这档说破，得到：%s", got)
	}
	if got := post(h.handler(), tallyRPC, "", ""); got.Code != http.StatusServiceUnavailable {
		t.Errorf("横幅说的和 auth 做的不一致（两次结果不同）：%d", got.Code)
	}
}
