// mcpsrv 的测试管的是「两面共用那一层」的判定：格位表、三态措辞、拒收理由、
// 以及「一条通知不该有回话」。这一层此前只被 Python 客户端整体跑过，Go 侧一条测试都没有 ——
// 协议层红了自己不知道，正是 #20 那一族。
package mcpsrv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentkernel"
)

func handle(t *testing.T, s *Server, body string) map[string]any {
	t.Helper()
	out := s.Handle([]byte(body))
	if out == nil {
		t.Fatalf("这条该有回话，实际 nil（那条判据根本没跑）：%.160s", body)
	}
	var rep map[string]any
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("回来的不是 JSON：%v（%.160s）", err, out)
	}
	return rep
}

func call(t *testing.T, s *Server, id int, method, params string) map[string]any {
	if strings.TrimSpace(params) == "" {
		params = "{}"
	}
	return handle(t, s, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params))
}

func toolText(t *testing.T, s *Server, id int, name, args string) (string, bool) {
	t.Helper()
	if strings.TrimSpace(args) == "" {
		args = "{}"
	}
	rep := call(t, s, id, "tools/call", fmt.Sprintf(`{"name":%q,"arguments":%s}`, name, args))
	res, ok := rep["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s 没有 result：%v", name, rep)
	}
	c, ok := res["content"].([]any)
	if !ok || len(c) == 0 {
		t.Fatalf("%s 回话里没有 content 段：%v", name, res)
	}
	first, _ := c[0].(map[string]any)
	txt, _ := first["text"].(string)
	isErr, _ := res["isError"].(bool)
	return txt, isErr
}

// TestInitializeNamesTheServer：握手回的那个名字就是挂载界面里显示的 serverName。
// 它不许漂成 agentkernel-v2 或 mcpkernel —— 那会让人以为接上了两颗内核。
func TestInitializeNamesTheServer(t *testing.T) {
	s := New()
	rep := call(t, s, 1, "initialize", `{"protocolVersion":"2024-11-05"}`)
	info := rep["result"].(map[string]any)["serverInfo"].(map[string]any)
	if info["name"] != "agentkernel" {
		t.Errorf("serverInfo.name 该是 agentkernel，实际 %v", info["name"])
	}
	if pv := rep["result"].(map[string]any)["protocolVersion"]; pv != ProtocolVersion {
		t.Errorf("protocolVersion 该是 %s，实际 %v", ProtocolVersion, pv)
	}
}

