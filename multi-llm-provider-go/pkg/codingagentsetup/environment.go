package codingagentsetup

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

const ServerName = "llm-provider"

type Agent struct {
	Provider    string
	DisplayName string
	CLI         string
	Host        bool
}

var supportedAgents = []Agent{
	{Provider: "cursor-cli", DisplayName: "Cursor Agent", CLI: "cursor-agent"},
	{Provider: "pi-cli", DisplayName: "Pi CLI", CLI: "pi"},
	{Provider: "codex-cli", DisplayName: "Codex CLI", CLI: "codex", Host: true},
	{Provider: "claude-code", DisplayName: "Claude Code", CLI: "claude", Host: true},
}

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type AttachedRunner func(context.Context, string, ...string) error

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type Environment struct {
	input       io.Reader
	reader      *bufio.Reader
	out         io.Writer
	errOut      io.Writer
	color       bool
	terminalUI  bool
	lookPath    func(string) (string, error)
	runner      CommandRunner
	runAttached AttachedRunner
	executable  func() (string, error)
	workingDir  func() (string, error)
	remove      func(string) error
	smoke       func(context.Context, string, []string, []string) error
}

func NewEnvironment(in io.Reader, out, errOut io.Writer) *Environment {
	environment := &Environment{
		input:      in,
		reader:     bufio.NewReader(in),
		out:        out,
		errOut:     errOut,
		color:      outputSupportsColor(out),
		terminalUI: terminalInteractiveUI(in, out),
		lookPath:   exec.LookPath,
		runner:     execRunner{},
		executable: os.Executable,
		workingDir: currentDirectory,
		remove:     os.Remove,
		smoke:      protocolSmokeTest,
	}
	environment.runAttached = func(ctx context.Context, name string, args ...string) error {
		command := exec.CommandContext(ctx, name, args...)
		command.Stdin = in
		command.Stdout = out
		command.Stderr = errOut
		return command.Run()
	}
	return environment
}

func (e *Environment) SetLookPath(fn func(string) (string, error)) {
	e.lookPath = fn
}

func (e *Environment) SetRunner(runner CommandRunner) {
	e.runner = runner
}

func (e *Environment) SetAttachedRunner(runner AttachedRunner) {
	e.runAttached = runner
}

func (e *Environment) SetExecutable(fn func() (string, error)) {
	e.executable = fn
}

func (e *Environment) SetWorkingDirectory(fn func() (string, error)) {
	e.workingDir = fn
}

func (e *Environment) SetRemove(fn func(string) error) {
	e.remove = fn
}

func (e *Environment) SetSmokeTest(fn func(context.Context, string, []string, []string) error) {
	e.smoke = fn
}

func SupportedAgents() []Agent {
	result := make([]Agent, len(supportedAgents))
	copy(result, supportedAgents)
	return result
}

func (e *Environment) installed(agent Agent) bool {
	_, err := e.lookPath(agent.CLI)
	return err == nil
}

func (e *Environment) prompt(label string) (string, error) {
	if _, err := fmt.Fprint(e.out, e.style("93;1", label)); err != nil {
		return "", err
	}
	value, err := e.reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func (e *Environment) warn(format string, args ...any) {
	label := e.style("33;1", "Warning:")
	fmt.Fprintf(e.errOut, label+" "+format+"\n", args...)
}

func outputSupportsColor(out io.Writer) bool {
	if forced := strings.TrimSpace(os.Getenv("CLICOLOR_FORCE")); forced != "" && forced != "0" {
		return true
	}
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func terminalInteractiveUI(in io.Reader, out io.Writer) bool {
	if os.Getenv("ACCESSIBLE") != "" {
		return false
	}
	input, inputOK := in.(*os.File)
	output, outputOK := out.(*os.File)
	if !inputOK || !outputOK {
		return false
	}
	inputInfo, inputErr := input.Stat()
	outputInfo, outputErr := output.Stat()
	return inputErr == nil && outputErr == nil &&
		inputInfo.Mode()&os.ModeCharDevice != 0 && outputInfo.Mode()&os.ModeCharDevice != 0
}

func (e *Environment) style(code, text string) string {
	if !e.color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (e *Environment) heading(text string) string {
	return e.style("96;1", text)
}

func (e *Environment) title(text string) string {
	return e.style("95;1", text)
}

func (e *Environment) success(text string) string {
	return e.style("92;1", text)
}

func (e *Environment) muted(text string) string {
	return e.style("90", text)
}
