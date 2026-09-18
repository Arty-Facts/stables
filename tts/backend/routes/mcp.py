"""MCP endpoint: the announcement queue as tools an agent can call.

Model Context Protocol over HTTP — a client POSTs JSON-RPC 2.0 and gets a JSON
result, which is the transport's simplest mode. That is enough for the case this
exists for: a Pi extension (or any MCP client) wants to tell the user something
and has no way to speak. It calls `notify`, and the TUI says it out loud when the
user is free.

Kept deliberately small: three tools over the same `NotificationQueue` the REST
routes use, so there is one implementation of the queue, one set of bounds, and
no second place for messages to pile up.
"""

from fastapi import APIRouter, Request, Response

from ..notifications import KINDS, NotificationQueue

#: The MCP revision this server implements.
PROTOCOL_VERSION = "2025-06-18"

#: JSON-RPC 2.0 error codes.
PARSE_ERROR = -32700
INVALID_REQUEST = -32600
METHOD_NOT_FOUND = -32601
INVALID_PARAMS = -32602
INTERNAL_ERROR = -32603

_NOTIFY_SCHEMA = {
    "type": "object",
    "properties": {
        "text": {"type": "string", "description": "What to say to the user."},
        "kind": {
            "type": "string",
            "enum": list(KINDS),
            "description": (
                "How to present it: say (speak it), summary (report), "
                "question (wait for an answer), ping (short notice)."
            ),
        },
        "source": {
            "type": "string",
            "description": "Where it came from, e.g. an extension name.",
        },
    },
    "required": ["text"],
}

_TOOLS = [
    {
        "name": "notify",
        "description": (
            "Leave a message for the user. It is spoken or shown by the voice "
            "client the next time the user is free. Use this to ask a question, "
            "report a finished task, or hand over a summary."
        ),
        "inputSchema": _NOTIFY_SCHEMA,
    },
    {
        "name": "get_notifications",
        "description": (
            "Read messages waiting for the user. Takes them off the queue by "
            "default; pass consume=false to look without taking."
        ),
        "inputSchema": {
            "type": "object",
            "properties": {
                "consume": {"type": "boolean", "default": True},
                "limit": {"type": "integer", "minimum": 1, "maximum": 100},
            },
        },
    },
    {
        "name": "clear_notifications",
        "description": "Discard every message waiting for the user.",
        "inputSchema": {"type": "object", "properties": {}},
    },
]


def _result(request_id, result) -> dict:
    return {"jsonrpc": "2.0", "id": request_id, "result": result}


def _error(request_id, code: int, message: str) -> dict:
    return {"jsonrpc": "2.0", "id": request_id, "error": {"code": code, "message": message}}


def _text(message: str, is_error: bool = False) -> dict:
    """The shape `tools/call` returns: content blocks plus an error flag."""
    return {"content": [{"type": "text", "text": message}], "isError": is_error}


def _call_tool(queue: NotificationQueue, name: str, params: dict) -> dict:
    if name == "notify":
        text = params.get("text")
        if not isinstance(text, str) or not text.strip():
            return _text("notify needs a non-empty 'text'", is_error=True)
        kind = params.get("kind", "say")
        note, dropped = queue.add(text, kind, str(params.get("source", "")))
        message = f"queued #{note.id} ({note.kind})"
        if dropped:
            # Say it plainly: the caller's message arrived, but it displaced
            # older ones, and silently claiming success would hide that.
            message += f"; dropped {dropped} older message(s)"
        return _text(message)

    if name == "get_notifications":
        consume = params.get("consume", True)
        limit = params.get("limit")
        if limit is not None and not isinstance(limit, int):
            return _text("limit must be an integer", is_error=True)
        items = queue.consume(limit) if consume else queue.pending(limit)
        if not items:
            return _text("no messages waiting")
        return _text("\n".join(f"[{n.kind}] {n.text}" for n in items))

    if name == "clear_notifications":
        return _text(f"cleared {queue.clear()} message(s)")

    return _text(f"unknown tool {name!r}", is_error=True)


def handle(queue: NotificationQueue, message: dict) -> dict | None:
    """Answer one JSON-RPC message, or None when it needs no reply."""
    if not isinstance(message, dict) or message.get("jsonrpc") != "2.0":
        return _error(None, INVALID_REQUEST, "expected a JSON-RPC 2.0 object")

    method = message.get("method")
    request_id = message.get("id")
    params = message.get("params") or {}
    if not isinstance(params, dict):
        return _error(request_id, INVALID_PARAMS, "params must be an object")

    # A JSON-RPC notification has no id and must not be answered.
    if method == "notifications/initialized":
        return None

    if method == "initialize":
        return _result(
            request_id,
            {
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": {"tools": {"listChanged": False}},
                "serverInfo": {"name": "stables-tts", "version": "1.0.0"},
                "instructions": (
                    "Leave messages for the user with the notify tool. They are "
                    "spoken by the voice client when the user is free."
                ),
            },
        )

    if method == "ping":
        return _result(request_id, {})

    if method == "tools/list":
        return _result(request_id, {"tools": _TOOLS})

    if method == "tools/call":
        name = params.get("name")
        if not isinstance(name, str):
            return _error(request_id, INVALID_PARAMS, "tools/call needs a tool name")
        arguments = params.get("arguments") or {}
        if not isinstance(arguments, dict):
            return _error(request_id, INVALID_PARAMS, "arguments must be an object")
        return _result(request_id, _call_tool(queue, name, arguments))

    return _error(request_id, METHOD_NOT_FOUND, f"unknown method {method!r}")


def router(queue: NotificationQueue) -> APIRouter:
    api = APIRouter()

    @api.post("/mcp")
    async def mcp(request: Request):
        try:
            body = await request.json()
        except Exception:
            return _error(None, PARSE_ERROR, "body is not valid JSON")

        # Batch requests are part of JSON-RPC; answer them in order, dropping the
        # notifications that take no reply.
        if isinstance(body, list):
            answers = [handle(queue, item) for item in body]
            return [a for a in answers if a is not None]

        answer = handle(queue, body)
        if answer is None:
            # A notification was accepted and takes no reply. Returning None here
            # would serialise as the JSON body "null"; a JSON-RPC notification
            # needs an empty body.
            return Response(status_code=202)
        return answer

    @api.get("/mcp")
    async def mcp_info():
        """Not the protocol — just enough to prove the endpoint is there."""
        return {
            "endpoint": "/mcp",
            "transport": "http-json-rpc",
            "protocolVersion": PROTOCOL_VERSION,
            "tools": [t["name"] for t in _TOOLS],
        }

    return api
