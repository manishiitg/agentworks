package musecli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// QuestionSelection is Muse's selection rule; Mode "multiple" is the
// checkbox form. A nil selection is a single choice.
type QuestionSelection struct {
	Mode          string `json:"mode,omitempty"`
	MinSelections int    `json:"min_selections,omitempty"`
	MaxSelections int    `json:"max_selections,omitempty"`
}
type Question struct {
	ID        string             `json:"id"`
	Header    string             `json:"header,omitempty"`
	Question  string             `json:"question"`
	Options   []QuestionOption   `json:"options"`
	Selection *QuestionSelection `json:"selection,omitempty"`
}

// Multiple reports whether the question is Muse's multi-select form.
func (q Question) Multiple() bool { return q.Selection != nil && q.Selection.Mode == "multiple" }

// QuestionAnswer carries SelectedLabel for a single choice and
// SelectedLabels for a multi-select question.
type QuestionAnswer struct {
	ID             string   `json:"id"`
	SelectedLabel  string   `json:"selected_label,omitempty"`
	SelectedLabels []string `json:"selected_labels,omitempty"`
}

// FirstOptionAnswers is the "let the agent choose" answer: the first option
// of every question, the same choice unattended runs make.
func FirstOptionAnswers(questions []Question) []QuestionAnswer {
	answers := make([]QuestionAnswer, len(questions))
	for i, q := range questions {
		answers[i].ID = q.ID
		if len(q.Options) == 0 {
			continue
		}
		if q.Multiple() {
			answers[i].SelectedLabels = []string{q.Options[0].Label}
		} else {
			answers[i].SelectedLabel = q.Options[0].Label
		}
	}
	return answers
}

type QuestionEvent struct {
	Sequence        int64            `json:"sequence"`
	NativeSessionID string           `json:"native_session_id"`
	RunID           string           `json:"run_id"`
	PromptID        string           `json:"prompt_id"`
	Kind            string           `json:"kind"`
	Questions       []Question       `json:"questions,omitempty"`
	Answers         []QuestionAnswer `json:"answers,omitempty"`
	Outcome         string           `json:"outcome,omitempty"`
}

// QuestionReader follows committed native rows. Its owner may acquire a
// native session only after the reader starts.
type QuestionReader struct {
	owner, nativeID, path, home string
	logRetry                    museLogPathRetry
	offset                      int64
}

func NewQuestionReaderForOwner(owner string) *QuestionReader { return &QuestionReader{owner: owner} }

