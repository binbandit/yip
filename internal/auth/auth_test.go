package auth

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := VerifyPassword(h, "correct horse battery"); !ok {
		t.Fatal("expected match")
	}
	if ok, _ := VerifyPassword(h, "correct horse batterx"); ok {
		t.Fatal("expected mismatch")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestPairingAndPinnedMutualTLS(t *testing.T) {
	ca, err := LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, csrPEM, err := NewNodeKeyAndCSR("build-mini")
	if err != nil {
		t.Fatal(err)
	}
	certPEM, serial, fp, err := ca.SignNodeCSR(csrPEM, "node-123", 24*time.Hour)
	if err != nil || serial == "" || fp == "" {
		t.Fatalf("sign: %v", err)
	}
	cert, _ := ParseCertPEM(certPEM)
	if id, sn := NodeIDFromCert(cert); id != "node-123" || sn != serial {
		t.Fatalf("node id %q serial %q", id, sn)
	}

	serverTLS, err := ca.ServerTLS(nil)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 2)
	go func() {
		for i := 0; i < 2; i++ {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			tc := c.(*tls.Conn)
			if err := tc.Handshake(); err != nil {
				got <- "err"
				c.Close()
				continue
			}
			peers := tc.ConnectionState().PeerCertificates
			if len(peers) > 0 {
				id, _ := NodeIDFromCert(peers[0])
				got <- id
			} else {
				got <- "anonymous"
			}
			c.Close()
		}
	}()

	// Pairing path: pinned CA, no client certificate.
	c, err := tls.Dial("tcp", ln.Addr().String(), PinnedTLS(ca.Fingerprint()))
	if err != nil {
		t.Fatalf("pinned dial: %v", err)
	}
	c.Close()
	if v := <-got; v != "anonymous" {
		t.Fatalf("pairing handshake saw %q", v)
	}
	// Wrong pin must fail.
	if c, err := tls.Dial("tcp", ln.Addr().String(), PinnedTLS("sha256:00")); err == nil {
		c.Close()
		t.Fatal("wrong pin accepted")
	}
	<-got
	// Connected path: mutual TLS.
	host, _, _ := net.SplitHostPort(ln.Addr().String())
	clientTLS, err := ClientTLS(ca.CertPEM, certPEM, keyPEM, host)
	if err != nil {
		t.Fatal(err)
	}
	_ = x509.NewCertPool()
	ln2, _ := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	defer ln2.Close()
	go func() {
		c, err := ln2.Accept()
		if err != nil {
			return
		}
		tc := c.(*tls.Conn)
		_ = tc.Handshake()
		peers := tc.ConnectionState().PeerCertificates
		if len(peers) > 0 {
			id, _ := NodeIDFromCert(peers[0])
			got <- id
		} else {
			got <- "anonymous"
		}
		c.Close()
	}()
	c2, err := tls.Dial("tcp", ln2.Addr().String(), clientTLS)
	if err != nil {
		t.Fatalf("mtls dial: %v", err)
	}
	c2.Close()
	if v := <-got; v != "node-123" {
		t.Fatalf("mtls peer %q", v)
	}
}

func TestSealer(t *testing.T) {
	s, err := LoadOrCreateKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := s.Seal([]byte("ghp_secret"))
	plain, err := s.Open(sealed)
	if err != nil || string(plain) != "ghp_secret" {
		t.Fatalf("open: %q %v", plain, err)
	}
}
