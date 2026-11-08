// cmd/kwire 是接口对账尺：量「Agent 接入最少要写对的那些字段，文档说的和代码收的到底一不一致」。
//
// 为什么要有这把尺（而不是再读一遍文档）：10-19 两台外部 Agent 各接一次，
// 一个拿文档、一个不拿 —— **拿文档的那一个被门口拒了两次，没拿的那一次过**。
// 拒它的两条（`Shares` 是 map、`Cuts` 是扁数组）文档里一个字都没写，而 tools/list 那份
// inputSchema 两侧都只到 `{"type":"object"}`：机器可读的接口真相在 Go struct 里，不在 schema 里。
// 所以这张尺两头都不靠抄：代码那头从 struct 反射 + **真跑一遍门口与落账**，
// 文档那头解析 contract.md 里带锚点的表。
//
// 「没给会怎样」这一列不许由人写：每条都从 `mcpsrv` 那一道门口闸、那一份严格解码、
// 那一条 `RecordCall` 现场问出来的。一次删一个键，看它撞到哪儿。
//
// 三态退出码与 contract.md §6、celltable_audit 同一条纪律：
//
//	0 = 对上；1 = 漂了（逐名点名）；2 = 核不了（夹具不过闸 / 有格子三份夹具里都是空的 / 文档解析不出行）。
//	「核不了」不许被读成合格。
//
// 用法：
//
//	go run ./cmd/kwire                 # 对账，终端只留纯 ASCII（GBK 打不出 ✓）
//	go run ./cmd/kwire -emit           # 把反射 + 实测那一份导成 markdown 表，贴进 contract.md §2.5
//	go run ./cmd/kwire -out 路径       # 完整逐路径读数写进文件（UTF-8），终端不变
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"agentkernel"
	"agentkernel/mcpsrv"
)

// 撞到哪儿只有这六种说法。写成常量是因为它们同时是判据里的比较值：
// 拼错一种，某一族洞就会静静落进「落账不变」那一档 —— 而那是最像合格的一档。
const (
	clsDoor   = "门口拒"
	clsDecode = "解码拒"
	clsRecord = "落账拒"
	clsSilent = "静默落账"
	clsHonest = "落账但记缺席"
	clsSame   = "落账不变"
)

// 三份夹具的名字。三份是必要的，而且每一份都在补前一份**合法地等于零**的那些格：
// 正常发里 `Failure.Kind` 是空的（没故障），删它测不出任何事；
// 故障发里它是 `http`；而 `Malformed`/`MalformedStreak`/`Downgrade` 在两发里都只能是 0/false
// —— 侧根上若有一处在所有夹具里都是空格，删它就等于没删，尺子会安静地少一根针。
const (
	fxLegit  = "正常发"
	fxFailed = "故障发"
	fxBroken = "参数被拒发"
)

var fixtureNames = []string{fxLegit, fxFailed, fxBroken}

// anchors 是文档里那三张表各自的锚点。锚点写成 HTML 注释：渲染看不见，机器认得。
var anchors = map[string]string{
	"params": "<!-- kwire:params -->",
	"units":  "<!-- kwire:units -->",
	"holes":  "<!-- kwire:holes -->",
}

type prow struct {
	Side string
	Path string
	Type string
	Door string // 是 / 否
	Drop string // 没给会怎样
}

type urow struct {
	Unit string
	Path string
	Type string
}

type hrow struct {
	Side  string
	Path  string
	Cells []string
	Cause string
}

// trial 是一次「删掉一个键，看它撞到哪儿」的实测读数。
type trial struct {
	Fixture string
	Side    string
	Path    string
	Class   string
	Cells   []string
	Note    string
}

type fixture struct {
	name    string
	session string
	actor   string
	in      kernel.InputMeta
	out     kernel.OutputMeta
	cells   map[string]string
	args    map[string]any
}

var (
	rays     []string            // 漂了的，逐名
	blind    []string            // 核不了的：侧根上的空针
	vacuous  []string            // 每一次「这一格本来就是空的」的读数（含深处叶子，只点名不拦）
	zeroish  = map[string][]string{}
	ranCount = map[string]int{}
	trialAll []trial
)