// TestCelltableAgreesWithKernelAndFreeze：格位表这一面的三个分母要同时满足两件事 ——
// ①等于内核**当场数出来的**，②等于封板冻住的那一组数。
// 只比①：表被改了也"自洽"；只比②：内核改了这里红，但没人说得清对的是哪一边。
func TestCelltableAgreesWithKernelAndFreeze(t *testing.T) {
	s := New()
	txt, isErr := toolText(t, s, 2, "kernel.celltable", "")
	if isErr {
		t.Fatalf("celltable 不该被拒：%s", txt)
	}
	in, out := kernel.InputMeta{}.Cells(), kernel.OutputMeta{}.Cells()
	seen := map[string]bool{}
	for _, c := range in {
		seen[c.Key] = true
	}
	for _, c := range out {
		seen[c.Key] = true
	}
	if len(in) != 20 || len(out) != 29 || len(seen) != 48 {
		t.Errorf("内核当场数出来的与封板值不一致：in=%d out=%d keys=%d（该 20/29/48）", len(in), len(out), len(seen))
	}
	want := fmt.Sprintf("当前 %d 格（输入侧 %d + 输出侧 %d）；唯一键名 %d 个", len(in)+len(out), len(in), len(out), len(seen))
	if !strings.HasPrefix(firstLine(txt), want) {
		t.Errorf("格位表第一行该是 %q，实际 %q", want, firstLine(txt))
	}
	if want != "当前 49 格（输入侧 20 + 输出侧 29）；唯一键名 48 个" {
		t.Errorf("封板值被改了：现在算出来是 %q（要动这张表走「先立实例再立判据」，不许顺手）", want)
	}
	if n := strings.Count(txt, "←"); n != len(in)+len(out) {
		t.Errorf("名单列了 %d 行，分母是 %d —— 名单与分母不许各自数各自的", n, len(in)+len(out))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestUnknownToolNamesIt：不存在的工具要点名"没有这个工具：<那个名字>"，
// 不许塌成一句通用错误 —— 那与「被某一条判据拒了」是两类事。
func TestUnknownToolNamesIt(t *testing.T) {
	s := New()
	txt, isErr := toolText(t, s, 3, "kernel.nonexistent_tool", "")
	if !isErr {
		t.Errorf("不存在的工具却回了非错：%s", txt)
	}
	if !strings.Contains(txt, "kernel.nonexistent_tool") {
		t.Errorf("没点名是哪个工具不存在：%s", txt)
	}
}

// TestUnknownMethodNamesIt：方法级同理，回 JSON-RPC error 而不是 result。
func TestUnknownMethodNamesIt(t *testing.T) {
	s := New()
	rep := handle(t, s, `{"jsonrpc":"2.0","id":4,"method":"kernel.not_a_method"}`)
	errv, ok := rep["error"]
	if !ok {
		t.Fatalf("未知方法该回 error，实际 %v", rep)
	}
	msg := errv.(map[string]any)["message"].(string)
	if !strings.Contains(msg, "kernel.not_a_method") {
		t.Errorf("报错没点名那个方法：%s", msg)
	}
}

// TestNotificationHasNoReply：通知（没有 id）处理但不回话。
// 回一条空 result 会被客户端读成"回了一条空结果"，那是另一种假绿。
func TestNotificationHasNoReply(t *testing.T) {
	s := New()
	if out := s.Handle([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); out != nil {
		t.Errorf("通知该没回话，实际 %.120s", out)
	}
}

// TestRefusalNamesWhoRefusedIt：空参数被拒时必须能读出**是哪一条**拒的。
// 「被拒了」本身不是断言 —— 这一条是 #20 那一族的直接对策。
func TestRefusalNamesWhoRefusedIt(t *testing.T) {
	s := New()
	txt, isErr := toolText(t, s, 5, "kernel.record_call", "")
	if !isErr {
		t.Fatalf("空一发的 record_call 该被拒，实际：%s", txt)
	}
	if !strings.Contains(txt, "整发拒收") || !strings.Contains(txt, "拒收累计 1 次") {
		t.Errorf("拒收的累计数没进回话：%s", txt)
	}
	if !strings.Contains(txt, "发号从 1 起") {
		t.Errorf("没点名是哪条判据拒的（该说发号 0 会被读成「还没发」）：%s", txt)
	}
	if n := s.Entries(); n != 0 {
		t.Errorf("整发拒收之后链上竟然有 %d 条 —— 拒收必须两侧都不写", n)
	}
}

// TestReadBackRefusesEmptySession：读侧少一个轴不能"照答一条空的"，必须当场拒。
// 空的 session 会把两本不同 session 的同号同侧并成一次回答 —— 那比报错更糟，
// 因为它长得像一次正常回答。
func TestReadBackRefusesEmptySession(t *testing.T) {
	s := New()
	txt, isErr := toolText(t, s, 11, "kernel.read_back", `{"session":"","call":1,"side":"output"}`)
	if !isErr {
		t.Errorf("空 session 的读请求没被拒，实际回的（看着像一次正常回答）：%s", txt)
	}
	if !strings.Contains(txt, "session") || !strings.Contains(txt, "三个轴") {
		t.Errorf("拒绝没点名缺的是哪个轴：%s", txt)
	}
	// 反面对照：轴给全时**不该**被这条拦住（否则判据写成"永远拒"也照样绿）。
	if _, isErr := toolText(t, s, 12, "kernel.read_back", `{"session":"none","call":1,"side":"output"}`); isErr {
		t.Errorf("三个轴给全的读请求被误拒了")
	}
}

// TestToolCountIsTheSameOwnerAsTheList：启动横幅上那个「N 个工具」必须由同一份清单数出来。
func TestToolCountIsTheSameOwnerAsTheList(t *testing.T) {
	s := New()
	rep := call(t, s, 6, "tools/list", "")
	list := rep["result"].(map[string]any)["tools"].([]any)
	if len(list) != ToolCount() {
		t.Errorf("tools/list 给了 %d 个，ToolCount 说 %d 个", len(list), ToolCount())
	}
	if len(list) != 10 {
		t.Errorf("工具数该是 10（封板时那一份），实际 %d", len(list))
	}
	for _, v := range list {
		n := v.(map[string]any)["name"].(string)
		if !strings.HasPrefix(n, "kernel.") {
			t.Errorf("工具名没有 kernel. 前缀：%s", n)
		}
	}
}

// TestVerifyOnEmptyChainRefusesGreen：空链经这一面回话时，**不许戴着 ✓**。
// 这条测试原来叫 `...IsGreenLiteral`，专门"记下今天它回 ✓"这个事实（10-15 从工具面撞到的：
// `✓ 链自洽（共 0 条，其中 0 条比对过原像）`）。用户表态后判据改了，于是它翻过头来钉新的那一面：
// 没有 ✓、点名「尚无账可核」，而**真有账时 ✓ 必须回来** —— 两个方向都要判，
// 否则把渲染写成"永远不打勾"也照样绿。原委与代价见 报告/已落地-空链不许打勾.txt。
func TestVerifyOnEmptyChainRefusesGreen(t *testing.T) {
	s := New()
	txt, isErr := toolText(t, s, 7, "kernel.verify", "")
	if isErr {
		t.Fatalf("空链不是错，不该回 isError：%s", txt)
	}
	if strings.Contains(txt, "✓") {
		t.Errorf("空链还戴着 ✓：%s", txt)
	}
	if !strings.Contains(txt, "尚无账可核") {
		t.Errorf("空链没点名「尚无账可核」：%s", txt)
	}
	// 反面对照：load 一本真账之后，✓ 与条数都要回来。
	raw, err := os.ReadFile(filepath.Join("..", "报告", "样例-agent.jsonl"))
	if err != nil {
		t.Fatalf("读不到样例账本：%v（不许跳过）", err)
	}
	esc, err := json.Marshal(string(raw))
	if err != nil {
		t.Fatalf("装不进 JSON：%v", err)
	}
	if _, isErr := toolText(t, s, 13, "kernel.load_journal", `{"text":`+string(esc)+`}`); isErr {
		t.Fatalf("空账里载入不该被拦")
	}
	after, isErr := toolText(t, s, 14, "kernel.verify", "")
	if isErr || !strings.Contains(after, "✓ 链自洽") || strings.Contains(after, "共 0 条") {
		t.Errorf("有了账之后 ✓ 那一面没回来：%s", after)
	}
}

// TestLoadJournalNeedsAcknowledgeToReplace：第二本账顶掉第一本之前必须点名 acknowledge=replace。
// 一本账最坏的结局不是被拒，是被**静悄悄换掉**。所以这支判定要连着判三件事：
// ①非空时不带 acknowledge ⇒ 拒，且理由要说清"会换掉多少条"；②带了 ⇒ 换得成；
// ③空账时不带 ⇒ 不该拦（那时候没有东西会被丢）。
// 只判①的话，把判据写成"永远拒"也照样绿。
func TestLoadJournalNeedsAcknowledgeToReplace(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "报告", "样例-agent.jsonl"))
	if err != nil {
		t.Fatalf("读不到样例账本：%v —— 这条不许因为文件不在就跳过，那等于没测", err)
	}
	esc, err := json.Marshal(string(raw))
	if err != nil {
		t.Fatalf("账本没法塞进 JSON：%v", err)
	}
	s := New()
	first, isErr := toolText(t, s, 8, "kernel.load_journal", `{"text":`+string(esc)+`}`)
	if isErr {
		t.Errorf("空账里载入不该被拦（没有东西会被丢）：%s", first)
	}
	if n := s.Entries(); n == 0 {
		t.Fatalf("载入后链上还是 0 条，后面的判定就没得判：%s", first)
	}
	second, isErr := toolText(t, s, 9, "kernel.load_journal", `{"text":`+string(esc)+`}`)
	if !isErr {
		t.Errorf("拿一本非空的账去顶掉它，不带 acknowledge 竟然被接受了：%s", second)
	}
	if !strings.Contains(second, "acknowledge=replace") {
		t.Errorf("拒绝没说要带什么才放行：%s", second)
	}
	if !strings.Contains(second, fmt.Sprintf("%d 条", s.Entries())) {
		t.Errorf("拒绝没点名会被换掉多少条：%s", second)
	}
	third, isErr := toolText(t, s, 10, "kernel.load_journal",
		`{"text":`+string(esc)+`,"acknowledge":"replace"}`)
	if isErr {
		t.Errorf("点名要替换之后该放行：%s", third)
	}
}

// escStr 把任意字符串装成 JSON 字面量 —— encode 交回来的文本里有换行、引号、中文，
// 手拼会把它拼坏，而那看起来像是"判据红了"。
func escStr(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("装不进 JSON：%v", err)
	}
	return string(b)
}

