// mcphttp —— 内核的 **streamable HTTP** 那一面：让一个只会填 URL 的 MCP 界面
// （Qoder 的用户级 stdio/HTTP 配置、别的产品的 agent、另一台机器上的调用方）
// 不用起进程就能接上同一颗内核。
//
// 它与 `cmd/mcpkernel` 共用 `mcpsrv`：方法名、10 个工具、每一句回话的措辞**只有一份**。
// 这条不是洁癖 —— 两边各写一遍的话，改了一边另一边没改，调用方拿到哪一套取决于它怎么连上的，
// 而那个差别从读数上看不出来。今天钉住这件事的是源码只有一处产清单（`mcpsrv.tools()`）加上
// `mcpsrv.TestToolCountIsTheSameOwnerAsTheList`（清单长度与启动横幅那个数同一处）。
// **没钉的那一半说清**：把两面各跑一遍、把 tools/list 回来的字节逐字节比一次，这个端到端对照还没有 ——
// 读代码能看出"只有一份"，跑起来真拿到的是不是同一份字节，今天没人验。
//
// 安全上的三条，都是这台机器上已经观测过的形状：
//  1. **默认只绑 127.0.0.1**。这一面是有写入口的（`kernel.record_call`），
//     不像 `cmd/kaudit` 那样只读；非回环绑定必须给 `AGENTKERNEL_TOKEN`，否则**拒绝启动**。
//  2. **方法与鉴权各管各的**：非 POST 一律 405，与口令无关（与只读外壳同一条规矩）。
//  3. **一条请求的体有上限**（4 MiB）：`load_journal` 的 text 是整本账，
//     不设上限等于让局域网里任何人用一条请求把这台风量 ~400 MB 的机器打死。
//
// 口令只从环境变量读，绝不进仓库、绝不进账本；比对用定长时间，不用 `!=`。
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"agentkernel/mcpsrv"
)

// maxBody 是一条请求允许的字节数。
const maxBody = 4 << 20

// queueWait 是等那把"一次一条"的锁最多等多久。等不到就 503，而不是无限挂起：
// 一个卡住的请求把后面所有请求吊住，那读起来像"闸门锁死了"，而它其实是服务被占住。
const queueWait = 30 * time.Second

// 服务端超时。之前用的是 `http.ListenAndServe` 的默认值（全 0 = 不限），
// 一个只发了一半头就不动的连接能永久占着 goroutine —— 这是我今天自己写进去的洞。
//
// idleTimeout 曾经也是 120 秒，那是**同一个洞里改出来的另一个洞**：这一面的连接闲置时长
// 不是"网络慢"，是"两次对话之间的间隔"，分钟级到十几分钟级。120 秒时服务端把 keep-alive 关了，
// 而客户端不知道、也不重连 —— 挂载就此红掉，且进程还活着（10-16 实测：闲置 30 秒照常回话，
// 闲置 130 秒 `ConnectionAbortedError: [WinError 10053]`）。所以这里给到 30 分钟量级：
// 慢连接那一族由 readHeaderTimeout 守，不靠把自家客户端踢下线来守。
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 60 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 30 * time.Minute
)

// newHTTPServer 单独成一个函数，是为了"超时到底有没有设"能被测试直接判，
// 而不是只能靠起真服务计时（那种测试在慢机器上是运气测试）。
func newHTTPServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// requireToken 是"回环也要口令"这道决定的**唯一**去处：auth() 与横幅都从这里读，
// 免得横幅说"必须给"而 auth 其实没强制（一个决定两处各判一遍，就是这个包最恨的那一族）。
func requireToken() bool { return os.Getenv("AGENTKERNEL_REQUIRE_TOKEN") == "1" }

