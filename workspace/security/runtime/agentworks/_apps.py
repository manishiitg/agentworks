"""App results retain the connected service's JSON shape; no account setup here."""
import json

from . import AutomationError, _call


def result_text(reply):
    if not reply.ok:
        raise AutomationError(reply.error or "The app request failed. Check its connection in this Crew.")
    return reply.text


def parsed(text):
    if text in ("", "(no output)"):
        return None
    try:
        return json.loads(text)
    except ValueError as error:
        raise AutomationError("The app returned an unreadable JSON result. Check its run log.") from error


def request(action, arguments, *, json_result=True):
    text = result_text(_call(action, arguments, None, False))
    if action == "google" and text.endswith("\n...[truncated]"):
        raise AutomationError("The Google result is too large. Request a smaller range or fewer records.")
    return parsed(text) if json_result else text
