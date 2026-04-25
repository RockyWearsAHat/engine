package remote

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// ecdsaGenKeyFn is injectable for tests to simulate ECDSA key generation failure.
var ecdsaGenKeyFn = ecdsa.GenerateKey

// randIntTLSFn is injectable for tests to simulate random serial generation failure.
var randIntTLSFn = rand.Int

// pemEncodeWriterFn, x509MarshalECKeyFn, tlsLoadX509KeyPairFn, x509CreateCertFn are injectable for tests.
var pemEncodeWriterFn = pem.Encode
var x509MarshalECKeyFn = x509.MarshalECPrivateKey
var tlsLoadX509KeyPairFn = tls.LoadX509KeyPair
var x509CreateCertFn = x509.CreateCertificate

// LoadOrCreateTLSConfig loads an existing TLS certificate from the storage path
// or generates a new self-signed ECDSA certificate for remote access.
func LoadOrCreateTLSConfig(storagePath string) (*tls.Config, error) {
	certPath := filepath.Join(storagePath, "cert.pem")
	keyPath := filepath.Join(storagePath, "key.pem")

	if _, err := os.Stat(certPath); err == nil {
		cert, loadErr := tls.LoadX509KeyPair(certPath, keyPath)
		if loadErr == nil {
			return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}, nil
		}
	}

	key, err := ecdsaGenKeyFn(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	serial, err := randIntTLSFn(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Engine Editor"},
			CommonName:   "Engine Remote Access",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.IPv4(0, 0, 0, 0), net.IPv4(127, 0, 0, 1)},
		DNSNames:              []string{"localhost"},
	}

	certDER, err := x509CreateCertFn(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}

	certOut, err := os.Create(certPath)
	if err != nil {
		return nil, fmt.Errorf("write cert: %w", err)
	}
	if err := pemEncodeWriterFn(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		certOut.Close()
		return nil, fmt.Errorf("encode cert: %w", err)
	}
	certOut.Close()

	keyDER, err := x509MarshalECKeyFn(key)
	if err != nil {
		return nil, fmt.Errorf("marshal key: %w", err)
	}
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return nil, fmt.Errorf("write key: %w", err)
	}
	if err := pemEncodeWriterFn(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}); err != nil {
		keyOut.Close()
		return nil, fmt.Errorf("encode key: %w", err)
	}
	keyOut.Close()

	cert, err := tlsLoadX509KeyPairFn(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load generated cert: %w", err)
	}

	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}, nil
}
