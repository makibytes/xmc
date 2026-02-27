//go:build mqtt

package broker

import (
"os"

"github.com/makibytes/xmc/broker/backends"
"github.com/makibytes/xmc/broker/mqtt"
"github.com/makibytes/xmc/cmd"
"github.com/makibytes/xmc/log"
"github.com/spf13/cobra"
)

// GetRootCommand returns the MQTT root command.
func GetRootCommand() *cobra.Command {
rootCmd := &cobra.Command{
Use:   "mmc",
Short: "MQTT Messaging Client",
Long:  "Command-line interface for MQTT messaging",
PersistentPreRun: func(c *cobra.Command, args []string) {
if verbose, _ := c.Flags().GetBool("verbose"); verbose {
log.IsVerbose = true
}
},
}

defaultServer := os.Getenv("MMC_SERVER")
if defaultServer == "" {
defaultServer = "tcp://localhost:1883"
}

var connArgs mqtt.ConnArguments
connArgs.Server = defaultServer
connArgs.User = os.Getenv("MMC_USER")
connArgs.Password = os.Getenv("MMC_PASSWORD")

rootCmd.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "MQTT broker URL")
rootCmd.PersistentFlags().StringVarP(&connArgs.User, "user", "u", connArgs.User, "Username")
rootCmd.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", connArgs.Password, "Password")
rootCmd.PersistentFlags().StringVar(&connArgs.ClientID, "client-id", "", "MQTT client ID (auto-generated if empty)")
rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

// TLS flags
rootCmd.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
rootCmd.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
rootCmd.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
rootCmd.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
rootCmd.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")

// Queue commands
queueFactory := cmd.QueueAdapterFactory(func() (backends.QueueBackend, error) {
return mqtt.NewQueueAdapter(connArgs)
})
rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewSendCommand, queueFactory))
rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReceiveCommand, queueFactory))
rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewPeekCommand, queueFactory))
rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewRequestCommand, queueFactory))

// Topic commands
topicFactory := cmd.TopicAdapterFactory(func() (backends.TopicBackend, error) {
return mqtt.NewTopicAdapter(connArgs)
})
rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewPublishCommand, topicFactory))
rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewSubscribeCommand, topicFactory))

rootCmd.AddCommand(cmd.NewVersionCommand())

return rootCmd
}