// TestEncodeJournalTextLoadsBackVerbatim —— encode_journal 交回来的那份文本，**原样**喂回
// kernel.load_journal 必须读回同一本账。
//
// 为什么这根必须存在：encode 回话的第一行是给人看的（「N 字节 JSONL，M 行（写盘是你的事…）」），
// 而 load 的 text 参数说明写的就是「encode_journal 返回的 JSONL 全文」。两头不认同一个形状的话，
// 调用方照着说明做就会被自己刚拿到的表头打死 —— ReadJournal 把第一行计成 BadLine、整本拒。
// 此前这条往返一支测试都没走过：现有测试喂的是磁盘上那份**不带表头**的样例账。
func TestEncodeJournalTextLoadsBackVerbatim(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "报告", "样例-agent.jsonl"))
	if err != nil {
		t.Fatalf("读不到样例账本：%v（不许跳过）", err)
	}
	src := New()
	if _, isErr := toolText(t, src, 1, "kernel.load_journal",
		`{"text":`+escStr(t, string(raw))+`}`); isErr {
		t.Fatalf("样例账进不去，后面没得判")
	}
	want := src.Entries()
	if want == 0 {
		t.Fatalf("样例账是空的，这条判据就没得判")
	}
	encoded, isErr := toolText(t, src, 2, "kernel.encode_journal", `{"payload":"omitted"}`)
	if isErr {
		t.Fatalf("编码被拒：%s", encoded)
	}
	if i := strings.IndexByte(encoded, '\n'); i < 0 || strings.HasPrefix(strings.TrimSpace(encoded[:i]), "{") {
		t.Fatalf("预期 encode 的第一行是人读表头，实际直接是 JSON：%q", encoded[:40])
	}

	// 正面：原样回灌。
	dst := New()
	back, isErr := toolText(t, dst, 3, "kernel.load_journal", `{"text":`+escStr(t, encoded)+`}`)
	if isErr {
		t.Errorf("encode 交回的文本原样喂回 load 被拒了（照着自家说明做反而不通）：%s", back)
	} else if n := dst.Entries(); n != want {
		t.Errorf("往返之后条数变了：进去 %d 条，回来 %d 条", want, n)
	} else if !strings.Contains(back, "表头") {
		t.Errorf("认出了自家表头却不说 —— 调用方会以为是纯 JSON 在往返：%s", back)
	}

	// 反面对照一：不带表头的正文也要能进，而且**不许少一条**
	// （这条守着"剥表头"退化成"永远丢第一行"那种写法）。
	i := strings.IndexByte(encoded, '\n')
	body := New()
	bare, isErr := toolText(t, body, 4, "kernel.load_journal", `{"text":`+escStr(t, encoded[i+1:])+`}`)
	if isErr {
		t.Errorf("不带表头的正文进不去：%s", bare)
	} else if n := body.Entries(); n != want {
		t.Errorf("不带表头时条数应为 %d，实际 %d（剥过头了？）", want, n)
	} else if strings.Contains(bare, "表头") {
		t.Errorf("正文第一行是账，不该被当成表头剥掉：%s", bare)
	}

	// 反面对照二：第一行是**别的**非 JSON 文本时，仍然要整本拒 ——
	// 否则"认表头"会变成"任何坏的第一行都放过"。
	// 反面固定串从内核取（kernel.EncodeHeaderMark）：测试里再抄一遍字面量就成了第二个 owner，
	// 而串一改，这一支会静默变成"拿一份合法表头去期望它被拒"的假红。
	garbage := strings.Replace(encoded, kernel.EncodeHeaderMark, "这不是账本 ，", 1)
	g := New()
	txt, isErr := toolText(t, g, 5, "kernel.load_journal", `{"text":`+escStr(t, garbage)+`}`)
	if !isErr {
		t.Errorf("第一行被换成别的废话居然还收下（认表头写宽了）：%s", txt)
	} else if !strings.Contains(txt, "解不开") {
		t.Errorf("拒了但没说是哪行解不开，调用方无从下手：%s", txt)
	}
}

