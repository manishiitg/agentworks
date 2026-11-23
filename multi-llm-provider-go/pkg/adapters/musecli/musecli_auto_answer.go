package musecli

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const metadataMuseAutoSelectRecommended = "muse_auto_select_recommended"
const metadataMuseUserChoice = "muse_user_choice"

// WithUserChoice leaves native questions open for a structured chat choice.
// The caller must consume QuestionReader events and submit the answer.
func WithUserChoice(enabled bool) llmtypes.CallOption {
	return func(o *llmtypes.CallOptions) {
		ensureMetadata(o)
		o.Metadata.Custom[metadataMuseUserChoice] = enabled
	}
}

type museUserChoiceKey struct{}

// WithAutoSelectRecommended controls native question widgets in the tmux lane.
// Enabled by default. Selects the first option regardless of recommendation
// labels. The legacy option name is retained for caller compatibility.
func WithAutoSelectRecommended(enabled bool) llmtypes.CallOption {
	return func(o *llmtypes.CallOptions) {
		ensureMetadata(o)
		o.Metadata.Custom[metadataMuseAutoSelectRecommended] = enabled
	}
}

type museAutoAnswerKey struct{}
type museAutoAnswerState struct {
	mu               sync.Mutex
	stopped          atomic.Bool
	lastQuestion     string
	observedQuestion string
	observedCursor   int
}
type museRecommendedAnswer struct {
	key, label      string
	review          bool
	current, target int
}

var museQuestionOptionRE = regexp.MustCompile(`^\s*([›❯>⟩]?)\s*(\d+)\.\s+(.+)$`)
var museQuestionRunningRE = regexp.MustCompile(`(?i)\s*[—·]\s*running\s*\([^)]*\)`)

// Parse only the active native widget, never recommendation text in history.
func museRecommendedQuestion(pane string) (museRecommendedAnswer, bool) {
	if musePendingUserInputError(pane) == nil {
		return museRecommendedAnswer{}, false
	}
	start := museQuestionWidgetStart(pane)
	widget := pane[start:]
	if strings.Contains(strings.ToLower(widget), "review answers before submit") {
		return museRecommendedReview(widget)
	}
	if museCheckboxWidget(strings.ToLower(widget)) {
		return museRecommendedCheckbox(widget)
	}
	end := strings.Index(strings.ToLower(widget), "enter to select")
	if end < 0 {
		return museRecommendedAnswer{}, false
	}
	widget = widget[:end]
	if musePaneShowsBlockingGate(widget) {
		return museRecommendedAnswer{}, false
	}
	var result museRecommendedAnswer
	result.current = -1
	result.target = -1
	var identity []string
	count, cursors := 0, 0
	for _, line := range strings.Split(widget, "\n") {
		match := museQuestionOptionRE.FindStringSubmatch(line)
		if match == nil {
			if museQuestionHeadingRE.MatchString(line) {
				line = line[strings.Index(strings.ToLower(line), "request user input"):]
			}
			identity = append(identity, museQuestionRunningRE.ReplaceAllString(strings.TrimSpace(line), ""))
			continue
		}
		label := strings.TrimSpace(match[3])
		identity = append(identity, match[2]+". "+label)
		if match[1] != "" {
			result.current = count
			cursors++
		}
		if count == 0 {
			result.target = count
			result.label = label
		}
		count++
	}
	result.key = strings.Join(identity, "\n")
	return result, count > 0 && cursors == 1
}

func museWithAutoAnswer(ctx context.Context, opts *llmtypes.CallOptions) context.Context {
	if opts != nil && opts.Metadata != nil {
		if value, ok := opts.Metadata.Custom[metadataMuseAutoSelectRecommended].(bool); ok && !value {
			return ctx
		}
		if enabled, _ := opts.Metadata.Custom[metadataMuseUserChoice].(bool); enabled {
			return museWithUserChoice(ctx)
		}
	}
	state := &museAutoAnswerState{}
	return context.WithValue(ctx, museAutoAnswerKey{}, state)
}

