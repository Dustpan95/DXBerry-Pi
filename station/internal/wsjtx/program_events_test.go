package wsjtx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
)

func eventView(t *testing.T, p *ProgramEvents, radio string) ProgramEventView {
	t.Helper()
	raw, ok := p.Snapshot(radio)
	var view ProgramEventView
	if !ok || json.Unmarshal(raw, &view) != nil || view.Version != 1 {
		t.Fatal("invalid event snapshot", string(raw))
	}
	return view
}

func TestProgramEventsExpireWithoutNewTrafficAndSeparateRadios(t *testing.T) {
	tr, clock, token := observation(t)
	acceptFixture(t, tr, token, "status-full-s3")
	acceptFixture(t, tr, token, "decode-full-s3")
	p := NewProgramEvents(func(_ context.Context, name string) (ProgramCollectionState, error) {
		snapshot, ok := tr.Snapshot(name)
		if !ok {
			return ProgramCollectionState{}, errors.New("private fixture error")
		}
		return ProgramCollectionState{Configured: true, Enabled: true, Observation: &snapshot}, nil
	}, func(string) bool { return true })
	p.SetWatch("radio1", 2)
	p.SetWatch("radio2", 1)
	updates, stop := p.Subscribe()
	defer stop()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-updates:
	case <-time.After(3 * time.Second):
		t.Fatal("event producer did not refresh watched radios")
	}
	first := eventView(t, p, "radio1")
	if !first.Available || first.State.Observation.Availability != Live || len(first.State.Observation.Decodes) != 1 {
		t.Fatal(first)
	}
	if other := eventView(t, p, "radio2"); other.Available || other.State != nil || other.Code != "unavailable" {
		t.Fatal("radio/error details crossed the event boundary", other)
	}
	clock.advance(FreshFor)
	deadline := time.After(3 * time.Second)
	for eventView(t, p, "radio1").State.Observation.Availability != Stale {
		select {
		case <-updates:
		case <-deadline:
			t.Fatal("idle expiry was not published")
		}
	}
	stale := eventView(t, p, "radio1").State.Observation
	if stale.Availability != Stale || stale.Status != nil || len(stale.Decodes) != 0 || stale.Epoch == first.State.Observation.Epoch {
		t.Fatal("idle expiry retained current readings", stale)
	}
}

func TestProgramEventsBoundHistoryAndKeepNewestCompleteDecodes(t *testing.T) {
	text := strings.Repeat("\x00", MaxTextSize)
	state := ProgramCollectionState{Observation: &Snapshot{Radio: "radio1", Availability: Live}}
	for i := 0; i < DecodeHistoryLimit; i++ {
		state.Observation.Decodes = append(state.Observation.Decodes, ReceivedDecode{Data: Decode{SNR: int32(i), Text: &text}})
	}
	raw := encodeProgramEvent("radio1", state)
	var view ProgramEventView
	if len(raw) > MaxProgramEventData || json.Unmarshal(raw, &view) != nil || !view.Available || view.DecodesOmitted == 0 {
		t.Fatal("unbounded or invalid event", len(raw), view)
	}
	history := view.State.Observation.Decodes
	if len(history)+view.DecodesOmitted != DecodeHistoryLimit || history[0].Data.SNR != int32(view.DecodesOmitted) || history[len(history)-1].Data.SNR != DecodeHistoryLimit-1 {
		t.Fatal("event did not retain the contiguous newest history")
	}
	for _, decode := range history {
		if decode.Data.Text == nil || *decode.Data.Text != text {
			t.Fatal("event truncated a decode field")
		}
	}
	if len(state.Observation.Decodes) != DecodeHistoryLimit {
		t.Fatal("wire budget modified the stored history")
	}
	if raw := encodeProgramEvent("radio2", state); bytes.Contains(raw, []byte("radio1")) || !bytes.Contains(raw, []byte("unavailable")) {
		t.Fatal("wrong-radio observation exposed", string(raw))
	}
}

