//go:build pulsar

package pulsar

import (
	"fmt"
	"time"

	pulsar "github.com/apache/pulsar-client-go/pulsar"
	"github.com/makibytes/xmc/broker/tlsutil"
)

// ConnArguments holds parameters for establishing a Pulsar connection.
type ConnArguments struct {
	Server   string
	User     string
	Password string
	TLS      TLSConfig
}

// TLSConfig is an alias for the shared TLS configuration.
type TLSConfig = tlsutil.TLSConfig

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
		tlsCfg, err := tlsutil.BuildTLSConfig(args.TLS)
		if err != nil {
			return nil, fmt.Errorf("building TLS config: %w", err)
		}
		opts.TLSConfig = tlsCfg
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

