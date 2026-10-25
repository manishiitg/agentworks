package workspace

import (
	"context"
	"fmt"
	"strings"
)

type databaseCallerKey struct{}

// WithDatabaseCaller binds data attribution to the requesting person. It does
// not change the workspace account, database permissions or shared access.
func WithDatabaseCaller(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, databaseCallerKey{}, strings.TrimSpace(userID))
}

// bindDatabaseParams resolves an explicit {"$current_user_id":true} parameter
// into the caller's ID. SQL remains parameterized and ordinary values stay as
// supplied; this is a query convenience, not row-level access control.
func (c *Client) bindDatabaseParams(ctx context.Context, params []interface{}) ([]interface{}, error) {
	var bound []interface{}
	for i, value := range params {
		marker, ok := value.(map[string]interface{})
		if !ok || len(marker) != 1 || marker["$current_user_id"] != true {
			continue
		}
		userID, _ := ctx.Value(databaseCallerKey{}).(string)
		if userID == "" {
			userID = c.DatabaseCallerID
		}
		if strings.TrimSpace(userID) == "" {
			return nil, fmt.Errorf("current user is unavailable; sign in before listing or creating your projects")
		}
		if bound == nil {
			bound = append([]interface{}(nil), params...)
		}
		bound[i] = userID
	}
	if bound != nil {
		return bound, nil
	}
	return params, nil
}
