package server

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// UnifiedWebhookRoutes gives every webhook one address, /api/hooks/<id> (PLAT-871). A webhook always starts a
// workflow (#3286: Crew message webhooks were removed); the same address also wakes a Watcher that listens for
// events. The older /api/hooks/workflow/<id> address is registered with the workflow receiver.
func UnifiedWebhookRoutes(router *mux.Router, workflows *SchedulerService, products *ProjectActionDispatcher) {
	receiver := webhookReceiver{find: findScheduleByIDAny, start: workflows.triggerSavedSchedule, existing: workflows.existingWebhookRun}
	router.HandleFunc("/hooks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if id := strings.TrimSpace(mux.Vars(r)["id"]); products != nil && uuid.Validate(id) == nil {
			w.Header().Set("Cache-Control", "no-store")
			if products.conditionService().receiveWebhookWake(w, r, id) {
				return
			}
		}
		receiver.receive(w, r)
	}).Methods("POST")
	router.HandleFunc("/hooks/{id}/runs/{run}", workflows.pollWebhookRun).Methods("GET")
	router.HandleFunc("/hooks/{id}/runs/{run}/artifact", workflows.downloadWebhookArtifact).Methods("GET", "HEAD")
}
