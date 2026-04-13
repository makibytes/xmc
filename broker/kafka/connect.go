//go:build kafka

package kafka

import (
	"crypto/tls"
	"fmt"
	"net/url"
	"strings"

	"github.com/makibytes/xmc/broker/tlsutil"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
)

// TLSConfig is an alias for the shared TLS configuration.
type TLSConfig = tlsutil.TLSConfig

type ConnArguments struct {
	Server   string
	User     string
	Password string
	TLS      TLSConfig
}

// parseKafkaURL parses the server URL and returns brokers and TLS config
func parseKafkaURL(serverURL string, tlsCfg TLSConfig) ([]string, *tls.Config, error) {
	// Ensure the URL has a scheme so url.Parse treats "host:port" as a host,
	// not as "scheme:opaque".
	rawURL := serverURL
	if !strings.Contains(serverURL, "://") {
		rawURL = "kafka://" + serverURL
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid server URL: %w", err)
	}

	// Extract brokers (can be comma-separated)
	brokers := strings.Split(u.Host, ",")

	var tlsConfig *tls.Config
	if u.Scheme == "kafka+ssl" || u.Scheme == "kafkas" || tlsCfg.Enabled {
		tlsConfig, err = tlsutil.BuildTLSConfig(tlsCfg)
		if err != nil {
			return nil, nil, err
		}
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
