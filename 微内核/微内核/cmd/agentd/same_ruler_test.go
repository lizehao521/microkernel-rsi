package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"agentkernel/mcpsrv"
)

// 「与 mcpkernel 同一份判据」这句现在只是代码读起来像：宿主走 mcpsrv.New()+Serialize，
// mcpkernel 也走那一份。这一根针把它换成读数 —— 同一发喂两边（进程内 / 交付件里那枚 exe 走 stdio），
// 四句回话必须逐字一样。不一样就只有两种解释：宿主自己漂了一套，或者交付件落后于树。
// 两边都是各自新建的空账，也不碰 8797/8799（stdio 面不开端口）。
func TestSameRulerAsShippedKernel(t *testing.T) {
	exe := os.Getenv("AGENTKERNEL_SHIPPED_MCPKERNEL")
	if exe == "" {
		exe = findShippedKernel(t)
	}
	st, err := os.Stat(exe)
	if err != nil {
		t.Fatalf("交付件里的 mcpkernel 找不到（%s）：%v —— 这一根针就没跑，别当成过了", exe, err)
	}
	t.Logf("对账的两把尺子：进程内 mcpsrv  vs  %s（%d 字节，%s）",
		exe, st.Size(), st.ModTime().Format("2006-01-02 15:04"))

	in, body, err := (&assembly{
		system: systemPrompt, toolspec: toolSpec, user: userAsk,
	}).buildInput(1)
	if err != nil {
		t.Fatalf("装配：%v", err)
	}
	out := buildOutput(1, proposal{
		Text: "27 + 15 = 42", UsageIn: 40, UsageOut: 6, FinishSaid: true, FinishReason: "stop", ReportsModel: true,
	}, body)
	good := sides("同一把尺子", in, out)

	badIn := in
	badIn.Call = 0
	bad := sides("同一把尺子", badIn, out)

	local := newKernelRPC(0, false)
	remote := startShipped(t, exe)
	defer remote.close()

	cases := []struct {
		tool     string
		args     map[string]any
		wantErr  bool
		journal  bool // encode_journal 走"逐行比字段"，理由见 compareJournal
		rendered bool // read_back 走"只让开行尾渲染栏"，理由见 compareRenderedCols
	}{
		{"kernel.celltable", nil, false, false, false},
		{"kernel.check_call", good, false, false, false},
		{"kernel.record_call", good, false, false, false},
		{"kernel.check_call", bad, true, false, false},
		{"kernel.read_back", map[string]any{"session": "同一把尺子", "call": 1, "side": "output"}, false, false, true},
		{"kernel.declare_reader", map[string]any{"reader": "同一把尺子", "key": "content_bytes", "side": "input"}, false, false, false},
		{"kernel.readers_report", nil, false, false, false},
		{"kernel.encode_journal", map[string]any{"payload": "included"}, false, true, false},
		// load_journal 这一发故意喂进不去的文本：两边都必须**拒**且拒得逐字一样。
		// 不拿成功路径比是因为换账会把两边的账换成同一份文件，比出来的"一致"就没意义了。
		{"kernel.load_journal", map[string]any{"text": "这不是 JSONL，一行都不是"}, true, false, false},
		{"kernel.tally", nil, false, false, false},
		{"kernel.verify", nil, false, false, false},
	}
	for i, c := range cases {
		l, lerr := local.call(c.tool, c.args)
		r, rerr := remote.call(c.tool, c.args)
		if lerr != nil || rerr != nil {
			t.Fatalf("第 %d 项 %s：本地 %v / 交付件 %v", i+1, c.tool, lerr, rerr)
		}
		if strings.TrimSpace(l.Text) == "" || strings.TrimSpace(r.Text) == "" {
			t.Fatalf("第 %d 项 %s：有一侧回的是空话（本地 %q / 交付件 %q）—— 两句空话相等不算「逐字一样」",
				i+1, c.tool, l.Text, r.Text)
		}
		// 逐字一样只证明"两边说同一句话"，不证明那句话会挑出错的：第 3 项（发号写 0）两边都必须拒、
		// 其余必须放行。少了这一条，把交付件换成一枚永远放行的 exe、或两边一起报空，针照样绿。
		want := "放行"
		if c.wantErr {
			want = "拒"
		}
		if l.IsErr != c.wantErr {
			t.Fatalf("第 %d 项 %s：这一发本该%s，却判成 isError=%v\n本地   ：%s",
				i+1, c.tool, want, l.IsErr, firstLine(l.Text))
		}
		if l.IsErr != r.IsErr {
			t.Fatalf("第 %d 项 %s：isError 不一样（本地 %v / 交付件 %v）\n本地：%s\n交付件：%s",
				i+1, c.tool, l.IsErr, r.IsErr, firstLine(l.Text), firstLine(r.Text))
		}
		if c.journal {
			compareJournal(t, i+1, l.Text, r.Text)
		} else if c.rendered {
			compareRenderedCols(t, i+1, l.Text, r.Text)
		} else if l.Text != r.Text {
			t.Fatalf("第 %d 项 %s：同一发判出了两句话\n本地   ：%s\n交付件：%s", i+1, c.tool, clip(l.Text), clip(r.Text))
		}
		t.Logf("%d/%d %-18s isError=%-5v %s", i+1, len(cases), c.tool, l.IsErr, firstLine(l.Text))
	}
}

