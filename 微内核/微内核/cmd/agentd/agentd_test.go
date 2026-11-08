package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"agentkernel"
)

func newRun(t *testing.T, script string, readBack bool, payload string, maxTurns int) *run {
	t.Helper()
	drv, err := buildDriver("scripted", script)
	if err != nil {
		t.Fatalf("造驱动：%v", err)
	}
	return &run{
		rpc:         newKernelRPC(0, false),
		drv:         drv,
		session:     "test-" + script,
		maxTurns:    maxTurns,
		readBack:    readBack,
		declare:     true,
		journalPath: filepath.Join(t.TempDir(), "journal.jsonl"),
		payload:     payload,
		log:         &bytes.Buffer{},
		errLog:      &bytes.Buffer{},
		declared:    map[string]bool{},
	}
}

// 这一支要证的唯一一件事：账真的驱动过下一步。
func TestLedgerDrivesAtLeastOneDecision(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	r.loop()
	if r.kpi < 1 {
		t.Fatalf("账本触发的决定 = %d，这条边虚接", r.kpi)
	}
	fromLedger := 0
	for _, d := range r.decisions {
		switch {
		case d.Basis == "本地信息，没问内核":
		case d.Basis == "fail_kind", d.Basis == "folded（输入侧）":
			fromLedger++
		default:
			t.Fatalf("第 %d 发的依据栏写的是 %q —— 只许是真格名或「本地信息，没问内核」", d.Shot, d.Basis)
		}
	}
	if fromLedger != r.kpi {
		t.Fatalf("依据是真格名的有 %d 条，KPI 却报 %d —— 两个数得是从同一批决定数出来的", fromLedger, r.kpi)
	}
}

// 反臂：把 read_back 那一调用去掉，KPI 必须自己掉回 0。
// 它不是叶子装饰 —— 没有它，上面那条「≥1」可以靠我们手里那份结构体自证。
func TestAntiArmNoReadBackMakesKPIZero(t *testing.T) {
	r := newRun(t, "full", false, "included", 20)
	r.loop()
	if r.shot == 0 {
		t.Fatalf("反臂那一趟一发都没落账，等于没跑")
	}
	if r.kpi != 0 {
		t.Fatalf("关掉 read_back 之后 KPI 还是 %d —— 那说明值是本地编的，不是账上读的", r.kpi)
	}
	for _, d := range r.decisions {
		if d.Basis != "本地信息，没问内核" {
			t.Fatalf("反臂那一趟第 %d 发的依据写着 %q", d.Shot, d.Basis)
		}
	}
}

func TestPolicyStopsOnConfig(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	r.loop()
	if r.stop != stopPolicy {
		t.Fatalf("结束原因是 %q，想要的是按账停（fail_kind=config）", r.stop)
	}
	if exitOf(r.stop) != rcPolicy {
		t.Fatalf("退出码没跟着结束原因走")
	}
}

func TestHappyScriptRunsOut(t *testing.T) {
	r := newRun(t, "happy", true, "included", 20)
	r.loop()
	if r.stop != stopScript {
		t.Fatalf("结束原因是 %q，想要的是剧本跑完", r.stop)
	}
	if exitOf(r.stop) != rcScript {
		t.Fatalf("跑完剧本那一路的退出码不对")
	}
}

// 三种结束原因必须彼此分得开（这是收口那块的可机器核对判据）。
func TestTurnCapIsItsOwnEnding(t *testing.T) {
	r := newRun(t, "full", true, "included", 2)
	r.loop()
	if r.stop != stopTurns || exitOf(r.stop) != rcTurns {
		t.Fatalf("轮次到那一路：%q / rc=%d", r.stop, exitOf(r.stop))
	}
}

// stripHeader 只剥 encode_journal 加在第一行那句人读表头（认它的规矩是 mcpsrv 那份：
// 不是 JSON 开头、且含固定串）。剥完之后 kernel.LoadLedger 才拿得到纯账。
func stripHeader(t *testing.T, b []byte) []byte {
	t.Helper()
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		t.Fatalf("这份账只有一行")
	}
	if bytes.HasPrefix(bytes.TrimSpace(b[:i]), []byte("{")) {
		return b
	}
	return b[i+1:]
}

func TestCheckpointLoadsSelfConsistent(t *testing.T) {
	r := newRun(t, "happy", true, "included", 20)
	r.loop()
	r.closeOut()
	if !strings.Contains(r.lastChain, "自洽") {
		t.Fatalf("回读那一路内核说的是：%q —— 想要的是自洽", r.lastChain)
	}
	b, err := os.ReadFile(r.journalPath)
	if err != nil {
		t.Fatalf("checkpoint 没写盘：%v", err)
	}
	l, _, rep, err := kernel.LoadLedger(stripHeader(t, b))
	if err != nil {
		t.Fatalf("载入被拒：%v（mismatch=%q）", err, rep.Mismatch)
	}
	if !rep.Decidable() || rep.Mismatch != "" {
		t.Fatalf("链的三态不对：%s", rep.String())
	}
	if got, want := len(l.Docs()), r.shot*49; got != want {
		t.Fatalf("载入后 %d 条，按发数应该是 %d 条（每发 49 格）", got, want)
	}
}

// 反臂：动一行字节，载入必须拒 —— 不许悄悄读成自洽。
func TestTamperedCheckpointIsRefused(t *testing.T) {
	r := newRun(t, "happy", true, "included", 20)
	r.loop()
	r.closeOut()
	b := stripHeader(t, mustRead(t, r.journalPath))
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("这份账只有 %d 行，动不了", len(lines))
	}
	tampered := strings.Join(lines[2:], "\n") + "\n" // 丢掉第一条条目：链上前后引用会断
	_, _, rep, err := kernel.LoadLedger([]byte(tampered))
	if err == nil && rep.Mismatch == "" {
		t.Fatalf("改过一条还不拒 —— 那这条链就只是装饰（三态读作：%s）", rep.String())
	}
}

