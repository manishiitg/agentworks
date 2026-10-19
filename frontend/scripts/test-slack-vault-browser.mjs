// Real shared panels; API fixtures never contact Slack or change a live account.
import assert from 'node:assert/strict'
import {mkdir,writeFile} from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import {createServer} from 'vite'
import {chromium} from 'playwright'
const output=process.env.BROWSER_CHECK_ARTIFACT_DIR||path.join(os.tmpdir(),'slack-vault-browser')
const server=await createServer({server:{host:'127.0.0.1',port:0}})
let browser
try{
 await mkdir(output,{recursive:true});await server.listen();browser=await chromium.launch({headless:true})
 for(const [name,width,theme] of [['desktop',1100,'dark'],['mobile',390,'light']]){
  const page=await browser.newPage({viewport:{width,height:1000}});const errors=[];const writes=[];const reads=[];const picked={};let attached=false
  page.on('pageerror',error=>errors.push(error.message))
  await page.addInitScript(()=>{window.__APP_RUNTIME_CONFIG__={apiBaseUrl:location.origin,workspaceApiBaseUrl:location.origin+'/api/wp',deploymentMode:'server'}})
  const bot={id:'saved-bot',name:'Team helper',configured:true,enabled:true,can_manage:true,scope_label:'Goal · Inbox',shared:false}
  const state=workspace=>({bots:[bot],scopes:[{workspace_path:'Workflow/goal',label:'Goal · Inbox'}],selected_id:picked[workspace]||'',allow_bots:true,can_select:true,target:{workspace_path:workspace},settings:{slug:'helper',platform_available:false,platform_bot:false,can_manage:true,dm_only:true,channels:[]}})
  await page.route('**/api/**',async route=>{
   const url=new URL(route.request().url());if(!url.pathname.startsWith('/api/'))return route.continue()
   let data={}
   if(url.pathname.endsWith('/integrations/slack/accounts')){
    if(route.request().method()==='POST'){const body=route.request().postDataJSON();writes.push(body);attached=true}
    data={accounts:[{id:'personal',owner:'owner',label:'Owner Slack',connected:true,mine:true,selected:attached,can_add:!attached,can_detach:attached}],can_connect:false}
   }else if(url.pathname.endsWith('/integrations/slack/bots')){
    if(route.request().method()==='POST'){const body=route.request().postDataJSON();writes.push(body);if(body.action==='select')picked[body.workspace_path]=body.connection_id;data=state(body.workspace_path||'')}
    else{const workspace=url.searchParams.get('workspace_path')||'';reads.push(workspace);data=state(workspace)}
   }
   return route.fulfill({contentType:'application/json',body:JSON.stringify(data)})
  })
  try{
   await page.goto(server.resolvedUrls.local[0]+`scripts/test-fixtures/slack-vault-qa.html?theme=${theme}`)
   await page.getByRole('heading',{name:'Act in Slack',exact:true}).waitFor()
   await page.getByRole('heading',{name:'Talk to this Crew',exact:true}).waitFor()
   assert.equal(await page.getByRole('navigation',{name:'Connections',exact:true}).getByText('Slack',{exact:true}).count(),0)
   assert.equal(await page.getByRole('navigation',{name:'Use and share',exact:true}).getByText('Slack',{exact:true}).count(),1)
   await page.getByRole('button',{name:'Use here',exact:true}).click()
   await page.getByRole('button',{name:'Remove here',exact:true}).waitFor()
   await page.getByLabel('Slack bot from My Vault').selectOption('saved-bot')
   await page.getByText('Answers in DMs with @Team helper.',{exact:true}).waitFor()
   assert.equal(writes.filter(body=>body.action==='select').length,1)
   assert.equal(writes.some(body=>body.bot_token||body.app_token),false)
   assert.equal(await page.locator('input[type="password"]').count(),0)
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true)
   await page.screenshot({path:path.join(output,`slack-product-${name}.png`),fullPage:true})
   await page.getByRole('button',{name:'Goal',exact:true}).click()
   await page.getByRole('heading',{name:'Talk to this Goal',exact:true}).waitFor()
   await page.getByRole('button',{name:'Flow',exact:true}).click()
   await page.getByRole('heading',{name:'Act in Slack',exact:true}).waitFor()
   assert.equal(await page.getByText('Talk to this Flow',{exact:true}).count(),0)
   assert.equal(reads.includes('Workflow/flow'),false)
   await page.getByRole('button',{name:'My Vault',exact:true}).click()
   await page.getByRole('button',{name:'Change bot',exact:true}).click()
   assert.equal(await page.getByLabel('Bot token',{exact:true}).inputValue(),'')
   assert.equal(await page.getByLabel('App token',{exact:true}).inputValue(),'')
   await page.getByLabel('Bot token',{exact:true}).fill('xoxb-browser-fixture')
   await page.getByRole('button',{name:'Save bot',exact:true}).click()
   await page.getByText('Bot saved. Choose it on each Crew or Goal’s Slack page.',{exact:true}).waitFor()
   assert.equal(writes.at(-1).action,'save');assert.equal(writes.at(-1).connection_id,'saved-bot')
   assert.equal(await page.locator('input[type="password"]').count(),0)
   assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true)
   await page.screenshot({path:path.join(output,`slack-vault-${name}.png`),fullPage:true})
   assert.deepEqual(errors,[])
  }catch(error){console.error(name,errors);await writeFile(path.join(output,`slack-failure-${name}.html`),await page.content());await page.screenshot({path:path.join(output,`slack-failure-${name}.png`),fullPage:true});throw error}
  await page.close()
 }
 console.log('Slack browser checks passed: Crew/Goal single page, Flow Act only, one menu entry, saved reference selection and write-only Vault credentials on desktop/mobile.')
}finally{await browser?.close();await server.close()}
