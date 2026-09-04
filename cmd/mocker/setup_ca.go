package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// caDir is where the STABLE local CA root lives, relative to the checkout:
// a host-side directory, gitignored, bind-mounted read-only into Caddy
// (docker-compose.tls.yml) and provisioned as the `tls internal` CA root by
// the `pki` block in deploy/Caddyfile. The root lives OUTSIDE every volume
// on purpose: Caddy's own root sits in caddy-data, and a mortal volume
// (`docker volume prune`, `down -v`) would make a fresh stack mint a fresh
// root — invalidating every trust decision made for the old one, piling
// another entry into the OS keychain, and breaking every client with
// "unable to get local issuer certificate" until trust is reinstalled.
// This Go half mirrors scripts/tls-ca.sh for the one path that cannot
// assume bash: `mocker setup` runs on Windows too.
const (
	caDir      = ".tls-ca"
	caCertName = caDir + "/root.crt"
	caKeyName  = caDir + "/root.key"
)

// ensureLocalCA returns the stable CA root's PEM, generating the key pair
// once if it does not exist yet. Idempotent: existing files are left
// exactly as they are, so re-running the wizard never rotates the root
// behind an installed trust.
func ensureLocalCA(dir string) ([]byte, error) {
	certPath := filepath.Join(dir, "root.crt")
	if pemBytes, err := os.ReadFile(certPath); err == nil {
		return pemBytes, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}
	// Same shape scripts/tls-ca.sh makes with openssl: P-256, CA:TRUE
	// (critical), ten years. Only the ROOT has to be stable — the
	// intermediate and the leaves stay Caddy-managed in the volume.
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mocker Local Authority - stable root"},
		NotBefore:             time.Now().Add(-time.Hour), // clock skew between hosts
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("self-sign CA root: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal CA key: %w", err)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	// 0600 on the key: whoever holds root.key can sign for every host of
	// the contour, so it is an offline-cracking target just like .env.
	if err := os.WriteFile(filepath.Join(dir, "root.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, err
	}
	return certPEM, nil
}
