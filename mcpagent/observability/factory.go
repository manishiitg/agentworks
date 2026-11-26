package observability

import (
	"strings"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

const (
	ProviderLangfuse  = "langfuse"
	ProviderLangsmith = "langsmith"
	ProviderNoop      = "noop"
)

// GetTracer returns a Tracer implementation based on the provided provider string.
func GetTracer(provider string) Tracer {
	provider = strings.ToLower(provider)

	switch provider {
	case "langfuse":
		if tracer, err := NewLangfuseTracerWithLogger(loggerv2.NewDefault()); err == nil {
			return tracer
		}
		// Fallback to noop if Langfuse init fails
		return NoopTracer{}
	case "langsmith":
		if tracer, err := NewLangsmithTracerWithLogger(loggerv2.NewDefault()); err == nil {
			return tracer
		}
		// Fallback to noop if LangSmith init fails
		return NoopTracer{}
	case "noop":
		return NoopTracer{}
	default:
		return NoopTracer{}
	}
}

// GetTracerWithLogger returns a Tracer implementation based on the provided provider string with an injected logger.
func GetTracerWithLogger(provider string, logger loggerv2.Logger) Tracer {
	provider = strings.ToLower(provider)

	switch provider {
	case "langfuse":
		if tracer, err := NewLangfuseTracerWithLogger(logger); err == nil {
			return tracer
		}
		// Fallback to noop if Langfuse init fails
		return NoopTracer{}
	case "langsmith":
		if tracer, err := NewLangsmithTracerWithLogger(logger); err == nil {
			return tracer
		}
		// Fallback to noop if LangSmith init fails
		return NoopTracer{}
	case "noop":
		return NoopTracer{}
	default:
		return NoopTracer{}
	}
}

// GetTracers returns multiple tracers from comma-separated providers.
// e.g., "langfuse,langsmith" returns both tracers.
func GetTracers(providers string, logger loggerv2.Logger) []Tracer {
	var tracers []Tracer
	for _, p := range strings.Split(providers, ",") {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" && p != "noop" {
			tracer := GetTracerWithLogger(p, logger)
			if _, isNoop := tracer.(NoopTracer); !isNoop {
				tracers = append(tracers, tracer)
			}
		}
	}
	return tracers
}