// 反臂（宿主级）：写出去的那份账被动过一个字节，宿主自己回读时就必须听到「拒收」，
// 不许它把改过的账念成自洽 —— 面板那块「链不符要印原话」的判分线在这儿先立着。
func TestHostRoundTripRefusesTamperedFile(t *testing.T) {
	r := newRun(t, "happy", true, "included", 20)
	r.loop()
	r.closeOut()
	b := mustRead(t, r.journalPath)
	i := bytes.IndexByte(b, '\n')
	j := bytes.IndexByte(b[i+1:], '\n')
	body := string(b[i+1 : i+1+j])
	body = strings.Replace(body, `"basis":1`, `"basis":2`, 1)
	if body == string(b[i+1:i+1+j]) {
		t.Fatalf("这一行里没有可改的出处字段，反臂没改成东西：%s", body)
	}
	fixed := string(b[:i+1]) + body + string(b[i+1+j:])
	if err := os.WriteFile(r.journalPath, []byte(fixed), 0o644); err != nil {
		t.Fatalf("写坏账：%v", err)
	}
	r.verifyRoundTrip("反臂")
	if !strings.Contains(r.lastChain, "拒收") && !strings.Contains(r.lastChain, "不符") {
		t.Fatalf("改了一格出处之后，宿主回读说的是：%q", r.lastChain)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读 %s：%v", p, err)
	}
	return b
}

// 反臂：不带原像编出去，那一面必须是「核不了」，不是绿。
func TestOmittedPayloadIsNotGreen(t *testing.T) {
	r := newRun(t, "happy", true, "omitted", 20)
	r.loop()
	r.closeOut()
	if strings.Contains(r.lastChain, "✓") || !strings.Contains(r.lastChain, "核不了") {
		t.Fatalf("没有原像却回读成绿/自洽：%q", r.lastChain)
	}
	b := stripHeader(t, mustRead(t, r.journalPath))
	l, _, rep, err := kernel.LoadLedger(b)
	if err != nil {
		t.Fatalf("载入被拒（不该）：%v", err)
	}
	if rep.Mismatch != "" {
		t.Fatalf("说不符（不该）：%s", rep.Mismatch)
	}
	if rep.Decidable() || len(rep.NoPreimage) == 0 {
		t.Fatalf("没有原像却读成可判/自洽：%s", rep.String())
	}
	if n := len(l.Docs()); n == 0 {
		t.Fatalf("载入后一条都没有")
	}
}

// 没有凭据的那一路必须如实说 NOT_RUN，且一条都不落账（不许拿剧本绿冒充真跑）。
func TestRealDriverStaysNotRunAndWritesNothing(t *testing.T) {
	os.Unsetenv("AGNES_KEY")
	os.Unsetenv("AGENTNES_KEY")
	drv, err := buildDriver("real", "full")
	if err != nil {
		t.Fatalf("造驱动：%v", err)
	}
	r := &run{
		rpc: newKernelRPC(0, false), drv: drv, session: "test-real", maxTurns: 20,
		readBack: true, declare: true, payload: "included", log: &bytes.Buffer{}, declared: map[string]bool{},
	}
	r.loop()
	r.closeOut()
	if r.stop != stopDriver || exitOf(r.stop) != rcNotRun {
		t.Fatalf("真端点这一路今天的结束原因应是跑不了，实得 %q / rc=%d", r.stop, exitOf(r.stop))
	}
	if r.shot != 0 || r.rpc.srv.Entries() != 0 {
		t.Fatalf("NOT_RUN 那一趟居然落了 %d 发 / %d 条", r.shot, r.rpc.srv.Entries())
	}
}

// 出处纪律：剧本这一路不许有任何一格 token 被记成 measured。
func TestScriptedNeverMarksTokensMeasured(t *testing.T) {
	r := newRun(t, "happy", true, "included", 20)
	r.loop()
	r.closeOut()
	_, j, _, err := kernel.LoadLedger(stripHeader(t, mustRead(t, r.journalPath)))
	if err != nil {
		t.Fatalf("载入被拒：%v", err)
	}
	n := 0
	for _, e := range j.Docs {
		// 格名住在 Kind 的冒号后面（model_output:tokens_out 这种）。
		if strings.Contains(e.Kind, "token") && e.Basis == kernel.BasisMeasured {
			t.Fatalf("第 %d 条把 token 记成了 measured：%s", e.Seq, e.Kind)
		}
		if e.Basis == kernel.BasisMeasured {
			n++
		}
	}
	if n == 0 {
		t.Fatalf("整本账一个 measured 都没有 —— 字节那几格本来是量得到的，别把三态压成一态")
	}
}

// 回边不许停在「读到了」：读到的那一格必须真改掉下一发的装配。
// 这一根针是冲着「旗标在同一发里就被清掉」那个形状立的 —— 那种接法 KPI 照样是 9，
// 但账对下一发一个字都不影响，虚接换了个住处。
func TestBackEdgeChangesTheNextAssembly(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	r.loop()
	r.closeOut()
	_, j, _, err := kernel.LoadLedger(stripHeader(t, mustRead(t, r.journalPath)))
	if err != nil {
		t.Fatalf("载入被拒：%v", err)
	}
	folded, notice := 0, 0
	for _, e := range j.Docs {
		switch e.Kind {
		case "model_input:folded":
			if !strings.Contains(e.Claim, "没交") && !strings.Contains(e.Claim, "空表") {
				folded++
			}
		case "model_input:notice_sent":
			if e.Claim == "是" {
				notice++
			}
		}
	}
	if folded == 0 {
		t.Fatalf("账上从没出现过一条真折叠记录 —— budget 那一发的决定没接到装配上")
	}
	if notice == 0 {
		t.Fatalf("折了历史却没把知情通知发出去（folded=%d 而 notice_sent=是 出现 0 次）", folded)
	}
}

