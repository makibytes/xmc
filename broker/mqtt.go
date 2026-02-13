//go:build mqtt

package broker

import (
	"github.com/makibytes/xmc/broker/mqtt"
	"github.com/spf13/cobra"
)

// GetRootCommand returns the MQTT root command
func GetRootCommand() *cobra.Command {
	return mqtt.NewBroker().RootCommand()
}
