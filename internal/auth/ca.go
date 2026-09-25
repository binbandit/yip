package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CA is the hub's private certificate authority. It issues the runner
// listener's server certificate and one client certificate per paired node.
// Nodes pin the CA fingerprint at pairing time.
type CA struct {
	Cert    *x509.Certificate
	Key     *ecdsa.PrivateKey
	CertPEM []byte
	dir     string
}

// LoadOrCreateCA loads ca.pem/ca.key from dir, creating them on first use.
func LoadOrCreateCA(dir string) (*CA, error) {
	certPath, keyPath := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if _, err := os.Stat(certPath); err == nil {
		certPEM, err := os.ReadFile(certPath)
		if err != nil {
			return nil, err
		}
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}
		cert, err := parseCertPEM(certPEM)
		if err != nil {
			return nil, err
		}
		kb, _ := pem.Decode(keyPEM)
		if kb == nil {
			return nil, errors.New("ca.key: no PEM block")
		}
		key, err := x509.ParseECPrivateKey(kb.Bytes)
		if err != nil {
			return nil, err
		}
		return &CA{Cert: cert, Key: key, CertPEM: certPEM, dir: dir}, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "yip hub CA", Organization: []string{"yip"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	return &CA{Cert: cert, Key: key, CertPEM: certPEM, dir: dir}, nil
}

// Fingerprint is the SHA-256 of the CA certificate, shown to the owner and
// pinned by runners during pairing.
func (c *CA) Fingerprint() string { return CertFingerprint(c.Cert) }

// CertFingerprint returns "sha256:<hex>" of a certificate's DER bytes.
func CertFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PublicKeyFingerprint returns a short owner-visible machine fingerprint.
func PublicKeyFingerprint(pub any) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	h := hex.EncodeToString(sum[:])
	var groups []string
	for i := 0; i < 16; i += 4 {
		groups = append(groups, strings.ToUpper(h[i:i+4]))
	}
	return strings.Join(groups, "-"), nil
}

// ServerTLS returns a TLS config for the runner listener: the hub presents a
// CA-issued server certificate and verifies client certificates when given.
// Pairing requests arrive without a client certificate; the runner
// connection endpoint requires one.
func (c *CA) ServerTLS(hosts []string) (*tls.Config, error) {
	cert, err := c.serverCert(hosts)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(c.Cert)
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.VerifyClientCertIfGiven,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

func (c *CA) serverCert(hosts []string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: "yip hub"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range append([]string{"localhost", "127.0.0.1", "::1"}, hosts...) {
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Cert, &key.PublicKey, c.Key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der, c.Cert.Raw}, PrivateKey: key}, nil
}

// SignNodeCSR issues a client certificate binding the CSR's key to nodeID.
// The node generated its private key locally; the hub never sees it.
func (c *CA) SignNodeCSR(csrPEM []byte, nodeID string, validity time.Duration) (certPEM []byte, serial string, fingerprint string, err error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, "", "", errors.New("invalid CSR")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, "", "", err
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, "", "", fmt.Errorf("CSR signature: %w", err)
	}
	if _, ok := csr.PublicKey.(*ecdsa.PublicKey); !ok {
		return nil, "", "", errors.New("node key must be ECDSA")
	}
	fingerprint, err = PublicKeyFingerprint(csr.PublicKey)
	if err != nil {
		return nil, "", "", err
	}
	sn := randomSerial()
	u, _ := url.Parse("yip://node/" + nodeID)
	tmpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: nodeID, Organization: []string{"yip node"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(validity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{u},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Cert, csr.PublicKey, c.Key)
	if err != nil {
		return nil, "", "", err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), sn.Text(16), fingerprint, nil
}

// NodeIDFromCert extracts the node ID from a verified client certificate.
func NodeIDFromCert(cert *x509.Certificate) (string, string) {
	for _, u := range cert.URIs {
		if u.Scheme == "yip" && u.Host == "node" {
			return strings.TrimPrefix(u.Path, "/"), cert.SerialNumber.Text(16)
		}
	}
	return "", ""
}

func randomSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

func parseCertPEM(b []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("no PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

// ParseCertPEM parses a PEM certificate.
func ParseCertPEM(b []byte) (*x509.Certificate, error) { return parseCertPEM(b) }

// NewNodeKeyAndCSR generates a node key locally and a CSR for pairing.
func NewNodeKeyAndCSR(name string) (keyPEM, csrPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: name}}, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

// PinnedTLS returns a client TLS config that trusts only a CA whose
// fingerprint equals pin (used before the runner has the CA stored).
func PinnedTLS(pin string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // replaced by VerifyPeerCertificate's pinned-chain check below
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) < 2 {
				return errors.New("hub did not present its CA chain")
			}
			ca, err := x509.ParseCertificate(raw[len(raw)-1])
			if err != nil {
				return err
			}
			if CertFingerprint(ca) != pin {
				return fmt.Errorf("hub CA fingerprint %s does not match the pinned %s", CertFingerprint(ca), pin)
			}
			leaf, err := x509.ParseCertificate(raw[0])
			if err != nil {
				return err
			}
			pool := x509.NewCertPool()
			pool.AddCert(ca)
			_, err = leaf.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
			return err
		},
	}
}

// ClientTLS returns the mutual-TLS config a paired runner uses.
func ClientTLS(caPEM, certPEM, keyPEM []byte, serverName string) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("invalid CA PEM")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS13,
	}, nil
}
