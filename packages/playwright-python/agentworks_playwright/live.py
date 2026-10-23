"""Playwright stays on its calling thread; only frame transport runs in a worker."""
import asyncio
import json
import os
import queue
import threading
import time
from urllib.parse import quote, urlencode, urlsplit, urlunsplit

import websocket


def _endpoint(api_url=None, token=None, session_id=None, label=None):
    api_url = api_url if api_url is not None else os.environ.get("MCP_API_URL")
    token = token if token is not None else os.environ.get("MCP_API_TOKEN")
    if not api_url or not token:
        raise ValueError("Live view needs MCP_API_URL and MCP_API_TOKEN from a workflow session.")
    try:
        url = urlsplit(api_url)
        if url.scheme not in ("http", "https") or not url.hostname or url.username or url.password or url.query or url.fragment:
            raise ValueError()
        path = url.path.rstrip("/")
        parts = path.split("/")
        if len(parts) < 3 or parts[-2] != "s" or not parts[-1]:
            session_id = session_id or os.environ.get("MCP_SESSION_ID")
            if not session_id:
                raise ValueError()
            path += "/s/" + quote(session_id, safe="")
        return urlunsplit(("wss" if url.scheme == "https" else "ws", url.netloc, path + "/tools/browser/live", urlencode({"label": (label or "Python Playwright")[:200]}), "")), token
    except ValueError:
        raise ValueError("Live view requires an HTTP(S) session API URL without embedded credentials or query parameters.") from None


class _Publisher:
    """Bounded latest-frame transport; never calls Playwright from its worker."""
    def __init__(self, **options):
        url, token = _endpoint(**options)
        self.warning = ""
        self.done = threading.Event()
        self.frames = queue.Queue(maxsize=1)
        self.tabs = queue.Queue(maxsize=1)
        self.socket = None
        try:
            self.socket = websocket.create_connection(url, header={"Authorization": "Bearer " + token}, timeout=10, suppress_origin=True)
            message = json.loads(self.socket.recv())
            if message.get("type") != "registered" or not isinstance(message.get("browser_session"), str):
                raise ValueError()
            self.session_id = message["browser_session"]
            self.socket.settimeout(1)
        except Exception:
            if self.socket:
                self.socket.close(timeout=0)
            raise RuntimeError("Live view registration failed. Check the workflow session and server support.") from None
        self.worker = threading.Thread(target=self._run, name="agentworks-live-view", daemon=True)
        self.worker.start()

    def put(self, channel, data):
        if self.done.is_set():
            return
        try:
            channel.put_nowait(data)
        except queue.Full:
            try:
                channel.get_nowait()
            except queue.Empty:
                pass
            try:
                channel.put_nowait(data)
            except queue.Full:
                pass

    def _run(self):
        heartbeat = time.monotonic()
        try:
            while not self.done.wait(0.25):
                for channel in (self.tabs, self.frames):
                    try:
                        self.socket.send(json.dumps(channel.get_nowait()))
                    except queue.Empty:
                        pass
                if time.monotonic() - heartbeat >= 10:
                    self.socket.send('{"type":"ping"}')
                    heartbeat = time.monotonic()
        except Exception:
            if not self.done.is_set():
                self.warning = "Live view disconnected; the test continued."
        finally:
            self.done.set()
            self.socket.close(timeout=0)

    def stop(self):
        self.done.set()
        self.worker.join(timeout=2)
        self.socket.close(timeout=0)


class _LiveBase:
    def __init__(self, context, options):
        browser = context.browser
        if browser and browser.browser_type.name != "chromium":
            raise ValueError("Live viewing currently supports Chromium only.")
        self.context = context
        self.publisher = _Publisher(**options)
        self.pages = {}
        self.active = None
        self.sequence = 0
        self.stopped = False

    @property
    def session_id(self):
        return self.publisher.session_id

    @property
    def warning(self):
        return self.publisher.warning

    def _tabs(self):
        self.publisher.put(self.publisher.tabs, {"type": "tabs", "tabs": [
            {"tabId": item["id"], "title": page.url[:180] or "Test page", "url": page.url[:2048], "active": page is self.active}
            for page, item in self.pages.items()]})

    def _frame(self, page, frame):
        item = self.pages.get(page)
        if not item or self.stopped:
            return
        item["frame"] = frame
        if page is self.active:
            self.publisher.put(self.publisher.frames, {"type": "frame", "data": frame["data"], "metadata": {
                "deviceWidth": round(frame["metadata"]["deviceWidth"]), "deviceHeight": round(frame["metadata"]["deviceHeight"])}})

    def _closed(self, page):
        self.pages.pop(page, None)
        if self.active is page:
            self.active = next(reversed(self.pages), None)
            item = self.pages.get(self.active)
            if item and item.get("frame"):
                self._frame(self.active, item["frame"])
        self._tabs()

    def _item(self, page):
        self.sequence += 1
        item = {"id": "t" + str(self.sequence), "cdp": None}
        self.pages[page] = item
        self.active = page
        item["close"] = lambda: self._closed(page)
        item["navigate"] = lambda frame: self._tabs()
        page.on("close", item["close"])
        page.on("framenavigated", item["navigate"])
        return item

    def _remove_listeners(self):
        self.context.remove_listener("page", self.on_page)
        self.context.remove_listener("close", self.on_close)
        for page, item in self.pages.items():
            page.remove_listener("close", item["close"])
            page.remove_listener("framenavigated", item["navigate"])


