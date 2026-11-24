package wsjtx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type observationClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *observationClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *observationClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func observation(t *testing.T) (*Tracker, *observationClock, Token) {
	t.Helper()
	c := &observationClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	tr := NewTracker(c)
	token, err := tr.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	return tr, c, token
}

func acceptFixture(t *testing.T, tr *Tracker, token Token, name string) {
	t.Helper()
	if err := tr.Accept(token, fixture(t, name)); err != nil {
		t.Fatal(err)
	}
}

func snapshot(t *testing.T, tr *Tracker, radio string) Snapshot {
	t.Helper()
	s, ok := tr.Snapshot(radio)
	if !ok {
		t.Fatalf("missing %s", radio)
	}
	return s
}

func TestTrackerWaitsWithoutInventingAReading(t *testing.T) {
	tr, c, token := observation(t)
	if _, ok := tr.Snapshot("absent"); ok {
		t.Fatal("invented a radio")
	}
	s := snapshot(t, tr, "radio1")
	if s.Availability != Waiting || s.LastSeen != nil || s.StatusSeenAt != nil || s.Schema != 0 || s.Epoch != 1 || s.Generation == 0 || s.InstanceID != fixtureID {
		t.Fatalf("unexpected new observation: %+v", s)
	}
	acceptFixture(t, tr, token, "heartbeat-s3")
	s = snapshot(t, tr, "radio1")
	if s.Availability != Live || s.Status != nil || s.StatusSeenAt != nil || len(s.Decodes) != 0 || s.LastSeen == nil || !s.LastSeen.Equal(c.Now()) {
		t.Fatalf("heartbeat invented a reading: %+v", s)
	}
	value(t, s.Heartbeat.Version, "3.0.2")
}

func TestTrackerKeepsStatusTimeSeparateFromLiveness(t *testing.T) {
	tr, c, token := observation(t)
	start := c.Now()
	acceptFixture(t, tr, token, "status-full-s3")
	acceptFixture(t, tr, token, "decode-full-s3")
	c.advance(30 * time.Second)
	acceptFixture(t, tr, token, "heartbeat-s3")
	s := snapshot(t, tr, "radio1")
	value(t, s.Status.Mode, "FT8")
	if !s.StatusSeenAt.Equal(start) || !s.LastSeen.Equal(c.Now()) || len(s.Decodes) != 1 || !s.Decodes[0].FirstSeen.Equal(start) {
		t.Fatalf("timestamps lost their meaning: %+v", s)
	}
	value(t, s.Decodes[0].Data.TimeMS, uint32(86399999))
	if !s.Decodes[0].SeenLive || s.Decodes[0].SeenReplay {
		t.Fatal("wrong arrival classification")
	}
}

func TestTrackerExpiryAndReconnectCannotReviveOldReadings(t *testing.T) {
	tr, c, token := observation(t)
	acceptFixture(t, tr, token, "heartbeat-s3")
	acceptFixture(t, tr, token, "status-full-s3")
	acceptFixture(t, tr, token, "decode-full-s3")
	last := c.Now()
	c.advance(FreshFor - time.Nanosecond)
	if snapshot(t, tr, "radio1").Availability != Live {
		t.Fatal("expired too early")
	}
	c.advance(time.Nanosecond)
	s := snapshot(t, tr, "radio1")
	if s.Availability != Stale || s.Epoch != 2 || s.Counters.Expirations != 1 || s.Status != nil || s.StatusSeenAt != nil || s.Heartbeat != nil || s.Schema != 0 || len(s.Decodes) != 0 || !s.LastSeen.Equal(last) {
		t.Fatalf("stale data still current: %+v", s)
	}
	c.advance(FreshFor)
	if s := snapshot(t, tr, "radio1"); s.Epoch != 2 || s.Counters.Expirations != 1 {
		t.Fatal("expired the same gap twice")
	}
	acceptFixture(t, tr, token, "heartbeat-s2")
	s = snapshot(t, tr, "radio1")
	if s.Availability != Live || s.Generation != token.generation || s.Epoch != 2 || s.Status != nil || len(s.Decodes) != 0 || !s.LastSeen.Equal(c.Now()) {
		t.Fatalf("reconnect revived old state: %+v", s)
	}
}

