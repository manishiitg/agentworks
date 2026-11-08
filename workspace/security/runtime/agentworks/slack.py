"""Read and send through this automation's existing Slack channel route."""
from . import JSONValue, _call
from ._apps import parsed, request, result_text


def read(route_id: str, *, since_ts: str | None = None, limit: int = 100) -> JSONValue:
    parameters = {"limit": limit}
    if since_ts is not None:
        parameters["oldest"] = since_ts
    return request("slack_read", {"route_id": route_id, "method": "conversations.history", "parameters": parameters})


def send(route_id: str, text: str) -> JSONValue:
    """Use the existing tracked sender. Sends are real, including mocked-agent runs."""
    return parsed(result_text(_call("send_slack", {"route_id": route_id, "message": text}, None, False)))


def reply(route_id: str, thread_ts: str, text: str) -> JSONValue:
    """Reply to a message read from this route; the server checks its channel."""
    return parsed(result_text(_call("send_slack", {"route_id": route_id, "message": text, "thread_ts": thread_ts}, None, False)))
