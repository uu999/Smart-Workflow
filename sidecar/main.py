"""Smart-Workflow Python sidecar.

M0: 只提供 /healthz。
M5: 实现 /run/python-code，用子进程隔离执行用户代码。

统一响应契约（对齐 Go 侧 httpx.Envelope）：
    成功: {"ok": true,  "data": {...}}
    失败: {"ok": false, "error": {"code": "...", "message": "...", "details": {...}}}
"""

import json
import os
import subprocess
import sys
from datetime import datetime, timezone

from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI(title="smart-workflow-sidecar", version="0.1.0")

# runner.py 与本文件同目录。
RUNNER_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "runner.py")

# 超时护栏：请求可申请更短，但不得超过硬上限。
MAX_TIMEOUT_SEC = 60
DEFAULT_TIMEOUT_SEC = 30


def ok(data):
    return {"ok": True, "data": data}


def fail(code, message, details=None):
    return {"ok": False, "error": {"code": code, "message": message, "details": details}}


@app.get("/healthz")
def healthz():
    return ok({"status": "ok", "time": datetime.now(timezone.utc).isoformat()})


# --- M5：单节点 Python 代码执行 ---


class CodeRunRequest(BaseModel):
    code: str
    inputs: dict = {}
    timeout_sec: int = DEFAULT_TIMEOUT_SEC


@app.post("/run/python-code")
def run_python_code(req: CodeRunRequest):
    if not req.code.strip():
        return fail("BAD_REQUEST", "code is empty")

    timeout = req.timeout_sec if req.timeout_sec > 0 else DEFAULT_TIMEOUT_SEC
    timeout = min(timeout, MAX_TIMEOUT_SEC)

    payload = json.dumps({"code": req.code, "inputs": req.inputs}, ensure_ascii=False)

    try:
        proc = subprocess.run(
            [sys.executable, RUNNER_PATH],
            input=payload.encode("utf-8"),
            capture_output=True,
            timeout=timeout,
        )
    except subprocess.TimeoutExpired:
        # 子进程已被 kill；超时视为节点失败。
        return fail("TIMEOUT", f"python code exceeded {timeout}s", {"timeout_sec": timeout})

    if not proc.stdout:
        # runner 没写回结果通道：多半是被 OOM/信号杀掉。
        stderr = proc.stderr.decode("utf-8", "replace")[:2048]
        return fail("RUNNER_CRASHED", "runner produced no result", {
            "returncode": proc.returncode,
            "stderr": stderr,
        })

    try:
        result = json.loads(proc.stdout.decode("utf-8", "replace"))
    except json.JSONDecodeError as exc:
        return fail("BAD_RUNNER_OUTPUT", f"cannot parse runner result: {exc}")

    logs = result.get("logs", "")
    if result.get("ok"):
        return ok({"outputs": result.get("outputs", {}), "logs": logs})

    err = result.get("error", {})
    return fail(err.get("code", "RUNTIME_ERROR"), err.get("message", "unknown error"), {"logs": logs})
