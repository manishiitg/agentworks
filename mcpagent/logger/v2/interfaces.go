package v2

// Logger is the primary logging interface
// This interface hides implementation details (no logrus leakage)
// and provides a clean, structured logging API
type Logger interface {
	// Basic logging methods
	// All methods accept a message and optional structured fields
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, err error, fields ...Field)
	Fatal(msg string, err error, fields ...Field)

	// Create child logger with preset fields
	// This allows creating contextual loggers with common fields
	With(fields ...Field) Logger

	// Resource cleanup
	// Close any open file handles or other resources
	Close() error
}

// Field represents a structured log field
// This replaces logrus.Fields and logrus.Entry to avoid dependency leakage
type Field struct {
	Key   string
	Value interface{}
}