// journalDerived 是"时间戳的下游"。机制是量出来的，不是编的：这台机（Win 10）的墙钟刻度粗，
// 两个进程落在同一个刻度里时整串 ts 完全相同（实测见 0 行不同那几次），跨一个刻度就整串不同；
// 而 ts 喂进 hash ⇒ prev_hash / chain_head / gate_sha256 / seal 跟着不同，头行那句"多少个字节"也跟着不同。
// 除这五格与头行的字节数之外，任何一格不同都是判据真的不一样，红。
//
// 8 次连跑的分布：5 次 50 行原始字节不同、3 次全同 —— 两种都判绿，因为比的是内容字段。
// 这条一开始写成"逐字一样"，于是**同一份代码**跑出 3 红 3 绿的抖动针；抖动针比没针更糟（教人重跑到绿为止）。
var journalDerived = map[string]bool{"ts": true, "hash": true, "prev_hash": true, "chain_head": true, "gate_sha256": true, "seal": true}

var headBytes = regexp.MustCompile(`^\d+ 字节`)

// compareJournal 不比整段字节 —— 上面那串倒影让它注定不同，硬要比就只能加掩码，
// 而掩码是藏差异的地方。改成两件事：① 头行把"字节数"那一格让开，其余逐字一样；
// ② 每一行把五格倒影让开，其余字段（kind/claim/basis/source/refused/payload…）必须全等。
// 让开了什么都让开了，逐名打印出来 —— 缺口必须看得见。
func compareJournal(t *testing.T, idx int, a, b string) {
	t.Helper()
	la := strings.Split(strings.TrimRight(a, "\n"), "\n")
	lb := strings.Split(strings.TrimRight(b, "\n"), "\n")
	if len(la) != len(lb) {
		t.Fatalf("第 %d 项 encode_journal：行数不一样（本地 %d 行 / 交付件 %d 行）", idx, len(la), len(lb))
	}
	ha := headBytes.ReplaceAllString(la[0], "N 字节")
	hb := headBytes.ReplaceAllString(lb[0], "N 字节")
	if ha != hb {
		t.Fatalf("第 %d 项 encode_journal：头行除了字节数还有别的东西不一样\n本地   ：%s\n交付件：%s", idx, la[0], lb[0])
	}
	differing := 0
	allowed := map[string]int{}
	var tsSample [2]string
	if len(la) > 1 {
		var e1, e2 map[string]any
		_ = json.Unmarshal([]byte(la[1]), &e1)
		_ = json.Unmarshal([]byte(lb[1]), &e2)
		tsSample[0], tsSample[1] = fmt.Sprint(e1["ts"]), fmt.Sprint(e2["ts"])
	}
	for i := 1; i < len(la); i++ {
		if la[i] == lb[i] {
			continue
		}
		differing++
		var ma, mb map[string]any
		if err := json.Unmarshal([]byte(la[i]), &ma); err != nil {
			t.Fatalf("第 %d 项：本地第 %d 行不是 JSON：%v", idx, i, err)
		}
		if err := json.Unmarshal([]byte(lb[i]), &mb); err != nil {
			t.Fatalf("第 %d 项：交付件第 %d 行不是 JSON：%v", idx, i, err)
		}
		keys := map[string]bool{}
		for k := range ma {
			keys[k] = true
		}
		for k := range mb {
			keys[k] = true
		}
		for k := range keys {
			if journalDerived[k] {
				if !reflect.DeepEqual(ma[k], mb[k]) {
					allowed[k]++
				}
				continue
			}
			if !reflect.DeepEqual(ma[k], mb[k]) {
				t.Fatalf("第 %d 项：第 %d 行的 %q 真的不一样（%v vs %v）—— 这一格不在时间戳的下游，掩码不许盖它",
					idx, i, k, ma[k], mb[k])
			}
		}
	}
	t.Logf("第 %d 项 encode_journal：%d 行逐行比过，%d 行原始字节不同；头行（去字节数）与所有非倒影字段全等；"+
		"让开的倒影逐名：%v｜两边的 ts 样本：%s vs %s",
		idx, len(la)-1, differing, allowed, tsSample[0], tsSample[1])
}

