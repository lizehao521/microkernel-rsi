package mcpsrv

// load_journal_door_test.go 守看板 #67 那一格：`kernel.load_journal` 曾经用宽松解码
// （`args[T]` 里那句 `_ = json.Unmarshal`），于是
//
//	{"journal":<合法非空账>,"acknowledge":"replace"}
//	  → 键名不是 text，Text 解成空串
//	  → 空串喂 LoadLedger 得到一本**合法的 0 条空账**
//	  → 那道「要点名 replace 才换」的闸因为点名了而放行
//	  → 整本账被静默换成空账，回话 isError=false。
//
// 防止静默替换的那个字段本身成了通行证。10-25 这一族修在两个地方：解码那一步不许丢掉错误
// （`decodeReadArgs`，四个读侧工具共用），以及空串的罪名要并进「没点名就拒」那一句里。
//
// 每一根针都配对照臂，而且**两头都要有**：只测「错的被拒」，把「修过头把正常往返拦死」
// 放过去就看不见了 —— 这个洞在 10-25 当场烧过一次（先写成单独一道一刀切挡空串，
// `gate_shell_test.go` 与 `review_fix_shell_test.go` 那两根「老格式载入」的针同时红）。

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// encodeTextOf 拿这一本账的出厂全文（第一行是人读表头，后面是 JSONL）。
// 它是 load_journal 唯一**该**收的形状，也是 #67 那一发里被塞进错误键名的那份东西。
func encodeTextOf(t *testing.T, s *Server, id int) string {
	t.Helper()
	txt, isErr := toolText(t, s, id, "kernel.encode_journal", `{"payload":"omitted"}`)
	if isErr {
		t.Fatalf("encode_journal 自己被拒：%s", txt)
	}
	return txt
}

func bodyOf(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("装不出参数：%v", err)
	}
	return string(b)
}

// recordOne 落一发合法的账，返回链上该有几条（这里固定 1 发 = 49 条，逐栏不猜）。
func recordOne(t *testing.T, s *Server, id int) {
	t.Helper()
	if txt, isErr := toolText(t, s, id, "kernel.record_call", legalCall(t, "", "")); isErr {
		t.Fatalf("合法的一发落不进账：%s", txt)
	}
}

func verifyCount(t *testing.T, s *Server, id int) string {
	t.Helper()
	txt, isErr := toolText(t, s, id, "kernel.verify", "")
	if isErr {
		t.Fatalf("verify 自己被拒：%s", txt)
	}
	return txt
}

// chainCount 从 verify 那句里取「共 N 条」。只比这个数，不比整句 ——
// 整句里还带「比对过原像几条」，而按 omitted 那一档编出去的账本来就不带原像，
// 比整句会把一条正当路判成坏（10-25 这一根针第一次就红在这里，红的是判据自己）。
func chainCount(t *testing.T, verifyText string) int {
	t.Helper()
	m := regexp.MustCompile(`共 (\d+) 条`).FindStringSubmatch(verifyText)
	if m == nil {
		t.Fatalf("verify 那句里没有「共 N 条」：%s", verifyText)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("那个数解不开：%v", err)
	}
	return n
}