// 预检那一路今天只有绿（四趟跑下来「预检被内核判据拒 0 发」）。
// 尺子必须先证会红：这里故意喂两种坏 —— 整块没给（门口挡）、发号写 0（内核判据挡），
// 并且要看到这两笔是分开数的，谁也不许冒充谁。
func TestPrecheckPathCanGoRedAndWritesNothing(t *testing.T) {
	r := newRun(t, "happy", true, "included", 20)
	in, _, err := (&assembly{
		system: systemPrompt, toolspec: toolSpec, user: userAsk,
	}).buildInput(1)
	if err != nil {
		t.Fatalf("装配一份合法输入就散了：%v", err)
	}
	out := buildOutput(1, proposal{
		Text: "先不管内容", UsageIn: 10, UsageOut: 2, FinishSaid: true, FinishReason: "stop", ReportsModel: true,
	}, []byte("{}"))

	// ① 门口挡：把 input 里 Notice 那一整块抹掉。
	raw, _ := json.Marshal(sides(r.session, in, out))
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatalf("回marsh：%v", err)
	}
	delete(args["input"].(map[string]any), "Notice")
	got, err := r.rpc.call("kernel.check_call", args)
	if err != nil || !got.IsErr {
		t.Fatalf("整块没给却被放过了（err=%v isError=%v）：%s", err, got.IsErr, got.Text)
	}
	if !strings.Contains(got.Text, "没有 Notice 这一整块") {
		t.Fatalf("拒收原话没点名到那一块：%s", got.Text)
	}

	// ② 内核判据挡：Notice 给了，发号写 0。
	bad := in
	bad.Call = 0
	got2, err := r.rpc.call("kernel.check_call", sides(r.session, bad, out))
	if err != nil || !got2.IsErr {
		t.Fatalf("发号 0 被预检放过（err=%v isError=%v）：%s", err, got2.IsErr, got2.Text)
	}
	if !strings.Contains(got2.Text, "发号从 1 起") {
		t.Fatalf("预检没说内核那句原话：%s", got2.Text)
	}

	// 落账那一路也得拒，且**两侧都不写**。
	got3, err := r.rpc.call("kernel.record_call", sides(r.session, bad, out))
	if err != nil || !got3.IsErr {
		t.Fatalf("发号 0 居然落得进账：%s", got3.Text)
	}
	if n := r.rpc.srv.Entries(); n != 0 {
		t.Fatalf("预检与落账都拒了，账上却有 %d 条", n)
	}
	tally, _ := r.rpc.call("kernel.tally", nil)
	if !strings.Contains(tally.Text, "预检（不落账的那一路）被内核判据拒 1 发") {
		t.Fatalf("预检那一笔没有单独数出来：%s", firstLine(tally.Text))
	}
	if !strings.Contains(tally.Text, "被拒的写入 1 次") {
		t.Fatalf("落账被拒那一笔没有单独数出来：%s", firstLine(tally.Text))
	}
	if strings.Contains(tally.Text, "被拒的写入 2 次") {
		t.Fatalf("两笔被加成一堆了 —— 门口挡的与内核拒的永不相加：%s", firstLine(tally.Text))
	}
}

// 落盘节奏：每 N 发编一次。这一根针盯的是「只在收口编」那一种偷懒 ——
// 中途进程死掉，账不能只剩最后一发。
func TestCheckpointEveryNLeaksNothingToTheLastShot(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	r.checkpointEvery = 2
	r.loop()
	b := mustRead(t, r.journalPath)
	_, j, _, err := kernel.LoadLedger(stripHeader(t, b))
	if err != nil {
		t.Fatalf("中途那次 checkpoint 的账读不回来：%v", err)
	}
	if len(j.Docs) < 49*3 {
		t.Fatalf("每 2 发就编一次，落盘的账却只有 %d 条（不到 3 发）", len(j.Docs))
	}
}

// 这本账吃饱了：循环该读出来并停手，而不是把发号烧光换五句一模一样的拒绝。
// KPI 不许因此变胖 —— 容量那一栏是外壳按部署方给的数，不是 49 格里的一格，两个数不相加。
func TestLedgerCapStopsAndStaysOutOfKPI(t *testing.T) {
	drv, err := buildDriver("scripted", "full")
	if err != nil {
		t.Fatalf("造驱动：%v", err)
	}
	r := &run{
		rpc: newKernelRPC(98, false), drv: drv, session: "test-cap", maxTurns: 20,
		readBack: true, declare: true, payload: "included",
		journalPath: filepath.Join(t.TempDir(), "journal.jsonl"),
		log:         &bytes.Buffer{}, declared: map[string]bool{},
	}
	r.loop()
	r.closeOut()
	if r.stop != stopCapped || exitOf(r.stop) != rcCapped {
		t.Fatalf("账本满了却没停在那一条上：%q / rc=%d", r.stop, exitOf(r.stop))
	}
	if r.caps != 1 {
		t.Fatalf("容量那一栏促成的停记了 %d 次，应为 1（一发一次）", r.caps)
	}
	if r.kpi != 2 {
		t.Fatalf("KPI = %d，容量那两笔混进来了（应为 2：只有落进账的两发由格驱动）", r.kpi)
	}
	if n := r.rpc.srv.Entries(); n != 98 {
		t.Fatalf("账上 %d 条，上限给的是 98", n)
	}
}