// compareRenderedCols 比 read_back 的两把尺子。
//
// 10-25 起，外壳把工具描述一直承诺的「出处、指纹」两栏补上了屏（挂在每一行行尾，
// 形状是 ⟦出处=… 指纹=…⟧）。交付件那枚 exe 是 10-19 编的、没有这两栏，所以整段字节必然不同 ——
// 但「不一样」有两种解释，这一族只有第二种才该红：
//
//	①渲染栏一栏多一栏少（**已知**：交付件落后于树，重跑 打包.py 之后自己消失）；
//	②键名 / 档 / 原话 / 形状标签任一不同（那是判据真的漂了，必须红）。
//
// 所以这里不比整段，改成三条：① 两边先按记号把渲染栏切掉，剩下的部分必须逐字一样；
// ② 切完之后行数、每一行都要对得上（记号本身不许盖住一行本来就不同的）；
// ③ 若两边**都**带栏（交付件重打包之后就是这一形），退回整行逐字比 —— 掩码不许长期当遮眼布。
// 让开了几行、哪一边让的，全部 t.Logf 印出来，缺口必须看得见。
func compareRenderedCols(t *testing.T, idx int, a, b string) {
	t.Helper()
	msg, bad := renderedColsVerdict(a, b)
	if bad != "" {
		t.Fatalf("第 %d 项 read_back：%s", idx, bad)
	}
	t.Logf("第 %d 项 %s", idx, msg)
}

