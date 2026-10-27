package server

import (
	"net/http"
	"net/http/httptest"

	"github.com/gorilla/mux"
)

func personalRoute(api *StreamingAPI, handler func(*StreamingAPI, http.ResponseWriter, *http.Request), method, target, body, user string, vars map[string]string) *httptest.ResponseRecorder {
	req := mux.SetURLVars(profileRouteRequest(method, target, []byte(body), user), vars)
	rec := httptest.NewRecorder()
	handler(api, rec, req)
	return rec
}
