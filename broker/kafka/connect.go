//go:build kafka

package kafka

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/broker/tlsutil"
	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl"
	"github.com/segmentio/kafka-go/sasl/plain"
)

type ConnArguments = backends.CommonConnArgs

// parseKafkaURL parses the server URL and returns brokers and TLS config
func parseKafkaURL(serverURL string, tlsCfg tlsutil.TLSConfig) ([]string, *tls.Config, error) {
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

// buildDialer returns a *kafka.Dialer configured for tlsConfig/SASL, or nil when
// neither applies. Writers and readers only need a dialer when one of them is set.
func buildDialer(connArgs ConnArguments, tlsConfig *tls.Config) *kafka.Dialer {
	sasl := getSASLMechanism(connArgs.User, connArgs.Password)
	if tlsConfig == nil && sasl == nil {
		return nil
	}
	return &kafka.Dialer{TLS: tlsConfig, SASLMechanism: sasl}
}

// dnsNoHostRe matches Go's "lookup <host>: no such host" DNS resolution error,
// optionally followed by "on <resolver>" (seen on some platforms/resolvers).
var dnsNoHostRe = regexp.MustCompile(`lookup ([A-Za-z0-9._-]+)(?: on \S+)?: no such host`)

// hintAdvertisedListeners appends an advertised.listeners hint when err is a DNS
// "no such host" failure for a host that is NOT one of the configured bootstrap
// brokers. That signature means the broker handed back an advertised address the
// client cannot reach — the most common Kafka misconfiguration: a broker
// advertising its container hostname (e.g. "kafka:9092") to clients outside its
// network. If the failing host IS a bootstrap host, the -s URL itself is wrong,
// which is a different problem, so err is returned unchanged.
func hintAdvertisedListeners(err error, brokers []string) error {
	if err == nil {
		return nil
	}
	m := dnsNoHostRe.FindStringSubmatch(err.Error())
	if m == nil {
		return err
	}
	host := m[1]
	for _, b := range brokers {
		if hostOnly(b) == host {
			return err
		}
	}
	return fmt.Errorf("%w\n\nhint: the broker advertised host %q is not reachable from "+
		"this client. Kafka clients connect to the broker's advertised.listeners, not the "+
		"-s bootstrap URL. Fix the broker's advertised.listeners "+
		"(e.g. KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://localhost:9092).", err, host)
}

// hostOnly strips a trailing ":port" from a bootstrap broker address, if present.
func hostOnly(broker string) string {
	if h, _, err := net.SplitHostPort(broker); err == nil {
		return h
	}
	return broker
}