// 轨迹解剖必须点名到格：KPI 有几回，表里就得有几行写着格名。
// 同时它不许带原话正文 —— 面板那五条禁区之一，这里先守着。
func TestTraceNamesEveryLedgerCellAndCarriesNoPayload(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	r.tracePath = filepath.Join(t.TempDir(), "trace.html")
	r.caveat = "测试用的那句说明。"
	r.loop()
	r.closeOut()
	h := string(mustRead(t, r.tracePath))
	if got, want := strings.Count(h, `<span class="cell">`), r.kpi; got != want {
		t.Fatalf("轨迹里点名 fail_kind 的行数 = %d，KPI = %d —— 两者必须一样", got, want)
	}
	if strings.Contains(h, "总和是 84") || strings.Contains(h, "payload_b64") {
		t.Fatalf("轨迹把模型原话或账本字节抄进去了（禁区第 2 条）")
	}
	if !strings.Contains(h, "本地剧本，不是模型自主决定") {
		t.Fatalf("轨迹没写明这一趟的端点是谁决定的：那一句必须在页首，不许藏在脚注")
	}
}

// 尺子会红：反臂那一趟的轨迹必须整列是「本地」，并把 KPI=0 说成虚接。
func TestTraceCanShowTheRedArm(t *testing.T) {
	r := newRun(t, "happy", false, "included", 20)
	r.tracePath = filepath.Join(t.TempDir(), "trace.html")
	r.loop()
	r.closeOut()
	h := string(mustRead(t, r.tracePath))
	if r.kpi != 0 {
		t.Fatalf("这一趟本来就该 KPI=0")
	}
	if !strings.Contains(h, "本地信息，没问内核") || !strings.Contains(h, "这就是「尺子会红」那一趟") {
		t.Fatalf("反臂那一趟的轨迹没把虚接说出来")
	}
	if strings.Contains(h, `<span class="cell">`) {
		t.Fatalf("反臂那一趟不该有任何一行是格驱动")
	}
}

// 五档不许塌：absent 单独一列；读不到出处分布时要显「读不到」，不许填 0。
func TestTraceBucketsParseAndDontCollapse(t *testing.T) {
	got, total, ok := tallyBuckets("账 441 条（absent=75 estimated=18 measured=304 model_says=9 relayed=35）；被拒的写入 0 次")
	if !ok || total != 441 {
		t.Fatalf("parse 没认出来：%v / %d", ok, total)
	}
	sum := 0
	for _, k := range basisOrder {
		sum += got[k]
	}
	if sum != 441 {
		t.Fatalf("五档相加 %d ≠ 441（塌了一档）", sum)
	}
	if _, _, ok := tallyBuckets("内核今天不配合"); ok {
		t.Fatalf("读不到却报 present —— 页面就会拿 0 冒充没数")
	}
}

// 回边的另一半：不是「我决定折历史」就算折了。
// 这一根要求账上出现第二格被消费的证据（input/folded），KPI 里必须含这一条。
func TestFoldIsConfirmedFromTheLedgerNotFromTheFlag(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	r.loop()
	r.closeOut()
	foldDriven := 0
	for _, d := range r.decisions {
		if strings.HasPrefix(d.Basis, "folded") {
			foldDriven++
		}
	}
	if foldDriven == 0 {
		t.Fatalf("KPI=%d 但没有一条依据是 folded —— 折叠只停在旗标上，没回到账上", r.kpi)
	}
	if r.kpi < 10 {
		t.Fatalf("KPI=%d，少了回读确认那一条（budget 那一趟应有 fail_kind + folded）", r.kpi)
	}
}

// 同一发、同一侧，换一个 session 就读不到东西：三轴身份不是装饰。
// 少一根轴会把两本不同 session 的同号同侧并成一次回答，那 KPI 就是虚的。
func TestWrongSessionAxisYieldsNothing(t *testing.T) {
	r := newRun(t, "happy", true, "included", 20)
	r.loop()
	rb, err := r.rpc.call("kernel.read_back", map[string]any{
		"session": "这一本不是我", "call": 1, "side": "output",
	})
	if err != nil {
		t.Fatalf("读回帧坏了：%v", err)
	}
	if !strings.Contains(rb.Text, "在链上一条都没有") {
		t.Fatalf("换了 session 还读得到东西（三轴没起作用）：%s", firstLine(rb.Text))
	}
	if _, _, ok := cellFromReadBack(rb.Text, "fail_kind"); ok {
		t.Fatalf("从别人那本账里读出了 fail_kind")
	}
	// 空 session 必须被当场点名拒绝，而不是退化成「读全部」。
	empty, _ := r.rpc.call("kernel.read_back", map[string]any{"session": "", "call": 1, "side": "output"})
	if !empty.IsErr || !strings.Contains(empty.Text, "session 是空的") {
		t.Fatalf("空 session 没被点名拒：%s", firstLine(empty.Text))
	}
}

// 本机假端点：真开 socket、真过网线。这一路要证的是「管路那一族的出处能升到实测」，
// 而不是「模型跑通了」—— 后者仍然 NOT_RUN。
func TestFakeEndpointMakesWireBytesMeasured(t *testing.T) {
	drv, err := buildDriver("fake", "full")
	if err != nil {
		t.Fatalf("起本机假端点：%v", err)
	}
	if c, ok := drv.(Closer); ok {
		defer func() { _ = c.Close() }()
	}
	r := &run{
		rpc: newKernelRPC(0, false), drv: drv, session: "test-fake", maxTurns: 20,
		readBack: true, declare: true, payload: "included",
		journalPath: filepath.Join(t.TempDir(), "journal.jsonl"),
		log:         &bytes.Buffer{}, declared: map[string]bool{},
	}
	r.loop()
	r.closeOut()
	if r.kpi < 1 {
		t.Fatalf("这一路 KPI=%d：真过网线的往来反而没让账驱动决定？", r.kpi)
	}
	_, j, _, err := kernel.LoadLedger(stripHeader(t, mustRead(t, r.journalPath)))
	if err != nil {
		t.Fatalf("载入被拒：%v", err)
	}
	wireOut, transport, sent := 0, 0, 0
	for _, e := range j.Docs {
		switch e.Kind {
		case "model_output:wire_bytes_out":
			if e.Basis == kernel.BasisMeasured {
				wireOut++
			}
		case "model_output:fail_kind":
			if strings.Contains(e.Claim, "transport") {
				transport++
			}
		case "model_output:sent":
			if strings.Contains(e.Claim, "发出去了") || strings.Contains(e.Claim, "真发") {
				sent++
			}
		}
	}
	if wireOut == 0 {
		t.Fatalf("真过了网线，`wire_bytes_out` 却没有一条是 measured")
	}
	if transport == 0 {
		t.Fatalf("假端点把连接直接关掉那一发没被分成 transport：这一路白开了")
	}
}