// tokenMode 把口令这道闸的现状说成人话（横幅用）。
func tokenMode() string {
	switch {
	case requireToken() && os.Getenv("AGENTKERNEL_TOKEN") == "":
		return "要求口令但没给（每个请求都会吃 503，这是自相矛盾的部署）"
	case requireToken():
		return "必须给（REQUIRE_TOKEN=1，回环也不例外）"
	case os.Getenv("AGENTKERNEL_TOKEN") != "":
		return "给了口令（非回环必须带；回环今天仍放行）"
	default:
		return "无（只绑回环 ⇒ 本机任意进程都能读写；要收紧就设 AGENTKERNEL_TOKEN，再加 AGENTKERNEL_REQUIRE_TOKEN=1）"
	}
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8797", "监听地址；非本机回环必须给 AGENTKERNEL_TOKEN")
	maxEntries := flag.Int("max-entries", 0, "这本账最多写多少条；0 = 不拦。10-18 攻击面实测「60 发合法记账涨到 2940 条、105ms」，"+
		"内存型 DoS 是现成的；上限的**数**归部署方定，所以这里只把「能不能设」做出来")
	flag.Parse()
	srv := mcpsrv.New()
	srv.SetEntryCap(*maxEntries)
	srv.SetAppendOnly(os.Getenv("AGENTKERNEL_APPEND_ONLY") == "1")
	hs := &httpServer{srv: srv}
	if err := hs.checkListen(*listen); err != nil {
		log.Fatalf("mcphttp: %v", err)
	}
	log.Printf("mcphttp（streamable HTTP）：http://%s/mcp —— %d 个工具，账本活在本进程记忆里（当前 %d 条），"+
		"并发刻意不做（排队上限 %s），条数上限 %s，只增=%s，口令=%s",
		*listen, mcpsrv.ToolCount(), hs.entries(), queueWait, mcpsrv.CapWords(*maxEntries),
		map[bool]string{true: "开（load_journal 不提供）", false: "关"}[srv.AppendOnly()],
		tokenMode())
	srvHTTP := newHTTPServer(hs.handler())
	srvHTTP.Addr = *listen
	if err := srvHTTP.ListenAndServe(); err != nil {
		log.Fatalf("mcphttp: %v", err)
	}
}

type httpServer struct {
	srv     *mcpsrv.Server
	session string
	// overloads 数的是"因排队超时被拒"的次数。被拒必须留得下数，
	// 否则它会读成"没发生过"——与内核那格 `被拒的写入 N 次` 同一个理由。
	overloads atomic.Int64
	// wait 是排队上限，零值=用 queueWait。留这个口子只为一件事：
	// 不留的话，测 503 的测试每次都得真等 30 秒，那种测试在慢机器上会被当成"卡死"而删掉。
	wait time.Duration
}

// checkListen：非回环 + 没口令 ⇒ 拒（理由要点名后果，不许只说"参数不对"）。
func (h *httpServer) checkListen(listen string) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("监听地址看不懂：%w", err)
	}
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return nil
	}
	if os.Getenv("AGENTKERNEL_TOKEN") == "" {
		return fmt.Errorf("要监听 %s（非本机回环）必须先给 AGENTKERNEL_TOKEN —— 这一面**有写入口**："+
			"没有口令等于任何人都能往链上记账", listen)
	}
	return nil
}

func (h *httpServer) entries() int { return h.srv.Entries() }

