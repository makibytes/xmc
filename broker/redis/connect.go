//go:build redmc

package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/makibytes/xmc/broker/tlsutil"
)

type ConnArguments struct {
	Server   string
	User     string
	Password string
	TLS      TLSConfig
}

type TLSConfig = tlsutil.TLSConfig

func Connect(args ConnArguments) (*redis.Client, error) {
	opt, err := redis.ParseURL(args.Server)
	if err != nil {
		return nil, fmt.Errorf("parsing Redis URL %q: %w", args.Server, err)
	}

	if args.User != "" {
		opt.Username = args.User
	}
	if args.Password != "" {
		opt.Password = args.Password
	}

	if args.TLS.Enabled || args.TLS.CACert != "" || args.TLS.ClientCert != "" {
		tlsCfg, err := tlsutil.BuildTLSConfig(args.TLS)
		if err != nil {
			return nil, fmt.Errorf("building TLS config: %w", err)
		}
		opt.TLSConfig = tlsCfg
	}

	client := redis.NewClient(opt)

	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("connecting to Redis %s: %w", args.Server, err)
	}

	return client, nil
}