// legalCall 用**真结构体**装出一份合法的一发（预算走 DeriveBudget，不在测试里抄公式），
// 然后按 dropSide/dropKey **删掉一个键** —— 删键才是这一族要钉的东西：
// 「这个键根本没出现」和「出现但填了零值」是两件事，而 json.Unmarshal 会把前者补成后者。
func legalCall(t *testing.T, dropSide, dropKey string) string {
	t.Helper()
	bud, err := kernel.DeriveBudget(32768, 12, map[kernel.PartKind]int{"system": 55, "user": 45},
		kernel.EstimatedOf(300, "骨架+模型名粗估"))
	if err != nil {
		t.Fatalf("DeriveBudget 没过，测试自己先坏了：%v", err)
	}
	in := kernel.InputMeta{
		Call: 1, Order: []kernel.PartKind{"system", "user"},
		Parts:        []kernel.PartCount{{Kind: "system", Parts: 1, Bytes: 11}, {Kind: "user", Parts: 1, Bytes: 9}},
		ContentBytes: 20, Tokens: kernel.EstimatedOf(7, "1 token ≈ 3 字节的粗估"),
		Budget: bud,
		Notice: kernel.Notice{Sent: false, Trimmed: false, Why: "没折叠没裁尾"},
	}
	out := kernel.OutputMeta{
		Call: 1, Finish: kernel.Finish{Reason: "stop", Said: true},
		Model: "some-model", ModelSaid: true, WireFormat: "chat.completion", WireFormatSaid: true,
		Says:         kernel.Says{Bytes: 2, Kept: 2, SHA256: sha42},
		UsageIn:      kernel.RelayedOf(7, "endpoint-usage"),
		UsageOut:     kernel.RelayedOf(1, "endpoint-usage"),
		WireBytesOut: kernel.MeasuredOf(96), WireBytesIn: kernel.MeasuredOf(118),
		Chunks: kernel.AbsentBecause("非流式"), FirstByteMs: kernel.AbsentBecause("非流式"),
		MaxGapMs: kernel.AbsentBecause("非流式"),
		Failure:  kernel.Failure{Status: kernel.AbsentBecause("没故障"), RetryAfterMs: kernel.AbsentBecause("没故障")},
	}
	// 落账要用**没记过的发号**：去重门一挡，就分不清是存在性检查拒的还是门拒的。
	in.Call, out.Call = 41, 41
	if dropSide != "" {
		// 走一遍 map，把那一侧里的那个键删掉，模拟"整块没给"
		wrapper := map[string]any{"session": "presence-test", "actor": "probe",
			"input": in, "output": out}
		for side := range wrapper {
			if side != dropSide {
				continue
			}
			raw, err := json.Marshal(wrapper[side])
			if err != nil {
				t.Fatalf("装不出 JSON：%v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("解不开：%v", err)
			}
			delete(m, dropKey)
			wrapper[side] = m
		}
		b, err := json.Marshal(wrapper)
		if err != nil {
			t.Fatalf("装不回 JSON：%v", err)
		}
		return string(b)
	}
	b, err := json.Marshal(map[string]any{"session": "presence-test", "actor": "probe",
		"input": in, "output": out})
	if err != nil {
		t.Fatalf("装不出 JSON：%v", err)
	}
	return string(b)
}

const sha42 = "73475cb40a568e8da8a045ced110137e159f890ac4da883b6b17dc651b3a8049"

// TestShellDoesNotInventMissingBlocks —— 外壳不许替调用方把「整块没给」造成一份零值的账。
//
// 10-16 现场（报告/已查明-外壳把没填造成量到了-1016.txt 第三节）：input 里**没有 Notice 这个键**，
// check_call 回「输入侧：自洽」、record_call 直接收下，读回来是
//
//	notice_sent    measured  否
//	notice_why     measured  （这一格是空的）
//
// 而对照那一发（老老实实给了 Notice）唯一的差别就是半句理由。也就是
// **「没人告诉我们」和「我们量到它没发」在这本账上同形，而且都盖 measured**。
// 内核自己最恨这一族（缺席不许塌成 0、忘了填 ≠ 缺席，Absent 那一档存在的唯一理由），
// 而它被抹平的位置在门口 —— 判据表一个字段都不用动。
func TestShellDoesNotInventMissingBlocks(t *testing.T) {
	// 基线：完整一份必须两面都过（否则下面的判据可以写成"永远拒"照样绿）。
	s := New()
	if txt, isErr := toolText(t, s, 1, "kernel.check_call", legalCall(t, "", "")); isErr {
		t.Fatalf("完整的一发被预检拒了，后面没得判：%s", txt)
	}
	if txt, isErr := toolText(t, s, 2, "kernel.record_call", legalCall(t, "", "")); isErr {
		t.Fatalf("完整的一发落不进账：%s", txt)
	}

	for _, tc := range []struct {
		side, key, wants string
	}{
		{"input", "Notice", "Notice"},
		{"output", "Failure", "Failure"},
		{"output", "Finish", "Finish"},
	} {
		for _, tool := range []string{"kernel.check_call", "kernel.record_call"} {
			s2 := New()
			txt, isErr := toolText(t, s2, 3, tool, legalCall(t, tc.side, tc.key))
			if !isErr {
				t.Errorf("%s 把「%s 里整块没有 %s」当合法收了：%s", tool, tc.side, tc.key, txt)
				continue
			}
			if !strings.Contains(txt, tc.key) {
				t.Errorf("%s 拒了但没点名是哪一块：%s", tool, txt)
			}
			// 判"它把这当成过了"要认那句**形状**，不许认"自洽"这个词 ——
			// 真拒收的回话里就印着「输出侧账不自洽」，按词去查会把一次正确的拒绝读成一次通过
			// （同一个毛病我在别处犯过一次：类别词永远出现在汇总里）。
			if strings.Contains(txt, "侧：自洽") {
				t.Errorf("%s 一边拒一边说自洽，读起来像过了：%s", tool, txt)
			}
		}
	}
}

// cutsWireArgs 从 legalCall 那份**合法**夹具出发，只动两处：input 里的 `Cuts`、顶层 session。
// 只动这两处是判据成立的前提 —— 同时动第三处，红了就说不清是谁的功劳。
// drop=true 是「这个键压根没出现」，drop=false 是「出现、但是 `[]`」：#66 修的就是这两件事的区分。
func cutsWireArgs(t *testing.T, drop bool, session string) string {
	t.Helper()
	var w map[string]any
	if err := json.Unmarshal([]byte(legalCall(t, "", "")), &w); err != nil {
		t.Fatalf("legalCall 那份夹具解不开，下面每条臂都在测空气：%v", err)
	}
	in, ok := w["input"].(map[string]any)
	if !ok {
		t.Fatalf("夹具里没有 input 这一侧：%.200v", w)
	}
	if _, ok := in["Cuts"]; !ok {
		t.Fatalf("夹具本身就没印出 Cuts 这个键 —— 「把它删掉」这一臂会恒绿（删一个不存在的东西≠没交）")
	}
	if drop {
		delete(in, "Cuts")
	} else {
		in["Cuts"] = []any{}
	}
	w["session"] = session
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("装不回 JSON：%v", err)
	}
	return string(b)
}

