//go:build nats

package nats

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	natsclient "github.com/nats-io/nats.go"
)

// ConnArguments holds parameters for establishing a NATS connection.
type ConnArguments struct {
	Server   string
	User     string
	Password string
	TLS      TLSConfig
}

// TLSConfig holds TLS parameters for NATS connections.
type TLSConfig struct {
	Enabled    bool
	CACert     string
	ClientCert string
	ClientKey  string
	Insecure   bool
}

// Connect creates and returns a NATS connection.
func Connect(args ConnArguments) (*natsclient.Conn, error) {
	opts := []natsclient.Option{}

	if args.User != "" {
		opts = append(opts, natsclient.UserInfo(args.User, args.Password))
	}

	if args.TLS.Enabled || args.TLS.CACert != "" || args.TLS.ClientCert != "" {
		tlsCfg, err := buildTLSConfig(args.TLS)
		if err != nil {
			return nil, fmt.Errorf("building TLS config: %w", err)
		}
		opts = append(opts, natsclient.Secure(tlsCfg))
	}

	nc, err := natsclient.Connect(args.Server, opts...)
	if err != nil {
		return nil, fmt.Errorf("connecting to NATS server %s: %w", args.Server, err)
	}

	return nc, nil
}

// ConnectWithJetStream connects to NATS and returns both the connection and JetStream context.
func ConnectWithJetStream(args ConnArguments) (*natsclient.Conn, natsclient.JetStreamContext, error) {
	nc, err := Connect(args)
	if err != nil {
		return nil, nil, err
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("creating JetStream context: %w", err)
	}

	return nc, js, nil
}

func buildTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: cfg.Insecure, //nolint:gosec
	}

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
			return nil, fmt.Errorf("loading client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return tlsCfg, nil
}
