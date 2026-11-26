// Receive a synthetic queued message from the managed Pat service in the QEMU guest.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/la5nta/wl2k-go/fbb"
	"github.com/la5nta/wl2k-go/mailbox"
	"github.com/la5nta/wl2k-go/transport/telnet"
)

func receive(box *mailbox.DirHandler) (fbb.TrafficStats, error) {
	c, err := telnet.DialTimeout("127.0.0.1:18772", "N0CALL-2", "DXB-FIXTURE", 3*time.Second)
	if err != nil {
		return fbb.TrafficStats{}, err
	}
	defer c.Close()
	if err := c.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
		return fbb.TrafficStats{}, err
	}
	session := fbb.NewSession("N0CALL-2", "N0CALL", "AA00aa", box)
	return session.Exchange(c)
}

func run() error {
	marker, err := os.ReadFile("/fixture/pat-manager-guest")
	if err != nil || string(marker) != "isolated-pat-manager\n" || os.Geteuid() == 0 || len(os.Args) != 4 {
		return errors.New("unprivileged manager guest and expected MID, subject and body required")
	}
	mid, subject, body := os.Args[1], os.Args[2], os.Args[3]
	dir, err := os.MkdirTemp("", "pat-peer-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	box := mailbox.NewDirHandler(dir, false)
	first, err := receive(box)
	if err != nil {
		return fmt.Errorf("initial exchange: %w", err)
	}
	if len(first.Received) != 1 || first.Received[0] != mid || len(first.Sent) != 0 {
		return errors.New("expected the original message ID in the first exchange")
	}
	second, err := receive(box)
	if err != nil {
		return fmt.Errorf("reconnect: %w", err)
	}
	if len(second.Received) != 0 || len(second.Sent) != 0 {
		return errors.New("unexpected message on reconnect")
	}
	messages, err := box.Inbox()
	if err != nil {
		return err
	}
	if len(messages) != 1 {
		return fmt.Errorf("expected one inbox entry, got %d", len(messages))
	}
	msg := messages[0]
	text, err := msg.Body()
	if err != nil || strings.TrimSpace(text) != body || msg.MID() != mid || msg.Subject() != subject ||
		msg.From().Addr != "N0CALL" || len(msg.To()) != 1 || msg.To()[0].Addr != "N0CALL-2" {
		return errors.New("received message content or addressing mismatch")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
