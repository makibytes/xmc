//go:build !artemis && !kafka && !ibmmq && !mqtt && !rabbitmq

package broker

import "github.com/spf13/cobra"

// GetRootCommand returns nil when no broker is selected
func GetRootCommand() *cobra.Command {
	return nil
}