func (r *QuestionReader) Poll() ([]QuestionEvent, error) {
	key, err := musePersistentKey(r.owner)
	if err != nil {
		return nil, err
	}
	musePersistentPool.Lock()
	entry := musePersistentPool.m[key]
	var nativeID, path, home string
	if entry != nil {
		nativeID = entry.nativeSessionID
		path = entry.logPath
		home = entry.accountDataHome
	}
	musePersistentPool.Unlock()
	if nativeID == "" {
		if s := museMSPSessionFor(r.owner); s != nil {
			s.mu.Lock()
			nativeID, home = s.sessionID, s.dataHome
			s.mu.Unlock()
		}
	}
	if nativeID == "" {
		return nil, nil
	}
	if home == "" {
		home = museXDGDataHome()
	}
	if path == "" && r.nativeID == nativeID && r.home == home && r.path != "" {
		path = r.path
	}
	if path == "" {
		path = r.logRetry.resolve(home+"\x00"+nativeID, func() string { return museSessionLogPath(nativeID, home) })
	}
	if path == "" {
		return nil, nil
	}
	if r.nativeID != nativeID || r.path != path {
		r.nativeID, r.path, r.home, r.offset = nativeID, path, home, 0
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		r.path = ""
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if r.offset > info.Size() {
		r.offset = 0
	}
	if _, err := f.Seek(r.offset, io.SeekStart); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(f)
	var result []QuestionEvent
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return result, readErr
		}
		r.offset += int64(len(line))
		var row struct {
			Sequence int64  `json:"sequence"`
			Type     string `json:"payload_type"`
			Payload  struct {
				Kind  string `json:"kind"`
				RunID string `json:"run_id"`
				Event struct {
					Kind      string           `json:"kind"`
					PromptID  string           `json:"prompt_id"`
					Questions []Question       `json:"questions"`
					Answers   []QuestionAnswer `json:"answers"`
					Outcome   string           `json:"outcome"`
				} `json:"event"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &row) != nil || row.Type != "runtime.session" || row.Payload.Kind != "run" {
			continue
		}
		e := row.Payload.Event
		if e.PromptID == "" || (e.Kind != "user_input_prompt_requested" && e.Kind != "user_input_prompt_settled") {
			continue
		}
		if e.Kind == "user_input_prompt_requested" && (len(e.Questions) == 0 || len(e.Questions) > 12) {
			continue
		}
		result = append(result, QuestionEvent{Sequence: row.Sequence, NativeSessionID: nativeID, RunID: row.Payload.RunID, PromptID: e.PromptID, Kind: e.Kind, Questions: e.Questions, Answers: e.Answers, Outcome: e.Outcome})
	}
	return result, nil
}

var questionSubmissionMu sync.Mutex

// museCollapse folds whitespace so text the TUI wrapped across pane lines
// compares equal to the structured text.
func museCollapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// museWidgetRow is one numbered row of the live question widget, with any
// wrapped continuation lines folded in.
type museWidgetRow struct {
	text    string // collapsed, checkbox marker removed
	box     bool
	checked bool
	submit  bool
}

type museWidget struct {
	rows   []museWidgetRow
	cursor int
	text   string // collapsed widget text
	review bool
}

func museReadWidget(pane string) (museWidget, bool) {
	if musePendingUserInputError(pane) == nil {
		return museWidget{}, false
	}
	start := museQuestionWidgetStart(pane)
	if start < 0 {
		return museWidget{}, false
	}
	raw := pane[start:]
	w := museWidget{cursor: -1, text: museCollapse(raw)}
	w.review = strings.Contains(strings.ToLower(w.text), "review answers before submit")
	cursors := 0
	for _, line := range strings.Split(raw, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "enter to select") || strings.Contains(lower, "enter to toggle") || strings.Contains(line, "↑/↓") {
			break
		}
		m := museQuestionOptionRE.FindStringSubmatch(line)
		if m == nil {
			// An indented line after a row is that row's wrapped tail.
			if len(w.rows) > 0 && strings.TrimSpace(line) != "" && strings.TrimLeft(line, " ") != line {
				last := &w.rows[len(w.rows)-1]
				last.text = museCollapse(last.text + " " + line)
			}
			continue
		}
		row := museWidgetRow{text: museCollapse(m[3])}
		if state := museCheckboxStateRE.FindStringSubmatch(row.text); state != nil {
			row.box, row.checked = true, state[1] != " "
			row.text = strings.TrimSpace(row.text[len(state[0]):])
		} else if strings.HasPrefix(strings.ToLower(row.text), "submit") {
			row.submit = true
		}
		if m[1] != "" {
			w.cursor = len(w.rows)
			cursors++
		}
		w.rows = append(w.rows, row)
	}
	return w, len(w.rows) > 0 && cursors == 1
}

// museFindRow maps a structured option label to its unique widget row. The
// row may carry the description after the label, or be truncated with "…".
func museFindRow(w museWidget, label string) (int, error) {
	label = museCollapse(label)
	for i, row := range w.rows {
		if !row.submit && row.text == label {
			return i, nil
		}
	}
	found := -1
	for i, row := range w.rows {
		if row.submit {
			continue
		}
		match := row.text == label || strings.HasPrefix(row.text, label+" ")
		if cut, ok := strings.CutSuffix(row.text, "…"); ok && len(cut) >= 8 && strings.HasPrefix(label, strings.TrimSpace(cut)) {
			match = true
		}
		if !match {
			continue
		}
		if found >= 0 {
			return -1, fmt.Errorf("option %q matches more than one Muse row", label)
		}
		found = i
	}
	if found < 0 {
		return -1, fmt.Errorf("option %q is absent from the Muse widget", label)
	}
	return found, nil
}

// museWaitWidget polls the pane until the widget satisfies ready.
func museWaitWidget(ctx context.Context, session string, ready func(museWidget) bool) (museWidget, error) {
	for retry := 0; retry < 12; retry++ {
		pane, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			return museWidget{}, err
		}
		if w, ok := museReadWidget(pane); ok && ready(w) {
			return w, nil
		}
		select {
		case <-ctx.Done():
			return museWidget{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	// Structure only: never the question or option text.
	pane, _ := museTmuxCapturePane(ctx, session)
	start := museQuestionWidgetStart(pane)
	w, ok := museReadWidget(pane)
	cursors := 0
	if start >= 0 {
		for _, line := range strings.Split(pane[start:], "\n") {
			if m := museQuestionOptionRE.FindStringSubmatch(line); m != nil && m[1] != "" {
				cursors++
			}
		}
	}
	log.Printf("[MUSE_QUESTION] session=%s widget unreadable: pending=%v start=%d rows=%d cursors=%d review=%v question_on_screen=%v pane_lines=%d",
		session, musePendingUserInputError(pane) != nil, start, len(w.rows), cursors, w.review, ok, strings.Count(pane, "\n")+1)
	return museWidget{}, fmt.Errorf("Muse question widget changed")
}

func museMoveCursor(ctx context.Context, session string, from, to int) error {
	for pos := from; pos != to; {
		button := "Down"
		if pos > to {
			button = "Up"
			pos--
		} else {
			pos++
		}
		if err := museSendQuestionKey(ctx, session, button); err != nil {
			return err
		}
	}
	return nil
}

// museActivateRow moves to row, confirms the cursor landed on it for this
// question, and presses Enter.
func museActivateRow(ctx context.Context, session string, w museWidget, row int, question string) error {
	if err := museMoveCursor(ctx, session, w.cursor, row); err != nil {
		return err
	}
	if _, err := museWaitWidget(ctx, session, func(n museWidget) bool {
		return !n.review && n.cursor == row && strings.Contains(n.text, question)
	}); err != nil {
		return fmt.Errorf("Muse question changed during selection")
	}
	return museSendQuestionKey(ctx, session, "Enter")
}

func museAnswerQuestion(ctx context.Context, session string, q Question, a QuestionAnswer) error {
	question := museCollapse(q.Question)
	onQuestion := func(w museWidget) bool { return !w.review && strings.Contains(w.text, question) }
	w, err := museWaitWidget(ctx, session, onQuestion)
	if err != nil {
		return err
	}
	if !q.Multiple() {
		row, err := museFindRow(w, a.SelectedLabel)
		if err != nil {
			return err
		}
		return museActivateRow(ctx, session, w, row, question)
	}
	want := map[int]bool{}
	for _, label := range a.SelectedLabels {
		row, err := museFindRow(w, label)
		if err != nil {
			return err
		}
		want[row] = true
	}
	for row := range w.rows {
		if !w.rows[row].box || w.rows[row].checked == want[row] {
			continue
		}
		if err := museActivateRow(ctx, session, w, row, question); err != nil {
			return err
		}
		label := w.rows[row].text
		if w, err = museWaitWidget(ctx, session, func(n museWidget) bool {
			return onQuestion(n) && row < len(n.rows) && n.rows[row].checked == want[row]
		}); err != nil {
			return fmt.Errorf("Muse did not toggle %q", label)
		}
	}
	submit := -1
	for i, row := range w.rows {
		if row.submit {
			submit = i
		}
	}
	if submit < 0 {
		return fmt.Errorf("Muse submit row is missing")
	}
	return museActivateRow(ctx, session, w, submit, question)
}

func museValidateAnswer(q Question, a QuestionAnswer) error {
	known := map[string]int{}
	for _, option := range q.Options {
		known[option.Label]++
	}
	if !q.Multiple() {
		if known[a.SelectedLabel] != 1 {
			return fmt.Errorf("unknown option for %s", q.ID)
		}
		return nil
	}
	min, max := 1, len(q.Options)
	if q.Selection.MinSelections > 0 {
		min = q.Selection.MinSelections
	}
	if q.Selection.MaxSelections > 0 {
		max = q.Selection.MaxSelections
	}
	if len(a.SelectedLabels) < min || len(a.SelectedLabels) > max {
		return fmt.Errorf("choose %d to %d options for %s", min, max, q.ID)
	}
	seen := map[string]bool{}
	for _, label := range a.SelectedLabels {
		if known[label] != 1 || seen[label] {
			return fmt.Errorf("unknown option for %s", q.ID)
		}
		seen[label] = true
	}
	return nil
}

// SubmitQuestionAnswers accepts only labels from the latest outstanding
// structured prompt, then verifies each live widget before sending keys.
func SubmitQuestionAnswers(ctx context.Context, owner, promptID string, answers []QuestionAnswer) error {
	questionSubmissionMu.Lock()
	defer questionSubmissionMu.Unlock()
	if promptID == "" {
		return fmt.Errorf("prompt_id is required")
	}
	prompt, err := pendingQuestionPrompt(owner)
	if err != nil {
		return err
	}
	if prompt == nil || prompt.PromptID != promptID {
		return fmt.Errorf("Muse question is no longer pending")
	}
	if len(answers) != len(prompt.Questions) {
		return fmt.Errorf("answer every question")
	}
	for i, q := range prompt.Questions {
		if q.ID != answers[i].ID {
			return fmt.Errorf("question order changed")
		}
		if err := museValidateAnswer(q, answers[i]); err != nil {
			return err
		}
	}
	if s := museMSPSessionFor(owner); s != nil {
		return museMSPAnswer(ctx, s, promptID, answers)
	}
	key, err := musePersistentKey(owner)
	if err != nil {
		return err
	}
	musePersistentPool.Lock()
	entry := musePersistentPool.m[key]
	if entry == nil || !entry.userChoice || entry.nativeSessionID != prompt.NativeSessionID {
		musePersistentPool.Unlock()
		return fmt.Errorf("Muse choice session is unavailable")
	}
	session := entry.tmuxName
	musePersistentPool.Unlock()
	if !museTmuxSessionAlive(ctx, session) {
		return fmt.Errorf("Muse terminal is unavailable")
	}
	// A retry after a partial failure resumes at the question on screen;
	// earlier questions were already answered in the widget.
	startAt := 0
	if pane, err := museTmuxCapturePane(ctx, session); err == nil {
		if w, ok := museReadWidget(pane); ok {
			if w.review {
				startAt = len(prompt.Questions)
			}
			for i := len(prompt.Questions) - 1; i >= 0 && !w.review; i-- {
				if strings.Contains(w.text, museCollapse(prompt.Questions[i].Question)) {
					startAt = i
					break
				}
			}
		}
	}
	for i := startAt; i < len(prompt.Questions); i++ {
		if err := museAnswerQuestion(ctx, session, prompt.Questions[i], answers[i]); err != nil {
			return err
		}
	}
	// Multi-question prompts end at a review screen. Check every selected
	// label there before committing the answer.
	for retry := 0; retry < 12; retry++ {
		pane, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			return err
		}
		if musePendingUserInputError(pane) == nil {
			return nil
		}
		review, ok := museRecommendedQuestion(pane)
		if ok && review.review {
			text := museCollapse(pane[museQuestionWidgetStart(pane):])
			for _, a := range answers {
				labels := a.SelectedLabels
				if a.SelectedLabel != "" {
					labels = []string{a.SelectedLabel}
				}
				for _, label := range labels {
					if !strings.Contains(text, museCollapse(label)) {
						return fmt.Errorf("Muse review disagrees with selected answer")
					}
				}
			}
			if err := museMoveCursor(ctx, session, review.current, review.target); err != nil {
				return err
			}
			return museSendQuestionKey(ctx, session, "Enter")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("Muse answer review did not appear")
}

// PendingQuestion returns the outstanding structured prompt for owner, or nil.
func PendingQuestion(owner string) (*QuestionEvent, error) { return pendingQuestionPrompt(owner) }

func pendingQuestionPrompt(owner string) (*QuestionEvent, error) {
	events, err := NewQuestionReaderForOwner(owner).Poll()
	if err != nil {
		return nil, err
	}
	var prompt *QuestionEvent
	for i := range events {
		e := &events[i]
		if e.Kind == "user_input_prompt_requested" {
			prompt = e
		} else if prompt != nil && e.PromptID == prompt.PromptID {
			prompt = nil
		}
	}
	return prompt, nil
}

func museSendQuestionKey(ctx context.Context, session, key string) error {
	out, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", session, key).CombinedOutput()
	if err != nil {
		return fmt.Errorf("send Muse choice key: %w: %s", err, out)
	}
	return nil
}

// InterruptPendingQuestion presses Esc on owner's open question widget, which
// interrupts the Muse run. It is the last resort when not even the first
// option can be entered, so the chat is never left hanging on the widget.
func InterruptPendingQuestion(ctx context.Context, owner string) error {
	if MuseMSPSessionActive(owner) {
		return InterruptMuseMSP(ctx, owner)
	}
	key, err := musePersistentKey(owner)
	if err != nil {
		return err
	}
	musePersistentPool.Lock()
	entry := musePersistentPool.m[key]
	session := ""
	if entry != nil {
		session = entry.tmuxName
	}
	musePersistentPool.Unlock()
	if session == "" || !museTmuxSessionAlive(ctx, session) {
		return fmt.Errorf("Muse terminal is unavailable")
	}
	pane, err := museTmuxCapturePane(ctx, session)
	if err != nil {
		return err
	}
	if musePendingUserInputError(pane) == nil {
		return nil
	}
	return museSendQuestionKey(ctx, session, "Escape")
}
