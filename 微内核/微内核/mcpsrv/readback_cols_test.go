package mcpsrv

// readback_cols_test.go 守的是 A2（10-25）：`kernel.read_back` 的工具描述一直写着
// 「把某一发、某一侧的格子读回来（键名、人话、出处、指纹）」，而 10-18 二次修改闭环研究
// （报告/二次修改闭环-1020）量到的现场是**只印两栏** —— `RecordedCell` 里 `Source` 与
// `PayloadSHA` 都有值，就是没上屏。承诺四栏、印两栏，读账的人只能把"看不见"读成"没有"。
//
// 这一族同时守三态不塌：出处为空、原像为空、指纹为空是三件不同的事，
// 一句都不许塌成另一句，更不许塌成空串或 0。

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"

	"agentkernel"
)

var hex64 = regexp.MustCompile(`[0-9a-f]{64}`)

// TestReadBackShowsSourceAndFingerprint —— 承诺的四栏真的都在屏上了。
// 判的不是"字符串里有过这两个词"，而是**每一格那一行**都带行尾那两栏，
// 且带出处的那一格印的是账上那个值（不是渲染器自己编的话）。
func TestReadBackShowsSourceAndFingerprint(t *testing.T) {
	s := New()
	if txt, isErr := toolText(t, s, 1, "kernel.record_call", legalCall(t, "", "")); isErr {
		t.Fatalf("夹具那一发被落账拒了，后面没得判：%s", txt)
	}
	txt := readBackOutput(t, s)
	lines := strings.Split(txt, "\n")
	if len(lines) < 3 {
		t.Fatalf("读回来的正文连表头加格子都不到三行，这一判没得判：%q", txt)
	}
	// 第 1 行逐字不许动：下游两把尺子按这句话匹配（cmd/agentd/agentd_test.go、编程Agent 的接缝）。
	if !strings.HasPrefix(lines[0], "session=presence-test 的第 41 发 output 侧共 ") ||
		!strings.Contains(lines[0], "其中缺席 [") {
		t.Errorf("read_back 的第 1 行形状变了（下游按这句话匹配）：%q", lines[0])
	}
	cells := 0
	for _, ln := range lines[1:] {
		if !strings.Contains(ln, CellColsMark) {
			continue
		}
		cells++
		if !strings.HasSuffix(ln, "⟧") {
			t.Errorf("渲染栏没闭合，读的人没法切：%q", ln)
		}
	}
	if cells != len(lines)-1 {
		t.Errorf("%d 行里有 %d 行没带出处/指纹（工具描述承诺四栏，少一格就是一格）", len(lines)-1, len(lines)-1-cells)
	}
	// usage_in 是转述档，账上 Source 就是 "endpoint-usage"（legalCall 里那份）。
	if l := readBackLine(t, txt, "usage_in"); !strings.Contains(l, CellColsMark+"endpoint-usage") {
		t.Errorf("转述格没把账上的出处原样念出来：%q", l)
	}
	// 有原像在手 ⇒ 必须给一枚 64 位十六进制，并明说第三方可重算。
	if l := readBackLine(t, txt, "usage_in"); !hex64.MatchString(l) || !strings.Contains(l, "原像在手") {
		t.Errorf("这一本是有原像的，指纹栏却不说原像在手：%q", l)
	}
	// 缺席格：活账里它也带原像（落账那一路给每一格都是 json.Marshal(Value)），
	// 所以这里要看见"原像在手"，不许看见"无原像"—— 那会说反话。
	if l := readBackLine(t, txt, "chunks"); !strings.Contains(l, "原像在手") {
		t.Errorf("缺席格的原像栏说反了：%q", l)
	}
	if l := readBackLine(t, txt, "chunks"); strings.Contains(l, "无原像") {
		t.Errorf("这一本明明写了原像，缺席格却报无原像：%q", l)
	}
	// 指纹必须是**这一格原像的 sha256**，不是渲染器随手打的一串：自己重算一遍再比。
	for _, c := range kernel.CellsOf(s.ledger.Docs(), "presence-test", 41, kernel.KindModelOutput) {
		want := sha256hex(c.Payload)
		l := readBackLine(t, txt, c.Key)
		if !strings.Contains(l, want) {
			t.Errorf("%s：屏上那枚指纹与按同一规则重算出来的对不上（要 %s）：%q", c.Key, want, l)
		}
	}
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// TestFingerprintColumnKeepsTheThreeStatesApart —— 同一句"没有"不许盖住三件不同的事。
// 直接喂 `cellCols` 手工格值：真实链上凑不齐全部四形（现场只出得了两形），
// 而这一判守的就是"四形各长各的样"，所以按形状表判，反面也要判（不许塌成空串、不许印 0）。
func TestFingerprintColumnKeepsTheThreeStatesApart(t *testing.T) {
	const sha = "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b"
	cases := []struct {
		name string
		c    kernel.RecordedCell
		want string // 必须出现
		deny string // 必须不出现（防两形塌成一句）
	}{
		{"有原像在手", kernel.RecordedCell{
			Basis: kernel.BasisMeasured, PayloadSHA: sha, Payload: []byte("42")}, "原像在手", ""},
		{"指纹在链上、原像没编进去", kernel.RecordedCell{
			Basis: kernel.BasisMeasured, PayloadSHA: sha}, "无原像", "本来就没有原像"},
		{"缺席格在活账里也有原像（Value 序列化的那段字节）", kernel.RecordedCell{
			Basis: kernel.BasisAbsent, PayloadSHA: sha, Payload: []byte(`"非流式"`), Claim: "—（没量到，不是 0）"},
			"原像在手", "无原像"},
		{"链上没带 payload_sha256", kernel.RecordedCell{
			Basis: kernel.BasisMeasured}, "无指纹", sha},
	}
	seen := map[string]string{}
	for _, c := range cases {
		got := cellCols(c.c)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s：这一形该说「%s」，实际：%q", c.name, c.want, got)
		}
		if c.deny != "" && strings.Contains(got, c.deny) {
			t.Errorf("%s：这一形不许说「%s」（两形塌成一句了）：%q", c.name, c.deny, got)
		}
		// 三态不塌的下界：值栏不许是空串，也不许用 0 冒充"量到了 0"。
		i := strings.Index(got, "指纹=")
		if i < 0 {
			t.Errorf("%s：这一形没印指纹栏：%q", c.name, got)
			continue
		}
		tail := got[i+len("指纹=") : len(got)-1]
		if strings.TrimSpace(tail) == "" || strings.TrimSpace(tail) == "0" {
			t.Errorf("%s：指纹栏空着或写成 0 了：%q", c.name, got)
		}
		// 每一形都得是**独有**的一句话，共用一句就是塌了。
		if prev, dup := seen[got]; dup {
			t.Errorf("%s 与 %s 印出了逐字相同的句子 —— 那三态就并成一态了：%q", prev, c.name, got)
		}
		seen[got] = c.name
		t.Logf("%-26s %s", c.name, strings.TrimPrefix(got, "  "))
	}
	// 出处那两形同理：空 ≠ 丢了，必须点明"本来就不要求"。
	with := cellCols(kernel.RecordedCell{Basis: kernel.BasisRelayed,
		Source: "endpoint-usage", PayloadSHA: sha, Payload: []byte("7")})
	without := cellCols(kernel.RecordedCell{Basis: kernel.BasisMeasured, PayloadSHA: sha, Payload: []byte("1")})
	if !strings.Contains(with, CellColsMark+"endpoint-usage") {
		t.Errorf("转述格的出处没原样上屏：%q", with)
	}
	if strings.Contains(without, CellColsMark+" ") || !strings.Contains(without, "本来就不要求") {
		t.Errorf("measured 档本来就不要求出处，这一格得说清「本来就没有这一栏」，不许空着：%q", without)
	}
}