_CAPTURE = {"format": "jpeg", "quality": 60, "maxWidth": 1280, "maxHeight": 720, "everyNthFrame": 1}


class _SyncLive(_LiveBase):
    def start(self):
        self.on_page = self._attach
        self.on_close = self.stop
        self.context.on("page", self.on_page)
        self.context.on("close", self.on_close)
        try:
            for page in self.context.pages:
                self._attach(page)
        except Exception:
            self.stop()
            raise RuntimeError("Live view could not attach to Chromium.") from None
        return self

    def _attach(self, page):
        if self.stopped or page in self.pages or page.is_closed():
            return
        item = self._item(page)
        try:
            cdp = item["cdp"] = self.context.new_cdp_session(page)
            def frame_received(frame):
                self._frame(page, frame)
                try:
                    cdp.send("Page.screencastFrameAck", {"sessionId": frame["sessionId"]})
                except Exception:
                    pass
            cdp.on("Page.screencastFrame", frame_received)
            cdp.send("Page.startScreencast", _CAPTURE)
            self._tabs()
        except Exception:
            if not page.is_closed() and not self.stopped:
                self.publisher.warning = "Could not stream a Chromium page."
                raise

    def stop(self):
        if self.stopped:
            return
        self.stopped = True
        self.publisher.stop()
        self._remove_listeners()
        for item in list(self.pages.values()):
            if item["cdp"]:
                try:
                    item["cdp"].detach()
                except Exception:
                    pass
        self.pages.clear()

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.stop()


class _AsyncLive(_LiveBase):
    async def start(self):
        self.tasks = set()
        self.on_page = lambda page: self._schedule(self._attach(page))
        self.on_close = lambda: self._schedule(self.stop())
        self.context.on("page", self.on_page)
        self.context.on("close", self.on_close)
        try:
            for page in self.context.pages:
                await self._attach(page)
        except Exception:
            await self.stop()
            raise RuntimeError("Live view could not attach to Chromium.") from None
        return self

    def _schedule(self, coro):
        task = asyncio.create_task(coro)
        self.tasks.add(task)
        def complete(task):
            self.tasks.discard(task)
            if not task.cancelled() and task.exception():
                self.publisher.warning = "Could not stream a Chromium page."
        task.add_done_callback(complete)

    async def _attach(self, page):
        if self.stopped or page in self.pages or page.is_closed():
            return
        item = self._item(page)
        cdp = item["cdp"] = await self.context.new_cdp_session(page)
        if self.stopped or page.is_closed():
            await cdp.detach()
            return
        async def frame_received(frame):
            self._frame(page, frame)
            try:
                await cdp.send("Page.screencastFrameAck", {"sessionId": frame["sessionId"]})
            except Exception:
                pass
        cdp.on("Page.screencastFrame", lambda frame: self._schedule(frame_received(frame)))
        await cdp.send("Page.startScreencast", _CAPTURE)
        self._tabs()

    async def stop(self):
        if self.stopped:
            return
        self.stopped = True
        await asyncio.to_thread(self.publisher.stop)
        self._remove_listeners()
        pending = [task for task in self.tasks if task is not asyncio.current_task()]
        for task in pending:
            task.cancel()
        await asyncio.gather(*pending, return_exceptions=True)
        for item in list(self.pages.values()):
            if item["cdp"]:
                try:
                    await item["cdp"].detach()
                except Exception:
                    pass
        self.pages.clear()

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_):
        await self.stop()


def attach_live_browser(context, **options):
    """Attach on the sync Playwright thread; stop() never closes the context."""
    return _SyncLive(context, options).start()


async def attach_live_browser_async(context, **options):
    """Attach on the caller's asyncio loop; await stop() before context teardown."""
    # Registration only uses networking, but context inspection stays on its loop.
    live = _AsyncLive(context, options)
    return await live.start()
