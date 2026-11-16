// #2825: exercise grouping itself, not another Chrome process or profile.
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
const source=readFileSync(new URL('../extensions/agentworks-chrome/background.js',import.meta.url),'utf8');
const grouping=source.slice(source.indexOf('async function groupSharedTab('),source.indexOf('\nasync function share(',source.indexOf('async function groupSharedTab(')));
let existing=[{id:42,title:'Project display name'}], calls=[], logs=[];
const socket={};
const context=vm.createContext({socket,workspace:'Crew/id-not-a-title',projectName:'Project display name',groups:new Map(),shared:new Map([[7,{}]]),displayName:value=>value.trim(),diagnostic:(...args)=>logs.push(args),chrome:{tabs:{get:async()=>({id:7,windowId:3}),group:async args=>{calls.push(args);return args.groupId??91},ungroup:async()=>{}},tabGroups:{query:async args=>{assert.deepEqual(JSON.parse(JSON.stringify(args)),{windowId:3,title:'Project display name'});return existing},update:async(id,args)=>{assert.equal(args.title,'Project display name')}}}});
vm.runInContext(grouping,context);
await context.groupSharedTab(7);
assert.equal(calls[0].groupId,42);assert.equal(logs.at(-1)[2],'reused');
context.groups.clear(); existing=[]; calls=[];
await context.groupSharedTab(7);
assert.equal(calls[0].createProperties.windowId,3);assert.equal(logs.at(-1)[2],'created');
assert.deepEqual(JSON.parse(JSON.stringify(calls[0].tabIds)),[7]);
context.projectName='';
await assert.rejects(context.groupSharedTab(7),/display name/);
console.log('Chrome group reuse regression passed');
