import json
import sys
import urllib.request


def decision(kind, reason, updated=None):
    output = {
        "hookEventName": "PreToolUse",
        "permissionDecision": kind,
        "permissionDecisionReason": reason,
    }
    if updated is not None:
        output["updatedInput"] = updated
    print(json.dumps({"hookSpecificOutput": output}))


def answer_question(payload):
    original = payload.get("tool_input")
    if not isinstance(original, dict):
        raise ValueError("missing question input")
    questions = original.get("questions")
    if not isinstance(questions, list) or not 1 <= len(questions) <= 12:
        raise ValueError("invalid questions")
    canonical = []
    texts = set()
    for index, question in enumerate(questions):
        text = question.get("question", "")
        if not isinstance(text, str) or not text.strip() or text in texts:
            raise ValueError("question texts must be nonempty and unique")
        texts.add(text)
        canonical.append({
            "id": "question-" + str(index + 1),
            "header": question.get("header", ""),
            "question": text,
            "options": [{"label": option["label"].strip(), "description": option.get("description", "")}
                        for option in question["options"]],
            "multi_select": question.get("multiSelect") is True,
            "allow_other": True,
        })
    request = urllib.request.Request(
        CONFIG["api_url"].rstrip("/") + "/tools/custom/request_clarification",
        data=json.dumps({"questions": canonical}).encode("utf-8"),
        headers={"Content-Type": "application/json", "Authorization": "Bearer " + CONFIG["token"],
                 "X-Session-ID": CONFIG["session_id"]},
        method="POST",
    )
    # A redirect must never forward the session credential to another host.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None
    opener = urllib.request.build_opener(NoRedirect)
    with opener.open(request, timeout=1805) as response:
        envelope = json.load(response)
    if envelope.get("success") is not True:
        raise ValueError("clarification was cancelled or unavailable")
    result = envelope.get("result")
    if isinstance(result, str):
        result = json.loads(result)
    if not isinstance(result, dict) or result.get("status") != "answered":
        raise ValueError("clarification did not return submitted answers")
    returned = result.get("answers")
    if not isinstance(returned, list) or len(returned) != len(canonical):
        raise ValueError("incomplete answers")
    answers = {}
    for index, (question, answer) in enumerate(zip(canonical, returned)):
        if answer.get("id") != question["id"]:
            raise ValueError("answer belongs to another question")
        labels = answer.get("selected_labels", [])
        other = answer.get("other_text", "")
        if not isinstance(labels, list) or not all(isinstance(label, str) for label in labels):
            raise ValueError("invalid answer labels")
        if not isinstance(other, str):
            raise ValueError("invalid custom answer")
        chosen = labels + ([other.strip()] if other.strip() else [])
        allowed = {option["label"] for option in question["options"]}
        if not chosen or len(set(labels)) != len(labels) or any(label not in allowed for label in labels):
            raise ValueError("invalid answer")
        if not question["multi_select"] and len(chosen) != 1:
            raise ValueError("single choice needs one answer")
        # Claude's native answer format keys by original question text; multiple
        # selections use comma-separated labels, including a custom answer.
        answers[questions[index]["question"]] = ", ".join(chosen)
    updated = dict(original)
    updated["answers"] = answers
    decision("allow", "The user submitted these answers in AgentWorks.", updated)


try:
    payload = json.load(sys.stdin)
    if payload.get("tool_name") == "AskUserQuestion":
        answer_question(payload)
except Exception:
    # Do not leak credentials or leave a terminal menu open on transport errors.
    # An interruption must never become an assumed choice.
    decision("deny", "The clarification was cancelled, expired, or unavailable. No user answer was received; do not assume approval.")
