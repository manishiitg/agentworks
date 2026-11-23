package agycli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/acpcli"
)

// agyACPOutcome classifies agy turn failures for the ACP connector: the
// typed stop reason plus this prompt's own error step in agy's conversation
// store (quota, rate limit, server errors and their reset times).
type agyACPOutcome struct{ home string }

func (o agyACPOutcome) BeginTurn(sessionID string) func(model, stopReason string) error {
	return beginAgyACPOutcome(o.home, sessionID).failure
}

func (o agyACPOutcome) RPCFailure(model, sessionID string, err error) error {
	return agyACPRPCFailure(model, sessionID, err)
}

var agyNativeSessionID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var agyAPIError = regexp.MustCompile(`(?s)Error (\d{3}), Message: (.*?), Status: ([A-Z_]+)`)
var agyResetStamp = regexp.MustCompile(`\bquotaResetTimeStamp:([0-9TtZz:+.\-]+)`)
var agyResetDelay = regexp.MustCompile(`\b(?:quotaResetDelay|retryDelay):([0-9a-z.µμ]+)`)

// Only typed refusal or this prompt's own error-step payload is an error carrier.
// An assistant discussing quota limits is ordinary text, not a quota verdict.
func agyACPOutcomeError(model, stop, carrier string, now time.Time) error {
	quota := stop == "refusal" || strings.Contains(carrier, "reason:QUOTA_EXHAUSTED") || strings.Contains(carrier, "QUOTA_EXHAUSTED")
	if !quota && carrier == "" {
		return nil
	}
	kind, status, message := llmerrors.KindUnknown, 0, "Antigravity stopped this turn because of an error."
	if match := agyAPIError.FindStringSubmatch(carrier); match != nil {
		status, _ = strconv.Atoi(match[1])
		switch {
		case status == 429:
			kind = llmerrors.KindRateLimit
		case status >= 500:
			kind = llmerrors.KindServerError
		case status == 401 || status == 403:
			kind = llmerrors.KindAuth
		default:
			kind = llmerrors.KindInvalidRequest
		}
	}
	if quota {
		kind = llmerrors.KindQuotaExhausted
		message = "You have reached your Antigravity quota for this period."
	}
	failure := &llmerrors.Error{Provider: "agy-cli", Model: model, Kind: kind, Status: status, Err: errors.New(message)}
	if match := agyResetStamp.FindStringSubmatch(carrier); match != nil {
		failure.RetryAt, _ = time.Parse(time.RFC3339Nano, match[1])
	}
	if failure.RetryAt.IsZero() {
		if match := agyResetDelay.FindStringSubmatch(carrier); match != nil {
			if delay, err := time.ParseDuration(strings.ReplaceAll(match[1], "μ", "µ")); err == nil && delay > 0 {
				failure.RetryAt = now.Add(delay)
				failure.RetryAfter = delay
			}
		}
	}
	if stop == "refusal" && !strings.Contains(carrier, "QUOTA_EXHAUSTED") && failure.RetryAt.IsZero() {
		failure.Err = errors.New("Antigravity stopped this turn; it may be out of quota.")
	}
	return failure
}

// A pre-prompt floor and unchanged, unlinked native file keep old/foreign errors
// out of the current turn. Read-only SQLite includes the live WAL. No polling.
type agyACPOutcomeWatch struct {
	home, session string
	before        os.FileInfo
	floor         int
	enabled       bool
}