func TestTrackerRejectedTrafficDoesNotExtendLiveness(t *testing.T) {
	tr, c, token := observation(t)
	acceptFixture(t, tr, token, "status-full-s3")
	first := c.Now()
	c.advance(FreshFor - time.Nanosecond)
	wrong := bytes.Replace(fixture(t, "heartbeat-s3"), []byte(fixtureID), []byte("WSJT-X - radio2"), 1)
	for _, tc := range []struct {
		b   []byte
		err error
	}{
		{nil, ErrMalformed},
		{make([]byte, MaxDatagramSize+1), ErrTooLarge},
		{replace32(fixture(t, "heartbeat-s3"), 4, 4), ErrUnsupportedSchema},
		{fixture(t, "ignored"), ErrIgnoredType},
		{wrong, ErrIdentity},
	} {
		if err := tr.Accept(token, tc.b); !errors.Is(err, tc.err) {
			t.Fatalf("got %v, want %v", err, tc.err)
		}
	}
	s := snapshot(t, tr, "radio1")
	if !s.LastSeen.Equal(first) || s.Counters != (Counters{Accepted: 1, Malformed: 1, Oversized: 1, UnsupportedSchema: 1, IgnoredType: 1, WrongIdentity: 1}) {
		t.Fatalf("rejected traffic refreshed state: %+v", s)
	}
	c.advance(time.Nanosecond)
	// Expiry is checked on failed reception too, not only when somebody reads.
	if err := tr.Accept(token, nil); !errors.Is(err, ErrMalformed) {
		t.Fatal(err)
	}
	s = snapshot(t, tr, "radio1")
	if s.Availability != Stale || s.Status != nil || s.Counters.Expirations != 1 {
		t.Fatalf("failed traffic kept state alive: %+v", s)
	}
}

func TestTrackerTwoRadiosAreIndependent(t *testing.T) {
	tr, c, one := observation(t)
	two, err := tr.Start("radio2", "WSJT-X - radio2")
	if err != nil {
		t.Fatal(err)
	}
	acceptFixture(t, tr, one, "status-full-s3")
	c.advance(20 * time.Second)
	b := bytes.Replace(fixture(t, "heartbeat-s3"), []byte(fixtureID), []byte("WSJT-X - radio2"), 1)
	if err := tr.Accept(two, b); err != nil {
		t.Fatal(err)
	}
	if err := tr.Accept(one, b); !errors.Is(err, ErrIdentity) {
		t.Fatal(err)
	}
	c.advance(FreshFor - 20*time.Second)
	all := tr.Snapshots()
	if len(all) != 2 || all[0].Radio != "radio1" || all[1].Radio != "radio2" || all[0].Availability != Stale || all[1].Availability != Live || all[1].Status != nil || all[1].Counters.WrongIdentity != 0 {
		t.Fatalf("cross-radio state: %+v", all)
	}
}

func TestTrackerHistoryIsBoundedAndOldestIsEvicted(t *testing.T) {
	tr, _, token := observation(t)
	for i := 0; i < DecodeHistoryLimit+3; i++ {
		b := replace32(fixture(t, "decode-full-s3"), fixtureHeaderSize+17, uint32(i))
		if err := tr.Accept(token, b); err != nil {
			t.Fatal(err)
		}
	}
	s := snapshot(t, tr, "radio1")
	if len(s.Decodes) != DecodeHistoryLimit || s.Decodes[0].Data.DeltaFrequency != 3 || s.Decodes[len(s.Decodes)-1].Data.DeltaFrequency != uint32(DecodeHistoryLimit+2) || s.Counters.Evicted != 3 || s.Counters.Duplicates != 0 {
		t.Fatalf("wrong history bounds: %+v", s.Counters)
	}
}

