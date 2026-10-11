package producthost

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"regexp"

	api "github.com/manishiitg/agentworks/packages/server-product-sdk"
	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

var validProductID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
var validActorID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Mount uses the existing private service credential. Only the authenticated
// platform proxy may establish actor/role headers; raw browser headers fail.
func Mount(mux *http.ServeMux, st *store.MemoryStore, workspace, serviceToken string, factories []api.Factory) error {
	if len(factories) == 0 {
		return nil
	}
	if len(serviceToken) < 32 {
		return errors.New("server products require a private service credential")
	}
	adapter := New(st)
	authenticate := func(r *http.Request) (api.Identity, bool) {
		actor := r.Header.Get("X-AgentWorks-Actor")
		ok := r.Header.Get("Origin") == "" && r.Header.Get("Cookie") == "" && validActorID.MatchString(actor) && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+serviceToken)) == 1
		if !ok {
			return api.Identity{}, false
		}
		if err := st.EnsurePlatformUser(workspace, actor); err != nil {
			return api.Identity{}, false
		}
		return api.Identity{UserID: actor, WorkspaceID: workspace, ClientID: r.Header.Get("X-AgentWorks-Client")}, true
	}
	type roleKey struct{}
	host := api.Host{APIVersion: api.APIVersion, Storage: adapter, Audit: adapter, Authenticate: authenticate, IsAdmin: func(ctx context.Context, _ api.Identity) bool { admin, _ := ctx.Value(roleKey{}).(bool); return admin }}
	seen := map[string]bool{}
	for _, factory := range factories {
		if factory == nil {
			return errors.New("nil server product factory")
		}
		product, err := factory(host)
		if err != nil {
			return err
		}
		if !validProductID.MatchString(product.ID) || seen[product.ID] || product.Handler == nil {
			return errors.New("invalid or duplicate server product")
		}
		seen[product.ID] = true
		prefix := "/api/admin/products/" + product.ID
		handler := http.StripPrefix(prefix, product.Handler)
		mux.Handle(prefix+"/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := authenticate(r); !ok {
				http.Error(w, "unauthorized", 401)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), roleKey{}, r.Header.Get("X-AgentWorks-Admin") == "1"))
			handler.ServeHTTP(w, r)
		}))
	}
	return nil
}