func readBackLine(t *testing.T, txt, key string) string {
	t.Helper()
	for _, ln := range strings.Split(txt, "\n") {
		if f := strings.Fields(ln); len(f) >= 2 && f[0] == key {
			return ln
		}
	}
	t.Fatalf("%s 这一格没出现在读回来的回话里：%.400s", key, txt)
	return ""
}

// TestCutsAbsentVersusEmptyAreTwoSentencesOnTheWire —— #66 修完之后，Agent 读的那一面必须还分得开。
//
// 判据表改了、`Cells()` 交出 absent 了，都不等于调用方看得见：外壳给 Agent 的只有两句人话 ——
// 落账那一句和 `kernel.read_back` 那几行。10-17 挂载实测的原始读数就是这两发**逐字相同**
// （`报告/决策件-第11条-65Basis值域与66Cuts不可分辨-1017.txt`：check_call 各 55 字，
// read_back 的 folded/clipped/dropped 三格都是「measured 0 段（裁剪表是空的）」，缺席栏都是 `[]`）。
// 所以这一根针量的是**链上那两面**，不是结构体：改回 `cells.go` 的 cutCell 或 `input.go` 的 CutList
// 任何一处，它都会红（m85、m86 两根 kprobe 点名它）。
//
// 两头都要测。只测「没交 ⇒ 缺席」这一头，把「修过头」放过去就看不见了 ——
// 空表也被念成缺席，等于把同一族缺陷从 measured 那一侧搬到 absent 那一侧，账上照样分不清。
func TestCutsAbsentVersusEmptyAreTwoSentencesOnTheWire(t *testing.T) {
	s := New()
	// 三档档名从内核那一份定义里取，不在这儿抄常量（规矩三：一个定义一个 owner）。
	// CutLists 的声明顺序是 folded、clipped、dropped，而 read_back 按键名排序，
	// 下面按"逐名查成员"来断言，不依赖任何顺序。
	kinds := make([]string, 0, len(kernel.CutLists))
	for _, l := range kernel.CutLists {
		kinds = append(kinds, l.Key)
	}

	for _, tc := range []struct {
		name    string
		drop    bool
		session string
	}{
		{"没交这张表", true, "wire-cuts-absent"},
		{"交了一张空表", false, "wire-cuts-empty"},
	} {
		args := cutsWireArgs(t, tc.drop, tc.session)
		// 反臂挡在这里：修法不许变成「没交就拒」。#66 选的是口径 ②（缺席如实落账），不是加一条判据。
		if txt, isErr := toolText(t, s, 1, "kernel.check_call", args); isErr {
			t.Errorf("%s：%s 把合法的一发预检拒了 —— 「没交名单」不是违规：%s", tc.name, "kernel.check_call", txt)
		}
		if txt, isErr := toolText(t, s, 2, "kernel.record_call", args); isErr {
			t.Errorf("%s：%s 落不进账：%s", tc.name, "kernel.record_call", txt)
			continue
		}
		txt, isErr := toolText(t, s, 3, "kernel.read_back",
			fmt.Sprintf(`{"session":%q,"call":41,"side":"input"}`, tc.session))
		if isErr {
			t.Errorf("%s：读回自己被拒：%s", tc.name, txt)
			continue
		}
		for _, k := range kinds {
			line := readBackLine(t, txt, k)
			if tc.drop {
				if !strings.Contains(line, kernel.BasisAbsent.String()) {
					t.Errorf("%s：格 %s 没署成缺席，读的人仍分不清它和「量到 0 段」：%s", tc.name, k, line)
				}
				// 「没交」那句话认半句就够（认整句等于在这里抄一份字面量，改一处另一处不跟着红）。
				if !strings.Contains(line, "没交") {
					t.Errorf("%s：格 %s 署了缺席却没说是「没交」：%s", tc.name, k, line)
				}
				if strings.Contains(line, "裁剪表是空的") {
					t.Errorf("%s：格 %s 把没交念成了空表 —— 那正是 #66 的本体：%s", tc.name, k, line)
				}
				continue
			}
			if !strings.Contains(line, kernel.BasisMeasured.String()) {
				t.Errorf("%s：格 %s 被改署了 —— 交了一张空表是量过的「没有」，不该读成缺席：%s", tc.name, k, line)
			}
			if !strings.Contains(line, "裁剪表是空的") {
				t.Errorf("%s：格 %s 是空表却没念出那句实测过的「没有」：%s", tc.name, k, line)
			}
			if strings.Contains(line, "没交") {
				t.Errorf("%s：格 %s 明明交过（`[]`）却说是没交，修过头了：%s", tc.name, k, line)
			}
		}
		// 缺席栏是 10-17 那两条读数的第二个轴 —— 当时两发都印 `[]`，所以光看这一栏也分不清。
		// 它现在必须跟着出处一起分家：没交 ⇒ 点名三档，交空表 ⇒ 一档都不点。
		head := strings.SplitN(txt, "\n", 2)[0]
		i := strings.Index(head, "其中缺席 [")
		if i < 0 {
			t.Fatalf("%s：第一行没有缺席栏那句话，判据读不到：%s", tc.name, head)
		}
		list := head[i+len("其中缺席 "):]
		j := strings.Index(list, "]")
		if j < 0 {
			t.Fatalf("%s：缺席栏那句话没有收尾的括号：%s", tc.name, list)
		}
		fields := strings.Fields(strings.Trim(list[:j+1], "[]"))
		for _, k := range kinds {
			hit := false
			for _, f := range fields {
				if f == k {
					hit = true
				}
			}
			if hit != tc.drop {
				t.Errorf("%s：这一档在缺席栏里点成了 %v，与它自己那一行的出处对不上（整栏：%v）",
					tc.name, k, fields)
			}
		}
	}
}

