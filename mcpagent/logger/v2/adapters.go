package v2

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
)

// ToSlogLogger adapts v2.Logger to *slog.Logger, for mcp-go APIs that take a
// structured logger directly (server.WithLogger, transport.WithSSELogger as
// of mcp-go v1.0.0). Replaces ToUtilLogger: mcp-go's old util.Logger
// interface (Infof/Errorf) was removed in v1.0.0 in favor of log/slog
// throughout.
func ToSlogLogger(l Logger) *slog.Logger {
	return slog.New(&slogHandlerAdapter{logger: l})
}

// slogHandlerAdapter routes slog records to a v2.Logger, preserving
// structured attributes as Fields rather than flattening them into the
// message string. Handles WithAttrs/WithGroup so a caller that derives a
// scoped logger (as mcp-go's transports do per-request) still reaches the
// same v2.Logger with the accumulated attributes attached.
type slogHandlerAdapter struct {
	logger Logger
	attrs  []slog.Attr
	group  string
}

func (h *slogHandlerAdapter) Enabled(context.Context, slog.Level) bool { return true }

func (h *slogHandlerAdapter) Handle(_ context.Context, record slog.Record) error {
	fields := make([]Field, 0, record.NumAttrs()+len(h.attrs))
	for _, a := range h.attrs {
		fields = append(fields, Any(h.qualify(a.Key), a.Value.Any()))
	}
	record.Attrs(func(a slog.Attr) bool {
		fields = append(fields, Any(h.qualify(a.Key), a.Value.Any()))
		return true
	})
	switch {
	case record.Level >= slog.LevelError:
		h.logger.Error(record.Message, nil, fields...)
	case record.Level >= slog.LevelWarn:
		h.logger.Warn(record.Message, fields...)
	case record.Level >= slog.LevelInfo:
		h.logger.Info(record.Message, fields...)
	default:
		h.logger.Debug(record.Message, fields...)
	}
	return nil
}

func (h *slogHandlerAdapter) qualify(key string) string {
	if h.group == "" {
		return key
	}
	return h.group + "." + key
}

func (h *slogHandlerAdapter) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &slogHandlerAdapter{logger: h.logger, group: h.group}
	next.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return next
}

func (h *slogHandlerAdapter) WithGroup(name string) slog.Handler {
	next := &slogHandlerAdapter{logger: h.logger, attrs: h.attrs}
	if h.group == "" {
		next.group = name
	} else {
		next.group = h.group + "." + name
	}
	return next
}

// ToInterfacesLogger adapts v2.Logger to interfaces.Logger (multi-llm-provider-go)
func ToInterfacesLogger(l Logger) interfaces.Logger {
	return &interfacesLoggerAdapter{
		logger: l,
	}
}

// interfacesLoggerAdapter adapts v2.Logger to interfaces.Logger
type interfacesLoggerAdapter struct {
	logger Logger
}

func (a *interfacesLoggerAdapter) Infof(format string, v ...any) {
	a.logger.Info(fmt.Sprintf(format, v...))
}

func (a *interfacesLoggerAdapter) Errorf(format string, v ...any) {
	a.logger.Error(fmt.Sprintf(format, v...), nil)
}

func (a *interfacesLoggerAdapter) Debugf(format string, args ...interface{}) {
	a.logger.Debug(fmt.Sprintf(format, args...))
}