func TestTrackerOlderAndFutureArrivalsCannotReplaceCurrentStatus(t *testing.T) {
	tr, clock, token := observation(t)
	first := clock.Now()
	acceptFixture(t, tr, token, "status-full-s3")
	clock.advance(time.Second)
	acceptFixture(t, tr, token, "heartbeat-s3")
	old := parsed(t, "status-full-s3")
	old.Status.DialFrequency = 7074000
	for _, at := range []time.Time{first, clock.Now().Add(time.Second)} {
		if err := tr.accept(token, old, nil, at); !errors.Is(err, ErrLateDatagram) {
			t.Fatal(err)
		}
	}
	s := snapshot(t, tr, "radio1")
	if s.Status.DialFrequency != 14074000 || !s.LastSeen.Equal(clock.Now()) || !s.StatusSeenAt.Equal(first) || s.Counters.Late != 2 || s.Counters.Accepted != 2 {
		t.Fatal("an out-of-order arrival changed the current observation")
	}
}

func TestTrackerIdenticalInstanceIDsStillHaveSeparateRadioState(t *testing.T) {
	tr, _, one := observation(t)
	two, err := tr.Start("radio2", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	acceptFixture(t, tr, one, "status-full-s3")
	acceptFixture(t, tr, two, "decode-full-s3")
	first, second := snapshot(t, tr, "radio1"), snapshot(t, tr, "radio2")
	if first.Status == nil || len(first.Decodes) != 0 || second.Status != nil || len(second.Decodes) != 1 {
		t.Fatal("instance ID was used as a shared radio identity")
	}
	if !tr.Remove(one) || len(tr.Snapshots()) != 1 || snapshot(t, tr, "radio2").Generation != two.generation {
		t.Fatal("removing a same-ID instance affected the other radio")
	}
}

func TestTrackerLiveDuplicatesAndReplaysShareOneEvent(t *testing.T) {
	for _, replayFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(replayFirst), func(t *testing.T) {
			tr, c, token := observation(t)
			live := fixture(t, "decode-full-s3")
			replay := bytes.Clone(live)
			replay[fixtureHeaderSize] = 0
			first, second := live, replay
			if replayFirst {
				first, second = replay, live
			}
			start := c.Now()
			if err := tr.Accept(token, first); err != nil {
				t.Fatal(err)
			}
			c.advance(time.Second)
			if err := tr.Accept(token, second); err != nil {
				t.Fatal(err)
			}
			c.advance(time.Second)
			if err := tr.Accept(token, live); err != nil {
				t.Fatal(err)
			}
			s := snapshot(t, tr, "radio1")
			if len(s.Decodes) != 1 || s.Counters.Duplicates != 2 || !s.Decodes[0].SeenLive || !s.Decodes[0].SeenReplay || !s.Decodes[0].FirstSeen.Equal(start) || !s.Decodes[0].LastSeen.Equal(c.Now()) || s.Decodes[0].Data.New == replayFirst {
				t.Fatalf("lost duplicate/replay meaning: %+v", s.Decodes)
			}
			value(t, s.Decodes[0].Data.LowConfidence, true)
			value(t, s.Decodes[0].Data.OffAir, true)
		})
	}
}

func TestTrackerDoesNotMergeDifferentConfidenceOrLaterLiveEvents(t *testing.T) {
	tr, c, token := observation(t)
	acceptFixture(t, tr, token, "decode-full-s3")
	off := fixture(t, "decode-full-s3")
	off[len(off)-1] = 0
	if err := tr.Accept(token, off); err != nil {
		t.Fatal(err)
	}
	low := fixture(t, "decode-full-s3")
	low[len(low)-2] = 0
	if err := tr.Accept(token, low); err != nil {
		t.Fatal(err)
	}
	if len(snapshot(t, tr, "radio1").Decodes) != 3 {
		t.Fatal("discarded confidence/off-air difference")
	}
	c.advance(30 * time.Second)
	acceptFixture(t, tr, token, "heartbeat-s3")
	c.advance(FreshFor - 30*time.Second)
	acceptFixture(t, tr, token, "decode-full-s3")
	s := snapshot(t, tr, "radio1")
	if len(s.Decodes) != 4 || s.Counters.Duplicates != 0 || s.Counters.Expirations != 0 {
		t.Fatal("merged a later live observation using only UTC time-of-day")
	}
	replay := fixture(t, "decode-full-s3")
	replay[fixtureHeaderSize] = 0
	if err := tr.Accept(token, replay); err != nil {
		t.Fatal(err)
	}
	s = snapshot(t, tr, "radio1")
	if len(s.Decodes) != 4 || s.Decodes[0].SeenReplay || !s.Decodes[3].SeenReplay {
		t.Fatal("replay did not merge with the newest matching event")
	}
}

