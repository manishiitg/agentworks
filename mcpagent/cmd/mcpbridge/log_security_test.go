package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestBridgeLogfEscapesRecordSeparators(t *testing.T) {
	var out bytes.Buffer
	writer, flags := log.Writer(), log.Flags()
	defer func() { log.SetOutput(writer); log.SetFlags(flags) }()
	log.SetOutput(&out)
	log.SetFlags(0)
	bridgeLogf("tool=%s error=%v", "safe\nforged\rrecord\u2028extra\u2029line", "bad\nerror")
	got := out.String()
	if strings.Count(got, "\n") != 1 || strings.ContainsAny(got, "\r\u2028\u2029") {
		t.Fatalf("log contains injected record separators: %q", got)
	}
	if !strings.Contains(got, `safe\nforged\rrecord\u2028extra\u2029line`) {
		t.Fatalf("escaped values missing: %q", got)
	}
}
