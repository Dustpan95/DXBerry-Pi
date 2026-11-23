package cli

import (
	"context"
	"testing"
)

func TestProgramGenerationHelperIsNotABrowserCLI(t *testing.T) {
	calls := 0
	c := New("/trusted/bin", func(context.Context, string, []string, string, []string) (Result, error) {
		calls++
		return Result{}, nil
	})
	for _, args := range [][]string{nil, {"advance"}, {"--state", "/private/state", "advance"}, {"--state", "/private/state", "prepare-advance"}, {"--json"}} {
		if _, err := c.Run(context.Background(), Call{"dxberry-program-state", args, ""}); err == nil {
			t.Fatalf("root generation helper allowed: %q", args)
		}
	}
	if calls != 0 {
		t.Fatal("browser request reached root helper")
	}
}
