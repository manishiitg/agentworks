package server

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// UnifiedWebhookRoutes gives every webhook one address, /api/hooks/<id>, whether it belongs to a Crew or a Goal
// (PLAT-871, owner 2026-10-20: "should be same"). The id is looked up once: a Crew trigger goes to the Crew receiver,
// anything else to the workflow receiver, so both keep their own secret checks and run rules. The older
// /api/hooks/product/<id> and /api/hooks/workflow/<id> addresses stay as aliases ("keep aliases").
func UnifiedWebhookRoutes(router *mux.Router, workflows *SchedulerService, products *ProjectActionDispatcher) {
	receiver := webhookReceiver{find: findScheduleByIDAny, start: workflows.triggerSavedSchedule, existing: workflows.existingWebhookRun}
	isProduct := func(r *http.Request) bool {
		if products == nil {
			return false
		}
		match, err := products.findProductWebhook(r.Context(), strings.TrimSpace(mux.Vars(r)["id"]))
		return err == nil && match != nil
	}
	router.HandleFunc("/api/hooks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if isProduct(r) {
			products.receiveProductWebhook(w, r)
			return
		}
		receiver.receive(w, r)
	}).Methods("POST")
	router.HandleFunc("/api/hooks/{id}/runs/{run}", func(w http.ResponseWriter, r *http.Request) {
		if isProduct(r) {
			products.getProductWebhookRun(w, r)
			return
		}
		workflows.pollWebhookRun(w, r)
	}).Methods("GET")
	router.HandleFunc("/api/hooks/{id}/runs/{run}/artifact", workflows.downloadWebhookArtifact).Methods("GET", "HEAD")
}
