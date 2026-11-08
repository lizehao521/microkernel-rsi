package mcpsrv

// gate_shell_test.go 守「闸门拦了几次」这一格在**工具面**上的下落。
//
// 这一族的现场在 10-18：一个外部调用方被预检拦了 20 多次，回来问 `kernel.tally`，
// 四栏全是 0 —— 拦是拦了，账面上一件痕迹都没有，唯一 KPI 就此答不出。
// 所以这里的判据重点是两头都要有：故意错 10 次必须数到 10，全过 10 次必须还是 0。

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"agentkernel"
)

func grabCount(t *testing.T, txt, anchor string) int {
	t.Helper()
	m := regexp.MustCompile(regexp.QuoteMeta(anchor) + `[^0-9]*(\d+)`).FindStringSubmatch(txt)
	if m == nil {
		t.Fatalf("tally 回话里找不到 %q 后面那个数：%s", anchor, txt)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("%q 后面那个数解不开：%v", anchor, err)
	}
	return n
}

func tallyOf(t *testing.T, s *Server) string {
	t.Helper()
	txt, isErr := toolText(t, s, 900, "kernel.tally", "")
	if isErr {
		t.Fatalf("tally 自己报错了：%s", txt)
	}
	return txt
}

// badContentBytes 造一发**形状合法、账不自洽**的预检：段表相加 20，内容总字节填 19。
// 它必须走到内核判据才被拒 —— 用"整块没给"当这一栏的证据，测的就不是判据层了。
func badContentBytes(t *testing.T) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(legalCall(t, "", "")), &m); err != nil {
		t.Fatalf("夹具解不开：%v", err)
	}
	m["input"].(map[string]any)["ContentBytes"] = 19
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("装不回：%v", err)
	}
	return string(b)
}

func TestPrecheckRejectionsAreCountedSeparately(t *testing.T) {
	s := New()
	// 反臂（先跑，否则"永远拒"照样绿）：过了预检的那几发，两栏都不许动。
	for i := 0; i < 10; i++ {
		if txt, isErr := toolText(t, s, 10+i, "kernel.check_call", legalCall(t, "", "")); isErr {
			t.Fatalf("完整的一发被预检拒了，后面没得判：%s", txt)
		}
	}
	txt := tallyOf(t, s)
	if n := grabCount(t, txt, "被内核判据拒"); n != 0 {
		t.Errorf("过了 10 次预检，那一栏却报 %d —— 把「检查过」念成「拒了」就是这只计数器的死法", n)
	}
	if n := grabCount(t, txt, "预检在门口被挡下"); n != 0 {
		t.Errorf("门口那一栏跟着动了：%d（合法的一发一格都没挡）", n)
	}

	// 证红：故意错 10 次 ⇒ 那一栏必须是 10。
	for i := 0; i < 10; i++ {
		txt, isErr := toolText(t, s, 50+i, "kernel.check_call", badContentBytes(t))
		if !isErr {
			t.Fatalf("第 %d 发（内容字节与段表相加不符）竟被预检放过：%s", i+1, txt)
		}
		if !strings.Contains(txt, "两个数各算了一遍") {
			t.Errorf("第 %d 发拒了但拒的不是那一条判据：%s", i+1, txt)
		}
	}
	txt = tallyOf(t, s)
	if n := grabCount(t, txt, "被内核判据拒"); n != 10 {
		t.Errorf("故意错 10 次预检，那一栏报 %d（改前它报 0 —— 那正是外人撞的洞）", n)
	}
	// 两栏不相加：落账那一路一次都没走，所以「被拒的写入」必须还是 0。
	if n := grabCount(t, txt, "被拒的写入"); n != 0 {
		t.Errorf("预检拒的 10 发漏进了落账那一栏（%d）—— 两栏一合就答不出「谁拒的」", n)
	}
	if e := s.Entries(); e != 0 {
		t.Errorf("预检写了 %d 条进链 —— 预检一条都不该写", e)
	}

	// 门口那一栏：整块没给，走不到内核判据，所以只能进门口那一笔。
	for i := 0; i < 3; i++ {
		if _, isErr := toolText(t, s, 200+i, "kernel.check_call", legalCall(t, "input", "Notice")); !isErr {
			t.Fatal("Notice 整块没给竟被预检收下")
		}
	}
	txt = tallyOf(t, s)
	if n := grabCount(t, txt, "预检在门口被挡下"); n != 3 {
		t.Errorf("门口挡了 3 次预检，那一栏报 %d", n)
	}
	if n := grabCount(t, txt, "被内核判据拒"); n != 10 {
		t.Errorf("门口那 3 次挤进了内核那一栏（%d）—— 它们根本没走到判据", n)
	}
	if !strings.Contains(txt, "这一程完整可数") {
		t.Errorf("活进程自己数到的数该标成完整可数，得到：%s", txt)
	}
}

