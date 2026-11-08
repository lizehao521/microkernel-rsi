package kernel

// journal.go 拥有「一条账本记录落成字节、再从字节回到账本」这一份**格式**。
//
// 它不写磁盘：交出去的是 []byte，读回来吃的也是 []byte。
// 落到哪个文件、什么时候 fsync、要不要压缩，仍然住在框架那一侧（README 第 4 节第 2 条），
// 所以「内核不认识磁盘」这句话在这一份文件里字面上依然成立 ——
// 这一份连 os 都没有 import。
//
// 为什么格式必须由内核拥有，而不是让框架自己 `json.Marshal(entry)` 了事：
// Entry 的 Payload 带着 `json:"-"`（那是有意的设计，见 kernel.go 里那段注释），
// 于是「原样把 Entry 写成 JSONL」这一条最自然的出路，**默认就丢掉了原像**：
// 第三方拿到 PayloadH 却重算不出原值，而「别人能按同规则重算」是这一包全部的卖点。
// 更坏的是第二半：把那样的文件读回内存再跑 Verify()，
// 那条 `sum(e.Payload) != e.PayloadH` 会把**每一格**都报成「内容被动过」——
// 一个「没带原像」被念成了「有人篡改」。这是规矩一那一族最贵的实例：
// 两种在证据上完全不同的状态，长成了同一句话。

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PayloadMode 回答「这份字节流里带不带 payload 的原像」。
//
// 零值是 PayloadUnset 而不是某一种合法选择 —— 与 Basis 同一个道理（不变量 1）：
// 一个能靠「忘了填」就走通的参数，迟早会被填错的一侧默认掉。
// 这里刻意不替调用方选：带原像 ⇒ 第三方能重算，但敏感原文（Failure.Detail 那一格，
// 缺口第 5 条）就跟着落盘了；不带 ⇒ 只留指纹。两条都得有人签字。
type PayloadMode int

const (
	PayloadUnset    PayloadMode = iota // 没填 —— 编解码门口直接拒，不存在「默认不带原像」
	PayloadOmitted                     // 不写原像：链仍可自洽，但第三方重算不出原值
	PayloadIncluded                    // 写原像（base64）：第三方能按同一规则重算 PayloadH
)

func (m PayloadMode) String() string {
	switch m {
	case PayloadOmitted:
		return "不带原像"
	case PayloadIncluded:
		return "带原像"
	default:
		return "unset"
	}
}

// journalLine 是账本记录在字节流里的那一份形状。
//
// 字段名与 Entry 的 json tag 一一对着抄（seq/session/actor/step/kind/claim/basis/
// source/ts/payload_sha256/prev_hash/hash），所以框架如果已经拿 `json.Marshal(entry)`
// 写过一批，那批旧行在这里照样解得开 —— 而它缺 payload 这件事由 PayloadB64==nil 明说，
// 不由「读的人猜这一行是不是省略了」。
//
// 它不直接嵌 Entry：嵌进去的话 `json:"-"` 会跟着继承，
// 「带原像」这一档就没有地方落。
type journalLine struct {
	Seq     int       `json:"seq"`
	Session string    `json:"session"`
	Actor   string    `json:"actor"`
	Step    int       `json:"step,omitempty"`
	Kind    string    `json:"kind"`
	Claim   string    `json:"claim,omitempty"`
	Basis   Basis     `json:"basis"`
	Source  string    `json:"source,omitempty"`
	At      time.Time `json:"ts"`
	// PayloadB64 是指针而不是 []byte：nil 与「一段长度为 0 的原像」必须分得开。
	// 用非指针的话，一条空 payload 的记录（absent 那一档就是 nil payload）
	// 与一条省略了 payload 的记录序列化后一模一样 —— 又是「缺席长成 0」那一族，
	// 只是这次换到了字节流上。
	PayloadB64 *string `json:"payload_b64"`
	PayloadH   string  `json:"payload_sha256"`
	PrevHash   string  `json:"prev_hash"`
	Hash       string  `json:"hash"`
}

