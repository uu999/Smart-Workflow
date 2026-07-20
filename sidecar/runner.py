"""Smart-Workflow Python code runner（子进程入口）。

由 sidecar 以独立子进程方式启动：`python runner.py`。
- 从 stdin 读入 JSON：{"code": "...", "inputs": {...}}
- 在受控命名空间中 exec 用户代码
- 用户通过 sink(obj) 提交结构化输出（对齐 PaiFlow code 节点心智）
- 用户代码的 print/stderr 被隔离进日志缓冲，不污染结果通道
- 结果 JSON 从真实 stdout（fd 1）写回，sidecar 解析

结果契约（写回 stdout 的单行 JSON）：
    成功: {"ok": true,  "outputs": {...}, "logs": "..."}
    失败: {"ok": false, "error": {"code": "...", "message": "..."}, "logs": "..."}
"""

import io
import json
import os
import sys
import traceback

# 输出大小上限，防止用户代码刷爆内存/管道。
MAX_LOG_BYTES = 64 * 1024
MAX_OUTPUT_BYTES = 256 * 1024


def _truncate(text: str, limit: int) -> str:
    data = text.encode("utf-8", "replace")
    if len(data) <= limit:
        return text
    return data[:limit].decode("utf-8", "ignore") + "\n...[truncated]"


def _sanitize_error(exc: BaseException) -> str:
    """TD-6 脱敏：对外只暴露异常类型、消息，以及用户代码帧（<user_code>）的行号。
    绝不回传含服务器绝对路径（runner.py 等框架帧）的完整栈。
    """
    head = f"{type(exc).__name__}: {exc}"
    user_frames = []
    tb = exc.__traceback__
    while tb is not None:
        filename = tb.tb_frame.f_code.co_filename
        if filename == "<user_code>":
            user_frames.append(f'  File "<user_code>", line {tb.tb_lineno}')
        tb = tb.tb_next
    if user_frames:
        return head + "\n" + "\n".join(user_frames)
    return head


def main() -> None:
    # 先抢占真实 stdout（fd 1）作为唯一结果通道，
    # 再把 sys.stdout/stderr 换成缓冲，隔离用户 print。
    result_fd = os.dup(1)

    raw = sys.stdin.read()
    log_buf = io.StringIO()
    sys.stdout = log_buf
    sys.stderr = log_buf

    def emit(payload: dict) -> None:
        payload["logs"] = _truncate(log_buf.getvalue(), MAX_LOG_BYTES)
        blob = json.dumps(payload, ensure_ascii=False, default=str)
        os.write(result_fd, blob.encode("utf-8", "replace"))

    try:
        req = json.loads(raw) if raw.strip() else {}
    except json.JSONDecodeError as exc:
        emit({"ok": False, "error": {"code": "BAD_REQUEST", "message": f"invalid request json: {exc}"}})
        return

    code = req.get("code", "")
    inputs = req.get("inputs", {})
    if not isinstance(inputs, dict):
        emit({"ok": False, "error": {"code": "BAD_REQUEST", "message": "inputs must be an object"}})
        return

    collected: dict = {}

    def sink(obj):
        """用户提交输出：sink({"key": value})，多次调用累积合并。"""
        if not isinstance(obj, dict):
            raise TypeError("sink() expects a dict")
        collected.update(obj)

    sandbox = {
        "__name__": "__user__",
        "inputs": inputs,
        "sink": sink,
    }

    try:
        compiled = compile(code, "<user_code>", "exec")
        exec(compiled, sandbox)  # noqa: S102 - 进程隔离 + 超时 kill 提供隔离，非语言级沙箱
    except SyntaxError as exc:
        emit({"ok": False, "error": {"code": "SYNTAX_ERROR", "message": f"{exc}"}})
        return
    except BaseException as exc:  # noqa: BLE001 - 用户代码任何异常都要标准化回传
        # TD-6 脱敏：对外 message 只保留异常类型 + 消息 + 用户代码帧（<user_code>），
        # 裁掉含 runner.py 绝对路径的框架帧；完整 traceback 写入 logs（服务端可见）。
        full_tb = traceback.format_exc()
        print(full_tb, file=sys.stderr)  # sys.stderr 已重定向进 log_buf
        safe_msg = _sanitize_error(exc)
        emit({"ok": False, "error": {"code": "RUNTIME_ERROR", "message": safe_msg}})
        return

    try:
        # 严格序列化：不做 default=str 兜底，不可 JSON 化的输出显式报错，
        # 让 Agent 得到明确信号（如需 datetime，请在用户代码里自行 .isoformat()）。
        blob = json.dumps(collected, ensure_ascii=False)
    except (TypeError, ValueError) as exc:
        emit({"ok": False, "error": {"code": "OUTPUT_NOT_SERIALIZABLE", "message": f"{exc}"}})
        return
    if len(blob.encode("utf-8", "replace")) > MAX_OUTPUT_BYTES:
        emit({"ok": False, "error": {"code": "OUTPUT_TOO_LARGE", "message": "sink output exceeds size limit"}})
        return

    emit({"ok": True, "outputs": collected})


if __name__ == "__main__":
    main()