// TestCheckCallRefusalIsMachineReadable —— #20：预检在正文里说「拒」时，机器读得到的那一面也必须说拒。
//
// 10-17 说谎者靶场 A0 实测（报告/说谎者靶场-A0-1017.txt）：四条元数据谎在 check_call 上
// 全是 `isError=false`，而同一份数在 record_call 上是 `isError=true` —— 一个只读标志位的调用方
// 拿到的是四次「通过」，而拒它的理由其实就写在同一句回话里。
// 根因是一处写死的位：`case "kernel.check_call": return text(..., checkBoth(a), false)`。
// 修法不许是「去正文里嗅『kernel:』这个前缀」（那是在解析一句给人看的话），
// 而是拿当前这本账的**副本**真跑一遍 `RecordCall`，让预检与落账读同一份判据（不变量：一份判据一个 owner）。
func TestCheckCallRefusalIsMachineReadable(t *testing.T) {
	legal := legalCall(t, "", "")
	if txt, isErr := toolText(t, New(), 1, "kernel.check_call", legal); isErr {
		t.Fatalf("合法那份被预检拒了，后面每条臂都会恒红（对照不成立）：%s", txt)
	}
	for _, tc := range []struct {
		name   string
		wants  string
		mutate func(map[string]any)
	}{
		{"声称通知发了却没正文", "通知", func(a map[string]any) {
			a["input"].(map[string]any)["Notice"].(map[string]any)["Sent"] = true
		}},
		{"把转述的 token 标成实测", "转述", func(a map[string]any) {
			a["output"].(map[string]any)["UsageIn"].(map[string]any)["Basis"] = 1
		}},
		{"非流式把分帧写成量到 0", "分帧", func(a map[string]any) {
			c := a["output"].(map[string]any)["Chunks"].(map[string]any)
			c["Absent"], c["Basis"] = false, 1
		}},
		{"手搭一份对不上各段相加的额度", "两个数各算了一遍", func(a map[string]any) {
			a["input"].(map[string]any)["ContentBytes"] = 999999
		}},
	} {
		var wrapper map[string]any
		if err := json.Unmarshal([]byte(legal), &wrapper); err != nil {
			t.Fatalf("%s：底样解不开：%v", tc.name, err)
		}
		tc.mutate(wrapper)
		raw, err := json.Marshal(wrapper)
		if err != nil {
			t.Fatalf("%s：装不出 JSON：%v", tc.name, err)
		}
		s := New()
		txt, isErr := toolText(t, s, 2, "kernel.check_call", string(raw))
		if !isErr {
			t.Errorf("%s：check_call 正文说了拒而 isError=false，只读标志位的调用方拿到的是「通过」：%s", tc.name, txt)
		}
		rec, isErr2 := toolText(t, s, 3, "kernel.record_call", string(raw))
		if !isErr2 {
			t.Errorf("%s：record_call 把这份谎收下了（预检与落账不是同一份判据）：%s", tc.name, rec)
		}
		if isErr != isErr2 {
			t.Errorf("%s：同一份数两面结论不同 check=%v record=%v", tc.name, isErr, isErr2)
		}
		if !strings.Contains(txt, tc.wants) {
			t.Errorf("%s：预检拒了但没点名是哪一格：%s", tc.name, txt)
		}
	}
}