func main() {
	docPath := flag.String("doc", "contract.md", "对账的那份文档")
	readme := flag.String("readme", "README.md", "工具名至少要在文档里出现一处")
	out := flag.String("out", "", "把逐路径完整读数写进这个文件（UTF-8）；不写就只出终端摘要")
	emit := flag.Bool("emit", false, "只导出 markdown 表，不做对账")
	flag.Parse()

	specP, specU, err := buildSpec()
	if err != nil {
		fmt.Println("KWIRE NOT VERIFIABLE: " + err.Error())
		os.Exit(2)
	}
	fx, err := buildFixtures()
	if err != nil {
		fmt.Println("KWIRE NOT VERIFIABLE: " + err.Error())
		os.Exit(2)
	}
	runTrials(fx, specP, specU)
	holes := collectHoles()

	if *emit {
		emitTables(specP, specU, holes)
		return
	}

	doc, err := os.ReadFile(*docPath)
	if err != nil {
		fmt.Println("KWIRE NOT VERIFIABLE: 读不到 " + *docPath + "：" + err.Error())
		os.Exit(2)
	}
	checkParams(doc, specP)
	checkUnits(doc, specU)
	checkHoles(doc, holes)
	checkToolDocs(*readme, string(doc))
	checkStaleClaims(doc)

	if *out != "" {
		writeReport(*out, specP, specU, holes)
	}

	fmt.Printf("KWIRE paths=%d units=%d holes=%d trials=%d vacuous=%d tools_doc_missing=%d\n",
		len(specP), len(specU), len(holes), len(trialAll), len(vacuous), lastToolMiss)
	fmt.Printf("DRIFT %d NOT-VERIFIABLE %d\n", len(rays), len(blind))
	for _, r := range rays {
		fmt.Println("  DRIFT " + r)
	}
	for _, b := range blind {
		fmt.Println("  NOT-VERIFIABLE " + b)
	}
	switch {
	case len(rays) > 0:
		os.Exit(1)
	case len(blind) > 0:
		os.Exit(2)
	default:
		fmt.Println("KWIRE OK: 写侧最少接口与文档逐路径对上")
		os.Exit(0)
	}
}

// ---- 代码那一头：反射出 wire 形状 ----

func buildSpec() ([]prow, []urow, error) {
	var ps []prow
	usSeen := map[string]bool{}
	var us []urow
	queue := []reflect.Type{}

	addSide := func(side string, t reflect.Type) error {
		if t.Kind() != reflect.Struct {
			return fmt.Errorf("%s 侧不是结构体", side)
		}
		door := map[string]bool{}
		for _, k := range mcpsrv.DoorRequiredBlocks(side) {
			door[k] = true
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tok := jsonOf(f.Type)
			ps = append(ps, prow{Side: side, Path: f.Name, Type: tok,
				Door: yesNo(door[f.Name]), Drop: "-"})
			if u := unitOf(f.Type); u != nil && !usSeen[u.Name()] {
				usSeen[u.Name()] = true
				queue = append(queue, u)
			}
		}
		return nil
	}
	if err := addSide("input", reflect.TypeOf(kernel.InputMeta{})); err != nil {
		return nil, nil, err
	}
	if err := addSide("output", reflect.TypeOf(kernel.OutputMeta{})); err != nil {
		return nil, nil, err
	}
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tok := jsonOf(f.Type)
			us = append(us, urow{Unit: t.Name(), Path: f.Name, Type: tok})
			if u := unitOf(f.Type); u != nil && !usSeen[u.Name()] {
				usSeen[u.Name()] = true
				queue = append(queue, u)
			}
		}
	}
	sort.SliceStable(us, func(i, j int) bool {
		if us[i].Unit != us[j].Unit {
			return us[i].Unit < us[j].Unit
		}
		return us[i].Path < us[j].Path
	})
	return ps, us, nil
}

