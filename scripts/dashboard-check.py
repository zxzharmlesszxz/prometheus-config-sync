#!/usr/bin/env python3
from __future__ import annotations

import json
import sys
from pathlib import Path


def find_exprs(node, path):
    if isinstance(node, dict):
        panel_ctx = path
        title = node.get("title")
        if isinstance(title, str):
            panel_ctx = f"{path} / title={title!r}"
        ident = node.get("id")
        if ident is not None:
            panel_ctx = f"{panel_ctx} / id={ident}"

        expr = node.get("expr")
        if isinstance(expr, str):
            yield panel_ctx, expr, node.get("refId", "")

        for child in node.get("targets", []) if isinstance(node.get("targets", []), list) else []:
            expr = child.get("expr")
            if isinstance(expr, str):
                yield panel_ctx, expr, child.get("refId", "")

        for value in node.values():
            if isinstance(value, (dict, list)):
                yield from find_exprs(value, panel_ctx)
    elif isinstance(node, list):
        for idx, item in enumerate(node):
            yield from find_exprs(item, f"{path}[{idx}]")


def balanced(query: str):
    stack = []
    pairs = {')': '(', '}': '{', ']': '['}
    opener = set(pairs.values())

    in_double = False
    in_single = False
    escape = False

    for idx, ch in enumerate(query):
        if escape:
            escape = False
            continue

        if ch == '\\':
            escape = True
            continue

        if in_double:
            if ch == '"':
                in_double = False
            continue
        if in_single:
            if ch == "'":
                in_single = False
            continue

        if ch == '"':
            in_double = True
            continue
        if ch == "'":
            in_single = True
            continue

        if ch in opener:
            stack.append(ch)
            continue

        if ch in pairs:
            if not stack or stack[-1] != pairs[ch]:
                return False, idx, ch
            stack.pop()

    if in_double or in_single:
        return False, len(query), 'quote'
    if stack:
        return False, len(query), ''.join(stack)
    return True, len(query), ''


def main(path: str) -> int:
    dashboard_path = Path(path)
    try:
        data = json.loads(dashboard_path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        print(f"dashboard-check: file not found: {dashboard_path}", file=sys.stderr)
        return 1
    except json.JSONDecodeError as err:
        print(f"dashboard-check: invalid JSON in {dashboard_path}: {err}", file=sys.stderr)
        return 1

    errors = 0
    for path_ctx, expr, ref_id in find_exprs(data, "root"):
        expr = expr.strip()
        if not expr:
            print(f"dashboard-check: empty expr in {path_ctx} refId={ref_id}", file=sys.stderr)
            errors += 1
            continue
        ok, pos, symbol = balanced(expr)
        if not ok:
            print(
                f"dashboard-check: unbalanced tokens in {path_ctx} refId={ref_id} at position {pos} near {symbol!r}: {expr}",
                file=sys.stderr,
            )
            errors += 1

    if errors:
        print(f"dashboard-check: found {errors} expression validation issue(s) in {dashboard_path}", file=sys.stderr)
        return 1

    print(f"dashboard-check: PromQL expression structure looks syntactically balanced in {dashboard_path}")
    return 0


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("usage: dashboard-check.py <path>", file=sys.stderr)
        raise SystemExit(1)
    raise SystemExit(main(sys.argv[1]))
