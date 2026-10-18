package schedulepolicy

import "testing"

func TestPulseStampUsesOnlyCanonicalAfterRun(t *testing.T) {
	for _, raw := range []string{`{"schedules":[{"enabled":false}]}`, `{"schedules":[{"after_run":{"notify":true}}]}`} {
		if err := ValidatePulseStamp([]byte(raw), ExplicitPulseContractVersion); err == nil {
			t.Fatalf("incomplete settings stamped: %s", raw)
		}
	}
	for _, raw := range []string{`{"schedules":[]}`, `{"schedules":[{"enabled":false,"after_run":{"backup":true,"publish":false,"notify":true}},{"schedule_type":"calendar","after_run":{"backup":false,"publish":false,"notify":false}}]}`, `{"schedules":[{"schedule_type":"webhook"}]}`} {
		if err := ValidatePulseStamp([]byte(raw), ExplicitPulseContractVersion); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPulseContractVersionBoundary(t *testing.T) {
	for _, version := range []string{"", "1.0.40", "invalid"} {
		if RequiresExplicitPulse(version) {
			t.Errorf("unexpected new contract: %s", version)
		}
	}
	for _, version := range []string{"1.0.41", "1.0.42", "1.1.0", "2.0.0"} {
		if !RequiresExplicitPulse(version) {
			t.Errorf("missed contract: %s", version)
		}
	}
}