func yesNo(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

// jsonOf 交出「调用方这一格该写成什么」。名字里带类型的（Basis、PartKind 这一族）
// 保留名字：它们是枚举，裸写 number/string 会把「这一格只有六个取值」这件事弄丢。
func jsonOf(t reflect.Type) string {
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return "array<" + jsonOf(t.Elem()) + ">"
	case reflect.Map:
		return "object<键 " + jsonOf(t.Key()) + " 到 " + jsonOf(t.Elem()) + ">"
	case reflect.Struct:
		if t.Name() == "" {
			return "object<匿名结构体>"
		}
		return "unit " + t.Name()
	case reflect.Bool:
		return "boolean"
	case reflect.String:
		if n := t.Name(); n != "" && n != "string" {
			return "string(" + n + ")"
		}
		return "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n := t.Name(); n != "" && !isBuiltinInt(n) {
			return "number(" + n + ")"
		}
		return "number"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Pointer:
		return "引用<" + jsonOf(t.Elem()) + ">"
	case reflect.Interface:
		return "任意"
	}
	return "未知<" + t.String() + ">"
}

func isBuiltinInt(n string) bool {
	for _, b := range []string{"int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr"} {
		if n == b {
			return true
		}
	}
	return false
}

// unitOf 取出这个类型落下去要展开的那一层（数组/映射的叶子结构体算进去）。
func unitOf(t reflect.Type) reflect.Type {
	switch t.Kind() {
	case reflect.Struct:
		if t.Name() != "" {
			return t
		}
	case reflect.Slice, reflect.Array, reflect.Map:
		return unitOf(t.Elem())
	}
	return nil
}

// ---- 代码那一头：真跑一遍，删一个键问一处 ----

func buildFixtures() ([]*fixture, error) {
	list := []*fixture{newLegitFixture(), newFailedFixture(), newBrokenFixture()}
	for _, f := range list {
		b, err := json.Marshal(map[string]any{
			"session": f.session, "actor": f.actor, "input": f.in, "output": f.out,
		})
		if err != nil {
			return nil, fmt.Errorf("夹具 %s 编不出字节：%v", f.name, err)
		}
		if msg, refused := mcpsrv.DoorRefuse(b); refused {
			return nil, fmt.Errorf("夹具 %s 连门口都过不了：%s", f.name, msg)
		}
		in, out, msg := mcpsrv.DoorDecode(b)
		if msg != "" {
			return nil, fmt.Errorf("夹具 %s 严格解码解不开：%s", f.name, msg)
		}
		l := kernel.NewLedger()
		if _, err := l.RecordCall(f.session, f.actor, in, out); err != nil {
			return nil, fmt.Errorf("夹具 %s 自己就落不进账：%v", f.name, err)
		}
		if err := l.Verify(); err != nil {
			return nil, fmt.Errorf("夹具 %s 落完账链不自洽：%v", f.name, err)
		}
		f.cells = cellsOf(in, out)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("夹具 %s 回读不出字段表：%v", f.name, err)
		}
		f.args = m
	}
	return list, nil
}