// renderedColsVerdict 是那三条规则的本体，抽出来是为了能**单独**拿假回话喂它：
// 掩码这类东西最怕"顺手把什么都让开"，而真实两边今天只差渲染栏，试不出它有没有牙。
// 返回 (可以印出来的结论, 该红的罪名)；红时第二个返回值非空。
func renderedColsVerdict(a, b string) (string, string) {
	la := strings.Split(strings.TrimRight(a, "\n"), "\n")
	lb := strings.Split(strings.TrimRight(b, "\n"), "\n")
	if len(la) != len(lb) {
		return "", fmt.Sprintf("行数不一样（本地 %d 行 / 交付件 %d 行）—— 渲染栏不该改变行数", len(la), len(lb))
	}
	nA, nB := 0, 0
	for i := range la {
		if strings.Contains(la[i], mcpsrv.CellColsMark) {
			nA++
		}
		if strings.Contains(lb[i], mcpsrv.CellColsMark) {
			nB++
		}
		sa, sb := mcpsrv.StripCellCols(la[i]), mcpsrv.StripCellCols(lb[i])
		if sa != sb {
			return "", fmt.Sprintf("第 %d 行除了行尾渲染栏还有别的东西不一样\n本地   ：%s\n交付件：%s",
				i+1, clip(sa), clip(sb))
		}
	}
	switch {
	case nA == 0 && nB == 0:
		return "", fmt.Sprintf("两边都没有出处/指纹那两栏 —— 工具描述承诺四栏，这条掩码成了摆设（%q）", clip(la[0]))
	case nA != nB && nB != 0:
		return "", fmt.Sprintf("带栏的行数两边都有但不一样（本地 %d / 交付件 %d）—— "+
			"只允许「交付件一行都没带」这一形（那叫落后，不叫漂）", nA, nB)
	case nA == nB:
		for i := range la {
			if la[i] != lb[i] {
				return "", fmt.Sprintf("第 %d 行：两边**都**带栏了还不同 —— 这一形没有落后的余地，逐字比\n本地   ：%s\n交付件：%s",
					i+1, clip(la[i]), clip(lb[i]))
			}
		}
		return fmt.Sprintf("%d 行全等（两边都带渲染栏，逐字比，掩码没生效）", len(la)), ""
	default:
		return fmt.Sprintf("本地 %d 行带渲染栏、交付件 0 行（那枚 exe 是 10-19 编的，还没这两栏）—— "+
			"切掉之后 %d 行逐字一样；重跑 打包.py 之后自动退回整行逐字比", nA, len(la)), ""
	}
}

