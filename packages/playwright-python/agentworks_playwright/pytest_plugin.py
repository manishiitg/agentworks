"""Opt in via pytest_plugins = ['agentworks_playwright.pytest_plugin']."""
import os
import warnings

import pytest

from . import attach_live_browser


@pytest.fixture(autouse=True)
def agentworks_live_browser(request):
    # Don't turn unrelated unit tests into browser tests.
    if not {"page", "context"}.intersection(request.fixturenames):
        yield None
        return
    if not (os.environ.get("MCP_API_URL") or os.environ.get("MCP_API_TOKEN")):
        request.node.user_properties.append(("live-view", "Not connected to an AgentWorks workflow."))
        yield None
        return
    if request.getfixturevalue("browser_name") != "chromium":
        request.node.user_properties.append(("live-view", "Chromium only."))
        yield None
        return
    context = request.getfixturevalue("context")
    live = attach_live_browser(context, label=request.node.nodeid)
    request.node.user_properties.append(("live-browser", live.session_id))
    try:
        yield live
    finally:
        live.stop()
        if live.warning:
            warnings.warn(live.warning, RuntimeWarning, stacklevel=1)
