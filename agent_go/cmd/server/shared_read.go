package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"golang.org/x/sync/singleflight"
)

// The sidebar and the connection inventory ask for the same list from several screens at once; a log showed up to 9
// of the same navigation list in flight, each 2-9 s. Calls that overlap share one read. Nothing is kept after it
// finishes, so the next call reads again.
var sharedListReads singleflight.Group

type sharedReadReply struct {
	status int
	header http.Header
	body   []byte
}

// writeSharedRead runs read once for every call with this key that overlaps in time and writes the one reply to each.
// Key must hold everything the reply depends on (the person, the connection, the arguments). The read runs on a
// context that keeps the caller's identity but not its cancellation, so a screen that closes cannot fail the others.
func writeSharedRead(w http.ResponseWriter, r *http.Request, key string, read func(http.ResponseWriter, *http.Request)) {
	value, _, _ := sharedListReads.Do(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
		defer cancel()
		recorder := httptest.NewRecorder()
		read(recorder, r.WithContext(ctx))
		return sharedReadReply{recorder.Code, recorder.Header().Clone(), recorder.Body.Bytes()}, nil
	})
	reply := value.(sharedReadReply)
	for name, values := range reply.header {
		w.Header()[name] = append([]string(nil), values...)
	}
	w.WriteHeader(reply.status)
	_, _ = w.Write(reply.body)
}