// 反过来：剧本那一路不开 socket，就不许声称量到过网线字节。
func TestScriptedClaimsNoWireBytes(t *testing.T) {
	r := newRun(t, "happy", true, "included", 20)
	r.loop()
	r.closeOut()
	_, j, _, err := kernel.LoadLedger(stripHeader(t, mustRead(t, r.journalPath)))
	if err != nil {
		t.Fatalf("载入被拒：%v", err)
	}
	for _, e := range j.Docs {
		if strings.HasSuffix(e.Kind, ":wire_bytes_out") && e.Basis == kernel.BasisMeasured && e.Claim != "0" {
			t.Fatalf("剧本这一路没开 socket，却把过网线字节记成实测 %q（第 %d 条）", e.Claim, e.Seq)
		}
	}
}

// 分帧那三格第一次有东西可量：只允许真流式那一发有数，其余各发必须仍是缺席。
// 两头都要判 —— 非流式写数是编数，流式报缺席是把该量到的丢了。
func TestStreamingNumbersOnlyOnTheStreamedShot(t *testing.T) {
	drv, err := buildDriver("fake", "full")
	if err != nil {
		t.Fatalf("起本机假端点：%v", err)
	}
	if c, ok := drv.(Closer); ok {
		defer func() { _ = c.Close() }()
	}
	r := &run{
		rpc: newKernelRPC(0, false), drv: drv, session: "test-sse", maxTurns: 20,
		readBack: true, declare: false, payload: "included",
		journalPath: filepath.Join(t.TempDir(), "journal.jsonl"),
		log:         &bytes.Buffer{}, declared: map[string]bool{},
	}
	r.loop()
	r.closeOut()
	_, j, _, err := kernel.LoadLedger(stripHeader(t, mustRead(t, r.journalPath)))
	if err != nil {
		t.Fatalf("载入被拒：%v", err)
	}
	streamed, absent, chunkSum := 0, 0, int64(0)
	format := ""
	for _, e := range j.Docs {
		if e.Kind != "model_output:chunks" {
			continue
		}
		if e.Basis == kernel.BasisMeasured {
			streamed++
			n, _ := strconv.ParseInt(e.Claim, 10, 64)
			chunkSum += n
		} else {
			absent++
		}
	}
	if streamed == 0 || absent == 0 {
		t.Fatalf("分帧那格：measured %d 条、缺席 %d 条 —— 两种都得有（只有真流式那发该有数）", streamed, absent)
	}
	if chunkSum < int64(streamed) {
		t.Fatalf("流式那发数到的帧数是 %d，等于没帧", chunkSum)
	}
	for _, e := range j.Docs {
		if e.Kind == "model_output:wire_format" && strings.Contains(e.Claim, "chunk") {
			format = e.Claim
		}
	}
	if format == "" {
		t.Fatalf("流式那一发没有转述到对端自报的格式（该是 chat.completion.chunk）")
	}
}

// 「断开即失」的那一半被宿主收回去：跑一段、落盘、换个进程读回来接着跑，
// 而且**同一发不许记第二遍**。这两件事必须一起成立 ——
// 只证明能读回，是把可换账当成能续跑；只证明能续跑，是忘了去重门。
func TestResumeContinuesAndRefusesSecondRecording(t *testing.T) {
	r1 := newRun(t, "full", true, "included", 4)
	r1.loop()
	r1.closeOut()
	if r1.stop != stopTurns {
		t.Fatalf("第一段本该是额度到就停，实得 %q", r1.stop)
	}
	first := r1.rpc.srv.Entries()
	if first != 4*49 {
		t.Fatalf("第一段落了 %d 条，想要 4 发 = %d 条", first, 4*49)
	}

	drv, err := buildDriver("scripted", "full")
	if err != nil {
		t.Fatalf("造驱动：%v", err)
	}
	s := drv.(*scripted)
	s.i = 4 // 前 4 个结局已经在第一段跑过了
	r2 := &run{
		rpc: newKernelRPC(0, false), drv: drv, session: r1.session, maxTurns: 20,
		readBack: true, declare: true, payload: "included",
		journalPath: filepath.Join(t.TempDir(), "journal2.jsonl"),
		log:         &bytes.Buffer{}, declared: map[string]bool{},
	}
	if !r2.resume(r1.journalPath) {
		t.Fatalf("续跑没成：%s", r2.stop)
	}
	if r2.shot != 4 {
		t.Fatalf("读回来的发号是 %d，想要 4", r2.shot)
	}
	r2.loop()
	r2.closeOut()
	if got := r2.rpc.srv.Entries(); got != 9*49 {
		t.Fatalf("续跑后账上 %d 条，想要 9 发 = %d 条（第一段 %d）", got, 9*49, first)
	}
	if r2.kpi < 1 {
		t.Fatalf("续跑那一段 KPI=%d：读回来的账没再驱动过任何一步", r2.kpi)
	}

	// 再去重门上撞一次：第 2 发这一侧已经记过了，必须被挡且账一条不长。
	in, body, err := (&assembly{system: systemPrompt, toolspec: toolSpec, user: userAsk}).buildInput(2)
	if err != nil {
		t.Fatalf("装配：%v", err)
	}
	out := buildOutput(2, proposal{Text: "再记一次第 2 发试试", UsageIn: 5, UsageOut: 5, FinishSaid: true, FinishReason: "stop"}, body)
	res, err := r2.rpc.call("kernel.record_call", sides(r2.session, in, out))
	if err != nil {
		t.Fatalf("回帧坏了：%v", err)
	}
	if !res.IsErr || !strings.Contains(res.Text, "已经记过了") {
		t.Fatalf("同一发记了第二遍没被挡：%s", firstLine(res.Text))
	}
	if got := r2.rpc.srv.Entries(); got != 9*49 {
		t.Fatalf("被挡之后账还长了：%d 条", got)
	}
}

