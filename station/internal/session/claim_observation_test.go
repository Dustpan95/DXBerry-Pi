package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
)

func observeClaim(t *testing.T, o *Owners, radio, id string) TabClaimObservation {
	t.Helper()
	observation, ok := o.ObserveTabClaim(radio, id)
	if !ok || observation.generation == 0 || !o.TabClaimCurrent(observation) {
		t.Fatal("current tab claim unavailable", observation, ok)
	}
	return observation
}

func TestTabClaimObservationPreservesContinuousRenewal(t *testing.T) {
	c := newClock()
	o := NewOwners(c)
	if err := o.Claim("radio1", tab("login|tab")); err != nil {
		t.Fatal(err)
	}
	original := observeClaim(t, o, "radio1", "login|tab")
	c.Advance(IdleLimit - time.Second)
	o.Touch("radio1", "stranger")
	if !o.TabClaimCurrent(original) {
		t.Fatal("stranger touch invalidated ownership")
	}
	o.Touch("radio1", "login|tab")
	c.Advance(IdleLimit - time.Second)
	if !o.TabClaimCurrent(original) {
		t.Fatal("touch replaced continuous identity")
	}
	if err := o.Claim("radio1", Holder{Kind: "tab", ID: "login|tab", Label: "new display label"}); err != nil {
		t.Fatal(err)
	}
	if err := o.TakeOver("radio1", tab("login|tab")); err != nil {
		t.Fatal(err)
	}
	if renewed := observeClaim(t, o, "radio1", "login|tab"); original != renewed {
		t.Fatal("same holder renewal changed generation")
	}
	if err := o.Claim("radio1", tab("other")); !errors.Is(err, ErrHeld) {
		t.Fatal(err)
	}
	o.SetTransmitting(func(string) bool { return true })
	if err := o.TakeOver("radio1", tab("other")); !errors.Is(err, ErrTransmitting) {
		t.Fatal(err)
	}
	o.Release("radio1", "other")
	if !o.TabClaimCurrent(original) {
		t.Fatal("refused or unrelated operation replaced claim")
	}
}

func TestTabClaimObservationRejectsReleaseReclaimAndTakeoverABA(t *testing.T) {
	for _, action := range []string{"release", "release-all", "last-socket", "idle-expiry", "takeover-away-and-back", "holder-kind"} {
		t.Run(action, func(t *testing.T) {
			c := newClock()
			o := NewOwners(c)
			if err := o.Claim("radio1", tab("a")); err != nil {
				t.Fatal(err)
			}
			before := observeClaim(t, o, "radio1", "a")
			switch action {
			case "release":
				o.Release("radio1", "a")
			case "release-all":
				o.ReleaseAll("a")
			case "last-socket":
				o.SocketOpened("a")
				o.SocketClosed("a")
			case "idle-expiry":
				c.Advance(IdleLimit)
			case "takeover-away-and-back":
				if err := o.TakeOver("radio1", tab("b")); err != nil {
					t.Fatal(err)
				}
				if err := o.TakeOver("radio1", tab("a")); err != nil {
					t.Fatal(err)
				}
			case "holder-kind":
				if err := o.Claim("radio1", Holder{Kind: "program", ID: "a"}); err != nil {
					t.Fatal(err)
				}
			}
			if o.TabClaimCurrent(before) {
				t.Fatal("old lifecycle remained current")
			}
			if err := o.Claim("radio1", tab("a")); err != nil {
				t.Fatal(err)
			}
			after := observeClaim(t, o, "radio1", "a")
			if after.generation <= before.generation || o.TabClaimCurrent(before) {
				t.Fatal("generation was reused")
			}
		})
	}
}

func TestTabClaimObservationDoesNotRenewExpireOrRunDrop(t *testing.T) {
	c := newClock()
	o := NewOwners(c)
	var dropped atomic.Int32
	o.OnDrop(func(string) { dropped.Add(1) })
	if err := o.Claim("radio1", tab("a")); err != nil {
		t.Fatal(err)
	}
	original := observeClaim(t, o, "radio1", "a")
	c.Advance(IdleLimit - time.Nanosecond)
	observeClaim(t, o, "radio1", "a")
	c.Advance(time.Nanosecond)
	if _, ok := o.ObserveTabClaim("radio1", "a"); ok || o.TabClaimCurrent(original) {
		t.Fatal("expired claim accepted or observation renewed it")
	}
	o.mu.Lock()
	_, stored := o.claims["radio1"]
	o.mu.Unlock()
	if !stored || dropped.Load() != 0 {
		t.Fatal("read observation changed ownership or ran OnDrop")
	}
	if _, held := o.Holder("radio1"); held || dropped.Load() != 1 {
		t.Fatal("ordinary owner expiry behavior changed")
	}
}