// Returns pending=true while a widget exists, including after sending Enter.
// Callers must wait for it to disappear before treating the pane as idle.
func museHandlePendingQuestion(ctx context.Context, session, pane string) (pending bool, err error) {
	if userChoice, _ := ctx.Value(museUserChoiceKey{}).(bool); userChoice {
		return museHandleUserChoiceQuestion(ctx, session, pane)
	}
	state, _ := ctx.Value(museAutoAnswerKey{}).(*museAutoAnswerState)
	if state != nil {
		state.mu.Lock()
		defer state.mu.Unlock()
	}
	pendingErr := musePendingUserInputError(pane)
	if pendingErr == nil {
		if state != nil {
			state.lastQuestion = ""
			state.observedQuestion = ""
		}
		return false, nil
	}
	answer, ok := museRecommendedQuestion(pane)
	if state == nil || !ok {
		return true, pendingErr
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if state.stopped.Load() {
		return true, context.Canceled
	}
	if state.lastQuestion == answer.key {
		return true, nil
	}
	// Cursor's readiness handler also requires consecutive visible captures;
	// first paint can precede the native input loop becoming interactive.
	if state.observedQuestion != answer.key || state.observedCursor != answer.current {
		state.observedQuestion = answer.key
		state.observedCursor = answer.current
		return true, nil
	}
	currentPane, captureErr := museTmuxCapturePane(ctx, session)
	if captureErr != nil {
		return true, captureErr
	}
	current, valid := museRecommendedQuestion(currentPane)
	if !valid || current.key != answer.key || current.current != answer.current {
		return true, nil
	}
	// Move from the observed cursor, not from an assumed first/default option.
	for offset := answer.current; offset != answer.target; {
		if state.stopped.Load() {
			return true, context.Canceled
		}
		key := "Down"
		if offset > answer.target {
			key = "Up"
			offset--
		} else {
			offset++
		}
		if out, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, key).CombinedOutput(); err != nil {
			return true, fmt.Errorf("navigate Muse recommendation: %w: %s", err, out)
		}
	}
	// Recheck the widget and cursor after navigation before submitting anything.
	fresh, err := museTmuxCapturePane(ctx, session)
	if err != nil {
		return true, err
	}
	selected, ok := museRecommendedQuestion(fresh)
	if !ok || selected.key != answer.key || selected.current != answer.target {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if state.stopped.Load() {
		return true, context.Canceled
	}
	if out, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, "Enter").CombinedOutput(); err != nil {
		return true, fmt.Errorf("submit Muse recommendation: %w: %s", err, out)
	}
	state.lastQuestion = answer.key
	return true, nil
}

// Muse multi-question widgets have a final review row. Submit the completed
// answers without requiring recommendation labels.
func museRecommendedReview(widget string) (museRecommendedAnswer, bool) {
	result := museRecommendedAnswer{current: -1, target: -1, review: true, label: "Submit answers"}
	var identity []string
	inReview := false
	rows, answers, cursors := 0, 0, 0
	for _, raw := range strings.Split(widget, "\n") {
		line := strings.TrimSpace(raw)
		if strings.Contains(strings.ToLower(line), "review answers before submit") {
			inReview = true
			continue
		}
		if !inReview || line == "" {
			continue
		}
		if strings.HasPrefix(line, "─") {
			break
		}
		cursor := strings.HasPrefix(line, ">") || strings.HasPrefix(line, "›") || strings.HasPrefix(line, "❯") || strings.HasPrefix(line, "⟩")
		if cursor {
			result.current = rows
			cursors++
			line = strings.TrimSpace(strings.TrimLeft(line, ">›❯⟩"))
		}
		switch line {
		case "Submit answers":
			result.target = rows
		case "Interrupt turn":
		default:
			_, answer, ok := strings.Cut(line, ":")
			if !ok || strings.TrimSpace(answer) == "" {
				return result, false
			}
			answers++
		}
		identity = append(identity, line)
		rows++
	}
	result.key = "review\n" + strings.Join(identity, "\n")
	return result, inReview && answers > 0 && result.target >= 0 && cursors == 1 && !musePaneShowsBlockingGate(widget)
}

// Share only selector state across live delivery, generation, and retained
// polling. Automatic selections do not emit user-facing stream messages.
func museBindPersistentAutoAnswer(ctx context.Context, entry *musePersistentSession) context.Context {
	requested, _ := ctx.Value(museAutoAnswerKey{}).(*museAutoAnswerState)
	musePersistentPool.Lock()
	defer musePersistentPool.Unlock()
	if requested == nil {
		entry.autoAnswer = nil
		entry.userChoice, _ = ctx.Value(museUserChoiceKey{}).(bool)
		return ctx
	}
	entry.userChoice = false
	if entry.autoAnswer == nil {
		entry.autoAnswer = requested
	}
	return context.WithValue(ctx, museAutoAnswerKey{}, entry.autoAnswer)
}

var museCheckboxStateRE = regexp.MustCompile(`^\[([ xX✓✔])\]\s*`)

