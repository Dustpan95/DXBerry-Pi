package cli

import "testing"

func TestCaptureDiagnosticsRemainManual(t *testing.T) {
	c := newTest(t)
	for _, args := range [][]string{{"--json", "--capture"}, {"--json", "--capture", "--previous-boot"}} {
		if c.Validate(Call{Command: "dxberry-status", Args: args}) == nil {
			t.Fatalf("manual diagnostics unexpectedly exposed through the web CLI: %v", args)
		}
	}
}
