package utils

import "testing"

func TestTokenCounterSharesEncodingAcrossInstances(t *testing.T) {
	const encodingName = "cl100k_base"

	encodingCache.Delete(encodingName)

	first, err := getCachedEncoding(encodingName)
	if err != nil {
		t.Fatalf("getCachedEncoding first call: %v", err)
	}
	second, err := getCachedEncoding(encodingName)
	if err != nil {
		t.Fatalf("getCachedEncoding second call: %v", err)
	}
	if first != second {
		t.Fatalf("expected cached encoding pointer to be reused")
	}

	counterA := NewTokenCounter()
	counterB := NewTokenCounter()
	countA, err := counterA.CountTokensForProvider("hello world", "anthropic", "claude-sonnet-4")
	if err != nil {
		t.Fatalf("counterA CountTokensForProvider: %v", err)
	}
	countB, err := counterB.CountTokensForProvider("hello world", "anthropic", "claude-sonnet-4")
	if err != nil {
		t.Fatalf("counterB CountTokensForProvider: %v", err)
	}
	if countA != countB || countA == 0 {
		t.Fatalf("expected equal non-zero token counts, got %d and %d", countA, countB)
	}
}
