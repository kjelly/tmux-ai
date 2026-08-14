#!/usr/bin/env python3
"""Tiny interactive agent used by the pingpong smoke test.

It is deliberately not a coding agent. It reads one complete tmux-ai prompt,
recognizes the runner's dynamic protocol markers, and emits a deterministic
planner or reviewer response. This lets pingpong be tested without Codex,
Claude, or another external AI process.
"""

import json
import re
import sys


MARKER_RE = re.compile(r"[A-Z][A-Z0-9_]*__TMUXAI_[A-Za-z0-9_-]+_\d+")


def find_marker(lines, prefix):
    for line in lines:
        for marker in MARKER_RE.findall(line):
            if marker.startswith(prefix):
                return marker
    return ""


def protocol_from_prompt(lines):
    text_begin = find_marker(lines, "HANDOFF_TEXT_BEGIN__")
    text_end = find_marker(lines, "HANDOFF_TEXT_END__")
    status_begin = find_marker(lines, "HANDOFF_STATUS_BEGIN__")
    status_end = find_marker(lines, "HANDOFF_STATUS_END__")
    head_marker = find_marker(lines, "TMUXAI_PROMPT_HEAD__")
    head_nonce = ""
    for line in lines:
        marker_at = line.find(head_marker)
        if marker_at >= 0:
            remainder = line[marker_at + len(head_marker) :].strip()
            if remainder:
                head_nonce = remainder.split()[0]
                break

    # The marker is mentioned inside a natural-language instruction, not on a
    # line by itself. Do not select ALL_DONE__: coordinator owns that decision.
    done_marker = find_marker(lines, "DONE__")

    required = {
        "text_begin": text_begin,
        "text_end": text_end,
        "status_begin": status_begin,
        "status_end": status_end,
        "head_nonce": head_nonce,
        "done_marker": done_marker,
    }
    missing = [name for name, value in required.items() if not value]
    if missing:
        raise ValueError("missing protocol fields: " + ", ".join(missing))
    return required


def respond(role, prompt_lines):
    protocol = protocol_from_prompt(prompt_lines)
    if role == "planner":
        body = (
            "DEMO PLANNER\n"
            "1. 確認 pingpong prompt 能完整送達。\n"
            "2. 確認 coordinator 能把結果交給 reviewer。\n"
            "3. 確認 reviewer success 後 workflow 能安全停止。"
        )
        outcome = "needs_followup"
        summary = "demo planner 已提出三點驗證計畫，交給 reviewer 檢查。"
    else:
        body = (
            "DEMO REVIEWER\n"
            "三點驗證計畫完整、清楚，且不需要修改檔案或執行耗時指令。\n"
            "結論：可以停止 smoke test。"
        )
        outcome = "success"
        summary = "demo reviewer 確認三點計畫完整可執行。"

    receipt = {
        "version": 1,
        "outcome": outcome,
        "summary": summary,
        "prompt_head": protocol["head_nonce"],
        "tests": [],
    }
    print(protocol["text_begin"], flush=True)
    print(body, flush=True)
    print(protocol["text_end"], flush=True)
    print(protocol["status_begin"], flush=True)
    print(json.dumps(receipt, ensure_ascii=False, separators=(",", ":")), flush=True)
    print(protocol["status_end"], flush=True)
    print(protocol["done_marker"], flush=True)


def main():
    if len(sys.argv) != 2 or sys.argv[1] not in {"planner", "reviewer"}:
        print(f"usage: {sys.argv[0]} planner|reviewer", file=sys.stderr)
        return 2

    role = sys.argv[1]
    prompt = []
    echo_marker = "TMUXAI_PROMPT_ECHO__"
    for raw in sys.stdin:
        prompt.append(raw.rstrip("\r\n"))
        if echo_marker in prompt[-1]:
            try:
                respond(role, prompt)
            except ValueError as exc:
                print(f"demo agent protocol error: {exc}", file=sys.stderr, flush=True)
                return 1
            prompt = []
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
