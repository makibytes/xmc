//go:build mqtt

package mqtt

import (
	"fmt"
	"math/rand"
	"os"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/makibytes/xmc/broker/tlsutil"
)

// ConnArguments holds MQTT connection parameters.
type ConnArguments struct {
	Server   string // e.g. "tcp://localhost:1883" or "ssl://localhost:8883"
	User     string
	Password string
	TLS      TLSConfig
	ClientID string // auto-generated if empty
}

// TLSConfig is an alias for the shared TLS configuration.
type TLSConfig = tlsutil.TLSConfig

// Connect creates and connects a new MQTT client using the provided arguments.
func Connect(args ConnArguments) (pahomqtt.Client, error) {
	clientID := args.ClientID
	if clientID == "" {
		clientID = fmt.Sprintf("xmc-%d-%d", os.Getpid(), rand.Int31()) //nolint:gosec
	}

	opts := pahomqtt.NewClientOptions().
		AddBroker(args.Server).
		SetClientID(clientID).
		SetCleanSession(true).
		SetAutoReconnect(false)

	if args.User != "" {
		opts.SetUsername(args.User)
		opts.SetPassword(args.Password)
	}

	if args.TLS.Enabled {
		tlsCfg, err := tlsutil.BuildTLSConfig(args.TLS)
		if err != nil {
			return nil, fmt.Errorf("TLS configuration error: %w", err)
		}
		opts.SetTLSConfig(tlsCfg)
	}

	client := pahomqtt.NewClient(opts)
	token := client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("MQTT connect failed: %w", err)
	}

	return client, nil
}

