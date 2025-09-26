//go:build kafka

package kafka

import (
	"crypto/tls"
	"fmt"
	"net/url"
	"strings"

	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
)

type ConnArguments struct {
	Server   string
	User     string
	Password string
}

// parseKafkaURL parses the server URL and returns brokers and TLS config
func parseKafkaURL(serverURL string) ([]string, *tls.Config, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid server URL: %w", err)
	}

	// Extract brokers (can be comma-separated)
	brokers := strings.Split(u.Host, ",")

	var tlsConfig *tls.Config
	if u.Scheme == "kafka+ssl" || u.Scheme == "kafkas" {
		tlsConfig = &tls.Config{}
	}

	return brokers, tlsConfig, nil
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