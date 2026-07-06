//go:build mqtt

package mqtt

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/broker/tlsutil"
)

// ConnArguments holds MQTT connection parameters.
type ConnArguments struct {
	backends.CommonConnArgs
	ClientID string // auto-generated if empty
}

// ConnectV3 creates and connects a legacy MQTT 3.1.1 client (--mqtt-version 3).
func ConnectV3(args ConnArguments) (pahomqtt.Client, error) {
	clientID := args.ClientID
	if clientID == "" {
		clientID = fmt.Sprintf("xmc-%d-%d", os.Getpid(), rand.Int31()) //nolint:gosec
	}

	opts := pahomqtt.NewClientOptions().
		AddBroker(args.Server).
		SetClientID(clientID).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetMaxReconnectInterval(30 * time.Second)

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

// rejectV3Metadata fails a v3 send/publish that carries metadata the MQTT
// 3.1.1 protocol cannot represent, instead of silently dropping it. The
// default MQTT 5 adapters carry all of these natively.
func rejectV3Metadata(hasProps bool, messageID, correlationID, replyTo, contentType string, ttl int64) error {
	if hasProps || messageID != "" || correlationID != "" || replyTo != "" || contentType != "" || ttl > 0 {
		return fmt.Errorf("application properties and metadata require MQTT 5; remove --mqtt-version 3 or drop the metadata flags")
	}
	return nil
}