// TestWrongKeyLoadIsRefusedAndChainUntouched —— #67 那一发原样重放。
//
// 判据是三件事，缺一件都不算修上：① `isError=true`（原先是 false，这才是「静默」的来源）；
// ② 点名那个没打中的键名（只说「参数不对」等于让调用方自己猜哪个键写错了）；
// ③ 链上一条没动（换了就丢是这本账最坏的结局）。
func TestWrongKeyLoadIsRefusedAndChainUntouched(t *testing.T) {
	s := New()
	recordOne(t, s, 1)
	good := encodeTextOf(t, s, 2)
	before := verifyCount(t, s, 3)

	body := bodyOf(t, map[string]any{"journal": good, "acknowledge": "replace"})
	txt, isErr := toolText(t, s, 4, "kernel.load_journal", body)
	if !isErr {
		t.Fatalf("#67 那一发还是 isError=false —— 整本账可以被静默换成空账：%s", txt)
	}
	for _, want := range []string{"journal", "text", "这个工具只认"} {
		if !strings.Contains(txt, want) {
			t.Errorf("拒了但没点名 %q（调用方要知道自己哪个键写错）：%s", want, txt)
		}
	}
	if after := verifyCount(t, s, 5); after != before {
		t.Errorf("被拒的载入动了链上那条链：\n改前 %s\n改后 %s", before, after)
	}
	// ④ 这一发是**写请求在门口被挡下**，必须进门口那一笔，不许只回一句话就完事。
	if n := grabCount(t, tallyOf(t, s), "另有"); n != 1 {
		t.Errorf("门口那一栏没数到这一发：%d（该 1）—— 拒了看不见就是 #66 那一族", n)
	}

	// 反臂（同键名，不带 acknowledge）：这一形今天也是拒的，修完必须还是拒的。
	if txt2, isErr2 := toolText(t, s, 6, "kernel.load_journal",
		bodyOf(t, map[string]any{"journal": good})); !isErr2 {
		t.Fatalf("写侧没这个键还不带 acknowledge 竟被收下：%s", txt2)
	} else if !strings.Contains(txt2, "journal") {
		t.Errorf("拒了但没点名错键：%s", txt2)
	}
	// 正臂（不许修过头）：键名写对 + 点名 ⇒ 自己的账读回自己这条正常路必须还是通的。
	if txt3, isErr3 := toolText(t, s, 7, "kernel.load_journal",
		bodyOf(t, map[string]any{"text": good, "acknowledge": "replace"})); isErr3 {
		t.Errorf("把出厂全文按出厂键名喂回来被拒了 —— 这是修过头，不是修好：%s", txt3)
	} else if n, m := chainCount(t, before), chainCount(t, verifyCount(t, s, 8)); n != m {
		// 只比**条数**：上面那一份是 `payload:"omitted"` 编出去的，原像本来就不跟着走 ——
		// 拿整句 verify 比就会把「按不写原像那一档编的账读回来」这条正当路判成坏。
		t.Errorf("往返之后链上条数变了：往返前 %d 条，往返后 %d 条", n, m)
	} else if n <= 0 {
		t.Errorf("对照不成立：往返前链上就是 %d 条，这一根针什么都没守住", n)
	}
}

// TestEmptyTextCrimeIsNamed —— 老老实实给了空串（键名是对的）那一半。
//
// 两道闸共用一句回话，各拦各的：
//
//	没点名 + 有东西可丢 ⇒ 拒，且**罪名要先说「text 是空的」**（只说「没点名」会把
//	  「你一个字节都没交」说成「你没授权」—— 那是两种不同的修法）；
//	点名了 + 空串 ⇒ 照办，但回话必须说清那个 0 是谁的 0（三态不许塌）。
func TestEmptyTextCrimeIsNamed(t *testing.T) {
	s := New()
	recordOne(t, s, 1)

	txt, isErr := toolText(t, s, 2, "kernel.load_journal", `{"text":"   "}`)
	if !isErr {
		t.Fatalf("只有空白的 text 没被拒（手里这本有东西可丢）：%s", txt)
	}
	if !strings.Contains(txt, "text 是空的") {
		t.Errorf("拒了但罪名不是「text 是空的」，读的人以为只是没授权：%s", txt)
	}
	if !strings.Contains(txt, "0 条空账") {
		t.Errorf("没说清「空串喂装载器得到的是什么」：%s", txt)
	}
	// 这一发**不进**任何一栏，而且回话要自己认这一点：10-18 那道闸拦的一直是「你没授权换」，
	// 门口那一笔数的是「形状不对/没给/到上限」。把两种口径并成一栏，那个数就答不出「谁拒的」。
	// （这一族的来历见上面那段注释：并进门口那一笔要动 tally 那句枚举，而那句交付件里也印着。）
	if !strings.Contains(txt, "没计进任何一栏") {
		t.Errorf("没说清这一发没进哪一栏（「累计没涨」会被读成「没发生过」）：%s", txt)
	}
	if n := grabCount(t, tallyOf(t, s), "另有"); n != 0 {
		t.Errorf("未点名换账的载入被并进了门口那一栏：%d（这一路的单位不是「形状不对」，该 0）", n)
	}

	// 点名之后照办，但那个 0 不能冒充「读回了一本空账」。
	ok, isErr2 := toolText(t, s, 3, "kernel.load_journal", `{"text":"","acknowledge":"replace"}`)
	if isErr2 {
		t.Fatalf("带着 acknowledge=replace 仍被拒 —— 那是把文档里写明的逃生口焊死了：%s", ok)
	}
	if !strings.Contains(ok, "一个字节都没交") {
		t.Errorf("换了空账却没说清那个 0 是谁的 0（三态塌成「量到 0」）：%s", ok)
	}

	// 反臂（不许修过头的另一半）：本来就全空时载入空字节是**无损失**的 no-op，不该拦。
	fresh := New()
	if got, isErr3 := toolText(t, fresh, 1, "kernel.load_journal", `{"text":""}`); isErr3 {
		t.Errorf("空账载入空字节被拒了 —— 这一形没有东西可丢，拦它就是拦死正常往返：%s", got)
	}
}