func TestTrackerClearAndCloseHaveDifferentLifecycles(t *testing.T) {
	tr, c, token := observation(t)
	acceptFixture(t, tr, token, "heartbeat-s3")
	acceptFixture(t, tr, token, "status-full-s3")
	acceptFixture(t, tr, token, "decode-full-s3")
	acceptFixture(t, tr, token, "clear-s3")
	s := snapshot(t, tr, "radio1")
	if len(s.Decodes) != 0 || s.Status == nil || s.Heartbeat == nil || s.Counters.Clears != 1 || s.Availability != Live {
		t.Fatalf("Clear removed unrelated state: %+v", s)
	}
	acceptFixture(t, tr, token, "decode-full-s3")
	if snapshot(t, tr, "radio1").Counters.Duplicates != 0 {
		t.Fatal("Clear retained a duplicate tombstone")
	}
	acceptFixture(t, tr, token, "close-s3")
	c.advance(FreshFor * 2)
	if err := tr.Accept(token, fixture(t, "heartbeat-s3")); !errors.Is(err, ErrRetired) {
		t.Fatal(err)
	}
	s = snapshot(t, tr, "radio1")
	if s.Availability != Closed || s.Status != nil || s.StatusSeenAt != nil || s.Heartbeat != nil || len(s.Decodes) != 0 || s.Schema != 0 || s.Counters.Expirations != 0 {
		t.Fatalf("Close was not terminal: %+v", s)
	}
	replacement, err := tr.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	acceptFixture(t, tr, replacement, "heartbeat-s3")
	if err := tr.Accept(token, fixture(t, "close-s3")); !errors.Is(err, ErrRetired) {
		t.Fatal(err)
	}
	if snapshot(t, tr, "radio1").Availability != Live {
		t.Fatal("late Close retired a replacement")
	}
}

func TestTrackerRestartAndDelayedStopCannotTouchReplacement(t *testing.T) {
	tr, _, old := observation(t)
	acceptFixture(t, tr, old, "status-full-s3")
	current, err := tr.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	s := snapshot(t, tr, "radio1")
	if s.Generation <= old.generation || s.Availability != Waiting || s.Status != nil || s.Counters.Accepted != 0 {
		t.Fatalf("restart retained old state: %+v", s)
	}
	if tr.Remove(old) {
		t.Fatal("late stop removed replacement")
	}
	if err := tr.Accept(old, fixture(t, "status-full-s3")); !errors.Is(err, ErrRetired) {
		t.Fatal(err)
	}
	if snapshot(t, tr, "radio1").Counters.Accepted != 0 {
		t.Fatal("retired traffic entered replacement counters")
	}
	other := NewTracker(nil)
	if err := other.Accept(current, fixture(t, "heartbeat-s3")); !errors.Is(err, ErrRetired) || other.Remove(current) {
		t.Fatal("token crossed trackers")
	}
	if err := tr.Accept(Token{}, nil); !errors.Is(err, ErrRetired) {
		t.Fatal(err)
	}
	if !tr.Remove(current) || tr.Remove(current) {
		t.Fatal("wrong removal result")
	}
	if _, ok := tr.Snapshot("radio1"); ok {
		t.Fatal("removed session retained")
	}
	if err := tr.Accept(current, fixture(t, "heartbeat-s3")); !errors.Is(err, ErrRetired) {
		t.Fatal(err)
	}
}

