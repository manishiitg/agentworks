package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
)

// Fixed classes only: never log message content, credentials or storage paths.
func agentDeliveryErrorClass(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "connection_unavailable"
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return "store_invalid_json"
	}
	var network net.Error
	if errors.As(err, &network) {
		return "transport"
	}
	return "storage_or_authorization"
}
