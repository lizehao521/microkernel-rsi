package main

import (
	"fmt"
	"html"
	"os"
	"strconv"
	"strings"
)

// 这一份不是「面板」。面板（报告/面板方案-待批.txt）是 kaudit 那六条只读端点的表，
// 要等整轮落地、还要你圈那三个决定才开工。这里只是**同一趟循环自己吐的一份轨迹解剖**：
// 循环跑完，「哪一步是账决定的」必须能被人点开看，而不是只剩控制台滚过去的那几行。
// 三条禁区照抄面板那一份：不做时间线/瀑布（那是 OTel 那层已经做好的事）、
// 默认不显示原话原文、不做分数与排行。

type shotTrace struct {
	Call       int
	Assemble   string // 这一发装了什么（段名顺序 + 折没折历史 + 是不是换了说法）
	Precheck   string
	Record     string
	Basis      string
	Value      string
	BasisFlag  string   // 「fail_kind」或「本地」
	BasisCells []string // 这一发真正从账上读回来并用了的格名
	Action     string
	Why        string
	Extra      string    // 回边的另一半：这一发「我以为」的那件事，账上到底有没有落下
	Cells      int       // 这一发落进账几条
	OutCells   []cellRow // 输出侧 29 格，从 read_back 原样读回来的
	InCells    []cellRow // 输入侧 20 格，同上
	LocalFail  string    // 驱动当场给的故障类别（与读回的对照用）
	Drift      bool      // 读回的值与当场给的不一致：这一发的依据不算从账上读回来
}

var basisOrder = []string{"absent", "estimated", "measured", "model_says", "relayed"}

// tallyBuckets 从 kernel.tally 那句原话里取五档。取不到就回 present=false，
// 页面上必须显出来「读不到」—— 拿 0 顶替读不到是这本账最恨的那一族。
func tallyBuckets(text string) (map[string]int, int, bool) {
	i := strings.Index(text, "账 ")
	if i < 0 {
		return nil, 0, false
	}
	j := strings.Index(text[i:], "（")
	if j < 0 {
		return nil, 0, false
	}
	seg := text[i+j:]
	out := map[string]int{}
	for _, k := range basisOrder {
		mark := k + "="
		p := strings.Index(seg, mark)
		if p < 0 {
			return nil, 0, false
		}
		rest := seg[p+len(mark):]
		digits := ""
		for _, ch := range rest {
			if ch < '0' || ch > '9' {
				break
			}
			digits += string(ch)
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			return nil, 0, false
		}
		out[k] = n
	}
	head := strings.TrimSpace(text[i+len("账 ") : i+j])
	total, err := strconv.Atoi(strings.TrimSuffix(head, " 条"))
	if err != nil {
		total = 0
	}
	return out, total, true
}

func extraHTML(s string) string {
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "我以为") || strings.HasPrefix(s, "折历史的回读") {
		return `<br><span class="red">` + html.EscapeString(s) + `</span>`
	}
	return `<br><span class="dim">` + html.EscapeString(s) + `</span>`
}

// traceLedger 从**那一本事件日志**里数这一趟：发数、逐发条数、总条数（E8 同源）。
// 轨迹页从此不再自己乘一遍 —— 一份账两个投影；数不到就说「没量过」，绝不退回 r.shot 顶数：
// r.shot 数的是**落账的 shot**，被拒/装不出/驱动失败的那些发不算进去，
// 于是「共几发」在两处口径下本来就是两个数（这正是这条判据要抓的形）。
func (r *run) traceLedger() (turns int, perTurn map[int]int, all int, ok bool, why string) {
	perTurn = map[int]int{}
	if r.events == nil {
		return 0, perTurn, 0, false, "这一趟没开事件层（--events=false）"
	}
	path, _, degraded := r.events.stats()
	if path == "" {
		return 0, perTurn, 0, false, "事件层没落到盘上" + map[bool]string{true: "（降级：" + degraded + "）", false: ""}[degraded != ""]
	}
	evs, err := ReadEvents(path)
	if err != nil {
		return 0, perTurn, 0, false, "读不回那本账：" + err.Error()
	}
	rid := r.events.runID()
	for _, e := range evs {
		if e.Session != r.session || (rid != "" && e.Run != rid) {
			continue // 只数这一趟：同一对路径上追加的上一趟不算进这一页
		}
		all++
		if e.Turn > 0 {
			perTurn[e.Turn]++
		}
		if e.Kind == "turn_accepted" {
			turns++
		}
	}
	return turns, perTurn, all, true, ""
}

