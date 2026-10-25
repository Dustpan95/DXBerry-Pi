// Package tlscert makes the station's self-signed certificate.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Ensure writes station.crt (0644) and station.key (0600) into dir when either is missing and
// reports whether it did. Hosts are DNS names or IP addresses; they become the SANs. An existing
// pair is left alone whatever hosts say.
func Ensure(dir string, hosts []string) (changed bool, err error) {
	crt, key := filepath.Join(dir, "station.crt"), filepath.Join(dir, "station.key")
	if fileExists(crt) && fileExists(key) {
		return false, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return false, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return false, err
	}
	var dns []string
	var ips []net.IP
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			ips = append(ips, ip)
		} else if h != "" {
			dns = append(dns, h)
		}
	}
	sort.Strings(dns)
	sort.SliceStable(ips, func(i, j int) bool { return lessIP(ips[i], ips[j]) })
	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "dxberry"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	if err != nil {
		return false, err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return false, err
	}
	// key first: a crash between the two leaves a key with no certificate, which Ensure redoes
	if err := writeAtomic(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return false, err
	}
	if err := writeAtomic(crt, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// lessIP orders addresses numerically (10.0.0.5 before 10.0.0.90).
func lessIP(a, b net.IP) bool {
	a4, b4 := a.To16(), b.To16()
	for i := range a4 {
		if a4[i] != b4[i] {
			return a4[i] < b4[i]
		}
	}
	return false
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("install %s: %w", path, err)
	}
	return nil
}