// gateLine 是**唯一一行非链上**的计数记录，排在所有链行之后、作为最后一行。
//
// 为什么它不进链：闸门计数回答的是"拦了几次"，不是一条说法；把它塞成 Entry 会
// ① 污染 RecordCall 那句"写入被拒"的语义（拒收本身并没有发生一次写入），
// ② 让"账上 N 条"这个分母变成两本账混数 —— 而 N 正是 Verify 的判据。
// 为什么它又不能是裸的一行 JSON：那样改一个数字没人知道，而这一包存在的全部理由
// 就是"改一行没人知道"。折衷是给它单独盖一枚封条：封条盖的是**六个数 + 当时链尾的 hash**，
// 所以「改了计数」与「把计数接到另一本账上」都会在这里红，而链上任何一个字节都不必为此改动。
type gateLine struct {
	Gate      Gate   `json:"gate"`
	ChainHead string `json:"chain_head"`
	Seal      string `json:"gate_sha256"`
	Version   int    `json:"gate_version"`
}

// gateVersion 是封条盖法的版本号。改字段顺序、改拼接规则或**多盖一格**都必须 +1，
// 否则老文件读回来会被判成"有人改过计数"——那是把格式漂移念成篡改，本包最贵的那一族。
// v2 = 把口径 `State` 一起盖进去（10-18 两路互审各独立撞到"下界一次往返被洗成全量"，
// 少了这一位封条就护不住口径本身）。
const gateVersion = 2

// GateSeal 算计数那一栏的封条：六个数 + 口径，按固定顺序，再加当时的链尾，逐格换行连接后取 sha256。
// 它是导出函数，因为第三方要能按同一规则重算 —— 这一包全部的卖点就在这句话上。
//
// **它防得住什么、防不住什么，写在脸上**：无密钥，所以它防的是「无意损坏」与「事后抵赖」，
// 防不住「铁了心伪造的人」——他可以把六个数、口径、链尾一起改了再自己重算一遍这枚封条。
// 这一栏因此不是证据，是一条**可复核的声明**；要证据得看链上那 49 格。
func GateSeal(g Gate, chainHead string) string {
	return sum([]byte(strings.Join([]string{
		"gate" + strconv.Itoa(gateVersion),
		strconv.Itoa(g.Rejected), strconv.Itoa(g.PrecheckRejected), strconv.Itoa(g.PrecheckDoor),
		strconv.Itoa(g.DoorRefused), strconv.Itoa(g.BadRequests), strconv.Itoa(g.TransportRefused),
		strconv.Itoa(int(g.State)),
		chainHead,
	}, "\n")))
}

// chainHeadOf 取链尾 hash（空链是空串）。判"手里有没有原像"那类区分不住的两个字段
// 这里不复用：计数栏盖的是整条链的尾，与单条 payload 无关。
func chainHeadOf(docs []Entry) string {
	if n := len(docs); n > 0 {
		return docs[n-1].Hash
	}
	return ""
}

