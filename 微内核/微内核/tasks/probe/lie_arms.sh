#!/usr/bin/env bash
# a0_lie_arms.sh —— 说谎者靶场（元数据谎族）：往服务面逐条种「不实报账」，读结构化 verdict。
# 每条臂先验「锚点命中 1 处」才算破坏真落上了；落不上的单独报 ANCHOR-BAD，不参与分母。
# 用 bash+curl，不需要 go 也不需要 python；只打一个自己起的实例。
set -u
ADDR="${1:-127.0.0.1:8798}"
WORK="$LOCALAPPDATA/a0-arms"; OUT="${OUTFILE:-$WORK/readout.txt}"
mkdir -p "$WORK"; : > "$OUT"   # 每轮从头写：两轮并排留在同一份里会被读成一轮（10-17 真犯过一次）

SHA=$(printf '42' | sha256sum | cut -d' ' -f1)
IN_B='{"Call":1,"Order":["system","user"],"Parts":[{"Kind":"system","Parts":1,"Bytes":11},{"Kind":"user","Parts":1,"Bytes":9}],"ContentBytes":20,"Tokens":{"N":7,"Basis":3,"Source":"1 token 约 3 字节的粗估","Absent":false,"Refused":false,"Why":""},"Budget":{"CallBytesCeiling":32768,"TransportSlackPct":12,"ContentCeiling":28835,"Shares":{"system":55,"user":45},"LeftoverPct":0,"FixedOverhead":{"N":300,"Basis":3,"Source":"骨架+模型名粗估","Absent":false,"Refused":false,"Why":""},"EffectiveCeiling":28835,"ClampRounds":0},"Cuts":[],"Notice":{"Sent":false,"Trimmed":false,"Why":"这一发没折叠、没裁尾、没整段丢弃","Text":""}}'
# 注意：`Says` 里没有 Clipped 这个字段（`Says.Clipped()` 是方法）—— 10-17 #46 之后写进来会被整发拒收，
# 之前则是静默丢掉（这支靶场自己中过这一枪，所以下面那份底样里没有它）。
OUT_B='{"Call":1,"Streaming":false,"Finish":{"Reason":"stop","Said":true},"Model":"some-model","ModelSaid":true,"WireFormat":"chat.completion","WireFormatSaid":true,"Says":{"Bytes":2,"Kept":2,"SHA256":"'$SHA'"},"UsageIn":{"N":5,"Basis":2,"Source":"端点响应的 usage.prompt_tokens","Absent":false,"Refused":false,"Why":""},"UsageOut":{"N":2,"Basis":2,"Source":"端点响应的 usage.completion_tokens","Absent":false,"Refused":false,"Why":""},"WireBytesOut":{"N":180,"Basis":1,"Source":"","Absent":false,"Refused":false,"Why":""},"WireBytesIn":{"N":96,"Basis":1,"Source":"","Absent":false,"Refused":false,"Why":""},"Chunks":{"N":0,"Basis":0,"Source":"","Absent":true,"Refused":false,"Why":"这一发不是流式的，结构上没有分帧"},"FirstByteMs":{"N":0,"Basis":0,"Source":"","Absent":true,"Refused":false,"Why":"这一发不是流式的，没有首帧这件事"},"MaxGapMs":{"N":0,"Basis":0,"Source":"","Absent":true,"Refused":false,"Why":"这一发不是流式的，没有帧间"},"ToolCalls":0,"Malformed":0,"MalformedStreak":0,"Failure":{"Kind":"","Detail":"","Status":{"N":0,"Basis":0,"Source":"","Absent":true,"Refused":false,"Why":"这一发没故障，也就没有要记的状态码"},"RetryAfterMs":{"N":0,"Basis":0,"Source":"","Absent":true,"Refused":false,"Why":"这一发没断，也就没有要等多久"},"Downgrade":false}}'

