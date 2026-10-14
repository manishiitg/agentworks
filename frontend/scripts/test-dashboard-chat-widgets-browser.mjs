// Real Chromium geometry/focus regression. Conversation resolution is a fixture;
// deployed native Ask and published-function execution require live acceptance.
import assert from 'node:assert/strict'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const fixture = { name: 'dashboard-chat-fixture', configureServer(server) {
  server.middlewares.use('/__dashboard-chat-test', async (_req, res) => {
    res.setHeader('Content-Type', 'text/html')
    res.end(await server.transformIndexHtml('/__dashboard-chat-test', `<!doctype html>
    <style>body{margin:0} #host{position:relative;overflow:hidden;width:100%} iframe{display:block;width:100%;height:600px;border:0}</style>
    <div id="host"><iframe></iframe></div><input id="outside" aria-label="Other field">
    <script type="module">
      import { installDashboardChatWidgets } from '/src/components/workflow/reportWidgets/dashboardChatWidgets.ts';
      const host=document.querySelector('#host'), frame=document.querySelector('iframe');
      frame.srcdoc='<style>body{margin:0}.space{height:50px} #clip{height:300px;overflow:auto} #panel{width:70%;height:420px;margin-left:15%}@media(max-width:500px){#panel{width:100%;margin-left:0}}</style><div class="space"></div><div id="clip"><section id="panel" data-report-chat-panel></section><div style="height:500px"></div></div><button data-report-chat-button data-report-chat-target="#panel">Questions</button><input id="inside">';
      frame.onload=()=>{
        window.opens=0; window.popups=0; window.failure=''; window.positions=[];
        const overlays=new Map();
        const binding=installDashboardChatWidgets(frame, {available:true,container:host,
          openConversation:async()=>{window.opens++;}, popup:open=>{if(open)window.popups++;}, error:message=>{window.failure=message;},
          changed:(slots)=>{ window.positions=slots;
            for(const slot of slots){
              let overlay=overlays.get(slot.key);
              if(!overlay){overlay=document.createElement('section');overlay.dataset.nativeChat='';overlay.innerHTML='<textarea aria-label="Composer"></textarea>';host.append(overlay);overlays.set(slot.key,overlay);}
              Object.assign(overlay.style,{position:'absolute',left:slot.left+'px',top:slot.top+'px',width:slot.width+'px',height:slot.height+'px',clipPath:slot.clipPath,visibility:slot.visible?'visible':'hidden'});
            }
          }
        });
        window.controls=binding.api; frame.contentWindow.report={chat:binding.api}; window.ready=true;
      };
    </script>`))
  })
} }
const server = await createServer({ configFile: false, plugins: [fixture], optimizeDeps: { entries: [] }, server: { host: '127.0.0.1', port: 0 } })
let browser
try {
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const page = await browser.newPage({ viewport: { width: 1100, height: 800 } })
  await page.goto(server.resolvedUrls.local[0] + '__dashboard-chat-test')
  await page.waitForFunction(() => window.ready && window.positions.length === 1)
  assert.deepEqual(await page.evaluate(() => Object.keys(window.controls).sort()), ['available', 'close', 'mount', 'open'])
  const alignment = () => page.evaluate(() => {
    const frame=document.querySelector('iframe'), slot=frame.contentDocument.querySelector('#panel').getBoundingClientRect();
    const native=document.querySelector('[data-native-chat]').getBoundingClientRect(), bounds=frame.getBoundingClientRect();
    return { dx:Math.abs(native.left-(bounds.left+slot.left)), dy:Math.abs(native.top-(bounds.top+slot.top)), dw:Math.abs(native.width-slot.width), clip:window.positions[0].clipPath, visible:window.positions[0].visible };
  })
  let position = await alignment()
  assert.ok(position.dx < 1 && position.dy < 1 && position.dw < 1)
  assert.match(position.clip, /120px/) // panel retains full height, clipped by authored scrolling area.
  await page.locator('[aria-label="Composer"]').fill('My unsent question')
  await page.setViewportSize({ width: 420, height: 800 })
  await page.waitForFunction(() => window.positions[0].width === 420)
  position = await alignment()
  assert.ok(position.dx < 1 && position.dw < 1)
  assert.equal(await page.locator('[aria-label="Composer"]').inputValue(), 'My unsent question')
  await page.evaluate(() => document.querySelector('iframe').contentDocument.querySelector('#clip').scrollTop = 450)
  await page.waitForFunction(() => !window.positions[0].visible)
  assert.equal(await page.locator('[data-native-chat]').isVisible(), false)
  await page.evaluate(() => { const doc=document.querySelector('iframe').contentDocument;doc.querySelector('#clip').scrollTop=0;doc.querySelector('#panel').style.display='none'; })
  await page.waitForFunction(() => !window.positions[0].visible)
  await page.evaluate(() => document.querySelector('iframe').contentDocument.querySelector('#panel').style.display='block')
  await page.waitForFunction(() => window.positions[0].visible)
  await page.evaluate(() => window.controls.close('#panel'))
  await page.waitForFunction(() => !window.positions[0].visible)
  await page.frameLocator('iframe').getByRole('button', {name:'Questions'}).click()
  await page.waitForFunction(() => window.positions[0].visible)
  assert.equal(await page.locator('[aria-label="Composer"]').inputValue(), 'My unsent question')
  assert.equal(await page.evaluate(() => document.querySelector('iframe').contentDocument.querySelector('[data-native-chat]') === null), true)
  await page.evaluate(() => document.querySelector('iframe').contentDocument.querySelector('#panel').remove())
  await page.waitForFunction(() => window.positions.length === 0)
  await page.locator('#outside').fill('Keep typing')
  const error = await page.evaluate(() => window.controls.open().catch(error => error.message))
  assert.match(error, /Finish editing/)
  assert.equal(await page.evaluate(() => document.activeElement.id), 'outside')
  assert.equal(await page.evaluate(() => window.popups), 0)
  assert.equal(await page.evaluate(() => 'send' in window.controls || 'ask' in window.controls), false)
  console.log('Dashboard chat Chromium checks passed: authored geometry, responsive resize, clipping/scroll/hide, retained draft, host UI, four controls and focus protection (fixture; not deployed Ask acceptance).')
} finally {
  await browser?.close()
  await server.close()
}
