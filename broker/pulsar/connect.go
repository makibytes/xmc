//go:build pulsar

package pulsar

import (
	"fmt"
	"time"

	pulsar "github.com/apache/pulsar-client-go/pulsar"
	"github.com/apache/pulsar-client-go/pulsar/auth"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/broker/tlsutil"
)

type ConnArguments = backends.CommonConnArgs

// Connect creates and returns a Pulsar client.
func Connect(args ConnArguments) (pulsar.Client, error) {
	if args.User != "" && args.Token != "" {
		return nil, fmt.Errorf("use --user/--password for password auth or --token for token auth, not both")
	}

	opts := pulsar.ClientOptions{
		URL:               args.Server,
		OperationTimeout:  30 * time.Second,
		ConnectionTimeout: 30 * time.Second,
	}

	switch {
	case args.Token != "":
		opts.Authentication = pulsar.NewAuthenticationToken(args.Token)
	case args.User != "":
		provider, err := auth.NewAuthenticationBasic(args.User, args.Password)
		if err != nil {
			return nil, fmt.Errorf("creating basic auth: %w", err)
		}
		opts.Authentication = provider
	}

	if args.TLS.Enabled || args.TLS.CACert != "" || args.TLS.ClientCert != "" {
		tlsCfg, err := tlsutil.BuildTLSConfig(args.TLS)
		if err != nil {
			return nil, fmt.Errorf("building TLS config: %w", err)
		}
		opts.TLSConfig = tlsCfg
		opts.TLSAllowInsecureConnection = args.TLS.Insecure
		if args.TLS.ClientCert != "" {
			if args.User != "" || args.Token != "" {
				return nil, fmt.Errorf("TLS client certificate auth cannot be combined with --user/--password or --token")
			}
			opts.Authentication = pulsar.NewAuthenticationTLS(args.TLS.ClientCert, args.TLS.ClientKey)
		}
	}

	client, err := pulsar.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("connecting to Pulsar at %s: %w", args.Server, err)
	}
	return client, nil
}