// TestReadArgsNameStructFieldAndKeyTable —— 剩下三个读侧工具的严格解码。
//
// 结构体必须**有名字**：匿名 struct 时 `UnmarshalTypeError.Struct` 是空串，
// 「类型不对」那句就退化成「arguments 的某个字段」，调用方拿不到可归属的坐标。
func TestReadArgsNameStructFieldAndKeyTable(t *testing.T) {
	s := New()
	recordOne(t, s, 1)

	// ① 类型写错 ⇒ 点名到 结构.字段。
	txt, isErr := toolText(t, s, 2, "kernel.read_back", `{"session":"mcp","call":"第一发","side":"input"}`)
	if !isErr {
		t.Fatalf("call 给了字符串竟被收下（解成 0 就是「量到 0」那一族）：%s", txt)
	}
	// 归因必须由**我们自己那半句**给出。`err.Error()` 里也带着 "readBackArgs.call"
	// （stdlib 原话是 `Go struct field readBackArgs.call of type int`），所以第一版只查
	// `Contains(txt, "readBackArgs.call")` 会被那句括号喂饱 —— 臂 s2 把 who 掐成空串
	// （屏上变成 `参数被拒：arguments.call 类型不对`），那一根照样绿。查「冒号到类型不对之间」
	// 这一段：只有我们的格式化代码写得出这个形状，stdlib 写不出。
	if !strings.Contains(txt, "：readBackArgs.call 类型不对") {
		t.Errorf("没点名到 结构.字段（匿名 struct 就是这样丢掉的；括号里那句 stdlib 原话不算我们点的名）：%s", txt)
	}

	// ② 未知键 ⇒ 点名键名，并把这个工具认的键名表念出来。
	txt2, isErr2 := toolText(t, s, 3, "kernel.read_back", `{"sessn":"mcp","call":1,"side":"input"}`)
	if !isErr2 {
		t.Fatalf("拼错的 session 被静默吞了：%s", txt2)
	}
	for _, want := range []string{"sessn", "session", "call", "side"} {
		if !strings.Contains(txt2, want) {
			t.Errorf("没点名 %q：%s", want, txt2)
		}
	}

	// ③ 拿**账上的格名**当参数 —— 这一族只能靠"读回来的格"那半句区分，否则调用方以为自己在选格。
	txt3, isErr3 := toolText(t, s, 4, "kernel.read_back", `{"session":"mcp","call":1,"content_bytes":20}`)
	if !isErr3 {
		t.Fatalf("格名当参数被收下：%s", txt3)
	}
	if !strings.Contains(txt3, "content_bytes") || !strings.Contains(txt3, "读回来的格") {
		t.Errorf("没把「那是读回来的格，不是这一发的参数」说出来：%s", txt3)
	}

	// ④ encode_journal / declare_reader 同一条解码（一处改动，四处都要有证据）。
	if got, e := toolText(t, s, 5, "kernel.encode_journal", `{"paylod":"omitted"}`); !e || !strings.Contains(got, "paylod") {
		t.Errorf("encode_journal 的拼错键没被点名（e=%v）：%s", e, got)
	}
	if got, e := toolText(t, s, 6, "kernel.declare_reader", `{"reader":"r","ky":"usage"}`); !e || !strings.Contains(got, "ky") {
		t.Errorf("declare_reader 的拼错键没被点名（e=%v）：%s", e, got)
	}

	// 正臂：四个工具的文档形状一个都不许被新闸拦住。
	for _, tc := range []struct{ tool, args string }{
		{"kernel.read_back", `{"session":"mcp","call":1,"side":"input"}`},
		{"kernel.encode_journal", `{"payload":"omitted"}`},
		{"kernel.declare_reader", `{"reader":"人眼","key":"usage_in","side":"output"}`},
		{"kernel.load_journal", `{"text":"0 字节 JSONL，0 行链上条目 + 没有计数栏（写盘是你的事，内核只交字节）\n","acknowledge":"replace"}`},
	} {
		if got, e := toolText(t, s, 7, tc.tool, tc.args); e {
			t.Errorf("正常形状被新闸拦了（修过头）%s %s：%s", tc.tool, tc.args, got)
		}
	}
}

