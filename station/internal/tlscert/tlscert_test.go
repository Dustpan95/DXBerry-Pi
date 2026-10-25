package tlscert

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureWritesCertOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	changed, err := Ensure(dir, []string{"pi4", "dxberry.local", "10.0.0.90", "10.0.0.5"})
	if err != nil || !changed {
		t.Fatalf("first call: changed=%v err=%v", changed, err)
	}
	st, err := os.Stat(filepath.Join(dir, "station.key"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key: %v %v", st, err)
	}
	st, err = os.Stat(filepath.Join(dir, "station.crt"))
	if err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("crt: %v %v", st, err)
	}
	st, _ = os.Stat(dir)
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", st.Mode().Perm())
	}
	data, _ := os.ReadFile(filepath.Join(dir, "station.crt"))
	block, _ := pem.Decode(data)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "dxberry" {
		t.Fatalf("CN %q", cert.Subject.CommonName)
	}
	if len(cert.DNSNames) != 2 || len(cert.IPAddresses) != 2 {
		t.Fatalf("SANs %v %v", cert.DNSNames, cert.IPAddresses)
	}
	if cert.IPAddresses[0].String() != "10.0.0.5" || cert.IPAddresses[1].String() != "10.0.0.90" {
		t.Fatalf("IPs not sorted: %v", cert.IPAddresses)
	}
	if cert.DNSNames[0] != "dxberry.local" || cert.DNSNames[1] != "pi4" {
		t.Fatalf("DNS not sorted: %v", cert.DNSNames)
	}
	if cert.PublicKeyAlgorithm != x509.ECDSA {
		t.Fatalf("algorithm %v", cert.PublicKeyAlgorithm)
	}
	if d := cert.NotAfter.Sub(time.Now()); d < 9*365*24*time.Hour {
		t.Fatalf("validity %v", d)
	}
	changed, err = Ensure(dir, []string{"other"})
	if err != nil || changed {
		t.Fatalf("second call: changed=%v err=%v", changed, err)
	}
	again, _ := os.ReadFile(filepath.Join(dir, "station.crt"))
	if string(again) != string(data) {
		t.Fatal("certificate was rewritten")
	}
}

func TestEnsureRewritesWhenKeyIsMissing(t *testing.T) {
	dir := t.TempDir()
	if _, err := Ensure(dir, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "station.key"))
	changed, err := Ensure(dir, []string{"a"})
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
}