// TestRenderedColsMaskOnlyLetsTheRenderedColumnsGo —— 上面那条掩码的牙。
// 真实两边今天只差渲染栏，喂不出"判据漂了"那一形，所以这里拿手工拼的回话逐形判：
// 该放过的只有一种（本地带栏 / 交付件没带），其余每一形都必须点名红。
// 反臂（叶子）：把 `renderedColsVerdict` 的 case nA == nB 那一支整段去掉（= 只要切完相等就放行），
// 「两边都带栏但出处不同」那几形就全被吞掉 —— 这一根针必须红（臂登记在 kprobe，见 NEEDLES 里 r1）。
func TestRenderedColsMaskOnlyLetsTheRenderedColumnsGo(t *testing.T) {
	const sha = "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b"
	cols := func(src string) string { return "  " + mcpsrv.CellColsMark + src + "⟧" }
	head := "session=S 的第 1 发 output 侧共 2 格，其中缺席 []："
	cases := []struct {
		name string
		a, b string
		want string // "" = 该放过；否则=罪名里必须出现的关键词
	}{
		{"落后一形：本地两栏齐全、交付件一行都没带 ⇒ 放过（缺口由 Logf 印出来）",
			head + "\ncell measured 42" + cols("—（measured 档本来就不要求） 指纹="+sha) +
				"\nother measured 7" + cols("转述自对端 指纹="+sha),
			head + "\ncell measured 42\nother measured 7", ""},
		{"原话漂了（同一格 claim 不同）——切完还不同，必须红",
			head + "\ncell measured 42" + cols("出处 手") + "\nother measured 7" + cols("出处 手"),
			head + "\ncell measured 43" + "\nother measured 7", "别的东西不一样"},
		{"档漂了（measured vs estimated）必须红",
			head + "\ncell measured 42" + cols("出处 手"),
			head + "\ncell estimated 42", "别的东西不一样"},
		{"形状标签漂了必须红（⚠ 那一串也是判据，不是渲染）",
			head + "\ncell measured 42   ⚠有指令形状" + cols("出处 手"),
			head + "\ncell measured 42", "别的东西不一样"},
		{"行数漂了必须红（少一格就是一格）",
			head + "\ncell measured 42" + cols("甲") + "\nother measured 7" + cols("乙"),
			head + "\ncell measured 42", "行数不一样"},
		{"两边都没有栏 ⇒ 掩码是摆设，必须红",
			head + "\ncell measured 42\nother measured 7",
			head + "\ncell measured 42\nother measured 7", "成了摆设"},
		{"两边都带栏但出处不同 ⇒ 这一形没有落后的余地，必须红",
			head + "\ncell measured 42" + cols("手抄") + "\nother measured 7" + cols("指纹 "+sha),
			head + "\ncell measured 42" + cols("对端原话") + "\nother measured 7" + cols("无"), "带栏了还不同"},
		{"两边都带栏但**带的行数**不同 ⇒ 只允许「对面一行都没带」那一形，必须红",
			head + "\ncell measured 42" + cols("甲") + "\nother measured 7" + cols("乙"),
			head + "\ncell measured 42" + cols("甲") + "\nother measured 7", "只允许"},
	}
	for _, c := range cases {
		msg, bad := renderedColsVerdict(c.a, c.b)
		if c.want == "" {
			if bad != "" {
				t.Errorf("%s：这一形是该放过的（只差渲染栏），却报了「%s」", c.name, bad)
			}
			t.Logf("放过：%s ｜ 印出来的缺口：%s", c.name, msg)
			continue
		}
		if bad == "" {
			t.Errorf("%s：这一形判据真的不一样，掩码却放它过去了（结论=%s）—— 掩码没牙", c.name, msg)
			continue
		}
		if !strings.Contains(bad, c.want) {
			t.Errorf("%s：红了，但罪名没指着该指的那一条（要含 %q，实际：%s）", c.name, c.want, bad)
			continue
		}
		t.Logf("咬住：%s ｜ 罪名：%s", c.name, firstLine(bad))
	}
}

func clip(s string) string {
	if len(s) > 400 {
		return s[:400] + " …（截到这里，全长远在这之外）"
	}
	return s
}

type shipped struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	id   int
	t    *testing.T
	done chan struct{}
}

func startShipped(t *testing.T, exe string) *shipped {
	t.Helper()
	cmd := exec.Command(exe)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("交给付件接 stdin：%v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("交给付件接 stdout：%v", err)
	}
	cmd.Stderr = io.Discard // 横幅走 stderr，与本测试无关
	if err := cmd.Start(); err != nil {
		t.Fatalf("起交付件：%v", err)
	}
	return &shipped{cmd: cmd, in: stdin, out: bufio.NewReader(stdout), t: t, done: make(chan struct{})}
}

func (s *shipped) call(tool string, args map[string]any) (reply, error) {
	s.id++
	if args == nil {
		args = map[string]any{}
	}
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": s.id, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		return reply{}, err
	}
	if _, err := s.in.Write(append(raw, '\n')); err != nil {
		return reply{}, fmt.Errorf("写不进交付件那侧：%v", err)
	}
	line := make(chan string, 1)
	go func() {
		l, err := s.out.ReadString('\n')
		if err != nil {
			line <- ""
			return
		}
		line <- l
	}()
	select {
	case l := <-line:
		if l == "" {
			return reply{}, fmt.Errorf("交付件没回帧（%s）", tool)
		}
		var msg struct {
			Result *struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
			Error *struct{ Message string } `json:"error"`
		}
		if err := json.Unmarshal([]byte(l), &msg); err != nil {
			return reply{}, fmt.Errorf("%s 的回帧解不开：%v", tool, err)
		}
		if msg.Error != nil {
			return reply{Text: msg.Error.Message, IsErr: true}, nil
		}
		if msg.Result == nil || len(msg.Result.Content) == 0 {
			return reply{}, fmt.Errorf("%s 回了但没正文：%s", tool, l)
		}
		return reply{Text: msg.Result.Content[0].Text, IsErr: msg.Result.IsError}, nil
	case <-time.After(10 * time.Second):
		return reply{}, fmt.Errorf("等 %s 回帧超时（交付件那侧卡住了？）", tool)
	}
}

