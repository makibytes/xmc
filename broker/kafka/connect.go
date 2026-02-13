//go:build kafka

package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
)

// TLSConfig holds TLS connection parameters for Kafka
type TLSConfig struct {
	Enabled    bool
	CACert     string
	ClientCert string
	ClientKey  string
	Insecure   bool
}

type ConnArguments struct {
	Server   string
	User     string
	Password string
	TLS      TLSConfig
}

// parseKafkaURL parses the server URL and returns brokers and TLS config
func parseKafkaURL(serverURL string, tlsCfg TLSConfig) ([]string, *tls.Config, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid server URL: %w", err)
	}

	// Extract brokers (can be comma-separated)
	brokers := strings.Split(u.Host, ",")

	var tlsConfig *tls.Config
	if u.Scheme == "kafka+ssl" || u.Scheme == "kafkas" || tlsCfg.Enabled {
		tlsConfig, err = buildKafkaTLSConfig(tlsCfg)
		if err != nil {
			return nil, nil, err
		}
	}

	return brokers, tlsConfig, nil
}

func buildKafkaTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.Insecure,
	}

	if cfg.CACert != "" {
		caCert, err := os.ReadFile(cfg.CACert)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}

	if cfg.ClientCert != "" && cfg.ClientKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}

// getSASLMechanism returns SASL mechanism if credentials are provided
func getSASLMechanism(user, password string) sasl.Mechanism {
	if user != "" && password != "" {
		return &plain.Mechanism{
			Username: user,
			Password: password,
		}
	}
	return nil
}