package tmuxsize

import "testing"

func TestSizeDefaultsAndBounds(t *testing.T) {
	t.Setenv(EnvColumns, "")
	t.Setenv(EnvRows, "")
	columns, rows := Size()
	if columns != DefaultColumns || rows != DefaultRows {
		t.Fatalf("default size = %dx%d, want %dx%d", columns, rows, DefaultColumns, DefaultRows)
	}

	t.Setenv(EnvColumns, "200")
	t.Setenv(EnvRows, "60")
	columns, rows = Size()
	if columns != 200 || rows != 60 {
		t.Fatalf("custom size = %dx%d, want 200x60", columns, rows)
	}

	t.Setenv(EnvColumns, "20")
	t.Setenv(EnvRows, "4")
	columns, rows = Size()
	if columns != minColumns || rows != minRows {
		t.Fatalf("min bounded size = %dx%d, want %dx%d", columns, rows, minColumns, minRows)
	}
}