func TestGateCountsSurviveEncodeAndLoad(t *testing.T) {
	s := New()
	for i := 0; i < 10; i++ {
		if _, isErr := toolText(t, s, 10+i, "kernel.check_call", badContentBytes(t)); !isErr {
			t.Fatal("夹具那一发该被预检拒")
		}
	}
	for i := 0; i < 4; i++ {
		if _, isErr := toolText(t, s, 100+i, "kernel.check_call", legalCall(t, "input", "Notice")); !isErr {
			t.Fatal("整块没给该被门口挡下")
		}
	}
	enc, isErr := toolText(t, s, 300, "kernel.encode_journal", `{"payload":"omitted"}`)
	if isErr {
		t.Fatalf("编码被拒：%s", enc)
	}
	if !strings.Contains(enc, "1 行闸门计数") {
		t.Errorf("编出去的字节里没有计数那一行，KPI 还是活不过落盘：%s", firstLine(enc))
	}

	// 新进程把它读回来：六笔数必须跟着账走，而不是跟着进程走。
	s2 := New()
	body, _ := json.Marshal(map[string]any{"text": enc})
	got, isErr := toolText(t, s2, 2, "kernel.load_journal", string(body))
	if isErr {
		t.Fatalf("带计数的账本载入被拒：%s", got)
	}
	if !strings.Contains(got, "计数栏：有") || !strings.Contains(got, "拒预检 10") {
		t.Errorf("载入的回话没把计数栏的下落说清楚：%s", got)
	}
	txt := tallyOf(t, s2)
	if n := grabCount(t, txt, "被内核判据拒"); n != 10 {
		t.Errorf("读回来的进程里那一栏变成 %d（改前这里是 0，而且那个 0 长得像量到的）", n)
	}
	if n := grabCount(t, txt, "预检在门口被挡下"); n != 4 {
		t.Errorf("门口那四笔没跟着回来：%d", n)
	}
	if !strings.Contains(txt, "这一程完整可数") {
		t.Errorf("带着计数栏读回来该是完整可数，得到：%s", txt)
	}

	// 反臂一：同一个文件**去掉计数行**（老格式的等价物）读回一个空进程 ⇒ 必须报"不可知"，
	// 那一栏的数会显示 0，但口径必须说清这 0 不是量到的。
	plain, err := kernel.MarshalJournal(nil, kernel.PayloadOmitted)
	if err != nil {
		t.Fatalf("老格式编码被拒：%v", err)
	}
	bodyNoGate, _ := json.Marshal(map[string]any{"text": string(plain)})
	s3 := New()
	if got, isErr := toolText(t, s3, 2, "kernel.load_journal", string(bodyNoGate)); isErr {
		t.Fatalf("老格式载入被拒：%s", got)
	} else if strings.Contains(got, "计数栏：有") {
		t.Errorf("明明没有计数行，却把这栏说成「有」：%s", got)
	}
	if txt := tallyOf(t, s3); !strings.Contains(txt, "不可知") {
		t.Errorf("老格式读回来必须报不可知，得到：%s", txt)
	}

	// 反臂二：一本**数到 10 而链上还一条都没写**的活账，要载入一份没有计数栏的文件 ——
	// 这道闸原来只看"链上有几条"，所以它会放过去，把那 10 静悄悄丢掉（本次实测撞到的洞）。
	// 现在它必须被拦住，并且点名会丢什么。
	got4, isErr := toolText(t, s, 400, "kernel.load_journal", string(bodyNoGate))
	if !isErr {
		t.Fatalf("带计数的活账被一份没有计数栏的文件静悄悄换掉了：%s", got4)
	}
	if !strings.Contains(got4, "拒预检 10") || !strings.Contains(got4, "不可知") {
		t.Errorf("拒因该点名会丢的那笔，并说清换完变成「不可知」：%s", got4)
	}
	if n := grabCount(t, tallyOf(t, s), "被内核判据拒"); n != 10 {
		t.Errorf("被拒的载入把数动了：%d", n)
	}
	// 点名要丢之后才动手：换成那份没有计数栏的文件，数归 0 而口径必须说"不可知"（不是"量到 0"）。
	bodyAck, _ := json.Marshal(map[string]any{"text": string(plain), "acknowledge": "replace"})
	if got5, isErr := toolText(t, s, 401, "kernel.load_journal", string(bodyAck)); isErr {
		t.Fatalf("点名替换后仍被拒：%s", got5)
	}
	txt = tallyOf(t, s)
	if n := grabCount(t, txt, "被内核判据拒"); n != 0 {
		t.Errorf("换了别的账之后那一栏还留着上一本账的数：%d", n)
	}
	if !strings.Contains(txt, "不可知") {
		t.Errorf("清零之后口径必须说不可知，得到：%s", txt)
	}
	// 清零之后再拦一次 ⇒ 从这一刻起是本程量到的下界（Partial），不是不可知。
	if _, isErr := toolText(t, s, 410, "kernel.check_call", badContentBytes(t)); !isErr {
		t.Fatal("该拒的没拒")
	}
	if txt := tallyOf(t, s); !strings.Contains(txt, "只覆盖这一程") {
		t.Errorf("从不可知里数到第一笔，该升成只覆盖这一程，得到：%s", txt)
	}
}