// 反臂：账被改过一颗字，续跑必须**拒写**，而不是拿着坏账往下续。
func TestResumeRefusesInconsistentLedger(t *testing.T) {
	r1 := newRun(t, "happy", true, "included", 20)
	r1.loop()
	r1.closeOut()
	b := mustRead(t, r1.journalPath)
	i := bytes.IndexByte(b, '\n')
	j := bytes.IndexByte(b[i+1:], '\n')
	line := string(b[i+1 : i+1+j])
	fixed := string(b[:i+1]) + strings.Replace(line, `"basis":1`, `"basis":2`, 1) + string(b[i+1+j:])
	if fixed == string(b) {
		t.Fatalf("没改成东西：这一行里没有可改的出处字段")
	}
	bad := filepath.Join(t.TempDir(), "tampered.jsonl")
	if err := os.WriteFile(bad, []byte(fixed), 0o644); err != nil {
		t.Fatalf("写坏账：%v", err)
	}

	drv, err := buildDriver("scripted", "happy")
	if err != nil {
		t.Fatalf("造驱动：%v", err)
	}
	r2 := &run{
		rpc: newKernelRPC(0, false), drv: drv, session: r1.session, maxTurns: 20,
		readBack: true, payload: "included", journalPath: filepath.Join(t.TempDir(), "j2.jsonl"),
		log: &bytes.Buffer{}, declared: map[string]bool{},
	}
	if r2.resume(bad) {
		t.Fatalf("拿着改过的账居然让续跑：想演的是「拒写」")
	}
	// 上面那份改过的账先直接验一次：内核要么拒收，要么报不符，不许读成自洽。
	_, _, rep, err := kernel.LoadLedger(stripHeader(t, []byte(fixed)))
	if err == nil && rep.Mismatch == "" {
		t.Fatalf("改过一格出处还能读成自洽：%s", rep.String())
	}
	if n := r2.rpc.srv.Entries(); n != 0 {
		t.Fatalf("拒写那一趟居然还往账里写了 %d 条", n)
	}
	// 拒写要落到盘上：收口不许往 --journal-out 写那本 0 条的空账 ——
	// -resume 与 -journal-out 指同一本时，它会把被篡改的输入盖掉，现场就没了。
	r2.closeOut()
	if _, err := os.Stat(r2.journalPath); !os.IsNotExist(err) {
		t.Fatalf("拒写那一趟居然在 --journal-out 落了盘（stat err=%v）", err)
	}
}

// 续跑要带历史，只能靠宿主自己留的原文 —— 内核那 49 格里存的是元数据（bytes / kept / sha256），
// 装不下原文。这里验的是：重建出来的每一发都拿账上那枚指纹对过，才对得上才敢用。
func TestResumeRebuildsHistoryOnlyWhenFingerprintsMatch(t *testing.T) {
	r1 := newRun(t, "full", true, "included", 4)
	r1.loop()
	r1.closeOut()
	tf := r1.journalPath + ".transcript"
	if _, err := os.Stat(tf); err != nil {
		t.Fatalf("宿主没留往轮轨迹文件：%v", err)
	}

	drv, err := buildDriver("scripted", "full")
	if err != nil {
		t.Fatalf("造驱动：%v", err)
	}
	buf := &bytes.Buffer{}
	r2 := &run{
		rpc: newKernelRPC(0, false), drv: drv, session: r1.session, maxTurns: 20,
		readBack: true, declare: true, payload: "included", journalPath: r1.journalPath,
		log: buf, declared: map[string]bool{},
	}
	drv.(*scripted).i = 4
	if !r2.resume(r1.journalPath) {
		t.Fatalf("续跑没成：%s", firstLine(buf.String()))
	}
	if r2.historyOrig == "" {
		t.Fatalf("历史没重建出来：%s", buf.String())
	}
	if !strings.Contains(buf.String(), "与账上 model_says_sha256 对上") {
		t.Fatalf("没说出指纹对过：%s", buf.String())
	}

	// 反过来：原文被换掉（sha 保持账上那枚不动），一发都不该被采用。
	clean := mustRead(t, tf)
	tampered := strings.Replace(string(clean), "相加，得到 42", "相加，得到 99", 1)
	if tampered == string(clean) {
		t.Fatalf("没改成东西，反臂等于没跑")
	}
	if err := os.WriteFile(tf, []byte(tampered), 0o644); err != nil {
		t.Fatalf("写坏轨迹：%v", err)
	}
	drv2, err := buildDriver("scripted", "full")
	if err != nil {
		t.Fatalf("造驱动：%v", err)
	}
	buf2 := &bytes.Buffer{}
	r3 := &run{
		rpc: newKernelRPC(0, false), drv: drv2, session: r1.session, maxTurns: 20,
		readBack: true, payload: "included", journalPath: r1.journalPath,
		log: buf2, declared: map[string]bool{},
	}
	if !r3.resume(r1.journalPath) {
		t.Fatalf("账本身是好的，不该拒续跑：%s", buf2.String())
	}
	// 逐发判：被改的那一发不许进历史，没被改的那一发照常可用。
	if strings.Contains(r3.historyOrig, "得到 99") {
		t.Fatalf("改了原文那一发还是被当往轮轨迹用了：%q", cut(r3.historyOrig, 60))
	}
	if !strings.Contains(r3.historyOrig, "42 加 8 等于 50") {
		t.Fatalf("没被改的那一发也被一起丢了（该只丢对不上的那一段）：%q", r3.historyOrig)
	}
	if !strings.Contains(buf2.String(), "对不上") || !strings.Contains(buf2.String(), "[2]") {
		t.Fatalf("没点名是哪一发对不上：%s", buf2.String())
	}
}

