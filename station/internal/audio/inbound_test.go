package audio

import (
	"testing"
	"time"
)

// HasInbound tells the PTT layer whether a tab's connection carries its microphone yet: not
// before the browser sends audio, not for another tab, not after the connection ends.
func TestBridgeHasInboundOnceTheBrowserSends(t *testing.T) {
	b, _ := testBridge(t)
	if b.HasInbound("sess|tab1") {
		t.Fatal("inbound with no connection")
	}
	br := newBrowser(t)
	br.connect(t, b, "sess|tab1")
	br.firstRTP(t, 2*time.Second) // connected, the browser not talking yet
	time.Sleep(200 * time.Millisecond)
	if b.HasInbound("sess|tab1") {
		t.Fatal("inbound before the browser sent any audio")
	}
	stop := make(chan struct{})
	wg := br.talk(t, stop)
	deadline := time.Now().Add(3 * time.Second)
	for !b.HasInbound("sess|tab1") {
		if time.Now().After(deadline) {
			close(stop)
			wg.Wait()
			t.Fatal("no inbound while the browser sends audio")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if b.HasInbound("sess|tab2") {
		t.Fatal("another tab has this tab's microphone")
	}
	close(stop)
	wg.Wait()
	b.Close("sess|tab1")
	if b.HasInbound("sess|tab1") {
		t.Fatal("inbound after the connection closed")
	}
}