// TestOmittedJournalReadsBackAsNoPreimage —— 从「不写原像」那本读回来，必须报无原像。
// 走的是现场那条路：真记一发 → encode(omitted) → 换进新实例 → read_back。
// 这一判还钉住"不得报空 hash"：指纹本身在链上，读回来还要能看见它。
func TestOmittedJournalReadsBackAsNoPreimage(t *testing.T) {
	src := New()
	if txt, isErr := toolText(t, src, 1, "kernel.record_call", legalCall(t, "", "")); isErr {
		t.Fatalf("夹具那一发被落账拒了：%s", txt)
	}
	encoded, isErr := toolText(t, src, 2, "kernel.encode_journal", `{"payload":"omitted"}`)
	if isErr {
		t.Fatalf("编码被拒：%s", encoded)
	}
	dst := New()
	if back, isErr := toolText(t, dst, 3, "kernel.load_journal", `{"text":`+escStr(t, encoded)+`}`); isErr {
		t.Fatalf("自家编的账回灌被拒：%s", back)
	}
	txt := readBackOutput(t, dst)
	nNo, nYes := 0, 0
	for _, ln := range strings.Split(txt, "\n")[1:] {
		if !strings.Contains(ln, CellColsMark) {
			continue
		}
		if strings.Contains(ln, "原像在手") {
			nYes++
		}
		if strings.Contains(ln, "无原像") || strings.Contains(ln, "本来就没有原像") {
			nNo++
		}
		if !hex64.MatchString(ln) {
			t.Errorf("这本账的链上明明有 payload_sha256，读回来却不报指纹：%q", ln)
		}
	}
	if nYes > 0 {
		t.Errorf("按不写原像编的一本，读回来还喊「原像在手」——%d 行在撒谎", nYes)
	}
	if nNo == 0 {
		t.Errorf("按不写原像编的一本，读回来没有一行说「无原像」（调用方会以为手里有原像）：%q", txt)
	}
	t.Logf("omitted 往返后读回：%d 行带渲染栏，其中 %d 行如实报了无原像/本来就没有，0 行报原像在手", len(strings.Split(txt, "\n"))-1, nNo)
}

