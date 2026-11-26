package cursorcli

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// PR190/4574737737: a text correction overtook its preceding image steer.
// Serialize only native intake, not the SDK outcome (which waits for run end).
func TestCursorSDKImageSteersKeepReceiptOrderWithoutWaitingForOutcomes(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	script := `const fs=require('fs'),vm=require('vm'),assert=require('assert');const src=fs.readFileSync('cursor_sdk_bridge.cjs','utf8');const code=src.slice(src.indexOf('const control = request =>'),src.indexOf("  lines.on('line'"))+';control';const tick=()=>new Promise(r=>setImmediate(r));(async()=>{let releaseFrame,releaseOutcome;const outcome=new Promise(r=>releaseOutcome=r),calls=[],events=[],prepared=[];const state={busy:true,run:{steer:message=>(calls.push(message.text||message),outcome)},steers:[],followups:[],early:[],steerIntakes:Promise.resolve(),emit:e=>events.push(e),settle:()=>{},imageFrames:{message:input=>{prepared.push(input.text);return input.image_id?new Promise(r=>releaseFrame=r):Promise.resolve(input.text)}}};const control=vm.runInNewContext(code,state);control({type:'steer',id:'first',image_id:'image',text:'image first'});control({type:'steer',id:'second',text:'correction second'});assert.equal(state.steers.length,2);await tick();assert.deepEqual(prepared,['image first']);assert.equal(calls.length,0);releaseFrame({text:'image first',images:[{data:'pixels'}]});await tick();await tick();assert.deepEqual(calls,['image first','correction second']);assert.equal(events.filter(e=>e.type==='image_order'&&e.action==='submitted').length,2);releaseOutcome('complete_delivered');await Promise.all(state.steers.map(e=>e.done));let cancelledFrame;state.imageFrames.message=()=>new Promise(r=>cancelledFrame=r);control({type:'steer',id:'cancelled',image_id:'later',text:'must not send'});await tick();state.steers.at(-1).settled=true;cancelledFrame({text:'must not send'});await tick();await tick();assert.equal(calls.length,2);assert.ok(events.some(e=>e.type==='image_order'&&e.action==='skipped'));})().catch(e=>{console.error(e);process.exitCode=1});`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("native steering order: %v %s", err, out)
	}
}
