package presetpreview

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/presets"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }

func (c *testClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

type testTarget struct {
	mu     sync.Mutex
	target Target
	calls  int
	hook   func(context.Context, int) (Target, error)
}

func (t *testTarget) Inspect(ctx context.Context, r radios.Registration) (Target, error) {
	t.mu.Lock()
	t.calls++
	n := t.calls
	hook := t.hook
	target := t.target
	t.mu.Unlock()
	if r.Radio().Name != "radio1" {
		return Target{}, errors.New("incorrect fixture registration")
	}
	if hook != nil {
		return hook(ctx, n)
	}
	return target, nil
}

type previewFixture struct {
	s         *Service
	cfg       Config
	p         Principal
	r         Request
	clock     *testClock
	source    *testTarget
	records   string
	library   presets.Library
	wireMu    sync.Mutex
	wire      []string
	frequency int64
	vfo, ptt  string
}

func newFixture(t *testing.T) *previewFixture {
	t.Helper()
	if os.Geteuid() != 0 {
		if os.Getenv("DXB_PRESET_PREVIEW_INTEGRATION") == "1" {
			t.Fatal("integration fixture requires Docker root")
		}
		t.Skip("root-owned registration fixture; run this suite in Docker as root")
	}
	f := &previewFixture{p: Principal{"login-session", "phone#nonce"}, clock: &testClock{now: time.Now()}, frequency: 14074000, vfo: "VFOA", ptt: "0"}
	root := t.TempDir()
	store, err := presets.NewStore(filepath.Join(root, "presets"))
	if err != nil {
		t.Fatal(err)
	}
	f.library, err = store.Edit(context.Background(), presets.Request{Version: 1, Operation: "create", Radio: "radio1", ExpectedRevision: presets.Absent, Label: "Test frequency", FrequencyHz: 14075000})
	if err != nil {
		t.Fatal(err)
	}
	f.r = Request{"radio1", f.library.Presets[0].ID, f.library.Revision}
	f.records = filepath.Join(root, "radios.json")
	raw := `{"version":1,"radios":{"radio1":{"label":"Fixture","owner":"","rigctld_port":4532,"ptt":{"method":"rigctld"},"rig":{"model":3073,"control":"hamlib"},"cat":{"path":"usb-0:1.3:1.0"}}}}`
	if err := os.WriteFile(f.records, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := radios.NewRegistrationReader(f.records)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.TrimSpace(line)
					f.wireMu.Lock()
					f.wire = append(f.wire, cmd)
					value := ""
					switch cmd {
					case `+\get_ptt`:
						value = "PTT: " + f.ptt
					case `+\get_vfo`:
						value = "VFO: " + f.vfo
					case `+\get_freq`:
						value = fmt.Sprintf("Frequency: %d", f.frequency)
					}
					f.wireMu.Unlock()
					if value == "" {
						fmt.Fprint(conn, "unknown:\nRPRT -1\n")
						continue
					}
					fmt.Fprintf(conn, "%s:\n%s\nRPRT 0\n", strings.TrimPrefix(cmd, `+\`), value)
				}
			}()
		}
	}()
	client := rig.Dial(listener.Addr().String(), nil)
	t.Cleanup(client.Close)
	deadline := time.Now().Add(3 * time.Second)
	for !client.Connected() {
		if time.Now().After(deadline) {
			t.Fatal("fixture client did not connect")
		}
		time.Sleep(time.Millisecond)
	}
	caps := rig.Caps{ModelName: "Fixture", Probed: time.Now(), CanGetPTT: true, CanGetVFO: true, VFOs: []string{"VFOA", "VFOB"}, FreqRanges: []rig.FreqRange{{Lo: 14000000, Hi: 14350000}}}
	poller := rig.NewPoller(client, caps, nil, nil)
	f.source = &testTarget{target: Target{poller, caps, "/private/effective-route-and-program-generation-1"}}
	owners := session.NewOwners(f.clock)
	if err := owners.Claim("radio1", session.Holder{Kind: "tab", ID: session.TabID(f.p.Login, f.p.Tab)}); err != nil {
		t.Fatal(err)
	}
	f.cfg = Config{Definitions: store, Registrations: reader, Owners: owners, Targets: f.source, Now: f.clock.Now}
	f.s, err = New(f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func ready(t *testing.T, f *previewFixture) Preview {
	t.Helper()
	p, err := f.s.Preview(context.Background(), f.p, f.r)
	if err != nil {
		t.Fatal(err)
	}
	if p.Token == "" || p.CurrentFrequencyHz != 14074000 || p.RequestedFrequencyHz != 14075000 || p.DeltaHz != 1000 || p.VFO != "VFOA" {
		t.Fatal(p)
	}
	return p
}
func failure(t *testing.T, p Preview, err, want error) {
	t.Helper()
	if !errors.Is(err, want) || p != (Preview{}) {
		t.Fatalf("preview %+v error %v, want %v", p, err, want)
	}
}

func TestPreviewAndRecheckUseRealReadonlySources(t *testing.T) {
	f := newFixture(t)
	before, err := os.ReadFile(f.records)
	if err != nil {
		t.Fatal(err)
	}
	p := ready(t, f)
	f.clock.advance(500 * time.Millisecond)
	again, err := f.s.Recheck(context.Background(), f.p, f.r.Radio, p.Token)
	if err != nil || again.Token != p.Token || again.ExpiresInMS != 1500 {
		t.Fatal(again, err)
	}
	if f.source.calls != 4 {
		t.Fatal("effective target was not checked before and after each read", f.source.calls)
	}
	f.wireMu.Lock()
	wire := append([]string{}, f.wire...)
	f.wireMu.Unlock()
	if len(wire) != 12 {
		t.Fatal(wire)
	}
	for _, cmd := range wire {
		if cmd != `+\get_ptt` && cmd != `+\get_vfo` && cmd != `+\get_freq` {
			t.Fatal("control write emitted", cmd)
		}
	}
	after, err := os.ReadFile(f.records)
	if err != nil || string(after) != string(before) {
		t.Fatal("registration changed", err)
	}
	lib, err := f.cfg.Definitions.List(context.Background(), "radio1")
	if err != nil || lib.Revision != f.library.Revision {
		t.Fatal("definitions changed", err)
	}
	raw, err := json.Marshal(again)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private", "usb-", "login-session", "phone#nonce", "rigctld_port", "Identity", "Caps"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private metadata exposed", string(raw))
		}
	}
}

func TestPreviewBlocksWithoutCompleteEvidence(t *testing.T) {
	for _, which := range []string{"not-owner", "program", "unknown", "missing-radio", "wrong-revision", "missing-preset", "unsupported", "transmitting", "caps-mismatch"} {
		t.Run(which, func(t *testing.T) {
			f := newFixture(t)
			want := ErrUnavailable
			switch which {
			case "not-owner":
				f.cfg.Owners.Release("radio1", session.TabID(f.p.Login, f.p.Tab))
				want = ErrNotOwner
			case "program":
				f.source.hook = func(context.Context, int) (Target, error) { return Target{}, ErrProgramRunning }
				want = ErrProgramRunning
			case "unknown":
				f.source.hook = func(context.Context, int) (Target, error) {
					return Target{}, errors.New("/secret/process evidence unavailable")
				}
			case "missing-radio":
				if err := os.WriteFile(f.records, []byte(`{"version":1,"radios":{}}`), 0600); err != nil {
					t.Fatal(err)
				}
				want = ErrNotFound
			case "wrong-revision":
				f.r.Revision = strings.Repeat("a", 64)
				want = ErrConflict
			case "missing-preset":
				f.r.ID = strings.Repeat("a", 32)
				want = ErrNotFound
			case "unsupported":
				c := f.source.target.Caps
				c.FreqRanges = []rig.FreqRange{{Lo: 1, Hi: 2}}
				f.source.target.Caps = c
				f.source.target.Poller.SetCaps(c)
				want = ErrUnsupported
			case "transmitting":
				f.wireMu.Lock()
				f.ptt = "1"
				f.wireMu.Unlock()
				want = ErrTransmitting
			case "caps-mismatch":
				f.source.target.Caps.ModelName = "other"
			}
			p, err := f.s.Preview(context.Background(), f.p, f.r)
			failure(t, p, err, want)
			if len(f.s.tokens) != 0 {
				t.Fatal("failure published a token")
			}
		})
	}
}

func TestPreviewRefusesChangesDuringEvidenceCollection(t *testing.T) {
	for _, which := range []string{"registration", "definition", "claim-aba", "target-generation", "write-generation", "clock-expiry", "cancel"} {
		t.Run(which, func(t *testing.T) {
			f := newFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := ErrConflict
			f.source.hook = func(ctx context.Context, n int) (Target, error) {
				if n == 2 {
					switch which {
					case "registration":
						raw, err := os.ReadFile(f.records)
						if err != nil {
							t.Fatal(err)
						}
						q := f.records + ".new"
						if err := os.WriteFile(q, raw, 0600); err != nil {
							t.Fatal(err)
						}
						if err := os.Rename(q, f.records); err != nil {
							t.Fatal(err)
						}
					case "definition":
						_, err := f.cfg.Definitions.Edit(ctx, presets.Request{Version: 1, Operation: "update", Radio: "radio1", ID: f.r.ID, ExpectedRevision: f.r.Revision, Label: "Changed", FrequencyHz: 14075000})
						if err != nil {
							t.Fatal(err)
						}
					case "claim-aba":
						id := session.TabID(f.p.Login, f.p.Tab)
						f.cfg.Owners.Release("radio1", id)
						if err := f.cfg.Owners.Claim("radio1", session.Holder{Kind: "tab", ID: id}); err != nil {
							t.Fatal(err)
						}
					case "target-generation":
						target := f.source.target
						target.Identity += "changed"
						return target, nil
					case "write-generation":
						if err := f.source.target.Poller.Write(ctx, func(*rig.Client) error { return nil }, nil); err != nil {
							t.Fatal(err)
						}
					case "clock-expiry":
						f.clock.advance(Lifetime)
					case "cancel":
						cancel()
					}
				}
				return f.source.target, nil
			}
			if which == "clock-expiry" {
				want = ErrExpired
			}
			if which == "cancel" {
				want = context.Canceled
			}
			p, err := f.s.Preview(ctx, f.p, f.r)
			failure(t, p, err, want)
			if len(f.s.tokens) != 0 {
				t.Fatal("changed evidence published a token")
			}
		})
	}
}

func TestTokenBindingReplacementAndFixedExpiry(t *testing.T) {
	f := newFixture(t)
	p := ready(t, f)
	bound, err := f.s.Recheck(context.Background(), f.p, "radio2", p.Token)
	failure(t, bound, err, ErrExpired)
	if f.source.calls != 2 {
		t.Fatal("wrong-radio token lookup reached fresh sources")
	}
	wrong := f.p
	wrong.Tab = "another"
	got, err := f.s.Recheck(context.Background(), wrong, f.r.Radio, p.Token)
	failure(t, got, err, ErrExpired)
	wrong = f.p
	wrong.Login = "another-login"
	got, err = f.s.Recheck(context.Background(), wrong, f.r.Radio, p.Token)
	failure(t, got, err, ErrExpired)
	if _, err := f.s.Recheck(context.Background(), f.p, f.r.Radio, p.Token); err != nil {
		t.Fatal("foreign lookup destroyed the real token", err)
	}
	newer := ready(t, f)
	got, err = f.s.Recheck(context.Background(), f.p, f.r.Radio, p.Token)
	failure(t, got, err, ErrExpired)
	f.clock.advance(Lifetime)
	got, err = f.s.Recheck(context.Background(), f.p, f.r.Radio, newer.Token)
	failure(t, got, err, ErrExpired)
	if len(f.s.tokens) != 0 {
		t.Fatal("expired entries were retained")
	}
}

func TestPublicationRechecksDeadlineAfterLocalValidation(t *testing.T) {
	for _, operation := range []string{"preview", "recheck"} {
		t.Run(operation, func(t *testing.T) {
			f := newFixture(t)
			armed, advanced := false, false
			threshold := 2
			if operation == "recheck" {
				threshold = 4
			}
			f.cfg.Owners = session.NewOwners(clockFunc(func() time.Time {
				if armed && !advanced {
					f.clock.advance(Lifetime)
					advanced = true
				}
				return f.clock.Now()
			}))
			if err := f.cfg.Owners.Claim("radio1", session.Holder{Kind: "tab", ID: session.TabID(f.p.Login, f.p.Tab)}); err != nil {
				t.Fatal(err)
			}
			// Once fresh sources have been collected, cross the deadline during
			// the subsequent local ownership validation, after its initial sample.
			f.cfg.Now = func() time.Time {
				now := f.clock.Now()
				f.source.mu.Lock()
				calls := f.source.calls
				f.source.mu.Unlock()
				if calls >= threshold {
					armed = true
				}
				return now
			}
			var err error
			f.s, err = New(f.cfg)
			if err != nil {
				t.Fatal(err)
			}
			var got Preview
			if operation == "recheck" {
				p := ready(t, f)
				got, err = f.s.Recheck(context.Background(), f.p, f.r.Radio, p.Token)
			} else {
				got, err = f.s.Preview(context.Background(), f.p, f.r)
			}
			failure(t, got, err, ErrExpired)
			if !advanced || len(f.s.tokens) != 0 {
				t.Fatal("deadline checkpoint was not exercised or retained a token")
			}
		})
	}
}

func TestRecheckRequiresUnchangedFrequencyVFOAndOwnership(t *testing.T) {
	for _, which := range []string{"frequency", "vfo", "claim", "write", "program", "definition"} {
		t.Run(which, func(t *testing.T) {
			f := newFixture(t)
			p := ready(t, f)
			want := ErrConflict
			switch which {
			case "frequency":
				f.wireMu.Lock()
				f.frequency++
				f.wireMu.Unlock()
			case "vfo":
				f.wireMu.Lock()
				f.vfo = "VFOB"
				f.wireMu.Unlock()
			case "claim":
				id := session.TabID(f.p.Login, f.p.Tab)
				f.cfg.Owners.Release("radio1", id)
				if err := f.cfg.Owners.Claim("radio1", session.Holder{Kind: "tab", ID: id}); err != nil {
					t.Fatal(err)
				}
			case "write":
				if err := f.source.target.Poller.Write(context.Background(), func(*rig.Client) error { return nil }, nil); err != nil {
					t.Fatal(err)
				}
			case "program":
				f.source.hook = func(context.Context, int) (Target, error) { return Target{}, ErrProgramRunning }
				want = ErrProgramRunning
			case "definition":
				_, err := f.cfg.Definitions.Edit(context.Background(), presets.Request{Version: 1, Operation: "delete", Radio: "radio1", ExpectedRevision: f.r.Revision, ID: f.r.ID})
				if err != nil {
					t.Fatal(err)
				}
			}
			got, err := f.s.Recheck(context.Background(), f.p, f.r.Radio, p.Token)
			failure(t, got, err, want)
			got, err = f.s.Recheck(context.Background(), f.p, f.r.Radio, p.Token)
			failure(t, got, err, ErrExpired)
		})
	}
}

func TestPreviewCancellationAndBoundedAdmission(t *testing.T) {
	f := newFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	f.source.hook = func(ctx context.Context, n int) (Target, error) {
		if n == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return Target{}, ctx.Err()
			}
		}
		return f.source.target, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.s.Preview(ctx, f.p, f.r); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("source did not start")
	}
	p, err := f.s.Preview(context.Background(), f.p, f.r)
	failure(t, p, err, ErrBusy)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("source ignored cancellation")
	}
	if len(f.s.active) != 0 || len(f.s.tokens) != 0 {
		t.Fatal("canceled preview retained admission or token")
	}
}

func TestPreviewTokenCollisionEntropyFailureAndCapacity(t *testing.T) {
	f := newFixture(t)
	f.s.random = strings.NewReader(strings.Repeat("a", 64))
	p := ready(t, f)
	other := f.p
	other.Tab = "another-tab"
	id := session.TabID(other.Login, other.Tab)
	if err := f.cfg.Owners.TakeOver("radio1", session.Holder{Kind: "tab", ID: id}); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.Preview(context.Background(), other, f.r)
	failure(t, got, err, ErrUnavailable)
	if entry, ok := f.s.tokens[p.Token]; !ok || entry.key.principal != f.p {
		t.Fatal("collision overwrote prior entry")
	}
	f.s.random = strings.NewReader("")
	got, err = f.s.Preview(context.Background(), other, f.r)
	failure(t, got, err, ErrUnavailable)
	f = newFixture(t)
	for i := 0; i < MaxTokens; i++ {
		f.s.tokens[fmt.Sprint(i)] = evidence{key: key{Principal{Login: fmt.Sprint(i), Tab: "fixture"}, "other"}, started: f.clock.Now()}
	}
	got, err = f.s.Preview(context.Background(), f.p, f.r)
	failure(t, got, err, ErrBusy)
	if len(f.s.tokens) != MaxTokens {
		t.Fatal("capacity check evicted live evidence")
	}
	f = newFixture(t)
	for i := 0; i < MaxInFlight; i++ {
		f.s.active[key{Principal{Login: fmt.Sprint(i), Tab: "fixture"}, "other"}] = true
	}
	got, err = f.s.Preview(context.Background(), f.p, f.r)
	failure(t, got, err, ErrBusy)
	if f.source.calls != 0 {
		t.Fatal("over-capacity request reached sources")
	}
}

func TestDockerFixtureIsolation(t *testing.T) {
	if os.Getenv("DXB_PRESET_PREVIEW_INTEGRATION") != "1" {
		return
	}
	if os.Geteuid() != 0 {
		t.Fatal("integration fixture requires Docker root")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, network := range interfaces {
		if network.Flags&net.FlagLoopback == 0 {
			t.Fatal("integration fixture requires loopback-only network")
		}
	}
	for _, pattern := range []string{"/dev/snd", "/dev/ttyUSB*", "/dev/ttyACM*", "/dev/gpiochip*", "/dev/serial"} {
		paths, err := filepath.Glob(pattern)
		if err != nil || len(paths) > 0 {
			t.Fatal("hardware device exposed", pattern, err)
		}
	}
}

func TestPreviewFailsClosedWithoutConfiguration(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	var target *testTarget
	if _, err := New(Config{Definitions: &presets.Store{}, Registrations: &radios.RegistrationReader{}, Owners: &session.Owners{}, Targets: target}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("typed nil target accepted", err)
	}
	for _, s := range []*Service{nil, {}} {
		p, err := s.Preview(context.Background(), Principal{}, Request{})
		failure(t, p, err, ErrUnavailable)
		p, err = s.Recheck(context.Background(), Principal{}, "radio1", "")
		failure(t, p, err, ErrUnavailable)
	}
	for _, p := range []Principal{{}, {Login: "a|b", Tab: "c"}, {Login: "ok", Tab: "bad\n"}} {
		if validPrincipal(p) {
			t.Fatal("invalid principal accepted", p)
		}
	}
	for _, r := range []Request{{}, {Radio: "../radio", ID: strings.Repeat("a", 32), Revision: strings.Repeat("a", 64)}} {
		if validRequest(r) {
			t.Fatal("invalid request accepted", r)
		}
	}
}
