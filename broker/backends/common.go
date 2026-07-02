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
// persistent flags on c, reading defaults from environment variables with the
// given prefix (e.g. "KMC_" → KMC_SERVER, KMC_USER, KMC_PASSWORD).
func RegisterCommonFlags(c *cobra.Command, args *CommonConnArgs, envPrefix string, defaultServer string) {
	flags := c.PersistentFlags()
	flags.StringVarP(&args.Server, "server", "s", envOr(envPrefix+"SERVER", defaultServer), "Server URL")
	flags.StringVarP(&args.User, "user", "u", envOr(envPrefix+"USER", ""), "Username")
	flags.StringVarP(&args.Password, "password", "p", envOr(envPrefix+"PASSWORD", ""), "Password")
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