// 中途被打断（Ctrl-C / SIGINT）不许等于把这几发的账扔掉。
// 这里用一发包一层驱动：跑到第 3 发之后关掉 abort 通道，看循环是不是先交账再走。
type abortAfter struct {
	Driver
	n    int
	ch   chan struct{}
	once sync.Once
}

func (a *abortAfter) Propose(turn int, obs string, body []byte) (proposal, error) {
	if turn >= a.n {
		a.once.Do(func() { close(a.ch) })
	}
	return a.Driver.Propose(turn, obs, body)
}

func (a *abortAfter) Name() string { return "abort-after（测试用的包一层，不是新驱动）" }

func TestInterruptStillHandsOverTheLedger(t *testing.T) {
	base, err := buildDriver("scripted", "full")
	if err != nil {
		t.Fatalf("造驱动：%v", err)
	}
	ch := make(chan struct{})
	drv := &abortAfter{Driver: base, n: 3, ch: ch}
	jp := filepath.Join(t.TempDir(), "journal.jsonl")
	buf := &bytes.Buffer{}
	r := &run{
		rpc: newKernelRPC(0, false), drv: drv, session: "test-abort", maxTurns: 20,
		readBack: true, declare: true, payload: "included", journalPath: jp,
		log: buf, declared: map[string]bool{}, abort: ch,
	}
	r.loop()
	r.closeOut()
	if r.stop != stopAbort || exitOf(r.stop) != rcAbort {
		t.Fatalf("打断那一路的结束原因/退出码不对：%q / rc=%d", r.stop, exitOf(r.stop))
	}
	if got := r.rpc.srv.Entries(); got != 3*49 {
		t.Fatalf("打断前落了 %d 条，想要 3 发 = %d 条", got, 3*49)
	}
	if r.kpi != 3 {
		t.Fatalf("KPI=%d，想要 3（打断前那三发都该是格驱动）", r.kpi)
	}
	l, _, rep, err := kernel.LoadLedger(stripHeader(t, mustRead(t, jp)))
	if err != nil || rep.Mismatch != "" {
		t.Fatalf("交出去的账读不回来或不符：%v / %q", err, rep.Mismatch)
	}
	if len(l.Docs()) != 3*49 {
		t.Fatalf("文件里 %d 条，内存里 %d 条 —— 交账没交全", len(l.Docs()), r.rpc.srv.Entries())
	}
}

// 等待之中也要听得见打断：延迟设得比测试长，abort 提前关掉，
// 循环必须**立刻**返回而不是等满延迟 —— 这一段是 OS 那半没法验时，唯一能钉住的地方。
func TestAbortDuringDelayReturnsImmediately(t *testing.T) {
	drv, err := buildDriver("scripted", "full")
	if err != nil {
		t.Fatalf("造驱动：%v", err)
	}
	ch := make(chan struct{})
	close(ch) // 一开始就是「已打断」
	r := &run{
		rpc: newKernelRPC(0, false), drv: drv, session: "test-abort-delay", maxTurns: 20,
		readBack: true, payload: "included", journalPath: filepath.Join(t.TempDir(), "j.jsonl"),
		log: &bytes.Buffer{}, declared: map[string]bool{}, abort: ch,
		shotDelay: 30 * time.Second,
	}
	start := time.Now()
	r.loop()
	if took := time.Since(start); took > time.Second {
		t.Fatalf("打断没打断等待：过了 %v 才回来（延迟设的是 30s）", took)
	}
	if r.stop != stopAbort || exitOf(r.stop) != rcAbort {
		t.Fatalf("结束原因/退出码不对：%q / rc=%d", r.stop, exitOf(r.stop))
	}
	if n := r.rpc.srv.Entries(); n != 0 {
		t.Fatalf("一发都没该发出去，账上却有 %d 条", n)
	}
}

// 每一发的 49 格必须能在轨迹页上摆开（这是「看得见」的最后一寸），
// 而且「这一发用了它」的标记数必须等于 KPI —— 多一个少一个都说明页面上有假账。
func TestTraceCarriesPerShotCellTables(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	r.tracePath = filepath.Join(t.TempDir(), "trace.html")
	r.loop()
	r.closeOut()
	h := string(mustRead(t, r.tracePath))
	if !strings.Contains(h, "每发 49 格") {
		t.Fatalf("轨迹页没有那一节：每发格表")
	}
	if n := strings.Count(h, ">content_bytes<"); n != r.shot {
		t.Fatalf("输入侧格表里 content_bytes 出现 %d 次，应为 %d（每发一次）", n, r.shot)
	}
	if strings.Count(h, "<td>输出侧</td>") != r.shot*29 || strings.Count(h, "<td>输入侧</td>") != r.shot*20 {
		t.Fatalf("格表行数不对：输出侧 %d（应 %d）、输入侧 %d（应 %d）", strings.Count(h, "<td>输出侧</td>"), r.shot*29, strings.Count(h, "<td>输入侧</td>"), r.shot*20)
	}
	if n := strings.Count(h, ">fail_kind</td>"); n != r.shot {
		t.Fatalf("输出侧格表里 fail_kind 出现 %d 次，应为 %d", n, r.shot)
	}
	if n := strings.Count(h, `class="used"`); n != r.kpi {
		t.Fatalf("「这一发用了它」标了 %d 处，KPI 是 %d —— 两个数得从同一批决定数出来", n, r.kpi)
	}
	// 反臂那一趟：格表必须直说「读回关着」，不许摆一张空表冒充看过。
	r2 := newRun(t, "happy", false, "included", 20)
	r2.tracePath = filepath.Join(t.TempDir(), "trace2.html")
	r2.loop()
	r2.closeOut()
	h2 := string(mustRead(t, r2.tracePath))
	if !strings.Contains(h2, "读回关着") {
		t.Fatalf("反臂那一趟的格表没说实话（摆了空表还是藏了？）")
	}
	if strings.Contains(h2, ">fail_kind<") {
		t.Fatalf("读回关着那一趟不该有格表内容")
	}
}

