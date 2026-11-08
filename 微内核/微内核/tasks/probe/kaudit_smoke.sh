#!/usr/bin/env bash
# kaudit 冒烟：真起服务、真发请求，原始响应逐条留档。
# 分工：kaudit_probe.py 那 7 根针管"承诺被抹掉时测试认不认得出"；这个脚本管"真进程里到底是不是这样"。
# 控制台只印 ASCII（这台机器控制台是 GBK，中文会花），中文原文一律进报告文件。
set -u
GO=/c/Users/Admin/sdk/go/bin/go.exe
PY=/c/Users/Admin/sdk/py314/python.exe
cd "$(dirname "$0")/../.." || exit 9
JOURNAL="报告/样例-agent.jsonl"
REPORT="报告/kaudit-实测-$(date +%m%d-%H%M%S).txt"
BIN="$LOCALAPPDATA/kaudit-smoke.exe"
BINW=$(cygpath -w "$BIN")                 # Windows 版 exe 收文件要用 Windows 路径
TMPW=$(cygpath -w "$LOCALAPPDATA")
TAMPERED="$TMPW/kaudit-tampered.jsonl"
OMITTED="$TMPW/kaudit-omitted.jsonl"
LAN=$(ipconfig | tr -d '\r' | grep -oE "[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+" | grep -v "^127\." | grep -v "^255\.\|^0\." | head -1)
[ -z "$LAN" ] && LAN=127.0.0.1

say() { echo "$*" >>"$REPORT"; }
note() { say "$*"; printf '%s\n' "$(printf '%s' "$*" | tr -cd '\11-\176')"; }

rm -f "$REPORT"
note "==== kaudit 冒烟 $(date -Iseconds) ===="
note "LAN_IP=$LAN  JOURNAL=$JOURNAL"
note "本报告里每个 HTTP 码和响应体都是 curl 自己印的原文；中文只进文件不进控制台。"
note ""
"$GO" build -p 1 -o "$BIN" ./cmd/kaudit || { note "BUILD FAILED"; exit 1; }
note "build ok"
note ""

PID=""
PIDW=""
LEFTOVER=0
# 收进程必须按"拥有这个监听套接字的那个 PID"来收。
# 实测教训（10-15 09:47 那一轮）：bash 的 $! 是 MSYS 的 pid，taskkill 拿它去杀报"找不到进程"，
# 于是三个 kaudit-smoke.exe 留在机器上继续监听 —— 服务其实根本没被收掉，而脚本以为自己收了。
owner_of() { # $1=listen（形如 127.0.0.1:8788）
  netstat -ano | tr -d '\r' | awk -v a="$1" '$2 == a && $4 == "LISTENING" {print $5; exit}'
}
stop() {
  local addr="$1"
  PIDW=$(owner_of "$addr")
  if [ -n "$PIDW" ]; then
    taskkill //PID "$PIDW" //F >>"$REPORT" 2>&1 || true
    say "   (killed windows pid=$PIDW owning $addr)"
  else
    say "   !! 没找到监听 $addr 的进程，可能已经退出"
  fi
  sleep 1
  local still; still=$(owner_of "$addr")
  if [ -n "$still" ]; then
    note "!! 收不掉：$addr 上还有 pid=$still 在监听 —— 这一轮不算干净"
    LEFTOVER=1
  else
    note "listener on $addr 已收掉（pid=${PIDW:-无}）"
  fi
  PID=""
}

get() { # $1=url $2=token(可空)
  local args=(-s -m 5 -w "\n[HTTP %{http_code}] [%{size_download}B]\n")
  [ -n "$2" ] && args+=(-H "Authorization: Bearer $2")
  say ">> GET $1  token=${2:-<none>}"
  curl "${args[@]}" "$1" >>"$REPORT" 2>&1
}
post() { # $1=url $2=token(可空)
  local args=(-s -m 5 -X POST -d '{}' -w "\n[HTTP %{http_code}] [%{size_download}B]\n")
  [ -n "$2" ] && args+=(-H "Authorization: Bearer $2")
  say ">> POST $1 token=${2:-<none>}"
  curl "${args[@]}" "$1" >>"$REPORT" 2>&1
}

start() { # $1=listen $2=token $3=journal(可空，默认样例账)
  local log="$LOCALAPPDATA/kaudit-server.log"
  : >"$log"
  if [ -n "$2" ]; then export KAUDIT_TOKEN="$2"; else unset KAUDIT_TOKEN; fi
  "$BINW" -listen "$1" -journal "${3:-$JOURNAL}" >"$log" 2>&1 &
  PID=$!
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    curl -s -o /dev/null -m 2 "http://$1/healthz" && break
    sleep 1
  done
  say "---- server log (pid=$PID listen=$1) ----"
  cat "$log" >>"$REPORT" 2>/dev/null
  say ""
}