func newLegitFixture() *fixture {
	shares := map[kernel.PartKind]int{
		kernel.KindSystem: 8, kernel.KindMemory: 18, kernel.KindHistory: 32,
		kernel.KindObserved: 25, kernel.KindUser: 12, kernel.KindNotice: 5,
	}
	b, err := kernel.DeriveBudget(24000, 25, shares,
		kernel.EstimatedOf(1200, "骨架+模型名+工具清单+每段 40 字节"))
	if err != nil {
		fmt.Println("KWIRE NOT VERIFIABLE: 预算夹具不合法: " + err.Error())
		os.Exit(2)
	}
	return &fixture{
		name: fxLegit, session: "kwire-legit", actor: "kwire",
		in: kernel.InputMeta{
			Call:  2,
			Order: []kernel.PartKind{kernel.KindSystem, kernel.KindUser, kernel.KindHistory, kernel.KindNotice},
			Parts: []kernel.PartCount{
				{Kind: kernel.KindSystem, Parts: 1, Bytes: 500},
				{Kind: kernel.KindUser, Parts: 1, Bytes: 1200},
				{Kind: kernel.KindHistory, Parts: 1, Bytes: 800},
				{Kind: kernel.KindNotice, Parts: 1, Bytes: 60},
			},
			ContentBytes: 2560,
			Tokens:       kernel.EstimatedOf(640, "按段字节数估：CJK 一字一 token、其余四字节一 token"),
			Budget:       b,
			Cuts: []kernel.Cut{{
				Kind: kernel.KindHistory, Ref: "step1:read_file:a.md", RefCall: 1,
				Role: kernel.RoleTool, Squeeze: kernel.SqueezeFolded,
				FromBytes: 5000, ToBytes: 124, SHA256: hex64("被折掉的那一段原文"),
			}},
			Notice: kernel.Notice{Sent: true, Trimmed: true,
				Why: "超出一发额度，先折历史", Text: "本节被裁过：历史 1 段折成了桩"},
		},
		out: kernel.OutputMeta{
			Call: 2, Streaming: true,
			Finish:         kernel.Finish{Reason: "stop", Said: true},
			Model:          "agnes-1", ModelSaid: true,
			WireFormat:     "chat.completion.chunk", WireFormatSaid: true,
			Says:           kernel.Says{Bytes: 900, Kept: 900, SHA256: hex64("模型这一发的原话")},
			UsageIn:        kernel.RelayedOf(1587, "endpoint:agnes"),
			UsageOut:       kernel.RelayedOf(244, "endpoint:agnes"),
			WireBytesOut:   kernel.MeasuredOf(11570),
			WireBytesIn:    kernel.MeasuredOf(4096),
			Chunks:         kernel.MeasuredOf(37),
			FirstByteMs:    kernel.MeasuredOf(921),
			MaxGapMs:       kernel.MeasuredOf(1200),
			ToolCalls:      1,
			Malformed:      0,
			MalformedStreak: 0,
			Failure: kernel.Failure{
				Status:       kernel.AbsentBecause("这一发没故障，也就没有要记的状态码"),
				RetryAfterMs: kernel.AbsentBecause("这一发没故障，对端没说等多久"),
			},
		},
	}
}

func newFailedFixture() *fixture {
	f := newLegitFixture()
	f.name = fxFailed
	f.session = "kwire-fail"
	f.out = kernel.OutputMeta{
		Call: 3, Streaming: false,
		Finish:   kernel.Finish{Reason: "", Said: false},
		Model:    "agnes-1", ModelSaid: true,
		WireFormat: "", WireFormatSaid: false,
		Says:       kernel.Says{Bytes: 0, Kept: 0, SHA256: hex64("")}, // 一个字的原话也没回来，指纹是空串的指纹
		UsageIn:    kernel.AbsentBecause("429 那一发对端没回 usage"),
		UsageOut:   kernel.AbsentBecause("429 那一发对端没回 usage"),
		WireBytesOut: kernel.MeasuredOf(9800),
		WireBytesIn:  kernel.MeasuredOf(180),
		Chunks:       kernel.AbsentBecause("这一发不分帧"),
		FirstByteMs:  kernel.AbsentBecause("这一发不分帧"),
		MaxGapMs:     kernel.AbsentBecause("这一发不分帧"),
		ToolCalls:    0,
		Failure: kernel.Failure{
			Kind:         kernel.FailHTTP,
			Detail:       "429 Too Many Requests",
			Status:       kernel.MeasuredOf(429),
			RetryAfterMs: kernel.RelayedOf(30, "endpoint 响应头 Retry-After"),
		},
	}
	f.in.Call = 3
	return f
}

// newBrokenFixture 补的是前两发里合法为零的那三格：`Malformed`、`MalformedStreak`、`Downgrade`。
// 现场是一发流式、对端 400 拒了参数、提议里有两个执行不了的 —— 这三格在这样的发上才可能非零。
func newBrokenFixture() *fixture {
	f := newLegitFixture()
	f.name = fxBroken
	f.session = "kwire-broken"
	f.in.Call = 4
	f.out.Call = 4
	f.out.Says.Kept = 300 // 留住一截：这一发的「裁了没有」由 Says 那两格自己算
	f.out.ToolCalls = 3
	f.out.Malformed = 2
	f.out.MalformedStreak = 2
	f.out.Failure = kernel.Failure{
		Kind:         kernel.FailConfig,
		Detail:       "对端不接受 stream=true",
		Status:       kernel.MeasuredOf(400),
		RetryAfterMs: kernel.AbsentBecause("对端没说等多久"),
		Downgrade:    true,
	}
	return f
}

