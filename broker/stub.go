// NOTE: this constraint must be extended whenever a new broker tag is added.
//go:build !artemis && !kafka && !ibmmq && !mqtt && !rabbitmq && !nats && !pulsar && !redis && !google && !aws && !azure

package broker

import "github.com/spf13/cobra"

// GetRootCommand returns nil when no broker is selected
func GetRootCommand() *cobra.Command {
	return nil
}