package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/voicestt"
)

func main() {
	log.SetOutput(os.Stderr)

	var (
		modelDir  string
		stdinAuth bool
		showHelp  bool
	)
	fs := flag.NewFlagSet("voice-helper", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&modelDir, "model-dir", voicestt.DefaultModelDir(), "directory holding speech model files")
	fs.BoolVar(&stdinAuth, "stdin-auth", false, "read bearer token from first line of stdin and exit on stdin EOF")
	fs.BoolVar(&showHelp, "help", false, "show usage and exit")
	fs.BoolVar(&showHelp, "h", false, "show usage and exit")

	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(2)
	}
	if showHelp {
		fmt.Fprintf(os.Stderr, "Usage: voice-helper [--model-dir DIR] [--stdin-auth]\n")
		os.Exit(0)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var token string
	if stdinAuth {
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			log.Fatalf("[VOICE] failed to read auth token from stdin: %v", err)
		}
		token = strings.TrimSpace(line)
		if token == "" {
			log.Fatalf("[VOICE] empty auth token on stdin")
		}
		// Exit as soon as the parent closes stdin (including on parent crash).
		go func() {
			_, _ = io.Copy(io.Discard, reader)
			cancel()
		}()
	}

	mgr := voicestt.NewManager(modelDir)
	if err := voicestt.RunHelperServer(ctx, mgr, token, os.Stdout); err != nil {
		log.Fatalf("[VOICE] helper server failed: %v", err)
	}
}
