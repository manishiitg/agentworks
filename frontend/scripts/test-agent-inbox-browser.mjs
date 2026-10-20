// Render the actual shared Inbox/admin panels; fixtures cannot start a run or contact Google.
import assert from 'node:assert/strict'
import {mkdir} from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import {createServer} from 'vite'
import {chromium} from 'playwright'
const output=process.env.BROWSER_CHECK_ARTIFACT_DIR||path.join(os.tmpdir(),'agent-inbox-browser')
const server=await createServer({server:{host:'127.0.0.1',port:0}})
let browser
try{
 await mkdir(output,{recursive:true});await server.listen();browser=await chromium.launch({headless:true})
 for(const [name,width,theme] of [['desktop',1100,'dark'],['mobile',390,'light']]){
  const page=await browser.newPage({viewport:{width,height:1000}});const errors=[];const writes=[];const reads=[]
  page.on('pageerror',error=>errors.push(error.message))
  await page.addInitScript(()=>{window.__APP_RUNTIME_CONFIG__={apiBaseUrl:location.origin,workspaceApiBaseUrl:location.origin+'/api/wp',deploymentMode:'server'}})
  const mail=(id,sequence,body)=>({id,sequence,thread_id:'invoice-thread',from:'billing@example.com',subject:'Invoice for the October workshop',body,received_at:1792476000000+sequence,recipients:['company+agent-stable@example.com'],labels:['INBOX'],authenticated:true,attachments:sequence===1?[{id:'pdf',name:'invoice.pdf',size:1200}]:[]})
  await page.route('**/api/**',async route=>{
   const request=route.request();const url=new URL(request.url());reads.push(url.pathname);if(request.method()!=='GET')writes.push(url.pathname)
   const data=url.pathname.endsWith('/inbox')?{inbox:{id:'stable',address:'company+agent-stable@example.com'},receiving:true,mail:{messages:[mail('two',2,'Please find the revised invoice attached to the earlier message.'),mail('one',1,'Untrusted email text: <script>window.inboxInjected=true</script>')],next_cursor:1,has_more:false}}:{enabled:true,connection_id:'company',infrastructure_ready:true,receiving:true,mailboxes:[{id:'company',email:'company@example.com',client_name:'platform'}],provisioning:{oauth_clients:[{name:'platform',project_id:'company-project'}]}}
   return route.fulfill({contentType:'application/json',body:JSON.stringify(data)})
  })
  await page.goto(server.resolvedUrls.local[0]+`scripts/test-fixtures/agent-inbox-qa.html?theme=${theme}`)
  await page.getByRole('heading',{name:'Inbox',exact:true}).waitFor()
  await page.getByRole('button',{name:/Invoice for the October workshop/}).click()
  await page.getByText('invoice.pdf',{exact:true}).waitFor()
  assert.equal(await page.locator('article').count(),2)
  assert.equal(await page.evaluate(()=>window.inboxInjected),undefined)
  assert.equal(writes.length,0)
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true)
  await page.screenshot({path:path.join(output,`agent-inbox-${name}.png`),fullPage:true})
  await page.getByText('Company agent inboxes (admin)',{exact:false}).click()
  await page.getByRole('button',{name:'Disable agent inboxes',exact:true}).waitFor()
  assert.equal(await page.locator('input[type="password"]').count(),0)
  await page.screenshot({path:path.join(output,`agent-inbox-admin-${name}.png`),fullPage:true})
  reads.length=0
  await page.goto(server.resolvedUrls.local[0]+`scripts/test-fixtures/agent-inbox-qa.html?local=true&theme=${theme}`)
  await page.waitForLoadState('networkidle')
  assert.equal(await page.getByRole('heading',{name:'Inbox',exact:true}).count(),0)
  assert.equal(reads.some(value=>value.startsWith('/api/agent-email')),false)
  assert.deepEqual(errors,[])
  await page.close()
 }
 console.log('PASS: threaded Inbox, text-only email, attachment link, admin shared setup, desktop/mobile and local absence; no writes or live provider requests')
}finally{if(browser)await browser.close();await server.close()}
