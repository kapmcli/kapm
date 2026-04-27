package cli

import (
	"bufio"
	"context"
	"io"
	"testing"
	"time"
)

func TestReadLineCtx_CancelledCtxReturnsCtxErr(t *testing.T) {
	// Use a pipe so ReadString blocks indefinitely (no data written).
	pr, _ := io.Pipe()
	t.Cleanup(func() { _ = pr.Close() })

	p := &Prompter{r: bufio.NewReader(pr), w: io.Discard}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	start := time.Now()
	_, err := p.readLineCtx(ctx)
	if err != context.Canceled {
		t.Fatalf("readLineCtx() error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Errorf("readLineCtx() took %v, want < 100ms", elapsed)
	}
}
