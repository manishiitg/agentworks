package server

import "testing"

func TestFormatCrewSyncBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1468006, "1.4 MB"},
	}
	for _, tc := range cases {
		if got := formatCrewSyncBytes(tc.in); got != tc.want {
			t.Errorf("formatCrewSyncBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
