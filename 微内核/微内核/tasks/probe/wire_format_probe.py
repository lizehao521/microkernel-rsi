#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""One-off evidence probe: does the peer self-report a different `object` when we ask for stream=true?

Two calls, same tiny prompt. Reads the RESPONSE HEADERS too (the regular drivers never do).
Console output is ASCII-only (Windows GBK); the full text goes to a UTF-8 report.
"""
import io
import json
import os
import sys
import time
import urllib.error
import urllib.request

BASE = os.environ.get("AGNES_BASE", "https://api.agnes-ai.cn/v1")
MODEL = os.environ.get("AGNES_MODEL", "agnes-3.0-flash")
KEY = os.environ.get("AGNES_KEY", "")
ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
REPORT = os.environ.get("WIRE_REPORT", os.path.join(ROOT, "报告", "格式自报-流式对照.txt"))

lines = []


def say(s):
    lines.append(s)
    sys.stdout.buffer.write(s.encode("ascii", "replace") + b"\n")
    sys.stdout.flush()


def call(stream):
    body = {"model": MODEL, "messages": [{"role": "user", "content": "Say OK."}],
            "max_tokens": 8, "stream": stream}
    raw = json.dumps(body).encode("utf-8")
    rq = urllib.request.Request(BASE + "/chat/completions", data=raw, method="POST")
    rq.add_header("Authorization", "Bearer " + KEY)
    rq.add_header("Content-Type", "application/json")
    out = {"stream": stream, "status": None, "headers": {}, "objects": [],
           "frames": 0, "err": None, "bytes_in": 0, "took_ms": 0}
    t0 = time.time()
    try:
        with urllib.request.urlopen(rq, timeout=60) as resp:
            out["status"] = resp.status
            out["headers"] = {k.lower(): v for k, v in resp.getheaders()}
            ctype = out["headers"].get("content-type", "")
            if stream and "event-stream" in ctype:
                buf = b""
                while True:
                    chunk = resp.read(1)
                    if not chunk:
                        break
                    buf += chunk
                    out["bytes_in"] += len(chunk)
                    if buf.endswith(b"\n\n"):
                        for ln in buf.split(b"\n"):
                            if ln.startswith(b"data:"):
                                payload = ln[5:].strip()
                                if payload and payload != b"[DONE]":
                                    try:
                                        obj = json.loads(payload.decode("utf-8"))
                                        out["objects"].append(str(obj.get("object")))
                                    except Exception:
                                        out["objects"].append("<undecodable>")
                                out["frames"] += 1
                        buf = b""
            else:
                data = resp.read()
                out["bytes_in"] = len(data)
                try:
                    out["objects"].append(str(json.loads(data.decode("utf-8")).get("object")))
                except Exception:
                    out["objects"].append("<undecodable>")
    except urllib.error.HTTPError as e:
        out["status"] = e.code
        out["headers"] = {k.lower(): v for k, v in e.headers.items()}
        out["err"] = repr(e)
    except Exception as e:
        out["err"] = repr(e)
    out["took_ms"] = int((time.time() - t0) * 1000)
    return out


def main():
    if not KEY:
        say("AGNES_KEY missing in env")
        return 2
    say("wire_format probe: same prompt, stream=False then stream=True")
    say("endpoint=" + BASE + "/chat/completions  model=" + MODEL)
    say("")
    res = {}
    for stream in (False, True):
        r = call(stream)
        res[stream] = r
        uniq = sorted(set(r["objects"]))
        say("stream=%s  HTTP=%s  content-type=%s  bytes_in=%d  frames=%d  took=%dms"
            % (r["stream"], r["status"], r["headers"].get("content-type", "-"),
               r["bytes_in"], r["frames"], r["took_ms"]))
        say("   object self-reported by peer: %s" % (", ".join(uniq) if uniq else "(none)"))
        say("   distinct values over all frames: %d" % len(uniq))
        for k in ("x-new-api-version", "x-litellm-model-name", "x-request-id",
                  "x-litellm-response-cost-output"):
            say("   %-34s = %s" % (k, r["headers"].get(k, "(absent)")))
        if r["err"]:
            say("   err=" + r["err"][:160])
        say("")
    a, b = res[False], res[True]
    same = (sorted(set(a["objects"])) == sorted(set(b["objects"])))
    say("VERDICT: peer's `object` %s when stream flips  (non-stream=%s stream=%s)"
        % ("does NOT change" if same else "CHANGES",
           sorted(set(a["objects"])), sorted(set(b["objects"]))))
    say("header count: non-stream=%d stream=%d" % (len(a["headers"]), len(b["headers"])))
    say("")
    say("full header dump (stream=True):")
    for k, v in sorted(b["headers"].items()):
        if k == "set-cookie":
            k = "(redacted: cloudflare session token)"
            v = ""
        say("  %s: %s" % (k, v))
    io.open(REPORT, "w", encoding="utf-8").write("\n".join(lines) + "\n")
    say("")
    say("report written (utf-8): " + REPORT.encode("ascii", "replace").decode("ascii"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