// museRecommendedCheckbox plans the next step on Muse's multi-select question:
// with nothing checked, toggle the first option (the recommended one, as for a
// single choice); once one is checked, move to the Submit row and submit. The
// identity carries the checked state, so each step is a new action.
func museRecommendedCheckbox(widget string) (museRecommendedAnswer, bool) {
	result := museRecommendedAnswer{current: -1, target: -1}
	lower := strings.ToLower(widget)
	if end := strings.Index(lower, "enter to toggle"); end >= 0 {
		widget = widget[:end]
	}
	var identity []string
	rows, cursors, firstOption, submit, checked := 0, 0, -1, -1, 0
	for _, line := range strings.Split(widget, "\n") {
		match := museQuestionOptionRE.FindStringSubmatch(line)
		if match == nil {
			if museQuestionHeadingRE.MatchString(line) {
				line = line[strings.Index(strings.ToLower(line), "request user input"):]
			}
			if text := museQuestionRunningRE.ReplaceAllString(strings.TrimSpace(line), ""); text != "" {
				identity = append(identity, text)
			}
			continue
		}
		label := strings.TrimSpace(match[3])
		if state := museCheckboxStateRE.FindStringSubmatch(label); state != nil {
			if firstOption < 0 {
				firstOption = rows
				result.label = strings.TrimSpace(label[len(state[0]):])
			}
			if state[1] != " " {
				checked++
			}
		} else if strings.HasPrefix(strings.ToLower(label), "submit") {
			submit = rows
		}
		if match[1] != "" {
			result.current = rows
			cursors++
		}
		identity = append(identity, match[2]+". "+label)
		rows++
	}
	if firstOption < 0 || submit < 0 || cursors != 1 {
		return result, false
	}
	result.target = firstOption
	if checked > 0 {
		result.target = submit
		result.label = "Submit answer"
	}
	result.key = "checkbox\n" + strings.Join(identity, "\n")
	return result, true
}

// museUserChoiceWait is how long a chat question waits for the person before
// Muse takes its first option, exactly as an unattended run does. A closed
// tab or a missed card must not hold the turn forever.
var museUserChoiceWait = 10 * time.Minute

// museUserChoiceDismissAfter bounds that fallback: if even the first option
// cannot be entered (the widget cannot be read), Esc interrupts the run so the
// turn ends instead of hanging.
var museUserChoiceDismissAfter = 2 * time.Minute

type museUserChoiceGraceKey struct{}

// museUserChoiceGrace tracks how long the current question has been open.
type museUserChoiceGrace struct {
	mu        sync.Mutex
	since     time.Time
	dismissed bool
	auto      *museAutoAnswerState
}

func museWithUserChoice(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, museUserChoiceKey{}, true)
	return context.WithValue(ctx, museUserChoiceGraceKey{}, &museUserChoiceGrace{auto: &museAutoAnswerState{}})
}

// museHandleUserChoiceQuestion leaves a question for the person, then falls
// back to the unattended answer once museUserChoiceWait has passed.
func museHandleUserChoiceQuestion(ctx context.Context, session, pane string) (bool, error) {
	grace, _ := ctx.Value(museUserChoiceGraceKey{}).(*museUserChoiceGrace)
	open := musePendingUserInputError(pane) != nil
	if grace == nil {
		return open, nil
	}
	grace.mu.Lock()
	if !open {
		grace.since, grace.dismissed = time.Time{}, false
		grace.mu.Unlock()
		return false, nil
	}
	if grace.since.IsZero() {
		grace.since = time.Now()
	}
	waited := time.Since(grace.since)
	grace.mu.Unlock()
	if waited < museUserChoiceWait {
		return true, nil
	}
	// A person's answer being typed right now wins; retry on the next poll.
	if !questionSubmissionMu.TryLock() {
		return true, nil
	}
	defer questionSubmissionMu.Unlock()
	autoCtx := context.WithValue(ctx, museUserChoiceKey{}, false)
	autoCtx = context.WithValue(autoCtx, museAutoAnswerKey{}, grace.auto)
	pending, err := museHandlePendingQuestion(autoCtx, session, pane)
	if err == nil && (!pending || waited < museUserChoiceWait+museUserChoiceDismissAfter) {
		return pending, nil
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	grace.mu.Lock()
	dismissed := grace.dismissed
	grace.dismissed = true
	grace.mu.Unlock()
	if !dismissed {
		log.Printf("[MUSE_QUESTION] session=%s question unanswered for %s and the first option could not be entered (%v); interrupting with Esc", session, waited.Round(time.Second), err)
		if sendErr := museSendQuestionKey(ctx, session, "Escape"); sendErr != nil {
			return true, sendErr
		}
	}
	return true, nil
}
