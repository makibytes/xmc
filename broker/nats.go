//go:build nats

package broker

import (
	"os"

	"github.com/makibytes/xmc/broker/backends"
	natspkg "github.com/makibytes/xmc/broker/nats"
	"github.com/makibytes/xmc/cmd"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs natspkg.ConnArguments
	var retention string
	var maxMsgs int64
	var subjects []string

	defaultServer := os.Getenv("NMC_SERVER")
	if defaultServer == "" {
		defaultServer = "nats://localhost:4222"
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:   "nmc",
		Short: "NATS Messaging Client",
		Long:  "Command-line interface for NATS messaging",
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
			c.PersistentFlags().StringVarP(&connArgs.User, "user", "u", os.Getenv("NMC_USER"), "Username for authentication")
			c.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", os.Getenv("NMC_PASSWORD"), "Password for authentication")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
			c.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")
		},
		Queue: func() (backends.QueueBackend, error) { return natspkg.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return natspkg.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return natspkg.NewQueueAdapter(connArgs) },
		Manage: cmd.NewManageCommand(cmd.ManageSpec{
			ListQueues: func() ([]backends.QueueInfo, error) {
				streams, err := natspkg.ListStreams(connArgs)
				if err != nil {
					return nil, err
				}
				out := make([]backends.QueueInfo, len(streams))
				for i, s := range streams {
					out[i] = backends.QueueInfo{Name: s.Name, MessageCount: int64(s.MessageCount)}
				}
				return out, nil
			},
			CreateQueue: &cmd.ManageAction{
				SetupFlags: func(c *cobra.Command) {
					c.Flags().StringVar(&retention, "retention", "workqueue", "Stream retention policy (workqueue, limits, interest)")
					c.Flags().Int64Var(&maxMsgs, "max-msgs", 0, "Maximum number of messages (0 = unlimited)")
					c.Flags().StringSliceVar(&subjects, "subject", nil, "NATS subjects to bind (default: xmc.queue.<name>)")
				},
				Run: func(queue string) error { return natspkg.CreateStream(connArgs, queue, retention, maxMsgs, subjects) },
			},
			DeleteQueue: &cmd.ManageAction{Run: func(queue string) error { return natspkg.DeleteStream(connArgs, queue) }},
		}),
	})
}
