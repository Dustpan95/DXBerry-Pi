//go:build wsjtxcompat && wsjtxmanager && wsjtxscreen

package wsjtx

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func checkScreenOfflineDecode(t *testing.T, receiver screenReceiver, x func(...string) string, window, pid string) {
	t.Helper()
	start, ok := receiver.Snapshot("fixture1")
	if !ok || len(start.Observation.Decodes) != 0 {
		t.Fatal("expected fresh decode history before opening fixture audio")
	}
	read := func() Snapshot {
		t.Helper()
		current, ok := receiver.Snapshot("fixture1")
		s := current.Observation
		before := start.Observation
		if !ok || s.Availability != Live || s.Generation != before.Generation || s.Epoch != before.Epoch || s.InstanceID != before.InstanceID ||
			s.Status == nil || s.Status.TXEnabled || s.Status.Transmitting || s.Status.DialFrequency != screenFT8DialHz ||
			s.Counters.Malformed != before.Counters.Malformed || s.Counters.WrongIdentity != before.Counters.WrongIdentity ||
			current.Transport.WrongPeer != start.Transport.WrongPeer || current.Transport.QueueDrops != start.Transport.QueueDrops {
			t.Fatal("offline decode observation lost its idle FT8 session or UDP continuity")
		}
		return s
	}

	read()
	if x("getactivewindow") != window {
		t.Fatal("expected the verified main window before opening audio")
	}
	x("key", "ctrl+o")
	dialog := x("search", "--sync", "--all", "--onlyvisible", "--pid", pid, "--name", "^Open File$")
	if _, err := strconv.ParseUint(dialog, 10, 32); err != nil || x("getwindowpid", dialog) != pid || x("getactivewindow") != dialog {
		t.Fatal("expected the managed application's active Open File dialog")
	}
	x("type", "/fixture/260101_120000.wav")
	opened := time.Now()
	x("key", "Return")
	var decode ReceivedDecode
	for deadline := opened.Add(35 * time.Second); time.Now().Before(deadline); {
		for _, d := range read().Decodes {
			if d.Data.Text != nil && strings.TrimSpace(*d.Data.Text) == "CQ K1ABC FN42" {
				decode = d
				break
			}
		}
		if !decode.FirstSeen.IsZero() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	d := decode.Data
	if !decode.FirstSeen.After(opened) || !d.New || d.OffAir == nil || !*d.OffAir ||
		d.Mode == nil || *d.Mode != "~" || d.TimeMS == nil || *d.TimeMS != 12*60*60*1000 || d.DeltaFrequency < 1497 || d.DeltaFrequency > 1503 {
		t.Fatalf("expected the 12:00 FT8 file decode at 1500 Hz with its off-air flag, observed %d rows", len(read().Decodes))
	}
	t.Logf("Offline FT8 decode observed %v after opening WAV", decode.FirstSeen.Sub(opened))
	beforeClear := read().Counters.Clears
	if x("getactivewindow") != window {
		t.Fatal("expected the managed main window before clearing band activity")
	}
	// The Erase button uses the Alt+E mnemonic. Two presses within
	// 500 ms erase band activity as well as the RX frequency pane.
	x("key", "--repeat", "2", "--delay", "200", "alt+e")
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		s := read()
		if s.Counters.Clears > beforeClear && len(s.Decodes) == 0 {
			t.Log("Band activity Clear received and offline decode history emptied")
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("clearing band activity did not empty the production observer history")
}
