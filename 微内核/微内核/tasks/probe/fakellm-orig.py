# -*- coding: utf-8 -*-
# fakellm.py —— 一个只用于**管线自检**的假对话端点（形状照 agnes 那份真响应抄）。
# 目的：不花真调用的钱，把 qa_loop.py 的装配、落账、判定跑一遍。
# 它的回答是定死的，所以它证明不了模型答得对不对，只能证明脚本不会在判定阶段崩。
import json
from http.server import BaseHTTPRequestHandler, HTTPServer

TEXT = "红不了的承诺是散文。"


class H(BaseHTTPRequestHandler):
    count = 0

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(n).decode("utf-8"))
        H.count += 1
        if H.count % 4 == 0:
            # 让调用方在**不花钱**的情况下撞上一次真 429：看它记不记账，还是直接崩。
            raw = json.dumps({"error": {"message": "Rate limit reached (fake)", "type": "rate_limit"}},
                             ensure_ascii=False).encode("utf-8")
            self.send_response(429)
            self.send_header("Retry-After", "1")
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
            return
        stream = body.get("stream")
        mt = body.get("max_tokens", 64)
        usage = {"completion_tokens": 7, "prompt_tokens": max(1, len(json.dumps(body)) // 4),
                 "total_tokens": 7 + max(1, len(json.dumps(body)) // 4)}
        self.send_response(200)
        if not stream:
            payload = {"id": "fake", "created": 0, "model": "fake-model", "object": "chat.completion",
                       "choices": [{"finish_reason": "length" if mt <= 8 else "stop", "index": 0,
                                    "message": {"content": TEXT, "role": "assistant"}}],
                       "usage": usage}
            raw = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
            return
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        pieces = list(TEXT)

        def send(obj):
            self.wfile.write(b"data: " + json.dumps(obj, ensure_ascii=False).encode("utf-8") + b"\n\n")

        for p in pieces:
            send({"id": "fake", "created": 0, "model": "fake-model", "object": "chat.completion.chunk",
                  "choices": [{"index": 0, "delta": {"content": p}}]})
        send({"id": "fake", "created": 0, "model": "fake-model", "object": "chat.completion.chunk",
              "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]})
        send({"id": "fake", "created": 0, "model": "fake-model", "object": "chat.completion.chunk",
              "choices": [{"index": 0, "delta": {}}], "usage": usage})
        self.wfile.write(b"data: [DONE]\n\n")

    def log_message(self, *a):
        pass


HTTPServer(("127.0.0.1", 8799), H).serve_forever()