send(){ # $1=工具 $2=session $3=input(或 @@NONE@@) $4=output  → 回原始 JSON
  local body
  if [ "$3" = "@@NONE@@" ]; then body="{\"session\":\"$2\",\"actor\":\"$2\",\"output\":$4}"
  else body="{\"session\":\"$2\",\"actor\":\"$2\",\"input\":$3,\"output\":$4}"; fi
  printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"%s","arguments":%s}}' "$1" "$body" > "$WORK/req.json"
  curl -s -m 20 --data-binary "@$(cygpath -w "$WORK/req.json")" -H 'Content-Type: application/json' "http://$ADDR/mcp"
}
ask(){ # $1=工具 $2=arguments
  printf '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"%s","arguments":%s}}' "$1" "$2" > "$WORK/req.json"
  curl -s -m 20 --data-binary "@$(cygpath -w "$WORK/req.json")" -H 'Content-Type: application/json' "http://$ADDR/mcp"
}
ise(){ printf '%s' "$1" | tr -d '\n ' | grep -o '"isError":[a-z]*' | cut -d: -f2; }
# 这台机器的 cut -c 不按 locale，它就是**按字节**切（实测 LC_ALL=C.UTF-8 也一样，
# 30 恰好落在 3 字节边界上才看着无事），中文回话被切在字符中间就会写出**非法 UTF-8 的归档**。
# 10-17 实测两份 A0 归档里各有 8 / 6 段这种坏字节。所以：截断之后必过一道 iconv -c（丢掉残缺尾字节），
# 并且**洗过就在行尾留一个记号**，最后的条数由文件自己数出来 ——
# 不用 shell 计数器：`said` 是在 `$( )` 子 shell 里跑的，外面的变量加得再多也传不回来（第一版就这么自欺过）。
mangled=0
empty_reply=0

said(){
  local raw cut washed
  raw=$(printf '%s' "$1" | sed 's/\\n/\n/g' | grep -o 'kernel:[^"]*' | head -1 | cut -c1-120)
  washed=$(printf '%s' "$raw" | iconv -f utf-8 -t utf-8 -c)
  printf '%s' "$washed"
  [ "$(printf '%s' "$raw" | wc -c)" != "$(printf '%s' "$washed" | wc -c)" ] && printf '⟨半字已洗⟩'
}

planted=0; anchor_bad=0; rows=()
arm(){ # $1=名 $2=臂侧(in/out/none) $3=sed表达式 $4=锚点 $5=session
  local IN="$IN_B" OU="$OUT_B" HIT
  [ "$2" = "in" ] && IN=$(printf '%s' "$IN_B" | sed "$3")
  [ "$2" = "out" ] && OU=$(printf '%s' "$OUT_B" | sed "$3")
  if [ "$2" = "none" ]; then HIT=1; else HIT=$(printf '%s' "$IN$OU" | grep -c -F -- "$4"); fi
  if [ "$HIT" != "1" ]; then
    anchor_bad=$((anchor_bad+1)); rows+=("$1 | — | — | ANCHOR-BAD 命中 $HIT"); echo "ANCHOR-BAD $1 命中=$HIT" >> "$OUT"; return
  fi
  planted=$((planted+1))
  local CR RR raw; raw=$(send kernel.check_call "$5" "$IN" "$OU"); CR=$(ise "$raw")
  local RR2; RR2=$(send kernel.record_call "$5" "$IN" "$OU"); RR=$(ise "$RR2")
  # 「锚点命中 1 处」数的是**我自己拼的载荷**里有没有那段文本，跟端点答不答应是两件事：
  # 10-17 实测端点没起来时，七条臂仍然全部 planted、ANCHOR-BAD=0、退出码 0，
  # 而表里每一格都是空的 —— 那是把「没跑成」印成了「跑过了」。现在单列一个数。
  if [ -z "$CR" ] || [ -z "$RR" ]; then empty_reply=$((empty_reply+1)); fi
  rows+=("$1 | check=$CR | record=$RR | 命中 1｜回话空=$([ -z "$CR" ] || [ -z "$RR" ] && echo 是 || echo 否)")
  { echo "  种法：$3"; echo "  正文(check) $(said "$raw")"; echo "  正文(record) $(said "$RR2")"; echo "  原始(record) $RR2"; } >> "$OUT"
}

