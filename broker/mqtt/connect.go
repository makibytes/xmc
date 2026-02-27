//go:build mqtt

package mqtt

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/rand"
	"os"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// ConnArguments holds MQTT connection parameters.
type ConnArguments struct {
	Server   string // e.g. "tcp://localhost:1883" or "ssl://localhost:8883"
	User     string
	Password string
	TLS      TLSConfig
	ClientID string // auto-generated if empty
}

// TLSConfig holds TLS parameters for the MQTT connection.
type TLSConfig struct {
	Enabled    bool
	CACert     string
	ClientCert string
	ClientKey  string
	Insecure   bool
}

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
		tlsCfg, err := buildTLSConfig(args.TLS)
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

func buildTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	tlsCfg := &tls.Config{
		InsecureSkipVerify: cfg.Insecure, //nolint:gosec
	}

	if cfg.CACert != "" {
		caPEM, err := os.ReadFile(cfg.CACert)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert %q: %w", cfg.CACert, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("failed to parse CA cert %q", cfg.CACert)
		}
		tlsCfg.RootCAs = pool
	}

	if cfg.ClientCert != "" && cfg.ClientKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("loading client cert/key: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return tlsCfg, nil
}
