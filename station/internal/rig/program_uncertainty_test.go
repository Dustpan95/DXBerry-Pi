package rig

import (
	"context"
	"errors"
	"testing"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
)

func TestProgramUncertaintyRefusesNewCarrierButKeepsStopAvailable(t *testing.T) {
	h := newIxHarness(ixCaps())
	ctx := context.Background()
	failed := false
	programs := session.NewPrograms(func() []radios.Radio { return []radios.Radio{{Name: "radio1", Owner: "wsjtx"}} }, func(context.Context, string) (bool, string, error) {
		if failed {
			return false, "", errors.New("cannot verify program")
		}
		return false, "WSJT-X", nil
	})
	h.own.SetPrograms(programs)
	programs.Refresh(ctx)
	if err := h.own.Claim("radio1", ixHolder("a")); err != nil {
		t.Fatal(err)
	}
	if result := h.in.Tune(ctx, "a", true); !result.OK {
		t.Fatal(result)
	}
	failed = true
	programs.Refresh(ctx)
	keyOns := h.count("set_ptt 1")
	if result := h.in.Tune(ctx, "a", true); result.OK {
		t.Fatal("uncertainty permitted key-on")
	}
	if h.count("set_ptt 1") != keyOns {
		t.Fatal("refusal sent another key-on")
	}
	if result := h.in.Stop(ctx); !result.OK {
		t.Fatal("ownership uncertainty blocked STOP", result)
	}
	if h.rig.State().PTT || !h.rig.has("set_ptt 0") {
		t.Fatal("STOP did not unkey")
	}
	failed = false
	programs.Refresh(ctx)
	if result := h.in.Tune(ctx, "a", true); !result.OK {
		t.Fatal("verified stopped recovery did not restore the existing tab", result)
	}
	h.in.Stop(ctx)
}
