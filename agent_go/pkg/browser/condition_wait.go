package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"
)

// PLAT-746-308: daemon waits survive an IPC caller's cancellation and block the
// next command. Keep condition polling in the platform; the daemon only receives
// one immediate evaluation at a time, using the unchanged session/CDP/pin flags.
func (c *Client) executeConditionWait(ctx context.Context, args []string, opts *ExecuteOptions) (string, bool, error) {
	command := 0
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "--session" {
			command = i + 2
			break
		}
	}
	if command >= len(args) || args[command] != "wait" {
		return "", false, nil
	}
	fn := -1
	for i := command + 1; i < len(args); i++ {
		if args[i] == "--fn" {
			fn = i
			break
		}
	}
	if fn < 0 {
		return "", false, nil
	}
	if fn+1 >= len(args) {
		return "", true, fmt.Errorf("wait --fn requires a condition")
	}
	duration := 25 * time.Second
	eval := append([]string(nil), args[:command]...)
	eval = append(eval, "eval", conditionWaitExpression(args[fn+1]))
	for i := command + 1; i < len(args); i++ {
		if i == fn {
			i++
			continue
		}
		if args[i] == "--timeout" {
			if i+1 >= len(args) {
				return "", true, fmt.Errorf("wait --timeout requires milliseconds")
			}
			ms, err := strconv.ParseInt(args[i+1], 10, 64)
			if err != nil || ms <= 0 || ms > int64((1<<63-1)/int64(time.Millisecond)) {
				return "", true, fmt.Errorf("wait --timeout must be positive milliseconds")
			}
			duration = time.Duration(ms) * time.Millisecond
			i++
			continue
		}
		eval = append(eval, args[i])
	}
	if opts != nil && opts.Timeout > 0 && opts.Timeout < duration {
		duration = opts.Timeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	started := time.Now()
	// No page expressions, URLs or private relay capabilities belong in this log.
	defer func() {
		if waitCtx.Err() != nil {
			log.Printf("[BROWSER_WAIT] id=%d cancelled=true elapsed_ms=%d reason=%v", started.UnixNano(), time.Since(started).Milliseconds(), waitCtx.Err())
		}
	}()
	var lastError error
	ended := func(err error) (string, bool, error) {
		if lastError != nil {
			err = fmt.Errorf("browser readiness wait ended: %w (last check: %v)", err, lastError)
		}
		return "", true, err
	}
	for {
		if err := waitCtx.Err(); err != nil {
			return ended(err)
		}
		output, err := c.executeCommandOnce(waitCtx, eval, opts)
		if err != nil {
			lastError = err
		} else {
			var reply struct {
				Success bool `json:"success"`
				Data    struct {
					Result *bool `json:"result"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(output), &reply); err != nil || !reply.Success || reply.Data.Result == nil {
				lastError = fmt.Errorf("browser condition could not be evaluated while the page is changing")
			} else {
				lastError = nil
				if *reply.Data.Result {
					return `{"success":true,"data":{"waited":true}}`, true, nil
				}
			}
		}
		timer := time.NewTimer(300 * time.Millisecond)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return ended(waitCtx.Err())
		case <-timer.C:
		}
	}
}

func conditionWaitExpression(expression string) string {
	return "(async () => { let timer; try { return await Promise.race([(async () => { const v = (" + expression + "); const r = typeof v === 'function' ? await v() : await v; return !!r })(), new Promise(resolve => { timer = setTimeout(() => resolve(false), 2000) })]); } finally { clearTimeout(timer); } })()"
}