func agyACPConversationFile(home, session string) (string, os.FileInfo, error) {
	if home == "" || !agyNativeSessionID.MatchString(session) {
		return "", nil, errors.New("unscoped native log")
	}
	root, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", nil, err
	}
	current := root
	for _, part := range []string{".gemini", "antigravity-cli", "conversations", session + ".db"} {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return filepath.Join(root, ".gemini", "antigravity-cli", "conversations", session+".db"), nil, nil
		}
		if err != nil {
			return "", nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", nil, errors.New("linked native log")
		}
		if part == session+".db" {
			if !info.Mode().IsRegular() {
				return "", nil, errors.New("non-file native log")
			}
			return current, info, nil
		}
		if !info.IsDir() {
			return "", nil, errors.New("non-directory native log")
		}
	}
	return "", nil, errors.New("missing native log")
}
func agyACPOpenLog(path string) (*sql.DB, error) {
	// SQLite may read -wal/-shm companions. Neither may point at another file.
	for _, suffix := range []string{"-wal", "-shm"} {
		if info, err := os.Lstat(path + suffix); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			return nil, errors.New("linked native log companion")
		}
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro"}
	if filepath.VolumeName(path) != "" {
		uri.Path = "/" + uri.Path
	}
	return sql.Open("sqlite", uri.String())
}
func beginAgyACPOutcome(home, session string) agyACPOutcomeWatch {
	w := agyACPOutcomeWatch{home: home, session: session, floor: -1}
	path, info, err := agyACPConversationFile(home, session)
	if err != nil {
		log.Printf("[AGY_OUTCOME] session=%s source=unavailable stage=before", session)
		return w
	}
	w.before = info
	if info != nil {
		db, err := agyACPOpenLog(path)
		if err != nil {
			log.Printf("[AGY_OUTCOME] session=%s source=unavailable stage=baseline_open", session)
			return w
		}
		defer db.Close()
		if err = db.QueryRowContext(context.Background(), `SELECT COALESCE(MAX(idx),-1) FROM steps`).Scan(&w.floor); err != nil {
			log.Printf("[AGY_OUTCOME] session=%s source=unavailable stage=baseline", session)
			return w
		}
	}
	w.enabled = true
	return w
}
func (w agyACPOutcomeWatch) failure(model, stop string) error {
	var carrier string
	if w.enabled {
		path, info, err := agyACPConversationFile(w.home, w.session)
		if err == nil && info != nil && (w.before == nil || os.SameFile(w.before, info)) {
			db, err := agyACPOpenLog(path)
			if err == nil {
				var typ int
				var payload []byte
				err = db.QueryRowContext(context.Background(), `SELECT step_type,step_payload FROM steps WHERE idx>? AND ((step_type=? AND status=?) OR step_type IN (?,?)) ORDER BY idx DESC LIMIT 1`, w.floor, agyStepExecutorError, agyStepStatusDone, agyStepAssistant, agyStepToolCall).Scan(&typ, &payload)
				_ = db.Close()
				if err == nil && typ == 17 {
					if len(payload) > 16384 {
						payload = payload[:16384]
					}
					carrier = string(payload)
					if carrier == "" {
						carrier = "native type-17 error"
					}
				}
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				log.Printf("[AGY_OUTCOME] session=%s source=unavailable stage=after", w.session)
			}
		} else {
			log.Printf("[AGY_OUTCOME] session=%s source=unavailable stage=identity", w.session)
		}
	}
	failure := agyACPOutcomeError(model, stop, carrier, time.Now())
	if failure != nil {
		reset := ""
		if at := llmerrors.RetryAtOrZero(failure); !at.IsZero() {
			reset = at.UTC().Format(time.RFC3339)
		}
		log.Printf("[AGY_OUTCOME] session=%s kind=%s retry_at=%s", w.session, llmerrors.KindOf(failure), reset)
	}
	return failure
}

func agyACPModelError(model, session string, err error) error {
	if err == nil {
		return nil
	}
	var rpc *acpcli.RPCError
	lower := strings.ToLower(err.Error())
	if errors.As(err, &rpc) && strings.Contains(lower, "model") && (rpc.Code == -32602 || strings.Contains(lower, "unknown") || strings.Contains(lower, "not available") || strings.Contains(lower, "not found") || strings.Contains(lower, "unavailable")) {
		log.Printf("[CLI_MODEL_ERROR] provider=agy-cli session=%s kind=model_not_found", session)
		return &llmerrors.Error{Provider: "agy-cli", Model: model, Kind: llmerrors.KindModelNotFound, Err: fmt.Errorf("Antigravity model %q is not available for this account; pick another model.", model)}
	}
	return err
}

func agyACPRPCFailure(model, session string, err error) error {
	if mapped := agyACPModelError(model, session, err); mapped != err {
		return mapped
	}
	var rpc *acpcli.RPCError
	if !errors.As(err, &rpc) {
		return err
	}
	carrier := rpc.Message + " " + string(rpc.Data)
	// Explicit quota/reset details win over a generic HTTP status (including 429).
	if strings.Contains(carrier, "QUOTA_EXHAUSTED") {
		return agyACPOutcomeError(model, "", carrier, time.Now())
	}
	if rpc.Code == 429 || (rpc.Code >= 500 && rpc.Code < 600) {
		kind := llmerrors.KindServerError
		if rpc.Code == 429 {
			kind = llmerrors.KindRateLimit
		}
		log.Printf("[AGY_OUTCOME] session=%s source=rpc kind=%s status=%d", session, kind, rpc.Code)
		return &llmerrors.Error{Provider: "agy-cli", Model: model, Kind: kind, Status: rpc.Code, Err: errors.New("Antigravity could not finish this request.")}
	}
	if agyAPIError.MatchString(carrier) {
		return agyACPOutcomeError(model, "", carrier, time.Now())
	}
	return err
}
