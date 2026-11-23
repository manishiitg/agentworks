package v2

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// loggerImpl implements the Logger interface using logrus as the backend
// This struct hides all logrus implementation details
type loggerImpl struct {
	logrus *logrus.Logger
	file   *os.File
	fields []Field // Preset fields for child loggers
}

// New creates a new logger instance with the specified configuration
func New(cfg Config) (Logger, error) {
	// Create new logrus logger
	logrusLogger := logrus.New()

	// Set log level
	logLevel, err := logrus.ParseLevel(cfg.Level)
	if err != nil {
		return nil, fmt.Errorf("invalid log level: %w", err)
	}
	logrusLogger.SetLevel(logLevel)

	// Set formatter
	switch strings.ToLower(cfg.Format) {
	case "json":
		logrusLogger.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: time.RFC3339,
			CallerPrettyfier: func(f *runtime.Frame) (string, string) {
				filename := filepath.Base(f.File)
				return "", fmt.Sprintf("%s:%d", filename, f.Line)
			},
		})
	case "text":
		logrusLogger.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: time.RFC3339,
			CallerPrettyfier: func(f *runtime.Frame) (string, string) {
				filename := filepath.Base(f.File)
				return "", fmt.Sprintf("%s:%d", filename, f.Line)
			},
		})
	default:
		return nil, fmt.Errorf("unsupported log format: %s", cfg.Format)
	}

	// Enable caller information
	logrusLogger.SetReportCaller(true)

	// Set up output
	var file *os.File
	var writer io.Writer

	switch strings.ToLower(cfg.Output) {
	case "stdout":
		writer = os.Stdout
	case "stderr":
		writer = os.Stderr
	default:
		// Treat as file path
		if cfg.Output != "" {
			// Create log directory if it doesn't exist
			logDir := filepath.Dir(cfg.Output)
			if err := os.MkdirAll(logDir, 0755); err != nil { //nolint:gosec // 0755 permissions are intentional for user-accessible log directories
				return nil, fmt.Errorf("failed to create log directory: %w", err)
			}

			// Open log file
			//nolint:gosec // G304: cfg.Output comes from configuration, not user input
			file, err = os.OpenFile(cfg.Output, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
			if err != nil {
				return nil, fmt.Errorf("failed to open log file: %w", err)
			}
			writer = file
		} else {
			writer = os.Stdout
		}
	}

	// If file logging is enabled, add file output
	if cfg.EnableFile && cfg.FilePath != "" {
		// Create log directory if it doesn't exist
		logDir := filepath.Dir(cfg.FilePath)
		if err := os.MkdirAll(logDir, 0755); err != nil { //nolint:gosec // 0755 permissions are intentional for user-accessible log directories
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		// Open log file
		//nolint:gosec // G304: cfg.FilePath comes from configuration, not user input
		logFile, err := os.OpenFile(cfg.FilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}

		// Use multi-writer to write to both primary output and file
		if file != nil {
			writer = io.MultiWriter(writer, logFile)
		} else {
			writer = io.MultiWriter(writer, logFile)
			file = logFile
		}
	}

	logrusLogger.SetOutput(writer)

	return &loggerImpl{
		logrus: logrusLogger,
		file:   file,
		fields: []Field{},
	}, nil
}

// NewDefault creates a logger with sensible defaults
func NewDefault() Logger {
	cfg := DefaultConfig()
	logger, err := New(cfg)
	if err != nil {
		// Fallback to a basic logger if there's an error
		// This should rarely happen, but we need to handle it
		return NewNoop()
	}
	return logger
}

// NewNoop creates a no-op logger that does nothing
// This is useful for testing when you don't want any logging output
func NewNoop() Logger {
	return &noopLogger{}
}

// noopLogger is a no-op implementation of Logger
type noopLogger struct{}

func (n *noopLogger) Debug(msg string, fields ...Field)            {}
func (n *noopLogger) Info(msg string, fields ...Field)             {}
func (n *noopLogger) Warn(msg string, fields ...Field)             {}
func (n *noopLogger) Error(msg string, err error, fields ...Field) {}
func (n *noopLogger) Fatal(msg string, err error, fields ...Field) {}
func (n *noopLogger) With(fields ...Field) Logger                  { return n }
func (n *noopLogger) Close() error                                 { return nil }

// Helper function to convert Field slice to logrus.Fields
func fieldsToLogrusFields(fields []Field) logrus.Fields {
	logrusFields := make(logrus.Fields, len(fields))
	for _, field := range fields {
		logrusFields[field.Key] = field.Value
	}
	return logrusFields
}

// Helper function to get logrus entry with fields
func (l *loggerImpl) getEntry(fields []Field) *logrus.Entry {
	// Combine preset fields with provided fields
	allFields := append(l.fields, fields...)
	logrusFields := fieldsToLogrusFields(allFields)
	return l.logrus.WithFields(logrusFields)
}

// Implement Logger interface methods

func (l *loggerImpl) Debug(msg string, fields ...Field) {
	l.getEntry(fields).Debug(msg)
}

func (l *loggerImpl) Info(msg string, fields ...Field) {
	l.getEntry(fields).Info(msg)
}

func (l *loggerImpl) Warn(msg string, fields ...Field) {
	l.getEntry(fields).Warn(msg)
}

func (l *loggerImpl) Error(msg string, err error, fields ...Field) {
	entry := l.getEntry(fields)
	if err != nil {
		entry = entry.WithError(err)
	}
	entry.Error(msg)
}

func (l *loggerImpl) Fatal(msg string, err error, fields ...Field) {
	entry := l.getEntry(fields)
	if err != nil {
		entry = entry.WithError(err)
	}
	entry.Fatal(msg)
}

func (l *loggerImpl) With(fields ...Field) Logger {
	// Create a new logger with preset fields
	// This allows creating contextual loggers
	return &loggerImpl{
		logrus: l.logrus,
		file:   nil, // Child loggers don't own the file handle
		fields: append(l.fields, fields...),
	}
}

func (l *loggerImpl) Close() error {
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}
