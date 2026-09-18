#!/usr/bin/env python3
"""Drive the voice backend's MCP endpoint and report what it answered.

Why this exists: the MCP surface is how an agent says something to the user, and its
behaviour is *stateful* — the queue is take-once, the batch drops notifications, and
the REST routes and MCP share one queue. That is exactly the sort of thing that
passes unit tests and surprises a live client, so this drives the real HTTP endpoint
with the same JSON-RPC a client sends.

Standard library only, and it never touches a model: only the announcement queue is
exercised, so it works against a server that has never spoken.

    python3 agent_tools/probe_mcp.py                          # 127.0.0.1:17493
    python3 agent_tools/probe_mcp.py --url http://host:17493
    python3 agent_tools/probe_mcp.py --keep                   # leave messages behind

Exit status is 1 if any check fails, so it can gate a script.
"""

from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.request

DEFAULT_URL = "http://127.0.0.1:17493"

_failures: list[str] = []


def call(url: str, payload, timeout: float = 10.0) -> tuple[int, object, str]:
    """POST *payload* (dict, list, or raw string) and return status, JSON, raw text."""
    body = payload.encode() if isinstance(payload, str) else json.dumps(payload).encode()
    req = urllib.request.Request(
        url, data=body, headers={"content-type": "application/json"}, method="POST"
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode()
            status = resp.status
    except urllib.error.HTTPError as e:  # a 4xx/5xx still carries a JSON-RPC body
        raw, status = e.read().decode(), e.code
    try:
        return status, (json.loads(raw) if raw.strip() else None), raw
    except json.JSONDecodeError:
        return status, None, raw


def tool(url: str, name: str, arguments: dict | None = None, rid: int = 1):
    """Call one MCP tool and return its text plus the isError flag."""
    _, body, raw = call(
        url,
        {
            "jsonrpc": "2.0",
            "id": rid,
            "method": "tools/call",
            "params": {"name": name, "arguments": arguments or {}},
        },
    )
    if not isinstance(body, dict) or "result" not in body:
        return f"<no result: {raw[:120]}>", True
    result = body["result"]
    return result["content"][0]["text"], result.get("isError", False)


def check(label: str, ok: bool, detail: str = "") -> None:
    print(f"  {'ok  ' if ok else 'FAIL'}  {label}" + (f"  -- {detail}" if detail else ""))
    if not ok:
        _failures.append(label)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--url", default=DEFAULT_URL, help=f"backend base URL ({DEFAULT_URL})")
    ap.add_argument("--keep", action="store_true", help="do not clear the queue at the end")
    args = ap.parse_args()
    url = args.url.rstrip("/") + "/mcp"
    rest = args.url.rstrip("/")

    print(f"probing {url}")

    # The endpoint advertises itself without speaking the protocol.
    print("\ndiscovery")
    try:
        with urllib.request.urlopen(url, timeout=10) as resp:
            info = json.loads(resp.read().decode())
    except Exception as e:
        print(f"  FAIL  GET {url}: {e}")
        print("\nthe backend is not answering; is the container up?")
        return 1
    check("GET /mcp lists tools", sorted(info.get("tools", [])) == [
        "clear_notifications", "get_notifications", "notify"
    ], str(info.get("tools")))
    check("GET /mcp names a protocol version", bool(info.get("protocolVersion")),
          str(info.get("protocolVersion")))

    # Handshake, as a client performs it.
    print("\nhandshake")
    _, init, _ = call(url, {"jsonrpc": "2.0", "id": 1, "method": "initialize",
                            "params": {"clientInfo": {"name": "probe_mcp"}}})
    result = (init or {}).get("result", {})
    check("initialize answers", "result" in (init or {}))
    check("initialize names the server", result.get("serverInfo", {}).get("name") == "stables-tts",
          str(result.get("serverInfo")))
    check("initialize advertises tools", "tools" in result.get("capabilities", {}))

    status, body, raw = call(url, {"jsonrpc": "2.0", "method": "notifications/initialized"})
    # A JSON-RPC notification must not be answered; "null" would be an answer.
    check("notifications/initialized is 202 with an empty body",
          status == 202 and not raw.strip(), f"status={status} body={raw[:40]!r}")

    _, pong, _ = call(url, {"jsonrpc": "2.0", "id": 2, "method": "ping"})
    check("ping answers", (pong or {}).get("result") == {})

    # The queue: stateful, and take-once.
    print("\nqueue")
    tool(url, "clear_notifications")
    text, is_error = tool(url, "notify", {"text": "probe says hello", "kind": "summary",
                                          "source": "probe_mcp"})
    check("notify accepts a message", not is_error and "queued" in text, text)

    peeked, _ = tool(url, "get_notifications", {"consume": False})
    check("peek sees it", "probe says hello" in peeked, peeked.splitlines()[0] if peeked else "")
    peeked_again, _ = tool(url, "get_notifications", {"consume": False})
    check("peek does not consume", "probe says hello" in peeked_again)

    taken, _ = tool(url, "get_notifications")
    check("consume returns it", "probe says hello" in taken)
    again, _ = tool(url, "get_notifications")
    check("a second consume returns nothing", "no messages waiting" in again, again)

    # Errors: an agent must be told when it did something wrong.
    print("\nerrors")
    _, unknown, _ = call(url, {"jsonrpc": "2.0", "id": 3, "method": "no_such_method"})
    check("unknown method is -32601",
          (unknown or {}).get("error", {}).get("code") == -32601, json.dumps(unknown)[:80])
    _, no_name, _ = call(url, {"jsonrpc": "2.0", "id": 4, "method": "tools/call",
                               "params": {"arguments": {}}})
    check("tools/call without a name is -32602",
          (no_name or {}).get("error", {}).get("code") == -32602)
    _, broken, _ = call(url, "this is not json")
    check("malformed body is -32700",
          (broken or {}).get("error", {}).get("code") == -32700)

    empty, is_error = tool(url, "notify", {})
    # A tool failure is reported inside a successful JSON-RPC result, with isError.
    check("an empty notify is isError, not a crash", is_error, empty)

    # A batch answers in order and drops the notification that needs no reply.
    print("\nbatch")
    _, batch, raw = call(url, [
        {"jsonrpc": "2.0", "id": 10, "method": "ping"},
        {"jsonrpc": "2.0", "method": "notifications/initialized"},
        {"jsonrpc": "2.0", "id": 11, "method": "tools/call",
         "params": {"name": "notify", "arguments": {"text": "from a batch", "kind": "ping"}}},
    ])
    ids = [a.get("id") for a in batch] if isinstance(batch, list) else None
    check("a batch answers every request in order, and no more", ids == [10, 11], str(ids))

    # One queue, two faces: what MCP queued is visible over REST.
    print("\none queue, two faces")
    try:
        with urllib.request.urlopen(f"{rest}/v1/notifications?peek=true", timeout=10) as resp:
            pending = json.loads(resp.read().decode())
    except Exception as e:
        pending = {}
        check("REST notifications are readable", False, str(e))
    else:
        texts = [n["text"] for n in pending.get("items", [])]
        check("REST sees what MCP queued", "from a batch" in texts, str(texts))
        check("the REST view is bounded", pending.get("capacity", 0) > 0,
              f"capacity={pending.get('capacity')}")

    if not args.keep:
        tool(url, "clear_notifications")
        print("\nqueue cleared (--keep to leave messages behind)")

    if _failures:
        print(f"\n{len(_failures)} check(s) failed: {', '.join(_failures)}")
        return 1
    print("\nall checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
