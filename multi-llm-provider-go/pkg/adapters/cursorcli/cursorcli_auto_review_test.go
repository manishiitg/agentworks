package cursorcli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The chat's Auto-review mode reaches the Cursor SDK as local.autoReview; any other chat starts without it.
func TestCursorSDKBridgePassesAutoReview(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	for _, tc := range []struct {
		start string
		want  string
	}{
		{`{"type":"start","model":{"id":"composer-2.5"},"auto_review":true}`, "review=true"},
		{`{"type":"start","model":{"id":"composer-2.5"}}`, "review=undefined"},
	} {
		dir := t.TempDir()
		bridge, entry := filepath.Join(dir, "bridge.cjs"), filepath.Join(dir, "sdk.cjs")
		sdk := `
exports.Cursor={auth:{status:async()=>({status:'logged-in'})},models:{list:async()=>[{id:'composer-2.5',variants:[{isDefault:true,params:[]}]}]}};
exports.Agent={create:async(options)=>{process.stderr.write('review='+options.local.autoReview+'\n');
  return {agentId:'agent-new',send:async()=>({async *stream(){},wait:async()=>({status:'finished',result:'ok'})})};}};`
		for name, data := range map[string][]byte{bridge: cursorSDKBridge, entry: []byte(sdk)} {
			if err := os.WriteFile(name, data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, "node", bridge, entry)
		cmd.Stdin = strings.NewReader(tc.start + "\n" + `{"type":"prompt","text":"hi"}` + "\n" + `{"type":"close"}` + "\n")
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil || !strings.Contains(string(out), tc.want) {
			t.Fatalf("start %s: want %s, got %v %s", tc.start, tc.want, err, out)
		}
	}
}