// TestPreCheckPredictsDedupGate —— 预检必须能预见去重门：同一发记过之后再预检，要说「会被拒」。
//
// 这一条不是顺手加的：把 check_call 从「只跑两侧的 Check()」改成「拿账本副本真跑一遍 RecordCall」之后，
// 预检第一次看得见「这一发已经记过」这件事。改之前它对第二次落账回的是「两侧都自洽」，
// 而 record_call 同一份数回的是拒 —— 那正是 #20 的第五个形状，只是它的成因是去重而不是格子的值。
func TestPreCheckPredictsDedupGate(t *testing.T) {
	s := New()
	legal := legalCall(t, "", "")
	if _, isErr := toolText(t, s, 1, "kernel.check_call", legal); isErr {
		t.Fatalf("第一次预检就该过，否则这一臂在测别的东西")
	}
	if _, isErr := toolText(t, s, 2, "kernel.record_call", legal); isErr {
		t.Fatalf("落账被拒了，去重门没得判")
	}
	txt, isErr := toolText(t, s, 3, "kernel.check_call", legal)
	if !isErr {
		t.Errorf("同一发已记过，预检却回「可以落账」（isError=false）—— 预检答不出 record_call 会拒：%s", txt)
	}
	if !strings.Contains(txt, "记过") {
		t.Errorf("预检拒了但没点名是去重门：%s", txt)
	}
}

// TestUnknownWriteKeysAreRefusedAndNamed —— #46：写侧没打中的键不许静默变成一个 measured 0。
//
// 10-17 冷接上手量到的形状（`报告/冷接上手-1017.txt` 第五节是我自己复现的那一发）：把 `ContentBytes`
// 换成**读侧**那个键名 `content_bytes: 20`，闸门回 `整发落账 49 条`、`被拒的写入 0 次`，
// 而账上那一格是 `content_bytes measured 0` —— 调用方明明写了 20 个字节，值静默消失，
// 留下的是一格谁都没量的 0，署着「实测」。Go 的解码忽略大小写但**不忽略下划线**，
// 所以单词键"碰巧命中"、多词键静默落空；更深一层的 `Says.Clipped`（那是个方法，不是字段）同样被吞掉。
// 这一族不是判据表的事（一格没加），是解码这一步**没有 owner**：`args[T]` 今天连错误都丢掉（`_ =`）。
func TestUnknownWriteKeysAreRefusedAndNamed(t *testing.T) {
	// 基线：一份全都按字段名写的必须两面都过 —— 否则这条臂恒红。
	legal := legalCall(t, "", "")
	if txt, isErr := toolText(t, New(), 1, "kernel.check_call", legal); isErr {
		t.Fatalf("合法的预检被拒了，对照不成立：%s", txt)
	}
	if txt, isErr := toolText(t, New(), 2, "kernel.record_call", legal); isErr {
		t.Fatalf("合法的落不进账，对照不成立：%s", txt)
	}

	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		wants  []string
	}{
		{"读侧键名当写侧用（content_bytes）", func(a map[string]any) {
			in := a["input"].(map[string]any)
			v := in["ContentBytes"]
			delete(in, "ContentBytes")
			in["content_bytes"] = v
			in["Parts"] = []any{map[string]any{"Kind": "system", "Parts": 1, "Bytes": 0},
				map[string]any{"Kind": "user", "Parts": 1, "Bytes": 0}} // 让"相加=0"与"内容=0"自洽，别借别的判据拒
		}, []string{"content_bytes", "ContentBytes"}},
		{"嵌套一层的方法名（Says.Clipped）", func(a map[string]any) {
			a["output"].(map[string]any)["Says"].(map[string]any)["Clipped"] = false
		}, []string{"Clipped", "Says"}},
		{"类型写错也不许丢掉（Call 给了字符串）", func(a map[string]any) {
			a["input"].(map[string]any)["Call"] = "第一发"
		}, []string{"Call"}},
	} {
		var wrapper map[string]any
		if err := json.Unmarshal([]byte(legal), &wrapper); err != nil {
			t.Fatalf("%s：底样解不开：%v", tc.name, err)
		}
		tc.mutate(wrapper)
		raw, err := json.Marshal(wrapper)
		if err != nil {
			t.Fatalf("%s：装不出 JSON：%v", tc.name, err)
		}
		s := New()
		txt, isErr := toolText(t, s, 3, "kernel.check_call", string(raw))
		rec, isErr2 := toolText(t, s, 4, "kernel.record_call", string(raw))
		if !isErr || !isErr2 {
			t.Errorf("%s：没打中的键被静默吞了 check=%v record=%v ｜ %s ｜ %s", tc.name, isErr, isErr2, txt, rec)
			continue
		}
		for _, want := range tc.wants {
			if !strings.Contains(txt, want) || !strings.Contains(rec, want) {
				t.Errorf("%s：拒了但没点名 %q（预检 %s / 落账 %s）", tc.name, want, txt, rec)
			}
		}
		if strings.Contains(rec, "整发落账") {
			t.Errorf("%s：拒的同时又说落账了，读起来像过了：%s", tc.name, rec)
		}
		// 而且这一发**不许留在链上**：静默落空最坏的后果就是留下一格没人写的 0。
		v, isErr3 := toolText(t, s, 5, "kernel.verify", "")
		if isErr3 || !strings.Contains(v, "共 0 条") {
			t.Errorf("%s：被拒的写请求在链上留了东西：%s", tc.name, v)
		}
	}
}

