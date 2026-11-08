package main

import (
	"fmt"
	"io"
)

// checkLogAgainst 是「八条针变成一条可复跑命令」（E9）的头一块落点：
// 只读一本事件日志，把四支判据各自的读数一行一行交出去（GREEN/RED + 为什么 + 用的哪个上限）。
//
// 为什么要把它做进二进制里：判据的口径只有 Go 这一份才算数。冒烟早先是自己在 Python 里
// 数「每发取最小 seq 那条」，恰好对跨趟追加那本日志免疫（M2 两路都点到）—— 各数各的就会出现
// 「Go 红、Python 绿」那种两头都不认账的日志。尺子改成消费这一条命令的读数，两份口径就并成一份；
// Python 那侧只留一个用途：**比它对不对得上**（对不上就红，谁都不许默默覆盖谁）。
//
// 退出码沿用这一面已有的那六个里的两个：全绿 0 / 判据红 4（账上的_kind 指向停这一族）/
// 日志读不动 2（这一路跑不了）。不新造退出码。
func checkLogAgainst(w io.Writer, path string, limitMS int64) int {
	evs, err := ReadEvents(path)
	if err != nil {
		fmt.Fprintf(w, "check-log\t判据\t日志读得回来\tRED\t%v\n", err)
		return rcNotRun
	}
	rc := rcScript
	// 每一行都带上「用的是哪个上限」：只把口径印在末行的话，放宽过的 GREEN 与
	// 契约预算下的 GREEN 逐字同形，抄一条出去就能冒充（M5 扫端的 P1-6）。
	lim := fmt.Sprintf("ack_limit_ms=%d", limitMS)
	judges := [][2]string{
		{"ValidateLog", "序号接续、状态与出处合规、absent 带理由；" + lim},
		{"ValidateAccepts", "每发第一条是回执、预算内、载荷给全；" + lim},
		{"ValidateSettlement", "一发恰好一结、counts 与账对得上、开了账必有 run_settled；" + lim},
	}
	errs := []error{ValidateLog(evs), ValidateAcceptsWithin(evs, limitMS), ValidateSettlement(evs)}
	if len(evs) > 0 {
		tail := formatCursor(evs[len(evs)-1])
		judges = append(judges, [2]string{"ValidateReplay", "尾游标接得上（" + tail + "）；" + lim})
		errs = append(errs, ValidateReplay(evs, tail))
	}
	for i, j := range judges {
		if errs[i] != nil {
			rc = rcPolicy
			fmt.Fprintf(w, "check-log\t%s\t%s\tRED\t%v\n", j[0], j[1], errs[i])
			continue
		}
		fmt.Fprintf(w, "check-log\t%s\t%s\tGREEN\t%d 条\n", j[0], j[1], len(evs))
	}
	fmt.Fprintf(w, "check-log\t口径\tack_limit_ms=%d\tINFO\t%d 条日志、%d 支判据\n", limitMS, len(evs), len(judges))
	return rc
}
