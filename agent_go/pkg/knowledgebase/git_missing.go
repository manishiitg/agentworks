package knowledgebase

import (
	"context"
	"log"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

var gitCheck struct {
	once sync.Once
	err  *Error
}

// gitMissing is the error to show when this machine cannot run git, checked once per process; nil when git works.
// On a Mac without the Command Line Tools /usr/bin/git exists but only offers to install them, so git is run rather
// than looked up. The cause is logged once. Installing git needs an app restart, which the message says.
func gitMissing() *Error {
	gitCheck.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "git", "--version").CombinedOutput()
		if err == nil || ctx.Err() != nil {
			return
		}
		log.Printf("[BRAIN] git cannot run, so Brain keeps no history or backup: %v: %s", err, strings.TrimSpace(string(out)))
		message := "Brain needs Git, which isn't installed on this server. Ask your administrator to install Git, then restart AgentWorks."
		if runtime.GOOS == "darwin" {
			message = "Brain needs Git, which isn't installed on this Mac. Open Terminal and run: xcode-select --install, then reopen AgentWorks."
		}
		gitCheck.err = kbErr("GIT_MISSING", message)
	})
	return gitCheck.err
}

// GitMissingMessage is what Brain shows when this machine cannot run git; empty when git works.
func GitMissingMessage() string {
	if missing := gitMissing(); missing != nil {
		return missing.Message
	}
	return ""
}

// orGitMissing returns the git-missing error in place of err when git cannot run, so the person sees what to do.
func orGitMissing(err error) error {
	if missing := gitMissing(); missing != nil {
		return missing
	}
	return err
}