func (s *shipped) close() {
	_ = s.in.Close()
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = s.cmd.Process.Kill()
	}
}

// findShippedKernel 往上找交付件里那枚 mcpkernel，不数 ../ 的层数：
// 数层数这件事本身就是个会随目录移动而说谎的常量。
func findShippedKernel(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("拿当前目录：%v", err)
	}
	for i := 0; i < 7; i++ {
		p := filepath.Join(dir, "构建-1017", "agentkernel", "bin", "mcpkernel.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join("..", "构建-1017", "agentkernel", "bin", "mcpkernel.exe")
}

// TestHostClaimDropsTheRenderedCols —— 宿主取「原话」那一步的牙（10-25，A2 的下游）。
//
// `read_back` 从 10-25 起每行行尾多两栏（出处、指纹）。宿主 `cellFromReadBack` 切出来的
// 必须是**原话本身**，不许把渲染栏一起当成人话 —— 那些人话会进轨迹页、进 KPI 的依据栏，
// 一串 ⟦出处=…⟧ 混进去，读的人就分不出「账上那句」和「外壳那句」。
// 这一判不依赖交付件（纯函数），所以它比 TestSameRulerAsShippedKernel 更便宜、也不会被掩码放过。
func TestHostClaimDropsTheRenderedCols(t *testing.T) {
	const sha = "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b"
	text := "session=S 的第 1 发 output 侧共 2 格，其中缺席 []：\n" +
		"usage_in              relayed   40+6  ⟦出处=endpoint-usage 指纹=" + sha + "（原像在手）⟧\n" +
		"fail_kind             measured  HTTP 429   ⟦出处=本地观察 指纹=" + sha + "⟧\n" +
		"fail_kind_detail      measured  too many requests  ⟦出处=对端原话 指纹=" + sha + "⟧"

	basis, claim, ok := cellFromReadBack(text, "usage_in")
	if !ok {
		t.Fatalf("这一格没读到：%q", text)
	}
	if basis != "relayed" {
		t.Errorf("出处栏读成了 %q（该是档名 relayed）", basis)
	}
	if claim != "40+6" {
		t.Errorf("原话里混进了渲染栏：%q（该逐字是 40+6）", claim)
	}
	if strings.Contains(claim, "⟦") || strings.Contains(claim, "指纹") {
		t.Errorf("宿主把外壳那两栏当成人话了：%q", claim)
	}

	// 前缀撞车那一形跟着守：新栏加在行尾，切法一动就容易让 fail_kind 命中 fail_kind_detail 那一行。
	if _, c2, ok2 := cellFromReadBack(text, "fail_kind"); !ok2 || c2 != "HTTP 429" {
		t.Errorf("fail_kind 读串行了（前缀撞车那一族）：%q ok=%v", c2, ok2)
	}
	if _, c3, ok3 := cellFromReadBack(text, "fail_kind_detail"); !ok3 || c3 != "too many requests" {
		t.Errorf("fail_kind_detail 读错了：%q ok=%v", c3, ok3)
	}

	// 反臂的下界：交付件那种**没有渲染栏**的老行，切出来还是同一句原话。
	old := "usage_in              relayed   40+6"
	if _, c4, ok4 := cellFromReadBack(old, "usage_in"); !ok4 || c4 != "40+6" {
		t.Errorf("老格式那一行切不开了（两代格式必须都读得出同一句原话）：%q ok=%v", c4, ok4)
	}
}