# 起跑前必须先确认端点在答：拿不到非空 celltable 回话就 NOT_RUN 退出，不产出看起来像结果的空表。
probe0=$(ask kernel.celltable '{}')
if [ -z "$(printf '%s' "$probe0" | tr -d '
 ')" ]; then
  echo "NOT_RUN: 端点 $ADDR 没答（celltable 回话为空）—— 不产出空表冒充跑过" | tee "$OUT"; exit 4
fi

{ echo "== 说谎者靶场 A0 · $(date '+%F %T') · 实例 $ADDR =="
  echo "   替身声明：这一支是**脚本**（agent 写的 bash+curl），不是「没参与过这个项目的人」。"
  echo "   它能证明「谎落在门上会被结构化地拦住」，不能替 产品定位 §唯一验收 打勾 —— 那条要真人/真框架。"
  echo; } >> "$OUT"
arm 'L1 声称通知发了' in 's/"Sent":false/"Sent":true/' '"Sent":true' a0-l1
arm 'L2 非流式把分帧写成量到0' out 's/"Chunks":{"N":0,"Basis":0,"Source":"","Absent":true/"Chunks":{"N":0,"Basis":1,"Source":"","Absent":false/' '"Chunks":{"N":0,"Basis":1,"Source":"","Absent":false' a0-l2
arm 'L3 转述的token标成实测' out 's/"UsageIn":{"N":5,"Basis":2/"UsageIn":{"N":5,"Basis":1/' '"UsageIn":{"N":5,"Basis":1' a0-l3
arm 'L4 缺席不带理由' out 's/这一发没故障，也就没有要记的状态码/无/' '无' a0-l4
arm 'L5 模型原话指纹写歪' out 's|"SHA256":"'$SHA'"|"SHA256":"0'${SHA:1}'"|' '"SHA256":"0'${SHA:1}'"' a0-l5
arm 'L6 手搭预算（额度对不上）' in 's/"ContentBytes":20/"ContentBytes":999999/' '"ContentBytes":999999' a0-l6
arm 'L9 整块不给Notice' in 's/,"Notice":{[^}]*}//' '"Cuts":[]}' a0-l9

# 对照 1：合法底样应被收下；对照 2：同一发重记应被去重门拒
IN="$IN_B"; OU="$OUT_B"
raw=$(send kernel.record_call "a0-legal" "$IN" "$OU"); LEG=$(ise "$raw")
raw2=$(send kernel.record_call "a0-legal" "$IN" "$OU"); DUP=$(ise "$raw2")
rows+=("C1 合法落账（应收） | — | record=$LEG |")
rows+=("C2 同一发重记（应拒） | — | record=$DUP |")
{ echo "  C1 正文 $(said "$raw")"; echo "  C2 正文 $(said "$raw2")"; echo; } >> "$OUT"

for t in kernel.tally kernel.verify kernel.readers_report; do
  raw=$(ask "$t" '{}')
  # 过滤器不能靠"猜那句话长什么样"：上一版写的是 'kernel:\|账 \|链 \|声明'，
  # 而 verify 那句是 `✓ 链自洽（共 N 条…）` —— 「链」后面没有空格，一句都没匹配上，
  # 报告里那一栏就成了**没有读数**（我在 说谎者靶场-A0-1017.txt 里就是这么标的）。
  # 现在取回话正文第一行，取不到就明说取不到，绝不留成空白让人读成"通过"。
  line=$(printf '%s' "$raw" | sed 's/\\n/\n/g' | grep -v '^[[:space:]]*$' | head -1 | cut -c1-460)
  if printf '%s' "$line" | iconv -f utf-8 -t utf-8 >/dev/null 2>&1; then :; else
    line=$(printf '%s' "$line" | iconv -f utf-8 -t utf-8 -c)" ⟨半字已洗⟩"
  fi
  echo "$t → ${line:-（回话正文抠不出第一行 —— 这一栏没有读数，不许读成通过）}" >> "$OUT"
done

{ echo; echo "臂 | check.isError | record.isError | 锚点"; echo "------------------------------"; printf '%s\n' "${rows[@]}"; echo; echo "== 自印分母 =="; echo "种谎臂 7 条 + 对照 2 条；锚点命中 1 处 = $planted；ANCHOR-BAD = $anchor_bad；洗过残缺尾字节的行数（由本文件自己数出来）= __CNT__（非 0 表示那些行被 cut 剁在半字上，已洗成合法 UTF-8；要复核就数 ⟨半字已洗⟩）"; echo "回话为空的臂 = $empty_reply（要 0；非 0 则这一轮的数不可信 —— planted 只数我拼的载荷，不数端点答没答）"; echo "靶场只用 bash+curl（本机 go/python 状态见备注），不动仓库一行代码。"; } >> "$OUT"
cnt=$(grep -c "⟨半字已洗⟩" "$OUT" 2>/dev/null || echo 0)
sed -i "s/__CNT__/$cnt/" "$OUT" 2>/dev/null
echo "DONE planted=$planted anchor_bad=$anchor_bad washed_lines=$cnt empty_reply=$empty_reply → $OUT"
[ "$empty_reply" = "0" ] || exit 5