func TestTrackerRegistrationIsBoundedAndNeverReusesAGeneration(t *testing.T) {
	tr, _, token := observation(t)
	for _, tc := range []struct{ radio, id string }{{"", fixtureID}, {"../radio", fixtureID}, {"radio1", ""}, {"radio1", string([]byte{0xff})}, {"radio1", string(bytes.Repeat([]byte{'x'}, MaxIDSize+1))}} {
		if _, err := tr.Start(tc.radio, tc.id); !errors.Is(err, ErrInvalidSession) {
			t.Fatal(err)
		}
	}
	if snapshot(t, tr, "radio1").Generation != token.generation {
		t.Fatal("invalid start retired current session")
	}
	for i := 2; i <= MaxSessions; i++ {
		if _, err := tr.Start(fmt.Sprintf("radio%d", i), fixtureID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tr.Start("extra", fixtureID); !errors.Is(err, ErrSessionLimit) {
		t.Fatal(err)
	}
	replacement, err := tr.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if !tr.Remove(replacement) {
		t.Fatal("could not release a slot")
	}
	if _, err := tr.Start("extra", fixtureID); err != nil {
		t.Fatal(err)
	}
	tr.next = math.MaxUint64
	if _, err := tr.Start("radio2", fixtureID); !errors.Is(err, ErrGenerationExhausted) {
		t.Fatal(err)
	}
	if len(tr.Snapshots()) != MaxSessions {
		t.Fatal("generation exhaustion modified registered sessions")
	}
}

func corruptSnapshot(v reflect.Value) {
	if v.Kind() == reflect.Pointer {
		if !v.IsNil() {
			corruptSnapshot(v.Elem())
		}
		return
	}
	if v.Type() == reflect.TypeOf(time.Time{}) && v.CanSet() {
		v.Set(reflect.ValueOf(time.Unix(1, 0)))
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				corruptSnapshot(v.Field(i))
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			corruptSnapshot(v.Index(i))
		}
	case reflect.String:
		v.SetString("changed")
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.Uint8, reflect.Uint32, reflect.Uint64:
		v.SetUint(999)
	}
}

func TestTrackerSnapshotsOwnEveryMutableField(t *testing.T) {
	tr, _, token := observation(t)
	for _, name := range []string{"heartbeat-s3", "status-full-s3", "decode-full-s3"} {
		b := fixture(t, name)
		if err := tr.Accept(token, b); err != nil {
			t.Fatal(err)
		}
		clear(b)
	}
	s := snapshot(t, tr, "radio1")
	before, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	corruptSnapshot(reflect.ValueOf(&s).Elem())
	all := tr.Snapshots()
	corruptSnapshot(reflect.ValueOf(all))
	after, err := json.Marshal(snapshot(t, tr, "radio1"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("snapshot mutation reached private state")
	}
}

func TestTrackerConcurrentReceiversReadsAndReplacements(t *testing.T) {
	tr, _, first := observation(t)
	var current atomic.Value
	current.Store(first)
	packet := fixture(t, "decode-full-s3")
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			token, err := tr.Start("radio1", fixtureID)
			if err != nil {
				t.Error(err)
				return
			}
			current.Store(token)
		}
	}()
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				err := tr.Accept(current.Load().(Token), packet)
				if err != nil && !errors.Is(err, ErrRetired) {
					t.Error(err)
					return
				}
			}
		}()
	}
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			for _, s := range tr.Snapshots() {
				if s.InstanceID != fixtureID || len(s.Decodes) > DecodeHistoryLimit {
					t.Error("inconsistent concurrent snapshot")
					return
				}
			}
		}
	}()
	wg.Wait()
	last, err := tr.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	acceptFixture(t, tr, last, "status-full-s3")
	s := snapshot(t, tr, "radio1")
	if s.Generation != last.generation || s.Counters.Accepted != 1 || s.Status == nil {
		t.Fatalf("replacement corrupted: %+v", s)
	}
}

func TestTrackerSupportsTheZeroClockAndDefaultClock(t *testing.T) {
	for _, clock := range []Clock{&observationClock{}, nil} {
		tr := NewTracker(clock)
		token, err := tr.Start("radio1", fixtureID)
		if err != nil {
			t.Fatal(err)
		}
		acceptFixture(t, tr, token, "heartbeat-s3")
		if s := snapshot(t, tr, "radio1"); s.LastSeen == nil || s.Availability != Live {
			t.Fatalf("valid zero/default arrival lost: %+v", s)
		}
	}
}
