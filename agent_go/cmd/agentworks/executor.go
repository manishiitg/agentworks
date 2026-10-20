package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

// executorParams is one local connection: the folders to share and how they are guarded.
type executorParams struct {
	deviceID, stateDir string
	connected          func()
	waiting            func(reason string)
	trace              func(request localfiles.Request, response localfiles.Response, took time.Duration)
}

func runExecutor(ctx context.Context, o *options, p executorParams) error {
	if !localfiles.ValidID(p.deviceID) {
		return errors.New("--device requires a stable identifier (letters, numbers, underscores or hyphens)")
	}
	config, err := o.path()
	if err != nil {
		return err
	}
	stateDir := p.stateDir
	if stateDir == "" {
		stateDir = filepath.Join(filepath.Dir(config), "executor-state", p.deviceID)
	}
	_ = configureLocalShellSandbox()
	executor, err := localfiles.OpenSettings(p.deviceID, stateDir, []string{config})
	if err != nil {
		return err
	}
	defer executor.Close()
	executor.Hello.CLIVersion = cliVersion
	executor.Hello.CLIBuild = cliBuild
	client, err := o.client()
	if err != nil {
		return err
	}
	if p.trace != nil {
		client.WithExecutorTrace(p.trace)
	}
	delay := time.Second
	for {
		err = client.ServeExecutor(ctx, executor, func() {
			delay = time.Second
			fmt.Fprintf(o.stderr, "Computer %s connected. Choose folders in Crew Settings → Laptop access.\n", p.deviceID)
			if p.connected != nil {
				p.connected()
			}
		})
		if ctx.Err() != nil {
			return nil
		}
		var apiErr *agentworksclient.APIError
		if errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403 || apiErr.Status == 409 && apiErr.Code != "device_busy") {
			return err
		}
		if p.waiting != nil && errors.As(err, &apiErr) && apiErr.Code == "device_busy" {
			p.waiting("This computer already has a live connection. Run agentworks stop, then start again.")
		}
		// Exponential backoff (1s, 2s, 4s ... 30s) plus up to a quarter of the delay at random, so many computers that lost the
		// server at the same moment do not all retry in the same second.
		wait := delay + time.Duration(rand.Int64N(int64(delay/4)+1))
		fmt.Fprintf(o.stderr, "Executor disconnected; reconnecting in %s.\n", wait.Round(100*time.Millisecond))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}
