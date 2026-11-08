# -*- coding: utf-8 -*-
# fakellm2.py —— 管线自检用的假端点，比 fakellm.py 多会吐**工具提议**（含歪的与流式分片的）。
# 它的回答是定死的，所以它证明不了模型会不会调工具，只证明脚本装配/落账/判定不会崩。
import json
from http.server import BaseHTTPRequestHandler, HTTPServer

TEXT = "和是 42。"


def call(cid, name, args):
    return {"id": cid, "type": "function", "function": {"name": name, "arguments": args}}


class H(BaseHTTPRequestHandler):
    count = 0

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(n).decode("utf-8"))
        H.count += 1
        # max_tokens == 5 是本机自检专用的另一个旋钮（真端点那五发用的是 64/96/6/4，撞不上）：
        # 回一个**HTTP 200 但 body 是断的**。这是 protocol 那一档唯一的真形状 ——
        # 对端没报错、连接成了、字节也给了，话却不成话。
        if body.get("max_tokens") == 5:
            raw = '{"choices": [{"message": {"content": "和是 4'.encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
            return
        if H.count % 7 == 0:
            raw = json.dumps({"error": {"message": "Rate limit reached (fake)", "type": "rate_limit"}},
                             ensure_ascii=False).encode("utf-8")
            self.send_response(429)
            self.send_header("Retry-After", "1")
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
            return

        tools = bool(body.get("tools"))
        mt = body.get("max_tokens", 64)
        stream = bool(body.get("stream"))
        usage = {"completion_tokens": 7, "prompt_tokens": max(1, len(json.dumps(body)) // 4),
                 "total_tokens": 7 + max(1, len(json.dumps(body)) // 4)}

        # 路由只认 max_tokens 这一个旋钮（定死的假端点，别再叠第二判据）：
        #   没带 tools            → 说人话
        #   tools 且额度 ≤ 8      → 参数被截断的**歪提议**
        #   tools 且额度 ≥ 90     → 拿到工具结果后的那句人话
        #   其余（9..89）         → 正常提议；流式时参数分三片来
        if not tools:
            fin, content, calls = "stop", TEXT, None
        elif mt <= 4:
            fin, content, calls = "length", None, None      # 空手回来：一个字没吐、一个提议也没有
        elif mt <= 8:
            fin, content, calls = "tool_calls", None, [call("c_bad", "ledger_add", '{"a": 1')]
        elif mt >= 90:
            fin, content, calls = "stop", TEXT, None
        else:
            fin, content, calls = "tool_calls", None, [call("c_ok", "ledger_add", '{"a": 27, "b": 15}')]

        self.send_response(200)
        if not stream:
            msg = {"role": "assistant", "content": content}
            if calls and fin == "tool_calls":
                msg["tool_calls"] = calls
            payload = {"id": "fake", "created": 0, "model": "fake-model", "object": "chat.completion",
                       "choices": [{"finish_reason": fin, "index": 0, "message": msg}], "usage": usage}
            raw = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
            return

        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()

        def send(obj):
            self.wfile.write(b"data: " + json.dumps(obj, ensure_ascii=False).encode("utf-8") + b"\n\n")

        if fin == "tool_calls":
            # 流式里的提议是**分片**来的：id+名字在第一帧，参数拆成三片
            send({"id": "fake", "model": "fake-model", "object": "chat.completion.chunk",
                  "choices": [{"index": 0, "delta": {"role": "assistant", "tool_calls":
                            [{"index": 0, "id": calls[0]["id"], "function":
                              {"name": "ledger_add", "arguments": ""}}]}}]})
            for piece in ('{"a":', ' 27,', ' "b": 15}'):
                send({"id": "fake", "model": "fake-model", "object": "chat.completion.chunk",
                      "choices": [{"index": 0, "delta": {"tool_calls":
                                [{"index": 0, "function": {"arguments": piece}}]}}]})
            send({"id": "fake", "model": "fake-model", "object": "chat.completion.chunk",
                  "choices": [{"index": 0, "delta": {}, "finish_reason": "tool_calls"}]})
        else:
            for p in TEXT:
                send({"id": "fake", "model": "fake-model", "object": "chat.completion.chunk",
                      "choices": [{"index": 0, "delta": {"content": p}}]})
            send({"id": "fake", "model": "fake-model", "object": "chat.completion.chunk",
                  "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]})
        send({"id": "fake", "model": "fake-model", "object": "chat.completion.chunk",
              "choices": [], "usage": usage})
        self.wfile.write(b"data: [DONE]\n\n")

    def log_message(self, *a):
        pass


HTTPServer(("127.0.0.1", 8799), H).serve_forever()
