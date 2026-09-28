package sealbox

import (
	"bytes"
	"testing"
)

func TestSealOpenBindsKeyAndAAD(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	sealed, err := Seal(key, "value", []byte("place-a"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Open(key, sealed, []byte("place-a")); err != nil || got != "value" {
		t.Fatalf("open = %q %v", got, err)
	}
	if _, err := Open(key, sealed, []byte("place-b")); err == nil {
		t.Fatal("opened with another AAD")
	}
	if _, err := Open(bytes.Repeat([]byte{8}, 32), sealed, []byte("place-a")); err == nil {
		t.Fatal("opened with another key")
	}
}
