//go:build mqtt

package mqtt

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"os"
	"sync"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/broker/tlsutil"
)

// Connect5 creates and connects an MQTT 5 client (the default). The connection
// manager reconnects automatically; call Disconnect to release it.
func Connect5(args ConnArguments) (*autopaho.ConnectionManager, error) {
	clientID := args.ClientID
	if clientID == "" {
		clientID = fmt.Sprintf("xmc-%d-%s", os.Getpid(), backends.RandomSuffix())
	}

	u, err := url.Parse(args.Server)
	if err != nil {
		return nil, fmt.Errorf("invalid MQTT server URL %q: %w", args.Server, err)
	}

	var tlsCfg *tls.Config
	if args.TLS.Enabled {
		tlsCfg, err = tlsutil.BuildTLSConfig(args.TLS)
		if err != nil {
			return nil, fmt.Errorf("TLS configuration error: %w", err)
		}
		// --tls with a plain scheme: upgrade so the dialer actually uses TLS.
		if u.Scheme == "tcp" || u.Scheme == "mqtt" || u.Scheme == "" {
			u.Scheme = "ssl"
		}
	}

	// Surface the real reason when the initial connection can't be made;
	// AwaitConnection itself only reports its context deadline.
	var lastErrMu sync.Mutex
	var lastErr error

	cfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{u},
		TlsCfg:                        tlsCfg,
		KeepAlive:                     30,
		CleanStartOnInitialConnection: true,
		ConnectUsername:               args.User,
		OnConnectError: func(err error) {
			lastErrMu.Lock()
			lastErr = err
			lastErrMu.Unlock()
		},
		ClientConfig: paho.ClientConfig{
			ClientID: clientID,
		},
	}
	if args.User != "" {
		cfg.ConnectPassword = []byte(args.Password)
	}

	// The context passed to NewConnection bounds the manager's lifetime, not
	// one command, so adapters own it until Close → Disconnect.
	cm, err := autopaho.NewConnection(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("MQTT connect failed: %w", err)
	}

	awaitCtx, cancel := context.WithTimeout(context.Background(), tokenTimeout)
	defer cancel()
	if err := cm.AwaitConnection(awaitCtx); err != nil {
		_ = cm.Disconnect(context.Background())
		lastErrMu.Lock()
		defer lastErrMu.Unlock()
		if lastErr != nil {
			return nil, fmt.Errorf("MQTT connect failed: %w", lastErr)
		}
		return nil, fmt.Errorf("MQTT connect failed: %w", err)
	}

	return cm, nil
}
