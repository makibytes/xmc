//go:build pulsar

package pulsar

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	pulsar "github.com/apache/pulsar-client-go/pulsar"
)

// ConnArguments holds parameters for establishing a Pulsar connection.
type ConnArguments struct {
	Server   string
	User     string
	Password string
	TLS      TLSConfig
}

// TLSConfig holds TLS parameters for Pulsar connections.
type TLSConfig struct {
	Enabled    bool
	CACert     string
	ClientCert string
	ClientKey  string
	Insecure   bool
}

// Connect creates and returns a Pulsar client.
func Connect(args ConnArguments) (pulsar.Client, error) {
	opts := pulsar.ClientOptions{
		URL:               args.Server,
		OperationTimeout:  30 * time.Second,
		ConnectionTimeout: 30 * time.Second,
	}

	if args.User != "" {
		opts.Authentication = pulsar.NewAuthenticationToken(args.Password)
	}

	if args.TLS.Enabled || args.TLS.CACert != "" || args.TLS.ClientCert != "" {
		tlsCfg, err := buildTLSConfig(args.TLS)
		if err != nil {
			return nil, fmt.Errorf("building TLS config: %w", err)
		}
		opts.TLSConfig = tlsCfg
		opts.TLSTrustCertsFilePath = args.TLS.CACert
		opts.TLSAllowInsecureConnection = args.TLS.Insecure
		if args.TLS.ClientCert != "" {
			opts.Authentication = pulsar.NewAuthenticationTLS(args.TLS.ClientCert, args.TLS.ClientKey)
		}
	}

	client, err := pulsar.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("connecting to Pulsar at %s: %w", args.Server, err)
	}
	return client, nil
}

func buildTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	tlsCfg := &tls.Config{InsecureSkipVerify: cfg.Insecure} //nolint:gosec
	if cfg.CACert != "" {
		caCert, err := os.ReadFile(cfg.CACert)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA cert")
		}
		tlsCfg.RootCAs = pool
	}
	if cfg.ClientCert != "" && cfg.ClientKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("loading client cert: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return tlsCfg, nil
}
