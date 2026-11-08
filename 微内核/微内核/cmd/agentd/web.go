package main

import (
	"net/http"
	"strings"
)

// E7：三行界面（现在 / 已经 / 接下来）。这一页是**投影**，不是第二个算口径的地方 ——
// 所有数字都直接取自事件帧里后端已经交出的那几格；后端没给的，屏上写「没量过」，
// 不补零、不加总、不比大小（研究件 §5.5「界面只投影」/ 计划 E7）。
// 容器有界：日志列只留最近 12 行（会增长的区域一律有界）。

// threeLinePage 是零构建的单文件页：没有打包步骤，前端不自己算任何口径。
// 下面这段 JS 里出现 reduce / Math / parseInt / += 就等于让前端开始算账 ——
// TestPageDoesNotComputeAnything 就是抓这个的，臂 K5 会故意插一个 reduce 证明它会红。
const threeLinePage = `<!doctype html>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>agentd 事件面 · 三行</title>
<style>
body{font:14px/1.6 system-ui,sans-serif;margin:20px;max-width:760px;color:#141414;background:#fff}
h1{font-size:17px;margin:0 0 10px}
.row{border:1px solid #d6d6d6;border-radius:4px;padding:8px 10px;margin:8px 0;display:flex;gap:8px;align-items:baseline}
.row b{flex:0 0 4em;font-size:13px}
.row span{flex:1 1 auto;min-width:0;overflow-wrap:anywhere}
.tag{display:inline-block;border:1px solid #cfcfcf;border-radius:3px;padding:0 4px;margin-right:4px;font-size:12px}
.mut{color:#5a5a5a}
ul{margin:6px 0 0;padding-left:0;list-style:none;max-height:190px;overflow:auto;border-top:1px solid #eee}
li{font-family:ui-monospace,monospace;font-size:12px;padding:2px 0;border-bottom:1px solid #f2f2f2;overflow-wrap:anywhere}
.note{font-size:12px;color:#5a5a5a}@media (max-width:520px){body{margin:12px}.row b{flex:0 0 3em}.row span{flex:1 1 auto}}
</style>
<h1>agentd 事件面</h1>
<p class="note">三行都来自 /events 的帧，值一个不自己算；后端没交的就写「没量过」。</p>
<div class="row"><b>现在</b><span id="now">没量过</span></div>
<div class="row"><b>已经</b><span id="done">没量过</span></div>
<div class="row"><b>接下来</b><span id="next">没量过</span></div>
<ul id="log"></ul>
<script>
(function () {
  var KEEP = 12;
  function txt(v) {
    // 唯一的空值口径：后端没给 ⇒ 「没量过」。写 0 会被读成「量到了 0」。
    if (v === null || v === undefined || v === '') { return '没量过'; }
    return String(v);
  }
  function put(id, s) { document.getElementById(id).textContent = s; }
  function cell(k, v) { return k + '=' + txt(v); }
  function esc(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); }

  var es = new EventSource('/events');
  var lastHead = '';      // 最后一帧说的是哪一步
  var stream = '等第一条'; // 流自己的状态：接上 / 断了（浏览器会重连）
  var lastP = null;       // 最近一次后端交出的 progress（下一帧没带不抹掉）
  var lastSettle = null;  // 最近一次结算帧的 counts 与结束类别
  function renderNow() { if (lastHead !== '') { put('now', lastHead + ' · 流：' + stream); } else { put('now', stream); } }
  function renderDone() {
    var bits = [cell('done', lastP ? lastP.done : null), cell('total', lastP ? lastP.total : null),
                cell('limit', lastP ? lastP.limit : null)];
    if (lastSettle) {
      var ks = Object.keys(lastSettle.counts).sort();
      for (var i = 0; i < ks.length; i++) { bits.push(cell(ks[i], lastSettle.counts[ks[i]])); }
      bits.push(cell('结束类别', lastSettle.kind));
    }
    put('done', bits.join(' '));
  }
  es.onopen = function () { stream = '接上了 /events'; renderNow(); };
  es.onerror = function () { stream = '断了，重连由浏览器做'; renderNow(); };
  es.addEventListener('resume-lost', function (e) { stream = '接不上：' + txt(e.data); renderNow(); });
  es.onmessage = function (e) {
    var d;
    try { d = JSON.parse(e.data); } catch (err) { stream = '帧读不出 JSON'; renderNow(); return; }
    var head = txt(d.kind) + ' · ' + txt(d.state) + ' · 第 ' + txt(d.turn) + ' 发 · 出处 ' + txt(d.evidence);
    if (d.step && d.step.name) { head = head + ' · ' + txt(d.step.name); }
    lastHead = head;
    // 收到帧这件事本身就把流的状态更正为「在进」：不然一次服务端收尾触发的 onerror 会一直挂在屏上，
    // 读的人把「断了」当成「没数据」——而实测里帧确实还在一条条进来。
    stream = '在进';
    if (d.progress) { lastP = d.progress; }
    if (d.settle && d.settle.counts) { lastSettle = d.settle; }
    renderNow();
    renderDone();
    var nxt = '没量过';
    if (d.settle && d.settle.not_reached) {
      var rest = d.settle.not_reached;
      nxt = rest.length > 0 ? '这一发剩下：' + rest.join(' / ') : '四相都走完了';
    } else if (d.accept && d.accept.planned_shots !== undefined) {
      nxt = d.accept.planned_shots === 0 ? '剧本已尽' : '剧本还计划 ' + txt(d.accept.planned_shots) + ' 发';
    } else if (d.stop_why) {
      nxt = txt(d.stop_why);
    }
    put('next', nxt);
    var li = document.createElement('li');
    li.innerHTML = esc(txt(d.seq) + ' · ' + txt(d.kind) + ' · ' + txt(d.ts)) +
      (d.why ? ' <span class="mut">' + esc(txt(d.why)) + '</span>' : '');
    var ul = document.getElementById('log');
    ul.insertBefore(li, ul.firstChild);
    while (ul.children.length > KEEP) { ul.removeChild(ul.lastChild); }
  };
})();
</script>
`

// pageHandler 出这一页。它不读日志、不查数据库：所有数都从 /events 那条流里来，
// 所以「页面活不活」与「账本对不对」是两件事，各自有针。
// 挂在 "/" 上但**只认 "/"**：ServeMux 会把没登记的路径都兜到这儿来，
// 兜进来就当没这回事（404）—— 不拿「任何路径都出页面」冒充路由表。
func pageHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(threeLinePage))
	})
}

// pageScript 把页面里 <script> 那一段抠出来（针要拿它做 node --check 与「不许算口径」的判据）。
func pageScript() string {
	i := strings.Index(threeLinePage, "<script>")
	j := strings.Index(threeLinePage, "</scr"+"ipt>")
	if i < 0 || j < 0 || j <= i {
		return ""
	}
	return threeLinePage[i+len("<script>") : j]
}