note "-- A. 绑回环、不给口令：GET 都该 200，POST 该 405，少给一个轴该 400 --"
start "127.0.0.1:8788" ""
get "http://127.0.0.1:8788/healthz" ""
get "http://127.0.0.1:8788/v1/verify" ""
get "http://127.0.0.1:8788/v1/tally" ""
get "http://127.0.0.1:8788/v1/celltable" ""
get "http://127.0.0.1:8788/v1/turns" ""
get "http://127.0.0.1:8788/v1/read_back?session=agent&call=1&side=output" ""
get "http://127.0.0.1:8788/v1/read_back?call=1&side=output" ""
post "http://127.0.0.1:8788/v1/verify" ""
stop "127.0.0.1:8788"
note ""

note "-- B. 绑网段地址 $LAN、给口令：这就是别的电脑连过来读账的形状 --"
start "$LAN:8789" "smoke-token-please-change"
note "server pid=$PID listening on $LAN:8789"
get "http://$LAN:8789/v1/verify" ""
get "http://$LAN:8789/v1/verify" "smoke-token-please-change"
get "http://$LAN:8789/v1/read_back?session=agent&call=1&side=output" "smoke-token-please-change"
post "http://$LAN:8789/v1/read_back" ""
post "http://$LAN:8789/v1/read_back" "smoke-token-please-change"
stop "$LAN:8789"
note ""

note "-- C. 绑网段地址、不给口令：该拒绝启动，而不是先起来再等人发现 --"
unset KAUDIT_TOKEN
"$BINW" -listen "$LAN:8790" -journal "$JOURNAL" >"$LOCALAPPDATA/kaudit-refuse.log" 2>&1
note "exit=$? (该非 0)"
say "$(cat "$LOCALAPPDATA/kaudit-refuse.log")"
say ""

note "-- D. 账本被改过一个字（往某条 claim 前塞了'篡改'两个字）：该拒绝启动 --"
"$PY" -X utf8 - "$JOURNAL" "$TAMPERED" <<'EOF'
import sys
src, dst = sys.argv[1], sys.argv[2]
lines = open(src, encoding="utf-8").read().splitlines(True)
done = False
for i, ln in enumerate(lines):
    if not done and ln.lstrip().startswith("{") and '"claim":"' in ln:
        k = ln.index('"claim":"') + len('"claim":"')
        lines[i] = ln[:k] + "篡改" + ln[k:]
        done = True
print("tampered_one_claim=%s -> %s" % (done, dst))
open(dst, "w", encoding="utf-8", newline="").write("".join(lines))
EOF
say ""
"$BINW" -listen "127.0.0.1:8791" -journal "$TAMPERED" >"$LOCALAPPDATA/kaudit-tampered.log" 2>&1
note "exit=$? (该非 0)"
say "$(cat "$LOCALAPPDATA/kaudit-tampered.log")"
say ""

note "-- E. 指向一份不存在的账：该拒绝启动 --"
"$BINW" -listen "127.0.0.1:8792" -journal "no-such-file.jsonl" >"$LOCALAPPDATA/kaudit-missing.log" 2>&1
note "exit=$? (该非 0)"
say "$(cat "$LOCALAPPDATA/kaudit-missing.log")"
say ""

note "-- F. 供一份没有原像的账（链自洽、第三方重算不出原话）：读数必须是第三种答案 --"
"$GO" run -p 1 ./tasks/probe/omitjournal "$JOURNAL" "$OMITTED"
say ""
if [ -f "$OMITTED" ]; then
  start "127.0.0.1:8793" "" "$OMITTED"
  get "http://127.0.0.1:8793/v1/verify" ""
  stop "127.0.0.1:8793"
else
  say "!! 没生成 omitted 文件，这一段没跑成（不是绿）"
fi
note ""

note "-- 收尾自检：这一段端口上还有没有监听者 --"
LEFT=$(netstat -ano | tr -d '' | awk '$4 == "LISTENING" {print $2}' | grep -cE ":(8788|8789|8790|8791|8792|8793)$" || true)
note "残留监听数=$LEFT（该 0）"
[ "$LEFT" != "0" ] && LEFTOVER=1
say ""
note "报告全文：$REPORT"
if [ "$LEFTOVER" != "0" ]; then echo "SMOKE INCOMPLETE: leftover listener"; exit 5; fi
echo "SMOKE DONE"
