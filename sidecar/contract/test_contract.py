"""Go↔Python 契约的 Python 侧守卫（M5 反思③）。

与 Go 侧 internal/engine/nodes/contract_test.go 共用同一份 golden fixture
（contract/code_run.golden.json）。这里验证 sidecar/runner.py 真实产出的
envelope 字段结构与 golden 一致：任一边改字段名，两侧测试都会失败。

运行：python3 sidecar/contract/test_contract.py
（纯标准库，无需 pytest / 无需装依赖）
"""

import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SIDECAR_DIR = os.path.dirname(HERE)
RUNNER = os.path.join(SIDECAR_DIR, "runner.py")
GOLDEN = os.path.join(HERE, "code_run.golden.json")


def _keys(d):
    """返回字典的键集合（浅层），用于结构比对。"""
    return set(d.keys())


def run_runner(payload: dict) -> dict:
    proc = subprocess.run(
        [sys.executable, RUNNER],
        input=json.dumps(payload).encode(),
        capture_output=True,
        timeout=10,
    )
    return json.loads(proc.stdout.decode())


def main() -> int:
    with open(GOLDEN, encoding="utf-8") as f:
        golden = json.load(f)

    failures = []

    # 成功契约：runner sink 后应产出 {ok, outputs, logs}
    # 注意：runner 产出 outputs/logs 在顶层，sidecar/main.py 再包成 data.{outputs,logs}。
    # 契约锁定的是「字段名」，golden.success_response.data 的键即 runner 顶层业务键。
    got = run_runner({"code": "sink({'answer': 42})\nprint('debug line')", "inputs": {}})
    want_data_keys = _keys(golden["success_response"]["data"])  # {outputs, logs}
    if not got.get("ok"):
        failures.append(f"success: runner returned ok=false: {got}")
    else:
        # runner 顶层应含 outputs + logs（sidecar 会原样搬进 data）
        for k in want_data_keys:
            if k not in got:
                failures.append(f"success: runner output missing contract field {k!r}; got keys {_keys(got)}")

    # 失败契约：异常应产出 {ok:false, error:{code, message}}
    got_err = run_runner({"code": "raise ValueError('boom')", "inputs": {}})
    want_err_keys = _keys(golden["error_response"]["error"])  # {code, message}
    if got_err.get("ok"):
        failures.append(f"error: runner returned ok=true for raising code: {got_err}")
    elif "error" not in got_err:
        failures.append(f"error: runner missing 'error' object; got {_keys(got_err)}")
    else:
        for k in want_err_keys:
            if k not in got_err["error"]:
                failures.append(f"error: error object missing contract field {k!r}; got {_keys(got_err['error'])}")

    if failures:
        print("CONTRACT FAILED:")
        for f in failures:
            print("  -", f)
        return 1
    print("contract OK: runner envelope matches golden fixture")
    return 0


if __name__ == "__main__":
    sys.exit(main())
