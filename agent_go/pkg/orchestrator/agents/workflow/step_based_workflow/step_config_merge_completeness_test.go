package step_based_workflow

import (
	"reflect"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-061/E4. MergeAgentConfigFields copied 19 of 28 fields. The nine it
// dropped included LockCode, KnowledgebaseAccess and KnowledgebaseContribution
// — all of which gate writes — so on the merge path a saved lock or KB grant
// never reached the runtime.
//
// The merge path runs whenever a step already carries in-memory AgentConfigs
// (step_config.go: the else branch of the full-assign). No workflow currently
// puts agent_configs inline in plan.json, so this was latent rather than live —
// but a silent write-gating failure is not something to leave armed.
//
// This test is reflection-based on purpose: the failure mode is *forgetting*,
// so a hand-maintained list of fields would rot the same way the merge did.
// Adding a field to AgentConfigs without a merge case fails here.
func TestMergeAgentConfigFieldsCoversEveryField(t *testing.T) {
	source := populatedAgentConfigs()
	target := &AgentConfigs{}

	MergeAgentConfigFields(target, source, "step-under-test", loggerv2.NewNoop())

	sv := reflect.ValueOf(*source)
	tv := reflect.ValueOf(*target)
	typ := sv.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		// Legacy migration inputs are normalized and cleared when step_config is
		// read. They must never propagate into the active runtime config.
		if strings.HasPrefix(field.Name, "Legacy") {
			continue
		}
		if tv.Field(i).IsZero() && !sv.Field(i).IsZero() {
			t.Errorf("MergeAgentConfigFields drops %s (json:%q) — a config saved in step_config.json will not reach the runtime on the merge path",
				field.Name, field.Tag.Get("json"))
		}
	}
}

// populatedAgentConfigs sets every exported field to a non-zero value so the
// reflection walk above can tell "dropped" from "legitimately empty".
func populatedAgentConfigs() *AgentConfigs {
	truth := true
	turns := 42
	fixes := 3
	cfg := &AgentConfigs{}
	v := reflect.ValueOf(cfg).Elem()
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		if !typ.Field(i).IsExported() {
			continue
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("x")
		case reflect.Int:
			f.SetInt(1)
		case reflect.Slice:
			f.Set(reflect.MakeSlice(f.Type(), 1, 1))
		case reflect.Ptr:
			switch f.Type().Elem().Kind() {
			case reflect.Bool:
				f.Set(reflect.ValueOf(&truth))
			case reflect.Int:
				if typ.Field(i).Name == "ScriptedMaxFixIter" {
					f.Set(reflect.ValueOf(&fixes))
				} else {
					f.Set(reflect.ValueOf(&turns))
				}
			default:
				f.Set(reflect.New(f.Type().Elem()))
			}
		}
	}
	// A tier value has to be one the merge will accept as meaningful.
	cfg.ExecutionTier = "medium"
	return cfg
}

// PLAT-061/E3. `case "transport":` had an empty body that returned success, so
// an agent clearing it was told the field was cleared when no such field
// existed. `learning_mode`, `learnings_write_method` and
// `knowledgebase_write_method` were likewise accepted as clearable with nothing
// behind them — and the answer differed depending on whether AgentConfigs was
// nil.
//
// They cannot become hard errors: three workflows still carry these keys and
// some guidance referenced them, so a caller following stale instructions would
// fail its entire update call. They are acknowledged no-ops instead.
func TestRetiredClearFieldsAreAcknowledgedNotSilentlySucceeded(t *testing.T) {
	sc := &StepConfig{AgentConfigs: &AgentConfigs{}}
	for _, name := range []string{
		"transport", "learning_mode", "learnings_write_method",
		"knowledgebase_write_method", "db_access", "disable_tier_optimization",
		"enable_context_offloading", "todo_task_orchestrator_tier",
	} {
		if clearStepConfigField(sc, name) {
			t.Errorf("%q reported a successful clear, but no such field exists", name)
		}
		if isKnownAgentConfigClearField(name) {
			t.Errorf("%q is still advertised as a live clearable field", name)
		}
		why, retired := isRetiredStepConfigClearField(name)
		if !retired {
			t.Errorf("%q is neither live nor retired — it would surface as an unknown-field error and fail the whole call", name)
		}
		if why == "" {
			t.Errorf("%q is retired but carries no explanation for the caller", name)
		}
	}

	// A genuinely unknown name must still be an error — the no-op path is for
	// names that were real, not for typos.
	if _, retired := isRetiredStepConfigClearField("not_a_field_at_all"); retired {
		t.Error("an arbitrary name was treated as a retired field")
	}
}

// PLAT-061. learn_code_max_fix_iterations is retired: every value ever stored
// was a migration artifact rather than a judgment. The migration defaulted
// retries to 0 and only raised it when a legacy agent item declared
// repair_with_llm, so five hetznerssh steps carried 0 — silently disabling
// script repair — because their legacy items happened to lack a field, not
// because anyone decided fail-fast was right there.
//
// Migrated scripted steps must now inherit the uniform default. lock_code stays
// the deliberate way to skip the fix loop.
func TestMigrationNoLongerDerivesAFixIterationCount(t *testing.T) {
	if _, retired := isRetiredStepConfigClearField("learn_code_max_fix_iterations"); !retired {
		t.Error("learn_code_max_fix_iterations is not registered as retired, so clearing it would fail the whole update call")
	}
	// The struct field is gone; this compiles only while it stays gone.
	cfg := &AgentConfigs{}
	if v := reflect.ValueOf(*cfg).FieldByName("ScriptedMaxFixIter"); v.IsValid() {
		t.Error("ScriptedMaxFixIter is back on AgentConfigs — the migration artifact can be written again")
	}
}
