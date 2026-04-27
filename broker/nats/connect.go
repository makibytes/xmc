//go:build nats

package nats

import (
	"fmt"

	"github.com/makibytes/xmc/broker/tlsutil"
	natsclient "github.com/nats-io/nats.go"
)

// ConnArguments holds parameters for establishing a NATS connection.
type ConnArguments struct {
	Server   string
	User     string
	Password string
	TLS      TLSConfig
}

// TLSConfig is an alias for the shared TLS configuration.
type TLSConfig = tlsutil.TLSConfig

// Connect creates and returns a NATS connection.
func Connect(args ConnArguments) (*natsclient.Conn, error) {
	opts := []natsclient.Option{}

	if args.User != "" {
		opts = append(opts, natsclient.UserInfo(args.User, args.Password))
	}

	if args.TLS.Enabled || args.TLS.CACert != "" || args.TLS.ClientCert != "" {
		tlsCfg, err := tlsutil.BuildTLSConfig(args.TLS)
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
