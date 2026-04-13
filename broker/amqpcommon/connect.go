package amqpcommon

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/broker/tlsutil"
	"github.com/makibytes/xmc/log"
)

// ConnArguments holds common AMQP connection parameters
type ConnArguments struct {
	Server   string
	User     string
	Password string
	TLS      TLSConfig
}

// TLSConfig is an alias for the shared TLS configuration.
type TLSConfig = tlsutil.TLSConfig

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
		tlsCfg, err := tlsutil.BuildTLSConfig(args.TLS)
		if err != nil {
			return nil, nil, fmt.Errorf("TLS configuration error: %w", err)
		}
		connOptions.TLSConfig = tlsCfg
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