// TestStripCellColsReturnsThePreA2LineVerbatim —— 渲染栏是"可切的"，不是糊在原话里的。
// 下游两把尺子都按「第一个空格前是档名、其后整段都是原话」切（cmd/agentd/rpc.go、
// 编程 Agent 的接缝同形件）；`cmd/agentd/loop.go:763` 还拿原话与重算的 sha256 **逐字相等**
// 判能不能带历史续跑。所以切完必须逐字回到加栏之前那一行，多一个空格都算打断别人。
func TestStripCellColsReturnsThePreA2LineVerbatim(t *testing.T) {
	const sha = "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b"
	for _, c := range []kernel.RecordedCell{
		{Key: "usage_in", Basis: kernel.BasisRelayed, Claim: "7（转述自 endpoint-usage）",
			Source: "endpoint-usage", PayloadSHA: sha, Payload: []byte("7")},
		{Key: "chunks", Basis: kernel.BasisAbsent, Claim: "—（没量到，不是 0）：非流式",
			PayloadSHA: sha},
		{Key: "model_says_sha256", Basis: kernel.BasisMeasured, Claim: sha, PayloadSHA: sha,
			Payload: []byte("答案原文")},
	} {
		old := "  " + c.Key + "   " + c.Basis.String() + " " + c.Claim
		withCols := old + cellCols(c)
		if got := StripCellCols(withCols); got != old {
			t.Errorf("%s：切完回不到加栏前那一行（下游逐字比会假红）\n要：%q\n得：%q", c.Key, old, got)
		}
		if _, _, ok := cellHasColsButClaimClean(withCols, c.Key); !ok {
			t.Errorf("%s：这一行连键名都没切出来，夹具坏了", c.Key)
		}
		// 幂等：对没有栏的行不许动它（旧交付件/旧账本的回话也是这个入口）。
		if StripCellCols(old) != old {
			t.Errorf("%s：对不带渲染栏的行动了字节：%q", c.Key, StripCellCols(old))
		}
	}
}

// cellHasColsButClaimClean 用**下游那一套切法**（第一个空格前=档名、其后整段=原话）
// 走一遍，再按消费者该做的那样切掉渲染栏，断言原话里没有 ⟦ —— 这一条是给"两栏会不会糊进原话"
// 上的一道现场对照，不另起一套切法（另起一套就量不到真下游的错了）。
func cellHasColsButClaimClean(line, key string) (string, string, bool) {
	t := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(t, key) {
		return "", "", false
	}
	rest := t[len(key):]
	f := strings.Fields(rest)
	if len(f) == 0 {
		return "", "", false
	}
	claim := strings.TrimSpace(StripCellCols(rest[len(f[0]):]))
	if strings.Contains(claim, CellColsMark) || strings.Contains(claim, "指纹=") {
		return f[0], claim, false
	}
	return f[0], claim, true
}

// readBackOutput 与 inject_test.go 里那个 readBackInput 对称：读输出侧那一本。
func readBackOutput(t *testing.T, s *Server) string {
	t.Helper()
	txt, isErr := toolText(t, s, 91, "kernel.read_back", `{"session":"presence-test","call":41,"side":"output"}`)
	if isErr {
		t.Fatalf("read_back 自己报错了：%s", txt)
	}
	return txt
}
