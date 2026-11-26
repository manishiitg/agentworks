package picli

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// These are native RPC/model failure carriers, never assistant/tool text or
// guesses from whether an API-key environment variable happens to be set.
func piRPCRequestFailure(model, session string, err error, modelCarrier bool) error {
	if err == nil {
		return nil
	}
	var rpc *piRPCCommandError
	if !modelCarrier && !errors.As(err, &rpc) {
		return err
	}
	text := strings.ToLower(err.Error())
	kind := llmerrors.KindOf(llmerrors.Classify("pi-cli", model, err))
	missing := strings.Contains(text, "no api key") || strings.Contains(text, "api key not found") || strings.Contains(text, "credentials_not_configured") || strings.Contains(text, "not logged in") || strings.Contains(text, "not signed in") || strings.Contains(text, "login required")
	if missing || kind == llmerrors.KindAuth {
		log.Printf("[CLI_MODEL_ERROR] provider=pi-cli session=%s kind=auth", session)
		return &llmerrors.Error{Provider: "pi-cli", Model: model, Kind: llmerrors.KindAuth, Err: errors.New("Pi is not signed in, or its API key is missing or invalid. Run pi and use /login, or configure its key.")}
	}
	unavailable := strings.Contains(text, "model") && (strings.Contains(text, "not found") || strings.Contains(text, "unknown") || strings.Contains(text, "not available") || strings.Contains(text, "unavailable") || strings.Contains(text, "no available models"))
	if kind == llmerrors.KindModelNotFound || unavailable {
		log.Printf("[CLI_MODEL_ERROR] provider=pi-cli session=%s kind=model_not_found", session)
		return &llmerrors.Error{Provider: "pi-cli", Model: model, Kind: llmerrors.KindModelNotFound, Err: fmt.Errorf("Pi model %q is not available for this account; pick another model.", model)}
	}
	return err
}
