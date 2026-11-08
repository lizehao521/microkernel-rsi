package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"agentkernel/mcpsrv"
)

func hex64(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// site 是一个「调用方少写这一处」的写点。单位成员按站点展开，不按单位名合并：
// `Tokens` 与 `UsageIn` 同为 Measure，少写一格撞到的判据不一样。
type site struct {
	Side string
	Path string
}

func dropSites(ps []prow, us []urow) []site {
	members := map[string][]urow{}
	for _, u := range us {
		members[u.Unit] = append(members[u.Unit], u)
	}
	seen := map[string]bool{}
	var out []site
	var expand func(side, path, tok string)
	expand = func(side, path, tok string) {
		unit := unitName(tok)
		if unit == "" {
			return
		}
		suffix := ""
		if strings.HasPrefix(tok, "array<") {
			suffix = "[]"
		}
		for _, m := range members[unit] {
			cp := path + suffix + "." + m.Path
			key := side + " " + cp
			if !seen[key] {
				seen[key] = true
				out = append(out, site{side, cp})
			}
			expand(side, cp, m.Type)
		}
	}
	for _, p := range ps {
		key := p.Side + " " + p.Path
		if !seen[key] {
			seen[key] = true
			out = append(out, site{p.Side, p.Path})
		}
		expand(p.Side, p.Path, p.Type)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Side != out[j].Side {
			return out[i].Side < out[j].Side
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func unitName(tok string) string {
	i := strings.Index(tok, "unit ")
	if i < 0 {
		return ""
	}
	tail := tok[i+len("unit "):]
	if j := strings.IndexByte(tail, '>'); j >= 0 {
		return tail[:j]
	}
	return tail
}

// drop 交出一份「少写了这一处」的参数字节，第二只返回值是被删掉的原件（用来判这一发对照是不是空的）。
// 第三只为假表示这份夹具里根本没有这一处（正常发没有故障发才有的站点），那不是漂，是没测。
func (f *fixture) drop(side, path string) ([]byte, any, bool) {
	b, err := json.Marshal(f.args)
	if err != nil {
		return nil, nil, false
	}
	var clone map[string]any
	if json.Unmarshal(b, &clone) != nil {
		return nil, nil, false
	}
	sideRaw, ok := clone[side]
	if !ok {
		return nil, nil, false
	}
	cur, ok := sideRaw.(map[string]any)
	if !ok {
		return nil, nil, false
	}
	seg := strings.Split(path, ".")
	if seg[0] == "" {
		return nil, nil, false
	}
	var orig any
	last := seg[len(seg)-1]
	for i := 0; i < len(seg)-1; i++ {
		name := strings.TrimSuffix(seg[i], "[]")
		v, has := cur[name]
		if !has {
			return nil, nil, false
		}
		if strings.HasSuffix(seg[i], "[]") {
			arr, isArr := v.([]any)
			if !isArr || len(arr) == 0 {
				return nil, nil, false
			}
			hit := false
			for _, e := range arr {
				m, isObj := e.(map[string]any)
				if !isObj {
					return nil, nil, false
				}
				if o, has := m[last]; has {
					if !hit {
						orig, hit = o, true
					}
					delete(m, last)
				}
			}
			if !hit {
				return nil, nil, false
			}
			return mustMarshal(clone), orig, true
		}
		nxt, isObj := v.(map[string]any)
		if !isObj {
			return nil, nil, false
		}
		cur = nxt
	}
	o, has := cur[last]
	if !has {
		return nil, nil, false
	}
	orig = o
	delete(cur, last)
	return mustMarshal(clone), orig, true
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// isZeroish 答「这份夹具里这一格本来就是空的吗」。
// 空的 ⇒ 「删掉它」与「没删」是同一份参数，这一条对照测不出任何事 —— 空针。
// 空针不许被读成合格，所以它单独成为一种「核不了」。
func isZeroish(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case string:
		return x == ""
	case float64:
		return x == 0
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// collectHoles 把「少写了这一处，账却照落、还把格子改署成实测」的写点收成一张名单。
// 这就是 #66 那一族的全部规模：不是设想出来的，是一处一处删出来问出来的。
func collectHoles() []hrow {
	byKey := map[string]*hrow{}
	per := map[string]map[string]string{} // 写点 -> 夹具 -> 撞到哪儿（三份都记，好把「只在一份里洞」认出来）
	collect := func(t trial) map[string]string {
		k := t.Side + " " + t.Path
		if per[k] == nil {
			per[k] = map[string]string{}
		}
		return per[k]
	}
	for _, t := range trialAll {
		collect(t)[t.Fixture] = t.Class
		if t.Class != clsSilent {
			continue
		}
		k := t.Side + " " + t.Path
		h, ok := byKey[k]
		if !ok {
			h = &hrow{Side: t.Side, Path: t.Path}
			byKey[k] = h
		}
		h.Cells = append(h.Cells, t.Cells...)
	}
	var out []hrow
	for k, h := range byKey {
		sort.Strings(h.Cells)
		h.Cells = dedup(h.Cells)
		var cs []string
		for _, f := range fixtureNames {
			if v, ok := per[k][f]; ok {
				cs = append(cs, f+"="+v)
			}
		}
		h.Cause = strings.Join(cs, " ")
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Side != out[j].Side {
			return out[i].Side < out[j].Side
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func dedup(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// ---- 文档那一头：解析带锚点的表 ----

type docTable struct {
	found bool
	rows  [][]string
	head  []string
}

// parseTable 从锚点往后收连续的 `|` 行。第一行是表头，第二行是分隔，其余是数据。
// 单元格里的反引号与空白一律剥掉：文档里写 `` `Budget.Shares` ``，实读写 `Budget.Shares`，
// 这两种写法必须是同一条。
func parseTable(doc []byte, name string) docTable {
	src := string(doc)
	i := strings.Index(src, anchors[name])
	if i < 0 {
		return docTable{}
	}
	var rows [][]string
	for _, line := range strings.Split(src[i+len(anchors[name]):], "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "|") {
			if len(rows) > 0 {
				break
			}
			continue
		}
		cells := strings.Split(strings.Trim(t, "|"), "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(strings.Trim(strings.TrimSpace(cells[j]), "`"))
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return docTable{found: true}
	}
	return docTable{found: true, head: rows[0], rows: rows[2:]}
}

func checkParams(doc []byte, ps []prow) {
	tb := parseTable(doc, "params")
	if !tb.found {
		rays = append(rays, "params 表缺失：文档里没有 "+anchors["params"]+"（分母 "+fmt.Sprint(len(ps))+" 条路径没有一处写了类型）")
		return
	}
	if len(tb.rows) == 0 {
		blind = append(blind, "params 表解析不出数据行")
		return
	}
	seen := map[string]bool{}
	for _, r := range tb.rows {
		if len(r) < 5 {
			rays = append(rays, "params 表有一行不足五列："+strings.Join(r, " | "))
			continue
		}
		k := r[0] + " " + r[1]
		seen[k] = true
		var want *prow
		for i := range ps {
			if ps[i].Side+" "+ps[i].Path == k {
				want = &ps[i]
				break
			}
		}
		if want == nil {
			rays = append(rays, "params 表多出一行：文档写了 "+k+"，反射里没有这个写点")
			continue
		}
		if r[2] != want.Type {
			rays = append(rays, "类型漂 "+k+"：文档说 "+r[2]+"，代码是 "+want.Type)
		}
		if r[3] != want.Door {
			rays = append(rays, "门口漂 "+k+"：文档说整块必填="+r[3]+"，门口那份表说="+want.Door)
		}
		if r[4] != want.Drop {
			rays = append(rays, "后果漂 "+k+"：文档说 "+r[4]+"，实跑是 "+want.Drop)
		}
	}
	for _, p := range ps {
		if !seen[p.Side+" "+p.Path] {
			rays = append(rays, "params 表少一行：调用方要写的 "+p.Side+" "+p.Path+"（"+p.Type+"）文档里没提")
		}
	}
}

func checkUnits(doc []byte, us []urow) {
	tb := parseTable(doc, "units")
	if !tb.found {
		rays = append(rays, "units 表缺失：文档里没有 "+anchors["units"]+"（分母 "+fmt.Sprint(len(us))+" 个单位成员）")
		return
	}
	seen := map[string]bool{}
	for _, r := range tb.rows {
		if len(r) < 3 {
			rays = append(rays, "units 表有一行不足三列："+strings.Join(r, " | "))
			continue
		}
		k := r[0] + " " + r[1]
		seen[k] = true
		var want *urow
		for i := range us {
			if us[i].Unit+" "+us[i].Path == k {
				want = &us[i]
				break
			}
		}
		if want == nil {
			rays = append(rays, "units 表多出一行：单位 "+k+" 反射里没有这个成员")
			continue
		}
		if r[2] != want.Type {
			rays = append(rays, "单位成员类型漂 "+k+"：文档说 "+r[2]+"，代码是 "+want.Type)
		}
	}
	for _, u := range us {
		if !seen[u.Unit+" "+u.Path] {
			rays = append(rays, "units 表少一行："+u.Unit+"."+u.Path+"（"+u.Type+"）")
		}
	}
}

func checkHoles(doc []byte, hs []hrow) {
	tb := parseTable(doc, "holes")
	if !tb.found {
		rays = append(rays, "holes 表缺失：文档里没有 "+anchors["holes"]+"（实删实跑出 "+fmt.Sprint(len(hs))+" 个写点会静默落账）")
		return
	}
	seen := map[string]bool{}
	for _, r := range tb.rows {
		if len(r) < 3 {
			rays = append(rays, "holes 表有一行不足三列："+strings.Join(r, " | "))
			continue
		}
		seen[r[0]+" "+r[1]] = true
	}
	for _, h := range hs {
		k := h.Side + " " + h.Path
		if !seen[k] {
			rays = append(rays, "空洞未声明 "+k+"：少写这一处照样落账，改署实测的格是 "+strings.Join(h.Cells, "、")+"（"+h.Cause+"）")
		}
	}
	for _, r := range tb.rows {
		if len(r) >= 2 {
			k := r[0] + " " + r[1]
			hit := false
			for _, h := range hs {
				if h.Side+" "+h.Path == k {
					hit = true
				}
			}
			if !hit {
				rays = append(rays, "holes 表里这一条已经不再发生，别留在名单上："+k+"（这一族已修，改了判据表就得把这条划掉）")
			}
		}
	}
}

// staleClaims 是文档里几句**可以被代码证伪**的断言。
// 每条写清「这句话在主张什么」与「代码里哪一行的存在与否能证明它」：
// 主张过期的句子里，条件成立 ⇒ 这句话现在是错的 ⇒ 红。
// 这里只放点名道姓的句子，不放印象；加一条得先有一处实测把它证伪。
type staleClaim struct {
	Phrase   string // 文档里那句原话（子串匹配）
	CodeFile string // 去哪儿看
	CodeMark string // 看有没有这个记号
	// 记号存在 ⇒ 那句话过期（CodeMark 描述的是**修好之后**才有的行为）
}

var staleClaims = []staleClaim{
	{
		Phrase:   "交整数目前是静默丢字段",
		CodeFile: "mcpsrv/mcpsrv.go",
		CodeMark: "DisallowUnknownFields",
	},
	{
		Phrase:   "`cmd/mcpkernel` 的 `args[T]`",
		CodeFile: "mcpsrv/mcpsrv.go",
		CodeMark: "func decodeCallArgs",
	},
}

// retractMarks 让文档可以**引用**一句已经死了的断言 —— 只要引用它的那一行自己标明它死了。
// 不加这一条，「把契约自己的错记在案」与「把错的口径再抄一遍」在尺子眼里长得一样，
// 于是每次订正都得删掉来历；而删掉来历的文档，下一次会重犯同一个错。
var retractMarks = []string{"过期", "不再成立", "已修", "已改准", "都不对", "当时"}

func checkStaleClaims(doc []byte) {
	lines := strings.Split(string(doc), "\n")
	for _, c := range staleClaims {
		src, err := os.ReadFile(c.CodeFile)
		if err != nil {
			blind = append(blind, "断言核不了：读不到 "+c.CodeFile)
			continue
		}
		if !strings.Contains(string(src), c.CodeMark) {
			continue // 代码还没修到那一步，这句话今天仍然成立
		}
		for i, line := range lines {
			if !strings.Contains(line, c.Phrase) {
				continue
			}
			labeled := false
			for _, m := range retractMarks {
				if strings.Contains(line, m) {
					labeled = true
					break
				}
			}
			if !labeled {
				rays = append(rays, fmt.Sprintf("文档第 %d 行还在主张一句已过期：%q，而 %s 里现在有 %s"+
					"（要引用它，请在那一行里标明它死了：%s）",
					i+1, c.Phrase, c.CodeFile, c.CodeMark, strings.Join(retractMarks, "/")))
			}
		}
	}
}

var lastToolMiss int

func checkToolDocs(readme, doc string) {
	var b strings.Builder
	if r, err := os.ReadFile(readme); err == nil {
		b.WriteString(string(r))
	}
	all := doc + b.String()
	missing := []string{}
	for _, name := range mcpsrv.ToolNames() {
		short := strings.TrimPrefix(name, "kernel.")
		if !strings.Contains(all, short) {
			missing = append(missing, name)
		}
	}
	lastToolMiss = len(missing)
	for _, m := range missing {
		rays = append(rays, "工具没有一处文档："+m+"（contract.md 与 README.md 里搜不到这个名字，调用方只能靠猜）")
	}
}

// ---- 导出与读数 ----

func emitTables(ps []prow, us []urow, hs []hrow) {
	fmt.Println(anchors["params"])
	fmt.Println("| 侧 | 调用方要写的键 | JSON 类型 | 整块必填 | 少写这一处会怎样 |")
	fmt.Println("|---|---|---|---|---|")
	for _, p := range ps {
		fmt.Printf("| %s | %s | %s | %s | %s |\n", p.Side, p.Path, p.Type, p.Door, p.Drop)
	}
	fmt.Println()
	fmt.Println(anchors["units"])
	fmt.Println("| 单位 | 成员 | JSON 类型 |")
	fmt.Println("|---|---|---|")
	for _, u := range us {
		fmt.Printf("| %s | %s | %s |\n", u.Unit, u.Path, u.Type)
	}
	fmt.Println()
	fmt.Println(anchors["holes"])
	fmt.Println("| 侧 | 少写的那一处 | 落账后被改署成实测的格 | 三份夹具各撞到哪儿 |")
	fmt.Println("|---|---|---|---|")
	for _, h := range hs {
		fmt.Printf("| %s | %s | %s | %s |\n", h.Side, h.Path, strings.Join(h.Cells, "、"), h.Cause)
	}
}

func writeReport(path string, ps []prow, us []urow, hs []hrow) {
	var sb []string
	sb = append(sb, "接口对账尺读数（cmd/kwire）—— 逐写点：删掉这一处，实跑撞到哪一道")
	sb = append(sb, "")
	// 空洞按"写点在侧根上还是在块里"分两档自印：这两档的**修法不一样** ——
	// 块里的叶子要动门（门口只管整块在不在），侧根上的名单能靠 nil 与空表分开，
	// 侧根上的标量在 Go 里根本分不出"没给"与"给了零值"。手数这一档会数错（10-19 就错过一次：
	// 把 `output ToolCalls` 当成了块里）。带 `.` 的写点是块里的叶子，不带的在侧根上。
	rootH, leafH := 0, 0
	for _, h := range hs {
		if strings.Contains(h.Path, ".") {
			leafH++
		} else {
			rootH++
		}
	}
	sb = append(sb, fmt.Sprintf("写点 %d 个｜单位成员 %d 条｜删一处仍能落账的 %d 个（侧根 %d ＋ 块里叶子 %d）｜空针 %d 条｜单次删除 %d 回",
		len(ps)+len(us), len(us), len(hs), rootH, leafH, len(vacuous), len(trialAll)))
	sb = append(sb, "")
	sb = append(sb, "一、门口整块必填清单（实读 mcpsrv，不是文档抄的）")
	for _, side := range []string{"input", "output"} {
		sb = append(sb, fmt.Sprintf("  %s: %s", side, strings.Join(mcpsrv.DoorRequiredBlocks(side), "、")))
	}
	sb = append(sb, "")
	sb = append(sb, "二、逐写点实跑（同名写点在三份夹具里各跑一次）")
	for _, t := range trialAll {
		sb = append(sb, fmt.Sprintf("  [%s] %s %s => %s", t.Fixture, t.Side, t.Path, t.Class))
		if len(t.Cells) > 0 {
			// 这一行的标签跟着那一发撞到的那一档走。「改署实测的格」用在「落账但记缺席」那一条上
			// 是一句假话 —— 而它正是修好之后剩下的那种形状，把修好的读成没修，
			// 这把尺子就白造了。
			label := "改署实测的格: "
			if t.Class == clsHonest {
				label = "落成缺席的格: "
			}
			sb = append(sb, "      "+label+strings.Join(t.Cells, "、"))
		}
	}
	sb = append(sb, "")
	sb = append(sb, "三、少写仍能落账、且被署成实测的写点（缺席冒充那一族；#66 那一处 10-19 已修，见下面标签是「落成缺席」的那些行）")
	for _, h := range hs {
		sb = append(sb, fmt.Sprintf("  %s %s -> %s（%s）", h.Side, h.Path, strings.Join(h.Cells, "、"), h.Cause))
	}
	if len(vacuous) > 0 {
		sb = append(sb, "")
		sb = append(sb, "四、空针（三份夹具里这一格本来就是空的，这一条对照测不出事）")
		for _, v := range vacuous {
			sb = append(sb, "  "+v)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(sb, "\n")+"\n"), 0o644); err != nil {
		fmt.Println("KWIRE NOT VERIFIABLE: 报告写不出去 " + path + ": " + err.Error())
	}
}
