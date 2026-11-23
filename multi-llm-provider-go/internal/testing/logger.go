package testing

import (
	"fmt"
	"io"
	"os"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
)

// SimpleLogger is a basic logger implementation for testing
type SimpleLogger struct {
	output io.Writer
	level  string
}

var testLogger interfaces.Logger

// InitTestLogger initializes a simple test logger
func InitTestLogger(logFile string, level string) {
	var output io.Writer = os.Stdout
	if logFile != "" {
		file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err == nil {
			output = file
		}
	}
	testLogger = &SimpleLogger{
		output: output,
		level:  level,
	}
}

// GetTestLogger returns the shared test logger instance
func GetTestLogger() interfaces.Logger {
	if testLogger == nil {
		testLogger = &SimpleLogger{
			output: os.Stdout,
			level:  "info",
		}
	}
	return testLogger
}

// SimpleLogger implementation
func (l *SimpleLogger) Infof(format string, v ...any) {
	_, _ = fmt.Fprintf(l.output, "[INFO] "+format+"\n", v...) //nolint:errcheck // Logging to stdout/stderr, safe to ignore
}

func (l *SimpleLogger) Errorf(format string, v ...any) {
	_, _ = fmt.Fprintf(l.output, "[ERROR] "+format+"\n", v...) //nolint:errcheck // Logging to stdout/stderr, safe to ignore
}

func (l *SimpleLogger) Debugf(format string, args ...interface{}) {
	if l.level == "debug" {
		_, _ = fmt.Fprintf(l.output, "[DEBUG] "+format+"\n", args...) //nolint:errcheck // Logging to stdout/stderr, safe to ignore
	}
}
