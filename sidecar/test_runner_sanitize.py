"""TD-6 脱敏行为断言（M6 验收）。

契约测试（contract/test_contract.py）只锁「字段名」，本测试锁「message 内容」：
RUNTIME_ERROR 对外 message 必须脱敏——不含服务器绝对路径 / runner.py 框架帧，
只保留异常类型 + 消息 + <user_code> 帧；完整 traceback 仍留在 logs（服务端可见）。

运行（纯标准库，无需 pytest / 无需装依赖）：
    python3 sidecar/test_runner_sanitize.py
"""

import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
RUNNER = os.path.join(HERE, "runner.py")


def run_runner(payload: dict) -> dict:
    proc = subprocess.run(
        [sys.executable, RUNNER],
        input=json.dumps(payload).encode(),
        capture_output=True,
        timeout=10,
    )
    return json.loads(proc.stdout.decode())


def main() -> int:
    failures = []

    # 用例 A：单帧 RUNTIME_ERROR。
    got = run_runner({"code": "raise ValueError('boom')", "inputs": {}})
    if got.get("ok"):
        failures.append(f"A: expected ok=false, got {got}")
    else:
        err = got.get("error", {})
        msg = err.get("message", "")
        if err.get("code") != "RUNTIME_ERROR":
            failures.append(f"A: code = {err.get('code')!r}, want RUNTIME_ERROR")
        if "ValueError" not in msg:
            failures.append(f"A: message should keep exception type, got {msg!r}")
        # 脱敏：不得含 runner.py 框架帧或 sidecar 目录绝对路径。
        if "runner.py" in msg:
            failures.append(f"A: message leaks framework frame 'runner.py': {msg!r}")
        if HERE in msg:
            failures.append(f"A: message leaks server absolute path {HERE!r}: {msg!r}")

    # 用例 B：多帧（用户代码内嵌套调用抛异常）。
    multi_frame_code = (
        "def inner():\n"
        "    raise RuntimeError('deep failure')\n"
        "def outer():\n"
        "    inner()\n"
        "outer()\n"
    )
    got = run_runner({"code": multi_frame_code, "inputs": {}})
    if got.get("ok"):
        failures.append(f"B: expected ok=false, got {got}")
    else:
        err = got.get("error", {})
        msg = err.get("message", "")
        logs = got.get("logs", "")
        if "RuntimeError" not in msg:
            failures.append(f"B: message should keep exception type, got {msg!r}")
        # 对外 message：只应出现 <user_code> 帧，不含绝对路径。
        if "runner.py" in msg or HERE in msg:
            failures.append(f"B: message leaks server path: {msg!r}")
        if "<user_code>" not in msg:
            failures.append(f"B: message should reference <user_code> frame, got {msg!r}")
        # 完整 traceback 应留在 logs（服务端可见，含框架帧）。
        if "Traceback" not in logs:
            failures.append(f"B: full traceback should stay in logs, got logs={logs!r}")

    if failures:
        print("SANITIZE FAILED:")
        for f in failures:
            print("  -", f)
        return 1
    print("sanitize OK: RUNTIME_ERROR message is redacted, full traceback stays in logs")
    return 0


if __name__ == "__main__":
    sys.exit(main())
