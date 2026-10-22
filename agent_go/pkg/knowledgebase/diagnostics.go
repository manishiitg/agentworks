package knowledgebase

import "errors"

// diagnosticError carries a fixed operation name, never a file path or content.
// Unwrap keeps cancellation and underlying error classification intact.
type diagnosticError struct {
	step string
	err  error
}

func (e *diagnosticError) Error() string { return e.err.Error() }
func (e *diagnosticError) Unwrap() error { return e.err }

// DiagnosticStep returns the innermost recorded storage operation for HTTP logs.
func DiagnosticStep(err error) string {
	var diagnostic *diagnosticError
	if errors.As(err, &diagnostic) {
		return diagnostic.step
	}
	return ""
}

func withDiagnosticStep(step string, err error) error {
	var domain *Error
	// Keep domain errors as the same concrete type: callers use their codes.
	if err == nil || errors.As(err, &domain) || DiagnosticStep(err) != "" {
		return err
	}
	return &diagnosticError{step: step, err: err}
}
