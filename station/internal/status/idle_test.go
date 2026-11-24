package status

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func readCachedStatus(t *testing.T, url string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url+"/api/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status read returned %d", resp.StatusCode)
	}
}

func TestStatusFreshOneShotReadDoesNotScheduleIdleWork(t *testing.T) {
	r := newRig(t)
	eventually(t, "the startup cache", func() bool { gen, _, _ := snap(t, r.st); return gen != 0 })
	ts := serve(t, r.st, true)
	readCachedStatus(t, ts.URL)
	r.clock.Advance(24 * time.Hour)
	never(t, "a timer created by a fresh one-shot read", func() bool { return r.count() != 1 })
	r.clock.mu.Lock()
	timers := len(r.clock.waiters)
	r.clock.mu.Unlock()
	if timers != 0 {
		t.Fatalf("idle reporter retained %d periodic timers", timers)
	}
}

func TestStatusAgedHTTPReadRefreshesWithoutRetryStorm(t *testing.T) {
	r := newRig(t)
	eventually(t, "the startup cache", func() bool { gen, _, _ := snap(t, r.st); return gen != 0 })
	ts := serve(t, r.st, true)
	r.fail.Store(true)
	r.clock.Advance(time.Hour)
	readCachedStatus(t, ts.URL)
	eventually(t, "the requested failed attempt", func() bool {
		r.st.mu.Lock()
		failed := r.st.failed
		r.st.mu.Unlock()
		return r.count() == 2 && failed
	})
	for i := 0; i < 20; i++ {
		readCachedStatus(t, ts.URL)
	}
	r.clock.Advance(29 * time.Second)
	readCachedStatus(t, ts.URL)
	never(t, "a retry inside the read-demand interval", func() bool { return r.count() != 2 })
	r.clock.Advance(time.Second)
	r.fail.Store(false)
	readCachedStatus(t, ts.URL)
	eventually(t, "the next eligible read", func() bool { _, stale, _ := snap(t, r.st); return r.count() == 3 && !stale })
	r.clock.Advance(time.Hour)
	never(t, "continued background retries", func() bool { return r.count() != 3 })
}

func TestStatusInFlightReadDemandCoalescesButMutationSurvives(t *testing.T) {
	for _, mutation := range []bool{false, true} {
		name := "reads"
		if mutation {
			name = "reads-and-mutation"
		}
		t.Run(name, func(t *testing.T) {
			clock := newClock()
			var runs atomic.Int32
			started, release := make(chan struct{}), make(chan struct{})
			st := New(func(ctx context.Context) (string, error) {
				if runs.Add(1) == 2 {
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						return "", ctx.Err()
					}
				}
				return `{"ok":true}`, nil
			}, clock)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { st.Run(ctx); close(done) }()
			t.Cleanup(func() { cancel(); <-done })
			eventually(t, "startup completion", func() bool { gen, _, _ := snap(t, st); return gen != 0 })
			clock.Advance(time.Hour)
			st.Request()
			eventually(t, "an in-flight report", func() bool {
				select {
				case <-started:
					return true
				default:
					return false
				}
			})
			for i := 0; i < 1000; i++ {
				st.Request()
			}
			if mutation {
				st.Poke()
			}
			close(release)
			eventually(t, "report completion", func() bool { gen, _, _ := snap(t, st); return gen == clock.Now().UnixMilli() })
			clock.Advance(time.Second)
			want := int32(2)
			if mutation {
				want = 3
				eventually(t, "the retained mutation refresh", func() bool { return runs.Load() == want })
			}
			never(t, "an extra refresh from coalesced reads", func() bool { return runs.Load() != want })
			clock.Advance(24 * time.Hour)
			never(t, "a timer after demand ended", func() bool { return runs.Load() != want })
		})
	}
}

func TestStatusCacheAgeDoesNotDependOnListeners(t *testing.T) {
	clock := newClock()
	st := New(func(context.Context) (string, error) { return `{"ok":true}`, nil }, clock)
	st.refresh(context.Background())
	clock.Advance(89 * time.Second)
	_, stale, _ := snap(t, st)
	if stale {
		t.Fatal("recent unattended cache was marked stale early")
	}
	_, cancel := st.Subscribe()
	defer cancel()
	_, stale, _ = snap(t, st)
	if stale {
		t.Fatal("opening a listener changed the age threshold")
	}
	clock.Advance(2 * time.Second)
	_, stale, _ = snap(t, st)
	if !stale {
		t.Fatal("old cache was not marked stale")
	}
	cancel()
	_, stale, _ = snap(t, st)
	if !stale {
		t.Fatal("closing a listener made old readings appear fresh")
	}
}