func (h *httpServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", h.serveMCP)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		h.json(w, http.StatusNotFound, map[string]any{
			"error": "这一面只有一个入口", "endpoints": []string{"POST /mcp（JSON-RPC）"}})
	})
	// 405 排在鉴权之前：写/非 POST 的拒绝与口令无关，
	// 否则无口令的人探到 401，读到的是"配上口令就能这么干"。
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			h.json(w, http.StatusMethodNotAllowed, map[string]any{
				"error": "这一面只接 POST /mcp（JSON-RPC）；不支持 SSE 流，所以 GET 不给会话",
			})
			return
		}
		if !h.auth(w, r) {
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// auth：给了口令才强制；定长时间比对；401 的响应体里不许有账本内容。
//
// `AGENTKERNEL_REQUIRE_TOKEN=1` 把"给了口令才强制"改成"**不管绑哪都必须给**" ——
// 这一族（回环免口令）在 10-18 攻击面实测里点名为 A2：本机任意进程都能落假账/读整本账，
// 而 `actor`、`session` 是自报字符串，账上没有第二根轴能反证是谁写的。
// 默认不设 = 现状一字不变；设了就等于告诉部署方"我把回环也当不可信"。
func (h *httpServer) auth(w http.ResponseWriter, r *http.Request) bool {
	want := os.Getenv("AGENTKERNEL_TOKEN")
	if want == "" && requireToken() {
		// 这是一个自相矛盾的部署：要求口令却没给口令。宁可拒启动时说话，也不要"看着有闸、其实没闸"。
		h.json(w, http.StatusServiceUnavailable, map[string]any{
			"error": "AGENTKERNEL_REQUIRE_TOKEN=1 但 AGENTKERNEL_TOKEN 是空的 —— 这道闸等于没上，拒绝这样服务"})
		return false
	}
	if want == "" {
		return true // 只绑回环时允许无口令；checkListen 已经拦掉别的绑法
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.Header.Get("X-Agentkernel-Token")
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		h.json(w, http.StatusUnauthorized, map[string]any{"error": "口令不对或没给 —— 链上一个字都不给"})
		return false
	}
	return true
}

func (h *httpServer) serveMCP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		h.json(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error": fmt.Sprintf("一条请求最多 %d 字节（load_journal 的整本账也算在内）—— 超了不是拒绝记账，是这条请求本身不合法", maxBody)})
		return
	}
	// 先认出它是不是一条像样的 JSON-RPC：解不开就明说，不许塌成"服务端没回"。
	if !mcpsrvLooksLikeRPC(body) {
		// 这一笔必须计数（#44）：门挡下的请求哪个桶都不进，"被拒也要看得见"就在这族上断了。
		n := h.srv.NoteTransportRefusal()
		h.json(w, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("体不是一条 JSON-RPC 请求（要有 method）—— 门挡下累计第 %d 次，"+
				"这一笔进的是 tally 里「连协议层都没到」那个数，与「被协议层判成无效」分开数", n), "got_bytes": len(body)})
		return
	}
	out, ok, kind := h.srv.TrySerializeKind(body, h.queueWait())
	if !ok {
		n := h.overloads.Add(1)
		h.json(w, http.StatusServiceUnavailable, map[string]any{
			"error": fmt.Sprintf("这一面一次只处理一条请求（Ledger 无锁），%s 内没排到队 —— "+
				"排队超时被拒累计第 %d 次；这不是拒收你的账，是服务当前被占住", queueWait, n),
			"retry_after_ms": int(queueWait / time.Millisecond)})
		return
	}
	if kind == mcpsrv.OutcomeBad {
		// 这条请求根本没进门（解不开）。规范对这件事写的是 MUST return an HTTP error status code，
		// 而**不是**把理由塞进一个 200 的正文 —— 200 在客户端眼里是"传输成功"，
		// 只读状态码的那一层就会把一次拒绝当成一次通过（与 check_call 那个 isError=False 同族）。
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(out)
		return
	}
	if out == nil {
		// 通知类：处理了（可能改了状态），但没有回话。202 而不是 200 空体 ——
		// 空 200 会被客户端读成「回了一条空结果」。
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if strings.Contains(string(body), `"method":"initialize"`) {
		// 给一个会话号只是让严格的客户端高兴；服务端不据此分账（一本账、所有请求共享，
		// 与 stdio 的「一个进程一本账」同一条语义）。
		h.session = randomID()
		w.Header().Set("Mcp-Session-Id", h.session)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

func mcpsrvLooksLikeRPC(body []byte) bool {
	s := string(body)
	return len(body) > 0 && body[0] == '{' && strings.Contains(s, `"method"`)
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "session-unavailable"
	}
	return hex.EncodeToString(b)
}

func (h *httpServer) json(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "响应编不出来", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

// queueWait 返回这一面的排队上限（零值走默认，见 httpServer.wait 那条注释）。
func (h *httpServer) queueWait() time.Duration {
	if h.wait > 0 {
		return h.wait
	}
	return queueWait
}
