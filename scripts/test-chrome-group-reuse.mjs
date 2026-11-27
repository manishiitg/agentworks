// #2825 / #4464: exercise grouping itself, not another Chrome process or profile.
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
const source=readFileSync(new URL('../extensions/agentworks-chrome/background.js',import.meta.url),'utf8');
const grouping=source.slice(source.indexOf('async function groupSharedTab('),source.indexOf('\nasync function share(',source.indexOf('async function groupSharedTab(')));
let existing=[{id:42,title:'Project display name'}], calls=[], logs=[], failGroupId=null;
const socket={};
const context=vm.createContext({socket,workspace:'Crew/id-not-a-title',projectName:'Project display name',lastMethod:'',groups:new Map(),shared:new Map([[7,{}],[8,{}]]),displayName:value=>value.trim(),diagnostic:(...args)=>logs.push(args),chrome:{tabs:{get:async id=>({id,windowId:3}),group:async args=>{calls.push(args);if(failGroupId!==null&&args.groupId===failGroupId)throw new Error('No group');const id=args.groupId??91;if(!existing.some(g=>g.id===id))existing.push({id,title:''});return id},ungroup:async()=>{}},tabGroups:{query:async args=>{assert.deepEqual(JSON.parse(JSON.stringify(args)),{windowId:3,title:'Project display name'});return existing.filter(g=>g.title===args.title)},update:async(id,args)=>{assert.equal(args.title,'Project display name');const g=existing.find(entry=>entry.id===id);if(g)g.title=args.title}}}});
vm.runInContext(grouping,context);
await context.groupSharedTab(7);
assert.equal(calls[0].groupId,42);assert.equal(logs.at(-1)[6].outcome,'reused');
// Concurrent tabs in the same window must serialize so only one group is created.
context.groups.clear(); existing=[]; calls=[];
await Promise.all([context.groupSharedTab(7), context.groupSharedTab(8)]);
assert.equal(calls[0].createProperties.windowId,3);assert.deepEqual(JSON.parse(JSON.stringify(calls[0].tabIds)),[7]);
assert.equal(calls[1].groupId,91);assert.deepEqual(JSON.parse(JSON.stringify(calls[1].tabIds)),[8]);
assert.equal(logs.at(-1)[6].outcome,'reused');
// Stale cached group ID must re-query existing named groups before creating a duplicate.
context.groups.set(3, 55); failGroupId=55; existing=[{id:77,title:'Project display name'}]; calls=[];
await context.groupSharedTab(7);
assert.equal(calls[0].groupId,55);assert.equal(calls[1].groupId,77);assert.equal(logs.at(-1)[6].outcome,'reused');
failGroupId=null;
context.projectName='';
await context.groupSharedTab(7); assert.equal(logs.at(-1)[6].outcome,'waiting_for_name');
console.log('Chrome group reuse regression passed');
