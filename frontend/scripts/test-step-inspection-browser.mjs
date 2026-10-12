// Production inspector in Chromium. Fixtures supply only recorded API data;
// this check does not claim to run a provider or repair historical transcripts.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'step-inspection-browser')
const server = await createServer({server:{host:'127.0.0.1',port:0}})
let browser
try {
 await mkdir(output,{recursive:true})
 await server.listen()
 browser = await chromium.launch({headless:true})
 for (const [name,width,theme] of [['desktop',1100,'dark'],['phone',375,'dark'],['light',1100,'light']]) {
  const page = await browser.newPage({viewport:{width,height:850}})
  const errors = []
  page.on('pageerror',error=>errors.push(error.message))
  await page.goto(server.resolvedUrls.local[0]+`scripts/test-fixtures/step-inspection-qa.html?theme=${theme}`)
  await page.getByText('User message sent to this task',{exact:true}).waitFor()
  assert.equal(await page.getByLabel('Inspect task').inputValue(),'work-task','empty parent automatically opens work task')
  assert.equal(await page.getByRole('tablist',{name:'Chat and step inspections'}).getByRole('tab').count(),2)
  assert.match(await page.getByRole('tabpanel').last().textContent(),/Create status.json with status INITIALIZED/)
  await page.screenshot({path:path.join(output,`step-inspection-${name}-user.png`),fullPage:true})
  await page.getByRole('tab',{name:'System prompt',exact:true}).click()
  assert.match(await page.getByRole('tabpanel').last().textContent(),/Write only inside the assigned output folder/)
  await page.getByRole('tab',{name:'Tools (1)',exact:true}).click()
  await page.getByText('write_file',{exact:true}).click()
  assert.match(await page.getByRole('tabpanel').last().textContent(),/INITIALIZED/)
  assert.match(await page.getByRole('tabpanel').last().textContent(),/status.json saved/)
  await page.screenshot({path:path.join(output,`step-inspection-${name}-tools.png`),fullPage:true})
  await page.getByRole('tab',{name:'Result',exact:true}).click()
  assert.match(await page.getByRole('tabpanel').last().textContent(),/Created status.json with status INITIALIZED/)
  await page.getByLabel('Inspect task').selectOption('validation-task')
  await page.getByText(/No user message was captured/).waitFor()
  await page.getByRole('tab',{name:'Result',exact:true}).click()
  await page.getByText('Validation passed.',{exact:true}).waitFor()
  await page.getByLabel('Inspect task').selectOption('step-run')
  await page.getByText('Step completed and validation passed.',{exact:true}).waitFor()
  assert.equal(await page.getByRole('tablist',{name:'Chat and step inspections'}).getByRole('tab').count(),2,'task navigation creates no extra chat tabs')
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),true,`${name}: no page overflow`)
  await page.getByRole('tab',{name:'Chat',exact:true}).click()
  assert.equal(await page.getByLabel('Chat draft').inputValue(),'Keep this draft')
  assert.deepEqual(errors,[],`${name}: no component errors`)
  await page.close()
 }
 console.log('Step inspection Chromium checks passed: desktop, phone and light mode; exact task navigation, four record sections and retained chat draft.')
} finally {await browser?.close(); await server.close()}
