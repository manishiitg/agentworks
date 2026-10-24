import asyncio
import os
import sys
from pathlib import Path

from playwright.sync_api import sync_playwright, expect as sync_expect
from playwright.async_api import async_playwright, expect as async_expect
from agentworks_playwright import attach_live_browser, attach_live_browser_async

BLUE = '<body style="background:#123456"><input id="human" style="position:absolute;left:8px;top:80px"><button onclick="document.body.style.background=\'#285430\'">Confirm</button></body>'
YELLOW = '<body style="background:#cca020">Popup</body>'
output = Path(os.environ['AGENTWORKS_TEST_OUTPUT']) / 'python'
output.mkdir(parents=True, exist_ok=True)


def sync_demo():
    with sync_playwright() as p:
        browser = p.chromium.launch()
        context = browser.new_context(viewport={"width": 640, "height": 480}, record_video_dir=str(output))
        with attach_live_browser(context, label='Python sync checkout'):
            page = context.new_page()
            page.set_content(BLUE)
            sync_expect(page.locator("#human")).to_have_value("manualx", timeout=10000)
            page.wait_for_timeout(1500)
            page.get_by_role('button', name='Confirm').click()
            page.wait_for_timeout(1500)
            popup = context.new_page()
            popup.set_content(YELLOW)
            page.wait_for_timeout(1500)
            popup.close()
            page.wait_for_timeout(1500)
        assert not page.is_closed(), 'helper closed the user browser'
        page.get_by_role('button', name='Confirm').click()
        context.close()
        browser.close()


async def async_demo():
    async with async_playwright() as p:
        browser = await p.chromium.launch()
        context = await browser.new_context(viewport={"width": 640, "height": 480}, record_video_dir=str(output))
        async with await attach_live_browser_async(context, label='Python async checkout'):
            page = await context.new_page()
            await page.set_content(BLUE)
            await async_expect(page.locator("#human")).to_have_value("manualx", timeout=10000)
            await page.wait_for_timeout(1500)
            await page.get_by_role('button', name='Confirm').click()
            await page.wait_for_timeout(1500)
            popup = await context.new_page()
            await popup.set_content(YELLOW)
            await page.wait_for_timeout(1500)
            await popup.close()
            await page.wait_for_timeout(1500)
        assert not page.is_closed()
        await page.get_by_role('button', name='Confirm').click()
        await context.close()
        await browser.close()


if __name__ == '__main__':
    if sys.argv[1] == 'async':
        asyncio.run(async_demo())
    else:
        sync_demo()
