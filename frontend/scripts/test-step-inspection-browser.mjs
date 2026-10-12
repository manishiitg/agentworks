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
  await page.getByTestId('terminal-clear-view').waitFor()
  assert.equal(await page.getByLabel('Inspect task').inputValue(),'work-task','empty parent automatically opens work task')
  assert.equal(await page.getByRole('tablist',{name:'Chat and step inspections'}).getByRole('tab').count(),2)
  await page.getByText('Create status.json with status INITIALIZED and report the result.',{exact:true}).waitFor()
  await page.screenshot({path:path.join(output,`step-inspection-${name}-user.png`),fullPage:true})
  await page.getByTestId('inspection-system-prompt').locator('summary').click()
  await page.getByText('Write only inside the assigned output folder. Verify the requested JSON file before finishing.',{exact:true}).waitFor()
  await page.getByTestId('terminal-clear-tool-batch-toggle').click()
  await page.getByTestId('terminal-clear-tool-call').getByRole('button').first().click()
  await page.getByText('status.json saved.',{exact:true}).waitFor()
  await page.screenshot({path:path.join(output,`step-inspection-${name}-tools.png`),fullPage:true})
  await page.getByText('Created status.json with status INITIALIZED.',{exact:true}).waitFor()
  await page.getByLabel('Inspect task').selectOption('validation-task')
  await page.getByText('Validation passed.',{exact:true}).waitFor()
  await page.getByLabel('Inspect task').selectOption('step-run')
  await page.getByText('Step completed and validation passed.',{exact:true}).waitFor()
  assert.equal(await page.getByRole('tablist',{name:'Chat and step inspections'}).getByRole('tab').count(),2,'task navigation creates no extra chat tabs')
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),true,`${name}: no page overflow`)
  await page.getByRole('tab',{name:'Chat',exact:true}).click()
  assert.equal(await page.getByLabel('Chat draft').inputValue(),'Keep this draft')
  assert.deepEqual(errors,[],`${name}: no component errors`)
  await page.goto(server.resolvedUrls.local[0]+`scripts/test-fixtures/step-inspection-qa.html?theme=${theme}&mode=script`)
  await page.getByText('Final output',{exact:true}).waitFor()
  assert.equal(await page.getByTestId('inspection-execution-type').first().textContent(),'Script')
  await page.getByText('Full script',{exact:false}).click()
  await page.getByText(/import json/).waitFor()
  await page.getByText('Created status.json and validation passed.',{exact:true}).waitFor()
  assert.equal(await page.getByTestId('inspection-system-prompt').count(),0,'scripts do not show a fabricated agent prompt')
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth),true,`${name}: script does not overflow`)
  await page.screenshot({path:path.join(output,`step-inspection-${name}-script.png`),fullPage:true})
  assert.deepEqual(errors,[],`${name}: no script component errors`)
  for (const parent of ['query','steer']) {
   await page.goto(server.resolvedUrls.local[0]+`scripts/test-fixtures/step-inspection-qa.html?theme=${theme}&mode=launch&parent=${parent}`)
   const inspect = page.getByRole('button',{name:'Inspect Write a status file',exact:true})
   await inspect.waitFor()
   await inspect.click()
   await page.getByText('Create status.json with status INITIALIZED and report the result.',{exact:true}).waitFor()
   assert.equal(await page.getByLabel('Inspect task').inputValue(),'work-task',`${name}: ${parent} native lifecycle opens the exact step`)
   assert.deepEqual(errors,[],`${name}: no native launch component errors`)
  }
  await page.screenshot({path:path.join(output,`step-inspection-${name}-native-launch.png`),fullPage:true})
  await page.close()
 }
 console.log('Step inspection Chromium checks passed: desktop, phone and light mode; native query/steer launch inspection, exact task navigation, chat-style records and script source/output and retained chat draft.')
} finally {await browser?.close(); await server.close()}
