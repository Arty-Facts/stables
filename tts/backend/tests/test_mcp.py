"""The MCP endpoint and the REST routes over the same queue.

These cover the contract an extension depends on: the handshake, the tool list,
and that a `notify` tool call actually leaves something for the TUI to speak. The
`max_dropped` rule is here too, because a caller that cares about its message
being delivered has to be able to find out that it was not.
"""

import pytest
from fastapi.testclient import TestClient

from backend.app import create_app
from backend.engines.registry import Registry
from backend.notifications import NotificationQueue

from .test_routes import StubEngine


def _client(queue: NotificationQueue | None = None) -> TestClient:
    return TestClient(create_app(Registry([StubEngine()]), queue or NotificationQueue()))


def _rpc(client: TestClient, method: str, params: dict | None = None, request_id=1):
    body = {"jsonrpc": "2.0", "id": request_id, "method": method}
    if params is not None:
        body["params"] = params
    return client.post("/mcp", json=body)


# ── handshake ────────────────────────────────────────────────────────────────


def test_initialize_reports_the_protocol_and_capabilities():
    result = _rpc(_client(), "initialize", {"protocolVersion": "2025-06-18"}).json()["result"]
    assert result["protocolVersion"]
    assert "tools" in result["capabilities"]
    assert result["serverInfo"]["name"] == "stables-tts"


def test_initialized_notification_gets_no_body():
    """A JSON-RPC notification must not be answered with a result."""
    resp = _client().post(
        "/mcp", json={"jsonrpc": "2.0", "method": "notifications/initialized"}
    )
    assert resp.status_code == 202
    assert not resp.content


def test_ping_is_answered():
    assert _rpc(_client(), "ping").json()["result"] == {}


def test_unknown_method_is_method_not_found():
    body = _rpc(_client(), "no/such/method").json()
    assert body["error"]["code"] == -32601


def test_malformed_json_is_a_parse_error():
    resp = _client().post("/mcp", content=b"not json", headers={"Content-Type": "application/json"})
    assert resp.json()["error"]["code"] == -32700


def test_batch_requests_are_answered_in_order():
    client = _client()
    resp = client.post(
        "/mcp",
        json=[
            {"jsonrpc": "2.0", "id": 1, "method": "ping"},
            {"jsonrpc": "2.0", "method": "notifications/initialized"},
            {"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
        ],
    )
    answers = resp.json()
    # The notification takes no reply, so two answers come back for three inputs.
    assert [a["id"] for a in answers] == [1, 2]


def test_get_mcp_describes_the_endpoint():
    body = _client().get("/mcp").json()
    assert body["endpoint"] == "/mcp"
    assert "notify" in body["tools"]


# ── tools ────────────────────────────────────────────────────────────────────


def test_tools_list_advertises_notify():
    tools = _rpc(_client(), "tools/list").json()["result"]["tools"]
    names = {t["name"] for t in tools}
    assert {"notify", "get_notifications", "clear_notifications"} <= names
    notify = next(t for t in tools if t["name"] == "notify")
    assert notify["inputSchema"]["required"] == ["text"]


def test_notify_queues_a_message_for_the_tui():
    client = _client()
    result = _rpc(client, "tools/call", {"name": "notify", "arguments": {"text": "Tests are green"}})
    assert result.json()["result"]["isError"] is False

    # And the TUI sees it through the REST side of the same queue.
    items = client.get("/v1/notifications").json()["items"]
    assert [i["text"] for i in items] == ["Tests are green"]


def test_notify_without_text_is_a_tool_error_not_a_crash():
    result = _rpc(_client(), "tools/call", {"name": "notify", "arguments": {}}).json()
    assert result["result"]["isError"] is True


def test_notify_reports_dropped_messages():
    """A caller must be able to tell that older messages were displaced."""
    client = _client(NotificationQueue(capacity=1))
    _rpc(client, "tools/call", {"name": "notify", "arguments": {"text": "first"}})
    text = _rpc(
        client, "tools/call", {"name": "notify", "arguments": {"text": "second"}}
    ).json()["result"]["content"][0]["text"]
    assert "dropped 1" in text


def test_unknown_tool_is_reported_as_a_tool_error():
    result = _rpc(_client(), "tools/call", {"name": "explode", "arguments": {}}).json()
    assert result["result"]["isError"] is True


def test_tools_call_without_a_name_is_invalid_params():
    body = _rpc(_client(), "tools/call", {"arguments": {}}).json()
    assert body["error"]["code"] == -32602


# ── rest routes ──────────────────────────────────────────────────────────────


def test_post_notify_returns_the_queued_message():
    body = _client().post("/v1/notify", json={"text": "Heads up", "kind": "summary"}).json()
    assert body["notification"]["kind"] == "summary"
    assert body["pending"] == 1


def test_notify_rejects_empty_text():
    assert _client().post("/v1/notify", json={"text": "  "}).status_code == 400


def test_notify_conflicts_when_the_caller_forbade_drops():
    client = _client(NotificationQueue(capacity=1))
    client.post("/v1/notify", json={"text": "first"})
    resp = client.post("/v1/notify", json={"text": "second", "max_dropped": 0})
    assert resp.status_code == 409
    assert "queue full" in resp.json()["detail"]


def test_get_consumes_by_default_but_peek_does_not():
    client = _client()
    client.post("/v1/notify", json={"text": "once"})

    assert client.get("/v1/notifications", params={"consume": False}).json()["pending"] == 1
    assert client.get("/v1/notifications").json()["pending"] == 0
    assert client.get("/v1/notifications").json()["items"] == []


def test_delete_clears_the_queue():
    client = _client()
    client.post("/v1/notify", json={"text": "a"})
    client.post("/v1/notify", json={"text": "b"})
    assert client.delete("/v1/notifications").json()["removed"] == 2
