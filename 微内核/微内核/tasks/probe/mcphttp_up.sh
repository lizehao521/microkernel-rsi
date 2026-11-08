#!/usr/bin/env bash
# mcphttp_up.sh —— 起 / 查 / 收那颗 MCP-over-HTTP 服务，**按监听套接字的 owner 收**。
#
# 为什么要有它：挂载指着的是 `http://127.0.0.1:8797/mcp`，也就是**一个进程**。
# 它一死，界面里那 10 个工具就变红；而 10-15 那天我用 bash 的 `$!` 去 taskkill，
# 报的是「没有找到进程」，脚本以为自己收了 —— 实际留了三个监听在机上。
# 所以这里只认一种收法：`netstat` 反查拥有这个端口的 PID，杀掉，再验一次端口空了。
#
# 用法：
#   bash tasks/probe/mcphttp_up.sh up [监听地址]      # 起（默认 127.0.0.1:8797）
#   bash tasks/probe/mcphttp_up.sh status             # 它在不在、账上几条
#   bash tasks/probe/mcphttp_up.sh down               # 按 owner 收 + 残留自检
#
# 只绑回环时不需要口令。要绑非回环：先 export AGENTKERNEL_TOKEN=…（只从环境变量来），
# 否则服务自己拒绝启动——这一面**有写入口**，这条判据在 cmd/mcphttp 的测试与针里钉着。
set -u
GO=/c/Users/Admin/sdk/go/bin/go.exe
cd "$(dirname "$0")/../.." || exit 9
ADDR="${2:-127.0.0.1:8797}"
BINW=$(cygpath -w "../.bin/mcphttp.exe")
LOG="$LOCALAPPDATA/mcphttp-up.log"

owner() { netstat -ano | tr -d '\r' | awk -v a="$ADDR" '$2 == a && $4 == "LISTENING" {print $5; exit}'; }

case "${1:-status}" in
up)
  if [ -n "$(owner)" ]; then
    echo "ALREADY_LISTENING on $ADDR (pid=$(owner)) —— 不再起第二个：两个进程各持一本账，"
    echo "挂载指着的那个只会连到其中一个，而这不是并发，是**两本账**。要换就先 down。"
    exit 0
  fi
  [ -x "../.bin/mcphttp.exe" ] || { echo "NO_BINARY —— 先：go build -o ../.bin/mcphttp.exe ./cmd/mcphttp"; exit 3; }
  "$BINW" -listen "$ADDR" >"$LOG" 2>&1 &
  for _ in 1 2 3 4 5 6 7 8 9 10; do [ -n "$(owner)" ] && break; sleep 1; done
  PID=$(owner)
  [ -z "$PID" ] && { echo "START_FAILED（exit 前先看 $LOG）"; cat "$LOG"; exit 4; }
  echo "UP addr=$ADDR pid=$PID"
  # 为什么印这行：挂载/靶场/判定器都可能指向一个**旧字节**的 exe，而读的人以为在测今天这版。
  # 起一次就把用的那份字节自印出来，第三方把它抄进往来即可，不需要信谁的声明。
  SHA16=$(sha256sum ../.bin/mcphttp.exe | cut -c1-16)
  SZ=$(stat -c %s ../.bin/mcphttp.exe 2>/dev/null)
  echo "bytes sha256=$SHA16 大小=$SZ —— 这一行请原样抄进你的往来：没有它，别人核不出你测的是哪份字节"
  echo "server log: $(head -1 "$LOG")"
  ;;
status)
  PID=$(owner)
  if [ -z "$PID" ]; then echo "DOWN（$ADDR 上没有监听）"; exit 5; fi
  echo "UP pid=$PID"
  printf '%s' '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kernel.tally","arguments":{}}}' \
    > "$LOCALAPPDATA/mcphttp-up-tally.json"
  curl -s -m 8 --data-binary "@$(cygpath -w "$LOCALAPPDATA/mcphttp-up-tally.json")" \
    -H 'Content-Type: application/json' "http://$ADDR/mcp"
  echo ""
  ;;
down)
  PID=$(owner)
  if [ -z "$PID" ]; then echo "ALREADY_DOWN（$ADDR 上没有监听）"; exit 0; fi
  powershell -NoProfile -Command "Stop-Process -Id $PID -Force" >/dev/null 2>&1
  sleep 1
  LEFT=$(owner)
  if [ -n "$LEFT" ]; then echo "!! 收不掉：$ADDR 上还有 pid=$LEFT —— 这一轮不算干净"; exit 6; fi
  echo "DOWN（收掉了 pid=$PID；残留监听数=0）"
  ;;
*)
  echo "用法：$0 up|status|down [监听地址]"; exit 2
  ;;
esac
