package amqpcommon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/log"
)

// ConnArguments holds common AMQP connection parameters
type ConnArguments struct {
	Server   string
	User     string
	Password string
	TLS      TLSConfig
}

// TLSConfig holds TLS connection parameters
type TLSConfig struct {
	Enabled    bool
	CACert     string // Path to CA certificate file
	ClientCert string // Path to client certificate file
	ClientKey  string // Path to client key file
	Insecure   bool   // Skip certificate verification
}

// Connect establishes an AMQP 1.0 connection with SASL authentication
func Connect(args ConnArguments) (*amqp.Conn, *amqp.Session, error) {
	ctx := context.WithoutCancel(context.Background())

	var connOptions *amqp.ConnOptions
	if args.User == "" {
		connOptions = &amqp.ConnOptions{
			ContainerID: "xmcContainer",
			SASLType:    amqp.SASLTypeAnonymous(),
		}
	} else {
		connOptions = &amqp.ConnOptions{
			ContainerID: "xmcContainer",
			SASLType:    amqp.SASLTypePlain(args.User, args.Password),
		}
	}

	// Configure TLS if URL scheme is amqps:// or TLS is explicitly enabled
	if args.TLS.Enabled || strings.HasPrefix(args.Server, "amqps://") {
		tlsConfig, err := buildTLSConfig(args.TLS)
		if err != nil {
			return nil, nil, fmt.Errorf("TLS configuration error: %w", err)
		}
		connOptions.TLSConfig = tlsConfig
		log.Verbose("TLS enabled")
	}

	log.Verbose("connecting to %s...\n", args.Server)
	connection, err := amqp.Dial(ctx, args.Server, connOptions)
	if err != nil {
		return nil, nil, err
	}

	session, err := connection.NewSession(ctx, nil)
	if err != nil {
		return nil, nil, err
	}

	return connection, session, nil
}

func buildTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.Insecure,
	}

	// Load CA certificate if provided
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

	// Load client certificate and key if provided
	if cfg.ClientCert != "" && cfg.ClientKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}