func TestTabClaimObservationIsBoundToOwnersRadioAndTab(t *testing.T) {
	o := NewOwners(newClock())
	other := NewOwners(newClock())
	for _, owners := range []*Owners{o, other} {
		for _, radio := range []string{"radio1", "radio2"} {
			if err := owners.Claim(radio, tab("a")); err != nil {
				t.Fatal(err)
			}
		}
	}
	original := observeClaim(t, o, "radio1", "a")
	if other.TabClaimCurrent(original) || o.TabClaimCurrent(TabClaimObservation{}) {
		t.Fatal("foreign or empty observation accepted")
	}
	if original == observeClaim(t, o, "radio2", "a") {
		t.Fatal("different radios shared identity")
	}
	for _, id := range []string{"", "b"} {
		if _, ok := o.ObserveTabClaim("radio1", id); ok {
			t.Fatal("wrong tab accepted", id)
		}
	}
	if _, ok := o.ObserveTabClaim("missing", "a"); ok {
		t.Fatal("missing claim accepted")
	}
	if _, ok := (*Owners)(nil).ObserveTabClaim("radio1", "a"); ok || (*Owners)(nil).TabClaimCurrent(original) {
		t.Fatal("nil owner accepted")
	}
	o.ReleaseAll("a")
	if o.TabClaimCurrent(original) {
		t.Fatal("release-all failed to invalidate")
	}
}

func TestTabClaimObservationRejectsClockReversal(t *testing.T) {
	c := newClock()
	o := NewOwners(c)
	if err := o.Claim("radio1", tab("a")); err != nil {
		t.Fatal(err)
	}
	before := observeClaim(t, o, "radio1", "a")
	c.Advance(-time.Nanosecond)
	if _, ok := o.ObserveTabClaim("radio1", "a"); ok || o.TabClaimCurrent(before) {
		t.Fatal("negative claim age accepted")
	}
}

func TestTouchCannotReviveOldObservationAfterIdleOrClockReversal(t *testing.T) {
	for _, advance := range []time.Duration{IdleLimit, -time.Nanosecond} {
		t.Run(advance.String(), func(t *testing.T) {
			c := newClock()
			o := NewOwners(c)
			if err := o.Claim("radio1", tab("a")); err != nil {
				t.Fatal(err)
			}
			before := observeClaim(t, o, "radio1", "a")
			c.Advance(advance)
			if o.TabClaimCurrent(before) {
				t.Fatal("discontinuous claim accepted")
			}
			o.Touch("radio1", "a")
			if o.TabClaimCurrent(before) {
				t.Fatal("Touch revived evidence for a discontinuous claim")
			}
			observeClaim(t, o, "radio1", "a")
		})
	}
}

func TestTabClaimObservationIsNotProgramOrTransmitEvidence(t *testing.T) {
	o := NewOwners(newClock())
	if err := o.Claim("radio1", tab("a")); err != nil {
		t.Fatal(err)
	}
	before := observeClaim(t, o, "radio1", "a")
	p := NewPrograms(func() []radios.Radio { return []radios.Radio{{Name: "radio1", Owner: "wsjtx"}} }, func(context.Context, string) (bool, string, error) { return true, "WSJT-X", nil })
	p.Refresh(context.Background())
	o.SetPrograms(p)
	o.SetTransmitting(func(string) bool { return true })
	if holder, held := o.Holder("radio1"); !held || holder.Kind != "program" {
		t.Fatal(holder, held)
	}
	if !o.TabClaimCurrent(before) {
		t.Fatal("local claim identity silently became program/PTT authorization")
	}
	// The fresh program and radio/PTT checks are separate required evidence.
	// Reading this local record must neither call those sources nor change them.
	if err := o.Claim("radio1", tab("b")); !errors.Is(err, ErrProgramOwns) {
		t.Fatal("normal program exclusion changed", err)
	}
}

func TestClaimGenerationExhaustionDoesNotReuseIdentityOrReplaceHolder(t *testing.T) {
	o := NewOwners(newClock())
	if err := o.Claim("radio1", tab("a")); err != nil {
		t.Fatal(err)
	}
	before := observeClaim(t, o, "radio1", "a")
	o.mu.Lock()
	o.nextClaim = ^uint64(0)
	o.mu.Unlock()
	if err := o.TakeOver("radio1", tab("b")); !errors.Is(err, ErrClaimUnavailable) {
		t.Fatal(err)
	}
	if !o.TabClaimCurrent(before) {
		t.Fatal("failed allocation replaced holder")
	}
	if err := o.Claim("radio1", tab("a")); err != nil {
		t.Fatal("continuous holder cannot renew", err)
	}
	o.Release("radio1", "a")
	if err := o.Claim("radio1", tab("a")); !errors.Is(err, ErrClaimUnavailable) {
		t.Fatal(err)
	}
	if _, ok := o.ObserveTabClaim("radio1", "a"); ok {
		t.Fatal("exhausted generation reused")
	}
}

func TestConcurrentClaimsHaveDistinctGenerations(t *testing.T) {
	o := NewOwners(newClock())
	const count = 100
	results := make(chan TabClaimObservation, count)
	errors := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			radio := fmt.Sprintf("r%d", i)
			if err := o.Claim(radio, tab("a")); err != nil {
				errors <- err
				return
			}
			observation, ok := o.ObserveTabClaim(radio, "a")
			if !ok {
				errors <- fmt.Errorf("no claim for %s", radio)
				return
			}
			results <- observation
		}(i)
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	seen := map[uint64]bool{}
	for observation := range results {
		if seen[observation.generation] || !o.TabClaimCurrent(observation) {
			t.Fatal("duplicate or invalid claim generation")
		}
		seen[observation.generation] = true
	}
	if len(seen) != count {
		t.Fatal(len(seen))
	}
}
