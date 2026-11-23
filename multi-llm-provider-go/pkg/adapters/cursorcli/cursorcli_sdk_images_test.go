package cursorcli

import (
	"context"
	"errors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// PLAT-746-1000: real shipped bridge, with only the SDK replaced. A rejected
// image steer is prepared again from current access, never a queued byte copy.
func TestCursorSDKImagesPromptSteerAndFollowup(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	for _, tc := range []struct {
		name, steer string
		revoke      bool
		opens       int32
	}{{"accepted", "STEER", false, 2}, {"reverted", "QUEUED", false, 3}, {"revoked", "QUEUED", true, 3}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			entry := filepath.Join(dir, "sdk.cjs")
			unpack := `let intakes=0;function unpack(value){intakes++;if(typeof value==='string'){if(!REVOKE||intakes!==3)throw Error('native image missing');return value;}if(value.images.length!==1||value.images[0].mimeType!=='image/png'||Buffer.from(value.images[0].data,'base64').toString()!=='pixels')throw Error('native image changed');return value.text;}`
			unpack = strings.ReplaceAll(unpack, "REVOKE", map[bool]string{true: "true", false: "false"}[tc.revoke])
			fake := strings.Replace(fakeCursorSDK, "function agent(id)", unpack+"\nfunction agent(id)", 1)
			fake = strings.Replace(fake, "async send(text, opts) {", "async send(text, opts) { text=unpack(text);", 1)
			fake = strings.Replace(fake, "async steer(t) {", "async steer(t) { t=unpack(t);", 1)
			if err := os.WriteFile(entry, []byte(fake), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CURSOR_SDK_PATH", entry)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			owner := "cursor-images-" + tc.name
			var opened, skipped atomic.Int32
			factory := llmtypes.NativeImageInput{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
				n := opened.Add(1)
				if tc.revoke && n == 3 {
					return nil, "", 0, errors.New("access revoked")
				}
				return io.NopCloser(strings.NewReader("pixels")), "image/png", 6, nil
			}, Skipped: func(string) { skipped.Add(1) }}
			withImage := func() context.Context {
				return llmtypes.WithNativeImageInputs(ctx, owner, []llmtypes.NativeImageInput{factory})
			}
			stream := make(chan llmtypes.StreamChunk, 256)
			type result struct {
				resp *llmtypes.ContentResponse
				err  error
			}
			done := make(chan result, 1)
			go func() {
				resp, err := NewCursorCLIAdapter("", "composer-2.5", nil).GenerateContent(withImage(), []llmtypes.MessageContent{llmtypes.TextParts(llmtypes.ChatMessageTypeHuman, "hello world")}, WithWorkingDir(dir), WithInteractiveSessionID(owner), llmtypes.WithStreamingChan(stream))
				done <- result{resp, err}
			}()
			steered := false
			for chunk := range stream {
				if chunk.Type == llmtypes.StreamChunkTypeToolCallStart && !steered {
					steered = true
					if err := SendCursorSDKInput(withImage(), owner, tc.steer); err != nil {
						t.Fatal(err)
					}
				}
			}
			got := <-done
			if got.err != nil {
				t.Fatal(got.err)
			}
			if !steered || got.resp == nil || !strings.Contains(got.resp.Choices[0].Content, tc.steer[len(tc.steer)-5:]) {
				t.Fatal("text/image followup lost")
			}
			if opened.Load() != tc.opens || tc.revoke != (skipped.Load() > 0) {
				t.Fatalf("factory intake counts opened=%d skipped=%d", opened.Load(), skipped.Load())
			}
		})
	}
}

func TestCursorSDKImageFetchBoundedAndTextSurvives(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	script := `const fs=require('fs'),vm=require('vm'),assert=require('assert');const src=fs.readFileSync('cursor_sdk_bridge.cjs','utf8');const timers=[],events=[];const create=vm.runInNewContext(src.slice(src.indexOf('function createImageFrames('),src.indexOf('\nasync function main()'))+';createImageFrames',{setTimeout:fn=>(timers.push(fn),timers.length),clearTimeout:()=>{}});(async()=>{const idle={busy:false,early:[],followups:[],emit:e=>events.push(e)};const control=vm.runInNewContext(src.slice(src.indexOf('const control = request =>'),src.indexOf("  lines.on('line'"))+';control',idle);control({type:'steer',id:'late',image_id:'expired',text:'keep text'});assert.equal(events.at(-1).accepted,false);assert.equal(idle.early.length,0);const frames=create(e=>events.push(e));const input={image_id:'opaque',text:'text stays'};const first=frames.message(input);assert.equal(await frames.message(input),'text stays');assert.equal(events.filter(e=>e.type==='image_fetch').length,1);const request=events.find(e=>e.type==='image_fetch');const response={id:request.id,images:[{data:'cGl4ZWxz',mimeType:'image/png'}]};frames.accept(response);const message=await first;assert.equal(message.images[0].data,'cGl4ZWxz');assert.equal(response.images,undefined);const second=frames.message({image_id:'other',text:'still sends'});timers.at(-1)();assert.equal(await second,'still sends');assert.ok(events.some(e=>e.type==='image_notice'));assert.equal(input.image_id,'opaque');assert.equal(Object.keys(input).length,2);})().catch(e=>{console.error(e);process.exitCode=1});`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "node", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("bounded image fetch: %v %s", err, out)
	}
}
