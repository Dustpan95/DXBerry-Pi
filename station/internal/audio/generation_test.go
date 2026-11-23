package audio

import (
	"context"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// Exercise the actual Pion bridge: the old peer is removed, a replacement is
// connected while notification is delayed, then the old notification resumes.
func TestBridgeLossRetainsOriginalOfferGeneration(t *testing.T) {
	b, _ := testBridge(t)
	entered, resume := make(chan struct{}), make(chan struct{})
	defer close(resume)
	defer b.OnLost(func(tab TabID, reason string) {
		if reason == "original link dropped" {
			close(entered)
			<-resume
		}
	})()
	type event struct {
		tab        string
		generation uint64
	}
	events := make(chan event, 4)
	defer b.OnLostGeneration(func(tab TabID, generation uint64, reason string) {
		events <- event{tab, generation}
	})()
	connect := func(br *browser, generation uint64) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		answer, err := b.ConnectGeneration(ctx, br.offer(t), "sess|tab1", generation)
		if err != nil {
			t.Fatal(err)
		}
		if err := br.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}); err != nil {
			t.Fatal(err)
		}
		br.firstRTP(t, 2*time.Second)
	}
	connect(newBrowser(t), 41)
	b.mu.Lock()
	original := b.conns["sess|tab1"]
	b.mu.Unlock()
	go b.lose(original, "original link dropped")
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("loss notification did not enter")
	}
	connect(newBrowser(t), 42)
	resume <- struct{}{}
	select {
	case got := <-events:
		if got != (event{"sess|tab1", 41}) {
			t.Fatalf("delayed loss adopted the replacement identity: %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("original loss not delivered")
	}
	b.mu.Lock()
	current := b.conns["sess|tab1"]
	b.mu.Unlock()
	if current == nil || current == original || b.Listeners() != 1 {
		t.Fatal("replacement was removed by the original loss")
	}
	b.lose(current, "current link dropped")
	select {
	case got := <-events:
		if got != (event{"sess|tab1", 42}) {
			t.Fatalf("current loss identity: %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("current loss not delivered")
	}
}
