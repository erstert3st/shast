package verify

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"shast/internal/catalog"
	"shast/internal/sandbox"
)

// TestVerifyCatalog runs the real verification of the embedded catalog
// against the sandbox image. It skips without Docker or image and with
// -short.
func TestVerifyCatalog(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped with -short")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	docker, err := sandbox.Connect(ctx)
	cancel()
	if err != nil {
		t.Skipf("docker not available: %v", err)
	}
	defer docker.Close()
	var missing *sandbox.ImageMissingError
	if err := docker.Preflight(t.Context()); errors.As(err, &missing) {
		t.Skipf("%v", err)
	} else if err != nil {
		t.Fatal(err)
	}

	cs, err := catalog.Default()
	if err != nil {
		t.Fatal(err)
	}
	newSession := func(ctx context.Context) (Session, error) {
		return docker.StartSession(ctx, sandbox.Options{})
	}
	outcomes, err := Verify(t.Context(), cs, 1, newSession, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outcomes {
		if !o.Passed() {
			t.Errorf("%s: %v", o.Challenge.ID, o.Problems)
		}
	}
}