// TestReadSideRefusalsAreNotCountedInDoorBucket —— 桶的口径不能顺手扩。
//
// 门口那一笔的单位是「一次**写这本账**的请求」（#42 定的），读一发被挡不改变任何账；
// 把读塞进去，那个数就答不出「谁拒的」了 —— 而 declare_reader 的拒该进哪一桶是一个
// **还没圈的产品决定**（见 报告/面板方案-待批.txt），不是这里能顺手加的。
// 但拒绝必须自己说明白「没计进哪一笔」，否则「累计没涨」会被读成「没发生过」。
func TestReadSideRefusalsAreNotCountedInDoorBucket(t *testing.T) {
	s := New()
	recordOne(t, s, 1)
	base := grabCount(t, tallyOf(t, s), "另有")

	for _, tc := range []struct{ tool, args string }{
		{"kernel.read_back", `{"sessn":"mcp"}`},
		{"kernel.encode_journal", `{"paylod":"omitted"}`},
	} {
		txt, isErr := toolText(t, s, 50, tc.tool, tc.args)
		if !isErr {
			t.Fatalf("%s 的拼错键没被拒：%s", tc.tool, txt)
		}
		if !strings.Contains(txt, "没计进") {
			t.Errorf("%s 拒了却没说清自己没进哪一笔（「累计没涨」会被读成「没发生过」）：%s", tc.tool, txt)
		}
		if n := grabCount(t, tallyOf(t, s), "另有"); n != base {
			t.Errorf("%s 把读塞进了写那一桶：%d（基准 %d）", tc.tool, n, base)
		}
	}

	// declare_reader：声明表一条没改，也没挤进任何计数桶。
	before := func() string {
		txt, isErr := toolText(t, s, 60, "kernel.readers_report", "")
		if isErr {
			t.Fatalf("readers_report 报错：%s", txt)
		}
		return txt
	}
	b0 := before()
	txt, isErr := toolText(t, s, 61, "kernel.declare_reader", `{"reader":"r","ky":"usage"}`)
	if !isErr {
		t.Fatalf("declare_reader 的拼错键被收下：%s", txt)
	}
	if !strings.Contains(txt, "声明表一条没改") {
		t.Errorf("没说清声明表没动：%s", txt)
	}
	if !strings.Contains(txt, "单独的决定") {
		t.Errorf("这一发的拒该进哪一桶是没圈的决定，回话里要认这一点：%s", txt)
	}
	if b1 := before(); b1 != b0 {
		t.Errorf("被拒的声明改了声明表：\n改前 %s\n改后 %s", b0, b1)
	}
	if n := grabCount(t, tallyOf(t, s), "另有"); n != base {
		t.Errorf("declare_reader 的拒挤进了门口那一桶（那是单独的决定）：%d（基准 %d）", n, base)
	}
}