func TestProgramEventsCoalesceSignalsAndDetachCachedBytes(t *testing.T) {
	var count uint64
	p := NewProgramEvents(func(context.Context, string) (ProgramCollectionState, error) {
		count++
		return ProgramCollectionState{Observation: &Snapshot{Radio: "radio1", Counters: Counters{Accepted: count}}}, nil
	}, func(string) bool { return true })
	p.SetWatch("radio1", 2)
	updates, stop := p.Subscribe()
	for i := 0; i < 100; i++ {
		p.refresh(context.Background())
	}
	if len(updates) != 1 || eventView(t, p, "radio1").State.Observation.Counters.Accepted != 100 {
		t.Fatal("slow subscriber retained a backlog or old state")
	}
	raw, _ := p.Snapshot("radio1")
	raw[0] = '!'
	if eventView(t, p, "radio1").State.Observation.Counters.Accepted != 100 {
		t.Fatal("snapshot bytes alias the cache")
	}
	stop()
	stop()
	p.refresh(context.Background())
	if len(p.subs) != 0 {
		t.Fatal("subscription was retained")
	}
}

func TestProgramEventsDoNotPublishAnOldWatchAfterReplacement(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	p := NewProgramEvents(func(context.Context, string) (ProgramCollectionState, error) {
		close(entered)
		<-release
		return ProgramCollectionState{Enabled: true}, nil
	}, func(string) bool { return true })
	p.SetWatch("radio1", 2)
	done := make(chan struct{})
	go func() { p.refresh(context.Background()); close(done) }()
	<-entered
	p.SetWatch("radio1", 0)
	p.SetWatch("radio1", 2)
	close(release)
	<-done
	if view := eventView(t, p, "radio1"); view.Code != "loading" || view.State != nil {
		t.Fatal("old watch overwrote the replacement", view)
	}
}

func TestProgramEventsBoundWatchesAndRefuseUnknownSources(t *testing.T) {
	calls := 0
	p := NewProgramEvents(func(context.Context, string) (ProgramCollectionState, error) {
		calls++
		return ProgramCollectionState{}, nil
	}, func(string) bool { return false })
	p.SetWatch("../radio1", 2)
	p.SetWatch("radio1", 9)
	for i := 0; i <= MaxSessions; i++ {
		p.SetWatch(fmt.Sprint("radio", i), 2)
	}
	p.refresh(context.Background())
	if len(p.watches) != MaxSessions || calls != 0 || eventView(t, p, fmt.Sprint("radio", MaxSessions)).Code != "capacity" {
		t.Fatal("unbounded watches or unknown-source read")
	}
	for i := 0; i < MaxSessions; i++ {
		p.SetWatch(fmt.Sprint("radio", i), 0)
	}
	p.refresh(context.Background())
	if len(p.watches) != 0 || calls != 0 {
		t.Fatal("unwatched producer retained work")
	}
}

func TestProgramEventsDefaultOffReadDoesNotInitializeOrCheckHardware(t *testing.T) {
	state := filepath.Join(t.TempDir(), "absent", "wsjtx")
	s, err := NewProgramService(ProgramServiceConfig{StatePath: state, Approvals: &settings.Store{Dir: filepath.Join(filepath.Dir(state), "radios")}, Eligible: func(context.Context, string) error {
		t.Fatal("event read reached hardware eligibility")
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := NewProgramEvents(s.CollectionState, func(string) bool { return true })
	p.SetWatch("radio1", 2)
	p.refresh(context.Background())
	view := eventView(t, p, "radio1")
	if !view.Available || view.State.Configured || view.State.Enabled || view.State.Observation != nil {
		t.Fatal("default-off read invented collection state", view)
	}
	if _, err := os.Lstat(filepath.Dir(state)); !os.IsNotExist(err) {
		t.Fatal("event read created station state", err)
	}
}

func TestProgramEventsBudgetClearsPreviouslyAvailableState(t *testing.T) {
	block := false
	p := NewProgramEvents(func(ctx context.Context, _ string) (ProgramCollectionState, error) {
		if block {
			<-ctx.Done()
		}
		return ProgramCollectionState{Enabled: true}, nil
	}, func(string) bool { return true })
	p.SetWatch("radio1", 2)
	p.refresh(context.Background())
	block = true
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	p.refresh(ctx)
	if view := eventView(t, p, "radio1"); view.Available || view.State != nil || view.Code != "unavailable" {
		t.Fatal("cancelled producer kept the previous state", view)
	}
}

func TestProgramEventsRunStopsWithoutWatchesOrLeakedReads(t *testing.T) {
	var calls atomic.Int32
	p := NewProgramEvents(func(context.Context, string) (ProgramCollectionState, error) {
		calls.Add(1)
		return ProgramCollectionState{}, nil
	}, func(string) bool { return true })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event producer did not stop")
	}
	if calls.Load() != 0 {
		t.Fatal("unwatched producer read collection state")
	}
}
