#!/usr/bin/env bash
# 封板轮的起跑脚本（机器重启后跑这一个就够）。
#
# 为什么要有它：2026-10-14 这一天里，慢闸失败过三次，三次都不是判据弱，而是**跑法**错：
#   1) 两轮并排跑（我留了一条"等上一轮结束就起跑"的链，杀掉上一轮却留着那条链，它自己开跑了）；
#   2) 一边 kill 一边 rm -rf 正在跑的 workdir，把轮子踩崩；
#   3) 在别人（或我自己的审计）抢磁盘内存的时候跑，47 分钟出一根针，看着像挂死。
# 所以这里写死三条规矩：**一次只跑一轮**；**先验等价性，对不上就不放大轮**；
# **不删别人正在用的 workdir**（每次用带时间戳的新目录）。
#
# 用法（重启后，机器空载时）：
#   bash tasks/probe/run_freeze_round.sh            # 先等价性验 9 根，再放整轮（根数由脚本自己印）
#   bash tasks/probe/run_freeze_round.sh eq         # 只跑等价性那 9 根
set -u
# 工具链在哪台机器上不在脚本里写死（#63）：旧版本把 /c/Users/Administrator/... 写进三行，
# 换一台机器就撞空路径 —— 而人会以为"按规矩跑了"。先探，探不到就当场说清找过哪几处。
GO=""
for c in "${GO_OVERRIDE:-}" "$(command -v go 2>/dev/null || true)" \
         "/c/Users/Admin/sdk/go/bin/go.exe" "/c/Users/Administrator/sdk/go/bin/go.exe"; do
  [ -n "$c" ] && [ -f "$c" ] && GO="$c" && break
done
PY=""
for c in "${PY_OVERRIDE:-}" "$(command -v python 2>/dev/null || true)" \
         "/c/Users/Admin/sdk/py314/python.exe" "/c/Users/Administrator/sdk/py314/python.exe"; do
  [ -n "$c" ] && [ -f "$c" ] && PY="$c" && break
done
if [ -z "$GO" ] || [ -z "$PY" ]; then
  echo "!! 找不到工具链：GO=[$GO] PY=[$PY]"
  echo "   找过：PATH 里的 go/python、GO_OVERRIDE/PY_OVERRIDE、/c/Users/{Admin,Administrator}/sdk/{go,py314}"
  echo "   不起跑。把上面两个变量指到实际装的地方（脚本不替你猜）。"
  exit 9
fi
TMPD="${TEMP:-${LOCALAPPDATA:-/c/Users/Admin/AppData/Local}/Temp}"
echo "本轮用的 GO=$GO ｜ PY=$PY ｜ TEMP=$TMPD"
cd "$(dirname "$0")/../.." || exit 9
export PATH="$(dirname "$GO"):$PATH"
STAMP=$(date +%m%d-%H%M%S)
PROBE=tasks/probe/kprobe.py
# 已知读数的 9 根：m13b/m20 全量共红 1 条（半成品轮）、m25 是 RED-WIDER 且共红 32 条（第一轮）、
# m33/m80 是 RED-ONLY-NAMED 1 条（修好夹具之后的筛针轮）、g1-g4 是 kcheck 闸针（第一轮 GATE-RED 4）。
EQ_FILTER="m13b-,m20-,m25-,m33-,m80-,g1-,g2-,g3-,g4-"
# 期望：RED-ONLY-NAMED 5 根（m13b/m20/m33/m80 + 无）、RED-WIDER 1 根（m25）、GATE-RED 4 根，坏针 0。
EXPECT='go test 针 5 根：RED-ONLY-NAMED 4、RED-WIDER 1、VACUOUS 0、TEST-BROKE(没跑成) 0、TEST-HANG(超时) 0'

EQW="$TMPD/kprobe-eq-$STAMP"
EQR="$EQW-report"
mkdir -p "$EQW"
echo "== 阶段 1：等价性验 9 根（取材范围已缩到能编译的文件，必须复现存档分类）=="
"$PY" -u "$PROBE" "$GO" "$PWD" "$EQW" "$EQR" "$EQ_FILTER"
EQEXIT=$?
echo "EQ_EXIT=$EQEXIT"
grep -E '针数|go test 针|kcheck  针|两类共用|取材方式|对照 0|对照 T|空针|这一轮不绿' "$EQR"
if [ "${1:-}" = "eq" ]; then exit $EQEXIT; fi
if ! grep -qF "$EXPECT" "$EQR"; then
  echo "!! 等价性对不上这句期望读数：$EXPECT"
  echo "   不放大轮。先把差异逐根贴出来看清楚，再决定改探针还是改判据。"
  exit 3
fi
if ! grep -q 'GATE-RED 4' "$EQR"; then
  echo "!! 4 根 kcheck 闸针没全红 —— 闸门这一层没被验到，不放大轮。"
  exit 4
fi
echo "== 阶段 2：整轮（表里全部针，根数由脚本自己印，这里不写死）=="
FW="$TMPD/kprobe-final-$STAMP"
FR="$FW-report"
mkdir -p "$FW"
"$PY" -u "$PROBE" "$GO" "$PWD" "$FW" "$FR"
echo "ROUND_EXIT=$?"
grep -E '针数|go test 针|kcheck  针|两类共用|取材方式|共享树|对照 0|对照 T|空针|这一轮不绿' "$FR"
# 把整轮的原始读数归到 报告/ 里，让封板报告 §7 引用的是一份存在的文件，而不是我转述的话。
ARCHIVE="报告/封板轮-整轮-$STAMP.txt"
{ echo "整轮慢闸原始读数（由 tasks/probe/kprobe.py 自己印出，未编辑）。跑于 $STAMP；报告文件 $FR"; echo;
  cat "$FR" 2>/dev/null; } > "$ARCHIVE" 2>/dev/null && echo "已归档：$ARCHIVE"
echo "报告全文：$FR"
