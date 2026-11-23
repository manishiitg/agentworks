package musecli

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// PLAT-872-119: a large chat whose serve could not start or reopen in time showed "Try this message again", which
// failed the same way every turn. The person is told to start a new chat; the app re-reads the error's text.
func TestMuseReopenTimeoutTellsToStartNewChat(t *testing.T) {
	err := museReopenError("01a1d43a-0000-7000-8000-000000000000", fmt.Errorf("initialize: %w", context.DeadlineExceeded))
	display := llmerrors.ProviderDisplay(errors.New(err.Error()))
	if llmerrors.KindOf(err) != llmerrors.KindContextTooLong || display.NextStep != "Start a new chat with a shorter conversation." {
		t.Fatalf("kind %s, next step %q", llmerrors.KindOf(err), display.NextStep)
	}
	if fresh := museReopenError("", context.DeadlineExceeded); llmerrors.KindOf(fresh) == llmerrors.KindContextTooLong {
		t.Fatal("a new chat's start timeout was blamed on its history")
	}
}
