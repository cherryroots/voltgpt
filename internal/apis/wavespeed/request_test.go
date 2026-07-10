package wavespeed

import (
	"context"
	"errors"
	"testing"
)

func TestWaitForComplete_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := WaitForComplete(ctx, "request-id")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForComplete() error = %v, want context.Canceled", err)
	}
}