// 折叠块必须两侧都在同一个块里：曾经输出侧在 <details> 内、输入侧掉到块外，
// 看着像「只摆了 29 格」。这一根钉的是结构，不是好看。
func TestEachDetailsBlockHoldsBothSides(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	r.tracePath = filepath.Join(t.TempDir(), "trace.html")
	r.loop()
	r.closeOut()
	h := string(mustRead(t, r.tracePath))
	blocks := strings.Split(h, "<details>")
	if len(blocks) != r.shot+1 {
		t.Fatalf("折叠块 %d 个，应为 %d 发 = %d 个", len(blocks)-1, r.shot, r.shot)
	}
	for i, b := range blocks[1:] {
		if !strings.Contains(b, ">fail_kind</td>") || !strings.Contains(b, ">content_bytes</td>") {
			t.Fatalf("第 %d 发的折叠块里两侧格表没齐（输出侧或输入侧掉到块外了）", i+1)
		}
		if strings.Count(b, "</details>") != 1 {
			t.Fatalf("第 %d 发的折叠块闭合了 %d 次，应为 1", i+1, strings.Count(b, "</details>"))
		}
	}
}

// 标签必须配平。这一根是因为一次「改了代码没重编，拿旧二进制生成新页面」而补的：
// 数一下开合标签比看排版便宜，而且它不会被骗。
func TestTraceTagsBalance(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	r.tracePath = filepath.Join(t.TempDir(), "trace.html")
	r.loop()
	r.closeOut()
	h := string(mustRead(t, r.tracePath))
	for _, pair := range [][2]string{{"<details>", "</details>"}, {"<table>", "</table>"}, {"<summary>", "</summary>"}} {
		a, b := strings.Count(h, pair[0]), strings.Count(h, pair[1])
		if a != b {
			t.Fatalf("%s 开了 %d 次、%s 关了 %d 次 —— 页面结构是坏的", pair[0], a, pair[1], b)
		}
	}
	if n := strings.Count(h, "<table>"); n != 2*r.shot+1 {
		t.Fatalf("表格 %d 张，应为每发两侧共 %d 张再加解剖总表 1 张", n, 2*r.shot)
	}
}

// KPI 只数「值真从账上读回来」的那些发。这一根先证明正常路径上零漂移，
// 再用比较函数本身做反臂（叶针）：喂一对不一致进去，守卫必须说"漂"。
func TestDriftGuardKeepsKPIHonest(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	r.loop()
	r.closeOut()
	if r.drift != 0 {
		t.Fatalf("正常路径上漂了 %d 发，KPI=%d 就别再报了", r.drift, r.kpi)
	}
	if !strings.Contains(r.bufString(), "漂移：0 发") {
		t.Fatalf("收口没把零漂移这件事说出来")
	}

	cases := []struct {
		read string
		loc  kernel.FailKind
		want bool
		why  string
	}{
		{"http", kernel.FailHTTP, false, "对上"},
		{"（这一格是空的）", kernel.FailNone, false, "内核把无故障渲染成这句，不是漂移"},
		{"", kernel.FailNone, false, "空格值也是无故障"},
		{"http", kernel.FailTransport, true, "读回来的与当场给的不是一类"},
		{"transport", kernel.FailNone, true, "账上有故障而当场说没有 —— 这就是读错了发"},
	}
	for _, c := range cases {
		if got := driftOf(c.read, c.loc); got != c.want {
			t.Fatalf("driftOf(%q, %q) = %v，想要 %v（%s）", c.read, c.loc, got, c.want, c.why)
		}
	}
}

// 守卫不能只活在 stdout：页面必须把「当场给的 vs 读回的」摆出来，
// 并且漂移那一行要能红。红的那一半靠合成一行来验（正常路径永远等值，
// 不合成就只能证明它没坏，证明不了它会坏）。
func TestTraceShowsTheGuardAndCanBeRed(t *testing.T) {
	r := newRun(t, "full", true, "included", 20)
	r.tracePath = filepath.Join(t.TempDir(), "trace.html")
	r.loop()
	r.closeOut()
	h := string(mustRead(t, r.tracePath))
	if !strings.Contains(h, "当场给的 / 读回的") {
		t.Fatalf("页面没摆那一列")
	}
	if !strings.Contains(h, "漂移：<b>0</b>") {
		t.Fatalf("页面没说零漂移：%s", cut(h, 60))
	}
	if strings.Contains(h, `class="red">当场给的`) {
		t.Fatalf("正常路径上不该有红行")
	}
	if n := strings.Count(h, `<span class="pair">`); n != r.shot {
		t.Fatalf("等值行 %d 行，应为 %d 发", n, r.shot)
	}

	// 合成一行漂移：同一发当场说 transport、账上读回 http。
	r.drift = 1
	r.traces = append(r.traces, shotTrace{
		Call: 99, Value: "http", LocalFail: "transport", Drift: true,
		BasisFlag: "账上", Basis: "fail_kind", BasisCells: []string{"fail_kind"}, Action: noFailureAction,
	})
	if err := r.writeTrace(r.tracePath); err != nil {
		t.Fatalf("重写页面失败：%v", err)
	}
	h2 := string(mustRead(t, r.tracePath))
	if !strings.Contains(h2, `<span class="pair red">当场给的 transport ≠ 读回的 http`) {
		t.Fatalf("漂移那一行没红：见页面第 ④ 节")
	}
	if !strings.Contains(h2, "漂移：<b>1</b>") {
		t.Fatalf("页脚的漂移计数没跟着变")
	}
}