// TestHandleSeparatesParseErrorFromNotification —— `Handle` 不许把两件事回成同一个 nil：
// 「这条请求我解不开」（该回一条点名理由的错误）与「这是一条通知」（处理了，本来就没有回话）。
// 混起来的话，传输层只能把解不开的请求也当通知发 202 —— 调用方收到的是"成功且无需回话"。
func TestHandleSeparatesParseErrorFromNotification(t *testing.T) {
	s := New()
	bad := []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"kernel.verify","arguments":{broken}}}`)
	out := s.Handle(bad)
	if out == nil {
		t.Errorf("解不开的请求回了 nil —— 传输层只能把它当通知发 202")
	} else if !strings.Contains(string(out), "解不开") {
		t.Errorf("回了但没点名是解不开：%.200s", out)
	} else if !strings.Contains(string(out), `"error"`) {
		t.Errorf("解不开要回 JSON-RPC 的 error，不是伪装成 result：%.200s", out)
	}
	// 反面对照：真通知必须还是 nil（有回话就等于给通知造了一条结果）
	if got := s.Handle([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); got != nil {
		t.Errorf("真通知回了非 nil：%s", got)
	}
	// 这一笔要有数
	txt, isErr := toolText(t, s, 9, "kernel.tally", "")
	if isErr || !strings.Contains(txt, "被协议层判成无效") {
		t.Errorf("tally 没报出「被协议层判成无效」：%s", txt)
	}
}

// TestRequestWithoutMethodIsInvalidNotNotification —— 把 #41 里我自己搞混的一件改过来。
//
// JSON-RPC 的通知是「有 method、没有 id」；**没有 method 的消息根本不是请求**。
// 我第一版把 `r.Method == ""` 当成了通知，于是那条消息会安静地拿一个 202（"发成功了"）——
// 和 #41 那条洞是同一族：静默吞掉一条坏消息。探针那根针因此咬空（VACUOUS），
// 它没坏，它指着的那行代码本来就到不了通知那条路。
func TestRequestWithoutMethodIsInvalidNotNotification(t *testing.T) {
	s := New()
	out, kind := s.HandleOutcome([]byte(`{"jsonrpc":"2.0","id":5}`))
	if kind != OutcomeBad {
		t.Errorf("没有 method 的消息被分类成 %v（该 OutcomeBad）—— 它不是通知", kind)
	}
	if out == nil {
		t.Errorf("回话是 nil —— 传输层只能再发一次 202")
	} else {
		txt := string(out)
		if !strings.Contains(txt, "method") {
			t.Errorf("拒了但没点名缺的是 method：%s", txt[:min(200, len(txt))])
		}
		if !strings.Contains(txt, "-32600") {
			t.Errorf("无效请求该用 JSON-RPC 预定义码 -32600，不是 -32700 那种「解不开」：%s", txt[:min(240, len(txt))])
		}
	}

	// 反面对照（最关键的一支）：**真通知**照样必须是 nil + OutcomeNone。
	// 只判上面那一支的话，把分类写成"什么都是无效请求"也照样绿。
	got, k2 := s.HandleOutcome([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if got != nil || k2 != OutcomeNone {
		t.Errorf("真通知被误判了：kind=%v 回话 %s", k2, got)
	}

	// 而这一笔没进门的请求要有数（被拒也要看得见）
	txt, isErr := toolText(t, s, 10, "kernel.tally", "")
	if isErr || !strings.Contains(txt, "不是请求") {
		t.Errorf("tally 没把「解不开／不是请求」这一笔分开报出来：%s", txt)
	}
}
