package testing

import "github.com/manishiitg/multi-llm-provider-go/interfaces"

// NoopTracer is a no-op tracer implementation for testing
type NoopTracer struct{}

func (n *NoopTracer) EmitEvent(event interfaces.AgentEvent) error {
	return nil
}

func (n *NoopTracer) EmitLLMEvent(event interfaces.LLMEvent) error {
	return nil
}

func (n *NoopTracer) StartTrace(name string, input interface{}) interfaces.TraceID {
	return interfaces.TraceID("")
}

func (n *NoopTracer) EndTrace(traceID interfaces.TraceID, output interface{}) {
	// No-op
}

// InitializeTracer initializes a no-op tracer for testing
// In the main module, this would initialize Langfuse or other tracers
func InitializeTracer(logger interfaces.Logger) interfaces.Tracer {
	// For standalone testing, always use no-op tracer
	// The main module can override this if needed
	return &NoopTracer{}
}
