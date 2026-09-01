package backends

import (
	"os"

	"github.com/makibytes/xmc/broker/tlsutil"
	"github.com/spf13/cobra"
)

// CommonConnArgs holds the standard connection parameters shared across most
// non-cloud broker implementations.
type CommonConnArgs struct {
	Server   string
	User     string
	Password string
	Token    string
	TLS      TLSConfig
}

// TLSConfig is an alias for the shared TLS configuration.
type TLSConfig = tlsutil.TLSConfig

// RegisterCommonFlags registers the --server/-s, --user/-u, --password/-p
// persistent flags on c, bound to server/user/password and reading defaults
// from environment variables with the given prefix (e.g. "KMC_" →
// KMC_SERVER, KMC_USER, KMC_PASSWORD; defaultServer is used when
// <prefix>SERVER is unset). Takes field pointers rather than a *CommonConnArgs
// so brokers whose connection struct doesn't happen to match that shape
// (Artemis/RabbitMQ share amqpcommon.ConnArguments, which has no Token field)
// can still use it. serverHelp/userHelp/passwordHelp are the three flags'
// descriptions, which vary by broker (Kafka's URL-format hint, MQTT's "MQTT
// broker URL", SASL PLAIN login vs SASL vs plain "authentication", or no
// suffix at all).
func RegisterCommonFlags(c *cobra.Command, server, user, password *string, envPrefix, defaultServer, serverHelp, userHelp, passwordHelp string) {
	flags := c.PersistentFlags()
	flags.StringVarP(server, "server", "s", envOr(envPrefix+"SERVER", defaultServer), serverHelp)
	flags.StringVarP(user, "user", "u", envOr(envPrefix+"USER", ""), userHelp)
	flags.StringVarP(password, "password", "p", envOr(envPrefix+"PASSWORD", ""), passwordHelp)
}

// RegisterTLSFlags registers the --tls, --ca-cert, --cert, --key-file,
// --insecure persistent flags on c, bound to the given TLSConfig.
func RegisterTLSFlags(c *cobra.Command, tls *TLSConfig) {
	flags := c.PersistentFlags()
	flags.BoolVar(&tls.Enabled, "tls", false, "Enable TLS connection")
	flags.StringVar(&tls.CACert, "ca-cert", "", "Path to CA certificate file")
	flags.StringVar(&tls.ClientCert, "cert", "", "Path to client certificate file")
	flags.StringVar(&tls.ClientKey, "key-file", "", "Path to client private key file")
	flags.BoolVar(&tls.Insecure, "insecure", false, "Skip TLS certificate verification")
}

// envOr returns the value of the environment variable named by key, or
// fallback if the variable is empty or unset.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
