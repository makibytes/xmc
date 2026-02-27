package amqpcommon

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"
)

func TestBuildTLSConfig_Default(t *testing.T) {
	cfg := TLSConfig{}
	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = true, want false")
	}
	if tlsConfig.RootCAs != nil {
		t.Error("RootCAs should be nil for default config")
	}
}

func TestBuildTLSConfig_Insecure(t *testing.T) {
	cfg := TLSConfig{Insecure: true}
	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !tlsConfig.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = false, want true")
	}
}

func TestBuildTLSConfig_InvalidCACertPath(t *testing.T) {
	cfg := TLSConfig{CACert: "/nonexistent/path/ca.pem"}
	_, err := buildTLSConfig(cfg)
	if err == nil {
		t.Fatal("expected error for invalid CA cert path, got nil")
	}
}

func TestBuildTLSConfig_InvalidCACertContent(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test-ca-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString("not valid PEM content")
	tmpFile.Close()

	cfg := TLSConfig{CACert: tmpFile.Name()}
	_, err = buildTLSConfig(cfg)
	if err == nil {
		t.Fatal("expected error for invalid CA cert content, got nil")
	}
}

func TestBuildTLSConfig_WithValidCACert(t *testing.T) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Test CA"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	tmpFile, err := os.CreateTemp("", "test-ca-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	pem.Encode(tmpFile, &pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	tmpFile.Close()

	cfg := TLSConfig{CACert: tmpFile.Name()}
	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsConfig.RootCAs == nil {
		t.Error("RootCAs should not be nil after loading CA cert")
	}
}

func TestBuildTLSConfig_InvalidClientCert(t *testing.T) {
	certFile, err := os.CreateTemp("", "test-cert-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(certFile.Name())
	certFile.WriteString("invalid cert data")
	certFile.Close()

	keyFile, err := os.CreateTemp("", "test-key-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(keyFile.Name())
	keyFile.WriteString("invalid key data")
	keyFile.Close()

	cfg := TLSConfig{ClientCert: certFile.Name(), ClientKey: keyFile.Name()}
	_, err = buildTLSConfig(cfg)
	if err == nil {
		t.Fatal("expected error for invalid client cert, got nil")
	}
}

func TestBuildTLSConfig_WithValidClientCert(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Test Client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	certFile, err := os.CreateTemp("", "test-cert-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(certFile.Name())
	pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certFile.Close()

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyFile, err := os.CreateTemp("", "test-key-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(keyFile.Name())
	pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	keyFile.Close()

	cfg := TLSConfig{ClientCert: certFile.Name(), ClientKey: keyFile.Name()}
	tlsConfig, err := buildTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tlsConfig.Certificates) != 1 {
		t.Errorf("certificates count = %d, want 1", len(tlsConfig.Certificates))
	}
}