// MarshalJournalWithGate 在链行之后追加一行计数。
//
// 它**不**是 MarshalJournal 的默认包装：默认包装意味着"忘了填就落一行全 0 的计数"，
// 而全 0 会被下一个人读成"闸门一次都没拦" —— 这一包从第一天起就反对的那个形状。
// 所以要落计数就得自己把六笔数**连同口径**交过来（见 Gate 的注释：四笔住在壳上）。
//
// 门口还拦一种形状：`State=GateUnknown` 不许落盘。"不可知"的意思就是**没有这一行**，
// 写出一行却自称不可知，是把同一件事做了两种互相矛盾的记录（读的人要信哪一种？）；
// 越界的口径值同样在门口拒，不等到读侧才塌成别的一档。
func MarshalJournalWithGate(docs []Entry, mode PayloadMode, gate Gate) ([]byte, error) {
	if gate.State != GateLive && gate.State != GatePartial {
		return nil, fmt.Errorf("kernel: 计数那一栏的口径是 %v（%d）—— 落盘的计数行只认 1=完整可数 / 2=只覆盖这一程；"+
			"「不可知」的正确写法是**不要写这一行**", gate.State, int(gate.State))
	}
	b, err := MarshalJournal(docs, mode)
	if err != nil {
		return nil, err
	}
	head := chainHeadOf(docs)
	raw, err := json.Marshal(gateLine{
		Gate: gate, ChainHead: head, Seal: GateSeal(gate, head), Version: gateVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("kernel: 计数那一栏编不出来：%v", err)
	}
	return append(b, append(raw, '\n')...), nil
}

// MarshalJournal 把一段账本记录编成 JSONL（一行一条，末尾带换行）。**不带计数那一栏** ——
// 想知道闸门拦了几次，用 MarshalJournalWithGate。
//
// 它**不重算**任何指纹，也不修链 —— 编出来的每一行都原样带着 seal 出来的那些格，
// 所以「编完再读回来必须还能 Verify」这条判据测的是格式，不是哈希。
// 反过来说：谁要是手改过 docs 里的某一格，编出去的那一行自己就会在 ReadJournal
// 之后的链检查里红掉，这一份函数不替他兜。
func MarshalJournal(docs []Entry, mode PayloadMode) ([]byte, error) {
	switch mode {
	case PayloadUnset:
		return nil, fmt.Errorf("kernel: 没说要不要带原像（PayloadUnset）—— 这一格不许由一个默认值替调用方决定")
	case PayloadOmitted, PayloadIncluded:
	}
	var buf bytes.Buffer
	for i, e := range docs {
		line := journalLine{
			Seq: e.Seq, Session: e.Session, Actor: e.Actor, Step: e.Step, Kind: e.Kind,
			Claim: e.Claim, Basis: e.Basis, Source: e.Source, At: e.At,
			PayloadH: e.PayloadH, PrevHash: e.PrevHash, Hash: e.Hash,
		}
		if mode == PayloadIncluded {
			b64 := base64.StdEncoding.EncodeToString(e.Payload)
			line.PayloadB64 = &b64
		}
		// 用紧凑的 json.Marshal（不缩进）：JSONL 一行一条是格式本身的一部分，
		// 一个多行记录会让按行扫的读者（和第三方）把一条读成 N 条。
		raw, err := json.Marshal(line)
		if err != nil {
			return nil, fmt.Errorf("kernel: 第 %d 条编不出来（序号 %d）：%v", i+1, e.Seq, err)
		}
		buf.Write(raw)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// EncodeHeaderMark 是那句人读表头里唯一的固定串。
//
// **产这句话的仍在外壳**（`mcpsrv.go` 里 encode_journal 那句 fmt），**认它的判定搬进这里**：
// 串与判定原先都住在外壳，于是 `kernel.LoadLedger` 直接吃出厂文件时，第一行被计成坏账、整本拒
// （10-25 现场：`构建-1017/客服靶场/导出/账本-全量-1018.jsonl` 2941 行 → Lines=2941 / Parsed=2940 /
// BadLines=1，罪名「这不是空账本，是读坏了」；剥掉第一行才 Parsed=2940 / err=nil）。
// 「调用方照自家说明回灌」这条路的另一头，从来没有内核认识那句说明 ——
// 读不读得回来取决于读的那一端前面有没有挂着外壳，而字节是内核交的。
//
// 为什么不能散成两处字面量：两处各写一遍，改了一边另一边上"照自家说明回灌"就悄悄读不回来了。
const EncodeHeaderMark = "字节 JSONL，"

// IsEncodeHeader 认一行是不是那句人读表头。
//
// 判定刻意做窄：**不是一行 JSON、且含 EncodeHeaderMark** 才算表头。
// 宽一点（"任何非 JSON 行都算表头"）就会把"第一行是别的废话"那种真坏账放过，
// 而账本最不该发生的正是"少了一条还报绿"（`mcpsrv_test.go` 反面对照二钉的就是这一条）。
// 链行经 TrimSpace 后必然以 `{` 开头，所以这一条吃不掉任何一条真账。
func IsEncodeHeader(line string) bool {
	line = strings.TrimSpace(line)
	return line != "" && !strings.HasPrefix(line, "{") && strings.Contains(line, EncodeHeaderMark)
}

// Journal 是一次解码的结果，自带分母。
//
// 分母是重点而不是附注：读回来 47 条**不等于**读回来 47 格的可重算账。
// 其中几条没有原像，第三方就对几条重算不出原值，而这件事必须数得出来。
type Journal struct {
	Docs []Entry
	// Lines 扫了几行，Parsed 解开几行。两者不等时 BadLines 逐行说清是哪里解不开 ——
	// 一个「解开了 46/47」的读数如果被读成「46 条账」，中间那条就凭空消失了，
	// 而链上它会表现为一次截断（这倒会被 Verify 抓到，但抓到的是错的那个凶手）。
	Lines    int
	Parsed   int
	BadLines []string

	// WithPayload / NoPayload 是原像的两个分桶，加起来等于 Parsed。
	// NoPayload 不是 0 就是「这一份文件根本没法由第三方重算」的意思，
	// 所以它必须被单独印出来，而不是折进某个「总条数」里。
	WithPayload int
	NoPayload   int

	// GateLines / HasGate / Gate 是那一行**非链上**计数的下落。GateLines=0 不是"闸门没拦过"，
	// 是"这份字节里根本没有计数那一栏"（老账本、或别人手写的文件）—— 见 GateState 的三态。
	GateLines int
	HasGate   bool
	Gate      Gate
	// BadGateLines 是"看起来是计数行、但解不开"的行数。它单独一格，是为了让 String
	// 不必在「没有这一行」与「有一行读不动」之间替读者编一个答案。
	BadGateLines int
	// GateProblem 是这一栏自己的毛病（出现两行 / 不在链行之后 / 版本对不上 / 封条重算不符）。
	// 它**不是**链的红：链可以完全自洽而计数是坏的，两件事必须说两句话 ——
	// 合成一句就会让读的人去查一个不存在的篡改者（CheckChain 的注释里那条老账）。
	GateProblem string

	// HeaderLines 是被认出的人读表头行数。它**不进 Lines**（在 Lines++ 之前就被跳掉），
	// 所以也不能折进任何一个"总行数"里 —— 单开一格是为了让「2940 条账 + 1 行本来不是账」
	// 与「2941 行里有一行解开了、有一行没有」这两句话长得不一样。
	// 后一句是本包最恨的形状：一行没带账的字节被读成一条坏账，读者会去查一个不存在的篡改者。
	HeaderLines int
}

// String 把分母连同「这份文件能不能被第三方重算」一起说出来。
// 一个只说「读回 47 条」的 String 会把「47 条里 47 条没有原像」念成一次成功。
func (j Journal) String() string {
	tail := ""
	// 计数那一行不是链行，所以它不进 Parsed。拿 Lines 与 Parsed 直接比会把它念成
	// "有一行解不开" —— 一行本来好好带着 KPI 的字节，被读成一条坏账。
	if j.Lines != j.Parsed+j.GateLines {
		tail = "、✗ 有 " + strconv.Itoa(j.Lines-j.Parsed-j.GateLines) + " 行解不开"
	}
	// 表头那一行不在 Lines 的分母里，所以它得自己说一句：只念「读回 2940 条」，
	// 读的人会以为 2941 行字节都被他完整带走了；把它并进 tail，又是一个不存在的篡改者。
	head := ""
	if j.HeaderLines > 0 {
		head = "、另有 " + strconv.Itoa(j.HeaderLines) + " 行人读表头（不是账，不进 Lines）"
	}
	// 计数那一栏的下落必须跟在这句话后面：一个人读走"读回 47 条、全部带原像"就以为
	// 他把这本账完整带走了，而"闸门拦了几次"这一栏根本不在这些字节里。
	gate := "；计数栏：没有这一行 ⇒ 不可知（不是 0）"
	switch {
	case j.HasGate:
		gate = "；计数栏：有（拒落账 " + strconv.Itoa(j.Gate.Rejected) + "、拒预检 " +
			strconv.Itoa(j.Gate.PrecheckRejected) + "；口径=" + j.Gate.State.String() + "）"
	case j.GateLines > 0:
		gate = "；计数栏：有，但不可信 —— " + j.GateProblem
	case j.BadGateLines > 0:
		gate = "；计数栏：有 " + strconv.Itoa(j.BadGateLines) + " 行读不动 ⇒ 不可知（别替它编一个数）"
	}
	switch {
	case j.Parsed == 0:
		return "读回 0 条（这份账本是空的）" + head + tail + gate
	case j.NoPayload == 0:
		return "读回 " + strconv.Itoa(j.Parsed) + " 条、全部带原像（第三方能重算）" + head + tail + gate
	case j.WithPayload == 0:
		return "读回 " + strconv.Itoa(j.Parsed) + " 条、全部没有原像（链能自洽，但第三方重算不出原值）" + head + tail + gate
	default:
		return "读回 " + strconv.Itoa(j.Parsed) + " 条、带原像 " + strconv.Itoa(j.WithPayload) +
			" 条、没有原像 " + strconv.Itoa(j.NoPayload) + " 条（混合：重算能力逐格不同）" + head + tail + gate
	}
}

// ReadJournal 解 JSONL。它不验链 —— 解得开与对得上是两件事，
// 后者由 CheckChain / Verify 那一份判据回答（规矩三：一个定义只有一个 owner）。
//
// 空行跳过（末尾那个换行不算一条记录）；人读表头单独计进 HeaderLines 且不进 Lines；
// 解不开的行记进 BadLines 并带着行号，
// 因为「第 12 行坏了」是能去查的，「有一条坏了」不是。
func ReadJournal(b []byte) (Journal, error) {
	j := Journal{Docs: []Entry{}}
	var gateSeen *gateLine
	gateAt, lastDocAt := -1, -1
	for i, raw := range bytes.Split(b, []byte("\n")) {
		line := strings.TrimSpace(string(raw))
		if line == "" {
			continue
		}
		// 人读表头在 Lines++ **之前**就被跳掉，并且不去探计数行 —— 一句说明里既没有 "gate" 键
		// 也不是 JSON，往下一走的唯一出路是进 BadLines，而 Lines 会把它算进分母：
		// 于是 String 替读者编出一句「有 1 行解不开」，那一行本来就不是账，没人篡改它。
		// 位置不限第一行：encode_journal 交回来的表头按构造就在第一行，
		// 认成"任何位置的这一句"只多接受同一句说明重复出现，吃不到任何一条以 { 开头的真账。
		if IsEncodeHeader(line) {
			j.HeaderLines++
			continue
		}
		j.Lines++
		// 先看这一行是不是计数那一栏：它带一个 "gate" 键，链行里没有这个键。
		// 认不出来就顺着往下走的话，这一行会进 BadLines —— 于是"闸门拦了几次"这一栏
		// 被读成"有一行账坏了"，而那一行里的数字恰恰是产品唯一的 KPI。
		var probe struct {
			Gate json.RawMessage `json:"gate"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err == nil && len(probe.Gate) > 0 {
			var gl gateLine
			if err := json.Unmarshal([]byte(line), &gl); err != nil {
				// 解不开的计数行只进 BadLines，**不**计进 GateLines：
				// 双计会让 Journal.String 说出「计数栏：有（拒落账 0、拒预检 0）」——
				// 一行根本没解开的字节，被念成"数在手、都是 0"，那是本包最恨的形状换了个住处。
				j.BadGateLines++
				j.BadLines = append(j.BadLines, "第 "+strconv.Itoa(i+1)+" 行的计数栏解不开："+err.Error())
				continue
			}
			j.GateLines++
			if gateSeen != nil {
				j.GateProblem = fmt.Sprintf("计数栏出现了不止一行（第 %d 行与第 %d 行）—— 两行数字不一样时不知道信哪个",
					gateAt+1, i+1)
				continue
			}
			gateSeen, gateAt = &gl, i
			continue
		}
		var l journalLine
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			j.BadLines = append(j.BadLines, "第 "+strconv.Itoa(i+1)+" 行解不开："+err.Error())
			continue
		}
		lastDocAt = i
		e := Entry{
			Seq: l.Seq, Session: l.Session, Actor: l.Actor, Step: l.Step, Kind: l.Kind,
			Claim: l.Claim, Basis: l.Basis, Source: l.Source, At: l.At,
			PayloadH: l.PayloadH, PrevHash: l.PrevHash, Hash: l.Hash,
		}
		if l.PayloadB64 != nil {
			payload, err := base64.StdEncoding.DecodeString(*l.PayloadB64)
			if err != nil {
				j.BadLines = append(j.BadLines, "第 "+strconv.Itoa(i+1)+" 行的原像不是合法的 base64："+err.Error())
				continue
			}
			e.Payload = payload
			j.WithPayload++
		} else {
			// 这一行**没有带**原像 —— 与「带了一段空原像」是两件事（后者是 payload_b64:""）。
			// 这一位只在这里置真：读文件的人没有任何办法把它伪造出来，
			// 因为 journalLine 里没有一个字段对应它。
			e.payloadAbsent = true
			j.NoPayload++
		}
		j.Parsed++
		j.Docs = append(j.Docs, e)
	}
	// 计数那一栏的语义检查放在整本扫完之后：它的封条盖的是**链尾**，链尾要等所有链行都解出来才知道。
	if gateSeen != nil && j.GateProblem == "" {
		head := chainHeadOf(j.Docs)
		switch {
		case gateNegativeName(gateSeen.Gate) != "":
			j.GateProblem = "计数栏里 " + gateNegativeName(gateSeen.Gate) + " 是负数 —— 拦下来的次数不会是负的，这是坏数据不是读数"
		case gateSeen.Gate.State != GateLive && gateSeen.Gate.State != GatePartial:
			j.GateProblem = fmt.Sprintf("计数行带的口径是 %d —— 落盘的计数行只认 1=完整可数 / 2=只覆盖这一程；"+
				"「不可知」的正确形状是**没有这一行**，不是写一行自称不可知", int(gateSeen.Gate.State))
		case gateAt < lastDocAt:
			j.GateProblem = fmt.Sprintf("计数栏在第 %d 行，链行在它后面 —— 这一栏只许排在所有链行之后", gateAt+1)
		case gateSeen.Version != gateVersion:
			j.GateProblem = fmt.Sprintf("计数栏的盖法是 v%d，这份代码只认 v%d —— 不去猜怎么兼容，猜错就是把一串旧数当成现在的数",
				gateSeen.Version, gateVersion)
		case gateSeen.ChainHead != head:
			j.GateProblem = fmt.Sprintf("计数栏接的链尾是 %s，这本账的链尾是 %s —— 它是从另一本账上搬过来的（或链被剪过）",
				headTail(gateSeen.ChainHead), headTail(head))
		case gateSeen.Seal != GateSeal(gateSeen.Gate, gateSeen.ChainHead):
			j.GateProblem = "计数栏的封条重算不符：六个数或口径里有一格被动过（封条盖的是那七格加链尾）"
		default:
			j.Gate, j.HasGate = gateSeen.Gate, true
		}
	}
	if len(j.BadLines) > 0 {
		return j, fmt.Errorf("kernel: %d 行里 %d 行解不开 —— 这不是空账本，是读坏了：%v",
			j.Lines, len(j.BadLines), j.BadLines)
	}
	return j, nil
}

// headTail 取指纹尾 12 位用于报错文案：整条 64 位塞进一句话里，读的人找不到要看的那两位。
// 它不参与任何判据 —— 判据比的是整条。
func headTail(h string) string {
	if h == "" {
		return "（空链）"
	}
	if len(h) <= 12 {
		return h
	}
	return h[len(h)-12:]
}

// gateNegativeName 点名第一个为负的计数格（没有则空串）。
// 为什么要单独拦：封条是**重算**出来的，一个负数照样能和它自己的封条对得上，
// 于是"改了数并连封条一起重算"与"这本账的数本来就没错"在读侧长成同一句话。
// 链上那 11 格有 seq 与 hash 互相咬，这一栏只有一枚封条，所以值的范围也得有人守。
func gateNegativeName(g Gate) string {
	for _, p := range []struct {
		name string
		v    int
	}{{"rejected", g.Rejected}, {"precheck_rejected", g.PrecheckRejected}, {"precheck_door", g.PrecheckDoor},
		{"door_refused", g.DoorRefused}, {"bad_requests", g.BadRequests}, {"transport_refused", g.TransportRefused}} {
		if p.v < 0 {
			return p.name
		}
	}
	return ""
}

// ChainReport 是一段链的体检结果，三态：过、没过、核不了。
//
// 为什么必须有三态而不止两态（这是这一包最核心的那一条纪律，落到读盘这一路上）：
// Payload 不在手里时，「指纹重算不符」与「无从重算」是两件事。
// 把它们念成同一句话的后果是具体的：一个把账本存成不带原像的框架，
// 每次读回来都会被内核指控「内容被动过」，于是真篡改混在假警报里，没人再看那条报错。
type ChainReport struct {
	Entries int
	// Checked 是真正比对了 payload 与 PayloadH 的条数（手里有原像的那些）。
	Checked int
	// NoPreimage 是「手里没有原像、这一格无从重算」的条号。
	NoPreimage []int
	// Mismatch 是第一个真不符：payload 与它自己的指纹对不上，或整格 seal 重算不符。
	// 它非空 ⟺ 这条链被动过（或在内存里被改过），这是红，不是核不了。
	Mismatch string
}

// Decidable 答「这份报告够不够格说这条链是好的」。
// 只此一个访问器 —— 曾有过一个同体的 OK()，两个方法同一份判据就是两个 owner，
// 而「有没有一个方法在两种结论下都 +1」这一族正是不变量 3 要拦的。
// 没有原像的那些条让整份报告降级：链的字段级自洽仍然核了，
// 但「第三方能重算」那一半核不了 —— 读的人不许把它读成绿。
func (r ChainReport) Decidable() bool { return r.Mismatch == "" && len(r.NoPreimage) == 0 }

func (r ChainReport) String() string {
	if r.Mismatch != "" {
		return "✗ 链不符：" + r.Mismatch
	}
	if r.Entries == 0 {
		// 一本都还没有，不等于「核过了、全对得上」。这一族从建包第一天就被盯着：
		// 缺席 ≠ 0、忘了填 ≠ 缺席，而「0 条」过去顺手套了 ✓ 那一句 —— 每个新进程
		// 第一次 verify 都会经过它（10-15 从工具面实测撞到的原话：✓ 链自洽（共 0 条，其中 0 条比对过原像））。
		// 它也不是红：新进程本来就是空的。所以它是第三种情况的第三种说法。
		return "— 尚无账可核（共 0 条）：这条链上什么都还没核过 —— 别把它读成通过"
	}
	counted := "共 " + strconv.Itoa(r.Entries) + " 条，其中 " + strconv.Itoa(r.Checked) + " 条比对过原像"
	if len(r.NoPreimage) > 0 {
		// 这里刻意不打 ✓：一个「✓ …… 但有 47 条核不了」的读数会被读成一次通过，
		// 而这一包从建包第一天起就把「核不了」当第三种答案（见 isolate.go 的 Checked()）。
		return "— 字段级自洽（" + counted + "）；核不了：" + strconv.Itoa(len(r.NoPreimage)) +
			" 条没有原像（第 " + seqList(r.NoPreimage) + "）—— 那些条的 PayloadH 重算不出原值"
	}
	return "✓ 链自洽（" + counted + "）"
}

// CheckChain 逐条重算一段链。它与 Ledger.Verify 是**同一份**判据（Verify 委派到这里），
// 所以「内存里的账本」与「从盘上读回来的账本」不会慢慢漂成两套规则 ——
// 那是规矩三那一族（早先指纹长度这条判据就在输入侧与输出侧各写了一遍）。
//
// 它查四件事，逐条带上是第几条：
//  1. 序号连续（抽走一条会在这里红）；
//  2. prev_hash 等于前一条的 hash（截链或接错会在这里红）；
//  3. 手里有原像的那些条：sum(payload) == payload_sha256（改内容会在这里红）；
//  4. 整格 seal 重算 == hash（改任意一字段会在这里红）。
//
// 第 3 件对没有原像的条**跳过并记进 NoPreimage**，而不是拿 sum(nil) 去比 ——
// 那会让一条好账被念成篡改。
func CheckChain(docs []Entry) ChainReport {
	r := ChainReport{Entries: len(docs), NoPreimage: []int{}}
	for i, e := range docs {
		if e.Seq != i+1 {
			r.Mismatch = "第 " + strconv.Itoa(i+1) + " 条 seq=" + strconv.Itoa(e.Seq) + " 不连续（中间有被抽走的记录）"
			return r
		}
		wantPrev := ""
		if i > 0 {
			wantPrev = docs[i-1].Hash
		}
		if e.PrevHash != wantPrev {
			r.Mismatch = "第 " + strconv.Itoa(e.Seq) + " 条的 prev_hash 对不上前一条的 hash（链被截或接错）"
			return r
		}
		// 判「有没有原像」看的是 payloadAbsent（那一行到底没带），**不是** len(Payload)：
		// 一条 absent 格在内存里落账时 Payload 就是 nil，而它的 PayloadH 正是对「空」算出来的，
		// 拿 sum(nil) 去比它必须通过 —— 用它当「没有原像」的信号会把一条好账念成核不了。
		if e.payloadAbsent {
			r.NoPreimage = append(r.NoPreimage, e.Seq)
		} else {
			r.Checked++
			if ph := sum(e.Payload); ph != e.PayloadH {
				r.Mismatch = "第 " + strconv.Itoa(e.Seq) + " 条 payload 与它自己记的指纹不符（内容被动过）"
				return r
			}
		}
		if h := e.seal(); h != e.Hash {
			r.Mismatch = "第 " + strconv.Itoa(e.Seq) + " 条整格指纹重算不符（某格被改）want=" + h[:12] + " got=" + e.Hash[:12]
			return r
		}
	}
	return r
}

// seqList 把条号渲染成人话，最多点名 8 条。
// 为什么要有上限：一份不带原像的账读回来，「核不了」那一栏会点名全部 47 条 ——
// 那句话把真正要看的分母（几条）淹在了一串数字里。数由分母说，条号只给前几处线索。
func seqList(xs []int) string {
	if len(xs) == 0 {
		return "无"
	}
	parts := make([]string, 0, 9)
	head, ell := xs, ""
	if len(xs) > 8 {
		head, ell = xs[:8], "…"
	}
	for _, x := range head {
		parts = append(parts, strconv.Itoa(x))
	}
	if ell != "" {
		parts = append(parts, ell)
	}
	return strings.Join(parts, "、") + "（共 " + strconv.Itoa(len(xs)) + " 条）"
}

// LoadLedger 把一份 JSONL 读成一本可以接着记的账本。
//
// 它是缺口第 8 条里「跨账本」那一半的一半解药：去重闸 alreadyRecorded 认的是
// 同一个内存 Ledger 里的 session + 发号 + 侧，所以一个**新新的**账本看不见旧账。
// 从盘上读回来再记，它就看得见了 —— 于是「进程重启后把同一发再记一遍」被拒收。
// 剩下那一半（另起一本不读这份文件的账本、两个进程各写各的）仍然只能由框架负责，
// 这一条不许被写成已验证。
//
// 载入即逐条比对其自带的指纹与 hash（CheckChain）：
// 一个字段被改过的文件在这里就被拒，不会先进账本再等下一次 Verify。
// 但「没有原像」不是拒收的理由——那是认了的取舍（PayloadOmitted 那一档），
// 它由返回的 ChainReport 说清楚，并由 Journal 的两个分桶数出来。
func LoadLedger(b []byte) (*Ledger, Journal, ChainReport, error) {
	j, err := ReadJournal(b)
	if err != nil {
		return nil, j, ChainReport{}, err
	}
	rep := CheckChain(j.Docs)
	if rep.Mismatch != "" {
		return nil, j, rep, fmt.Errorf("kernel: 读回来的账本链不符，拒绝载入：%s", rep.Mismatch)
	}
	if j.GateProblem != "" {
		// 链好而计数坏，是两种红，得说两句：前者是"账被动过"，后者是"闸门那一栏不能信"。
		// 放着不管的话，下一句 tally 会把一个可疑的数当现值印出去 —— 而那是产品唯一的 KPI。
		return nil, j, rep, fmt.Errorf("kernel: 链是好的，但计数那一栏不可信，拒绝载入：%s", j.GateProblem)
	}
	l := NewLedger()
	l.docs = j.Docs
	if j.HasGate {
		// 口径跟着落盘走：一本「只覆盖这一程」的下界账，读回来必须**还是**下界 ——
		// 10-18 两路互审各独立撞到这里：原先只看"有没有这一行"就硬置 Live，
		// 一次往返就把下界洗成全量，正是这次改动要防的那个形状。
		l.countRejected, l.countPrecheck = j.Gate.Rejected, j.Gate.PrecheckRejected
		l.gateState = j.Gate.State
	} else {
		l.gateState = GateUnknown
	}
	return l, j, rep, nil
}
