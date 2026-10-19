// The real shared panels in Chromium with account/API responses. This checks
// interaction/layout, not live Google consent or deployed mail delivery.
import assert from 'node:assert/strict'
import {mkdir} from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import {createServer} from 'vite'
import {chromium} from 'playwright'
const output=process.env.BROWSER_CHECK_ARTIFACT_DIR||path.join(os.tmpdir(),'gmail-inbox-browser')
const server=await createServer({server:{host:'127.0.0.1',port:0}})
let browser
try{
 await mkdir(output,{recursive:true});await server.listen();browser=await chromium.launch({headless:true})
 for(const [name,width,theme] of [['desktop',1100,'dark'],['mobile',375,'light']]){
  const page=await browser.newPage({viewport:{width,height:1000}});const errors=[];const actions=[]
  page.on('pageerror',e=>errors.push(e.message))
  await page.addInitScript(()=>{window.__APP_RUNTIME_CONFIG__={apiBaseUrl:location.origin,workspaceApiBaseUrl:location.origin+'/api/wp',deploymentMode:'server'}})
  let approved=false
  const address='owner+agent-123456781234123412341234567890ab@example.com'
  const account={id:'saved',owner:'owner',label:'owner@example.com',mine:true,connected:true,can_use_inbox:true,google:{id:'saved',email:'owner@example.com',enabled:true,allow_read_access:true,auth:{scopes:['https://www.googleapis.com/auth/gmail.readonly']}}}
  await page.route('**/api/**',async route=>{
   const url=new URL(route.request().url());let data={}
   if(url.pathname.endsWith('/integrations/google/accounts')){
    if(route.request().method()==='POST')actions.push(route.request().postDataJSON().action)
    data={accounts:[account],can_connect:false,incoming_email:{inbox:{id:'inbox',address,connection_id:'saved'},receiving_account:'owner@example.com',bindings:[]}}
   }else if(url.pathname.endsWith('/integrations/slack/accounts'))data={accounts:[],can_connect:true,sign_in_ready:false}
   else if(url.pathname.endsWith('/admin/mcp-apps'))data={apps:[{key:'google',label:'Google',configured:true,servers:[]},{key:'slack',label:'Slack',configured:false,servers:[]}],redirect_uri:'https://app.example.com/api/oauth/callback'}
   else if(url.pathname.endsWith('/gmail-inbound/sender-consent')){approved=true;actions.push('approve');data={approved:true}}
   else if(url.pathname.endsWith('/gmail-inbound/route'))data={configured:true,watch_ready:true,inbox:{id:'inbox',address,connection_id:'saved'},receiving_account:'owner@example.com',sender_consent:{required:true,approved,config_hash:'a'.repeat(64),senders:[]},route:{id:'binding',address,connection_id:'saved',enabled:true,reply:false,workflow_trigger:true,route_selections:{}},deliveries:[]}
   else if(url.pathname.includes('/google-apps'))data={can_connect:false,oauth_clients:[],connections:[]}
   return route.fulfill({contentType:'application/json',body:JSON.stringify(data)})
  })
  await page.goto(server.resolvedUrls.local[0]+`scripts/test-fixtures/gmail-inbox-qa.html?theme=${theme}`)
  await page.getByRole('heading',{name:'Your incoming email',exact:true}).waitFor()
  assert.match(await page.getByLabel('Your incoming email').textContent(),/Anyone who emails this address/)
  await page.getByRole('button',{name:'Manage',exact:true}).click()
  await page.getByRole('button',{name:/Slack.*Not set up/}).click()
  await page.getByLabel('Slack client ID',{exact:true}).waitFor()
  assert.equal(await page.getByRole('button',{name:'Save app',exact:true}).isDisabled(),true)
  assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true)
  await page.screenshot({path:path.join(output,`gmail-inbox-${name}.png`),fullPage:true})
  await page.getByRole('button',{name:'New address',exact:true}).click()
  assert.deepEqual(actions,['rotate_inbox'])
  await page.getByRole('button',{name:'Open product',exact:true}).click()
  await page.getByRole('tab',{name:'Incoming email',exact:true}).waitFor()
  const approve=page.getByRole('button',{name:'Approve incoming email',exact:true})
  assert.equal(await approve.isDisabled(),true)
  await page.getByRole('checkbox').check();await approve.click()
  await page.getByText('Incoming email approved',{exact:true}).waitFor()
  assert.deepEqual(actions,['rotate_inbox','approve'])
  await page.screenshot({path:path.join(output,`gmail-approval-${name}.png`),fullPage:true})
  assert.deepEqual(errors,[])
  await page.close()
 }
 console.log('Gmail inbox browser checks passed: My Vault, Slack admin setup, explicit rotation and per-binding approval on desktop/mobile.')
}finally{await browser?.close();await server.close()}