func cellsOf(in kernel.InputMeta, out kernel.OutputMeta) map[string]string {
	m := map[string]string{}
	put := func(side string, cs []kernel.Cell) {
		for _, c := range cs {
			m[side+" "+c.Field] = c.Basis.String() + "|" + fmt.Sprintf("%v", c.Value)
		}
	}
	put("input", in.Cells())
	put("output", out.Cells())
	return m
}

func runTrials(list []*fixture, ps []prow, us []urow) {
	sites := dropSites(ps, us)
	for _, f := range list {
		for _, st := range sites {
			raw, orig, ok := f.drop(st.Side, st.Path)
			if !ok {
				continue // 这份夹具里没有这个站点（正常发没有故障发才有的那一处）
			}
			k := st.Side + " " + st.Path
			ranCount[k]++
			if isZeroish(orig) {
				vacuous = append(vacuous, f.name+" "+k)
				zeroish[k] = append(zeroish[k], f.name)
			}
			cls, cells := classify(f, raw)
			trialAll = append(trialAll, trial{Fixture: f.name, Side: st.Side, Path: st.Path, Class: cls, Cells: cells})
		}
	}
	// 空针有两种后果，分开算：
	// 侧根上的一格（调用方直接要写的那个键）在所有夹具里都是零值 ⇒ 这一处对照**整条测不出事**，
	// 尺子少一根针而读数看着像合格 —— 那是「核不了」，不是「一致」。
	// 深处的一格（`Budget.ClampRounds`、`Measure.Refused` 这一族）合法地常年为零，点名并写进报告，不拦。
	for _, p := range ps {
		k := p.Side + " " + p.Path
		if len(zeroish[k]) > 0 && len(zeroish[k]) == ranCount[k] {
			blind = append(blind, "空针（调用方要直接写的那一格）"+k+
				"：三份夹具里它都是零值，删与不删是同一份参数 —— 这一处测不出事，不许读成合格")
		}
	}
	fillDrops(ps)
}

// fillDrops 把「少写这一处会怎样」写回每一行。这一列一个人也不许写：
// 它是上面那一串实删实跑的汇总，三份夹具撞到的不一样就分别点名，不合并成一句好听的。
func fillDrops(ps []prow) {
	for i := range ps {
		var got []string
		classes := map[string]bool{}
		for _, t := range trialAll {
			if t.Side == ps[i].Side && t.Path == ps[i].Path {
				got = append(got, t.Fixture+"="+t.Class)
				classes[t.Class] = true
			}
		}
		switch {
		case len(got) == 0:
			ps[i].Drop = "没测（三份夹具里都没有这个站点）"
		case len(classes) == 1:
			ps[i].Drop = firstClass(got[0])
		default:
			ps[i].Drop = strings.Join(got, " ")
		}
	}
}

func firstClass(s string) string {
	if i := strings.IndexByte(s, '='); i >= 0 {
		return s[i+1:]
	}
	return s
}

func classify(f *fixture, raw []byte) (string, []string) {
	if msg, refused := mcpsrv.DoorRefuse(raw); refused {
		return clsDoor, strings.Split(msg, "；")
	}
	in, out, msg := mcpsrv.DoorDecode(raw)
	if msg != "" {
		return clsDecode, []string{msg}
	}
	l := kernel.NewLedger()
	if _, err := l.RecordCall(f.session, f.actor, in, out); err != nil {
		return clsRecord, []string{firstLine(err.Error())}
	}
	var changed, honest []string
	for k, after := range cellsOf(in, out) {
		before, has := f.cells[k]
		if !has || before == after {
			continue
		}
		// 出处被改成「缺席」的，是这一族**已经修好**的形状：没交的那一格现在会说「没交」，
		// 而不是借空表那句「0 段」冒充实测。它不该再算洞，也不该算「落账不变」——
		// 后者会让一次真修复在尺子上长得像什么都没发生。
		if strings.HasPrefix(after, kernel.BasisMeasured.String()+"|") {
			changed = append(changed, k)
		} else if strings.HasPrefix(after, kernel.BasisAbsent.String()+"|") {
			honest = append(honest, k)
		}
	}
	if len(changed) > 0 {
		sort.Strings(changed)
		return clsSilent, changed
	}
	if len(honest) > 0 {
		sort.Strings(honest)
		return clsHonest, honest
	}
	return clsSame, nil
}