func (r *run) writeTrace(path string) error {
	if path == "" {
		return nil
	}
	tallyText := r.lastTally
	buckets, total, ok := tallyBuckets(tallyText)
	// 发数与逐发条数只有一个来源：事件日志（E8）。数不到就明说没量过。
	turns, perTurn, allEvents, ledgerOK, ledgerWhy := r.traceLedger()
	shotNote := fmt.Sprintf("事件日志数出来的这一趟共 %d 发", turns)
	if !ledgerOK {
		shotNote = fmt.Sprintf("共几发：没量过（%s）—— 不拿落账的 shot 数顶", ledgerWhy)
	}
	sb := []string{}
	w := func(f string, a ...any) { sb = append(sb, fmt.Sprintf(f, a...)) }

	w(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>agentd 轨迹解剖 · %s</title>`, html.EscapeString(r.session))
	w(`<style>body{font:14px/1.6 system-ui,sans-serif;margin:24px;max-width:1100px;color:#1b1b1b}`)
	w(`h1{font-size:20px;margin:0 0 4px}h2{font-size:15px;margin:26px 0 8px;border-bottom:1px solid #ddd;padding-bottom:4px}`)
	w(`.bar{display:flex;gap:6px;align-items:flex-end;height:78px;margin:8px 0}`)
	w(`.bar div{width:96px;color:#fff;text-align:center;font-size:12px;padding-top:3px}`)
	w(`table{border-collapse:collapse;width:100%%;font-size:13px}.tw{overflow-x:auto}td,th{border:1px solid #ddd;padding:5px 7px;vertical-align:top;text-align:left}`)
	w(`.k{background:#0b6b34}.e{background:#8a6d1f}.m{background:#134d7a}.s{background:#6b3fa0}.a{background:#5a5a5a}`)
	w(`.red{color:#a3151e;font-weight:600}.dim{color:#666}.no{background:#eee;color:#555}`)
	w(`.used{background:#e8f5ee;border:1px solid #b7dfc6;padding:0 4px;border-radius:3px;font-size:12px}`)
	w(`.cell{background:#e8f5ee;border:1px solid #b7dfc6;padding:0 4px;border-radius:3px;font-family:ui-monospace,monospace;font-size:12px}`)
	w(`@media (max-width:640px){body{margin:12px}.bar div{width:64px}td,th{padding:3px 4px}}</style>`)

	// ① 结论条：不许折叠，链不符/核不了时不许长得像绿。
	w(`<h1>agentd 轨迹解剖</h1>`)
	w(`<p class="%s"><b>账本触发过的决定：%d</b>（%s）· 另计：容量栏促成的停 %d 次（不与上面相加）· 结束原因：%s</p>`,
		map[bool]string{true: "", false: "red"}[r.kpi > 0], r.kpi, html.EscapeString(shotNote), r.caps, html.EscapeString(string(r.stop)))
	if ledgerOK {
		shares := make([]string, 0, len(perTurn))
		for t := 1; t <= turns; t++ {
			if n, has := perTurn[t]; has {
				shares = append(shares, fmt.Sprintf("%d:%d", t, n))
			}
		}
		w(`<p class="dim">同一本账的另一个投影：这一趟在事件日志里共 %d 条（发级 %d 发，逐发条数 %s）· 每发 49 格 × %d 发 = %d —— 本页的发数与条数都从这里读，不在这里再算一遍。</p>`,
			allEvents, turns, html.EscapeString(strings.Join(shares, " ")), turns, turns*49)
	} else {
		w(`<p class="red">事件日志数不到发数：%s ⇒ 这一页不显示发数，「每发 49 格 × 几发」也只说没量过，不退回内存里那个数。</p>`, html.EscapeString(ledgerWhy))
	}
	w(`<p>%s</p>`, html.EscapeString(r.lastVerify))
	if !strings.Contains(r.lastVerify, "✓") {
		w(`<p class="red">链不是绿的：上面那句就是内核原话，不翻译。</p>`)
	}
	w(`<p class="dim">这一趟是 %s（%s）。%s</p>`, html.EscapeString(r.drv.Name()),
		map[bool]string{true: "端点自己决定下一发", false: "本地剧本，不是模型自主决定"}[r.drv.Autonomous()],
		html.EscapeString(r.caveat))

	// ② 出身分布：五档 + absent 单独看得见（缺席 ≠ 0）。
	w(`<h2>出身分布（五档不塌；absent 是「结构上没有这一格」，不是零）</h2>`)
	if !ok {
		w(`<p class="red">— 读不到出处分布（tally 那句原话：%s）</p>`, html.EscapeString(firstLine(tallyText)))
	} else {
		mx := 1
		for _, v := range buckets {
			if v > mx {
				mx = v
			}
		}
		w(`<div class="bar">`)
		cls := map[string]string{"absent": "a", "estimated": "e", "measured": "m", "model_says": "s", "relayed": "k"}
		for _, k := range basisOrder {
			h := 12 + int(float64(buckets[k])/float64(mx)*60)
			w(`<div class="%s" style="height:%dpx" title="%s">%s<br>%d</div>`, cls[k], h, k, k, buckets[k])
		}
		// 这句只讲出身分布的条数；「× 几发」归发数那一行（同一本账的那个投影）。
		w(`</div><p class="dim">合计 %d 条（内核 tally 原话数出来的，与事件日志的两处条数不是同一本账）。</p>`, total)
	}

	// ③ 一发解剖：每发都要说得出「依据住在账上哪一格」。
	w(`<h2>一发解剖</h2><div class="tw"><table><tr><th>发</th><th>这一发装了什么</th><th>预检（内核说什么）</th><th>落账</th><th>几条</th><th>依据</th><th>读到的值</th><th>当场给的 / 读回的</th><th>动作</th><th>为什么</th></tr>`)
	for _, t := range r.traces {
		basis := `<span class="red">本地信息，没问内核</span>`
		if len(t.BasisCells) > 0 {
			codes := []string{}
			for _, c := range t.BasisCells {
				codes = append(codes, `<span class="cell">`+html.EscapeString(c)+`</span>`)
			}
			basis = strings.Join(codes, " ")
		}
		cells := `<span class="red">读不到</span>`
		if t.Cells >= 0 {
			cells = strconv.Itoa(t.Cells)
		}
		w(`<tr><td>%d</td><td>%s</td><td class="dim">%s</td><td>%s</td><td>%s</td><td>%s</td><td><code>%s</code></td>%s<td>%s</td><td class="dim">%s%s</td></tr>`,
			t.Call, html.EscapeString(t.Assemble), html.EscapeString(t.Precheck), html.EscapeString(t.Record),
			cells, basis, html.EscapeString(t.Value), pairHTML(t), html.EscapeString(t.Action),
			html.EscapeString(t.Why), extraHTML(t.Extra))
	}
	w(`</table></div>`)

	// ③b 每发 49 格：键名、出处、人话。全部是 read_back 原样读回来的，不是我们内存里那份。
	w(`<h2>每发 49 格（read_back 原样读回来；带点的是这一发真用了的那几格）</h2>`)
	// 标记只打在**这一发真用了的那几格**上：某格在每一发的表里都存在（哪怕那发没挤历史），
	// 拿全局集合去标会把「这发用了」标到没用它的那些发上去。
	for _, t := range r.traces {
		used := map[string]bool{}
		for _, c := range t.BasisCells {
			used[c] = true
		}
		if len(t.OutCells) == 0 && len(t.InCells) == 0 {
			w(`<p class="red">第 %d 发：读回关着（或被拒），这一发没有格子可摆 —— 不补空表。</p>`, t.Call)
			continue
		}
		w(`<details><summary>第 %d 发：输出侧 %d 格 / 输入侧 %d 格</summary>`, t.Call, len(t.OutCells), len(t.InCells))
		for _, side := range []struct {
			name string
			rows []cellRow
		}{{"输出侧", t.OutCells}, {"输入侧", t.InCells}} {
			w(`<div class="tw"><table><tr><th>%s</th><th>键名</th><th>出处</th><th>人话（内核渲染的，不是原文）</th></tr>`, side.name)
			for _, c := range side.rows {
				mark := ""
				cls := ""
				if used[c.Key] {
					mark = ` <span class="used">这一发用了它</span>`
					cls = ` class="cell"`
				}
				w(`<tr><td>%s</td><td%s>%s</td><td>%s</td><td class="dim">%s%s</td></tr>`,
					side.name, cls, html.EscapeString(c.Key), html.EscapeString(c.Basis), html.EscapeString(c.Claim), mark)
			}
			w(`</table></div>`)
		}
		w(`</details>`)
	}

	// ④ 回边能不能红：这一列直接对着 KPI 那条判据。
	w(`<h2>这条边是真回边还是虚接</h2>`)
	w(`<p>%s</p>`, map[bool]string{
		true:  `每发的动作都由 <code>read_back</code> 读回来的那一格触发；把 <code>--read-back</code> 关掉，下面这张表里的依据会整列变红、KPI 掉回 0。`,
		false: `<span class="red">这一趟关掉了 read_back：依据整列都是「本地」，KPI = 0。这就是「尺子会红」那一趟，别把它读成 Agent 跑好了。</span>`,
	}[r.readBack])
	sb = append(sb, driftLine(r.drift, r.kpi))

	w(`<h2 class="dim">这份件里没有的东西</h2>`)
	w(`<p class="dim">没有原话正文（只有格名、出处、人话与指纹），没有进度条以外的任何分数或排行，没有时间线/瀑布图。` +
		`它也不接任何端口：这一面压根没有写入口，账是内核把字节交给宿主、宿主自己写的。</p>`)

	return os.WriteFile(path, []byte(strings.Join(sb, "")), 0o644)
}

// pairHTML 把「当场给的类别」与「账上读回的类别」并排摆出来：
// 守卫不能只在收口报一个 0 —— 看不见的那一格迟早被人跳过。
func pairHTML(t shotTrace) string {
	if t.LocalFail == "" && t.Value == "" {
		return `<td class="dim"><span class="pair">这发没从账上取值</span></td>`
	}
	loc := t.LocalFail
	if loc == "" {
		loc = "（无故障）"
	}
	read := t.Value
	if read == "" || strings.HasPrefix(read, "（") {
		read = "（无故障）"
	}
	if t.Drift {
		return `<td><span class="pair red">当场给的 ` + html.EscapeString(loc) + ` ≠ 读回的 ` + html.EscapeString(read) + `</span></td>`
	}
	return `<td><span class="pair">` + html.EscapeString(loc) + ` = ` + html.EscapeString(read) + `</span></td>`
}

// driftLine 是页脚那句：漂移数必须自己站上页面，不能只活在 stdout。
func driftLine(drift, kpi int) string {
	if drift == 0 {
		return fmt.Sprintf(`<p>漂移：<b>0</b> 发 —— 每一发「当场给的类别」与「账上读回的类别」都对得上（KPI %d 发全部成立在这种一一对上上）。</p>`, kpi)
	}
	return fmt.Sprintf(`<p class="red">漂移：<b>%d</b> 发 —— 这些发的依据不算从账上读回来，已从 KPI 里扣掉。看上面那一列标红的行。</p>`, drift)
}
