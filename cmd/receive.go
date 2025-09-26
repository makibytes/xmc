package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/makibytes/amc/broker/backends"
	"github.com/makibytes/amc/log"
	"github.com/spf13/cobra"
)

// NewReceiveCommand creates a receive command for queue-based brokers
func NewReceiveCommand(backend backends.QueueBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "receive <queue>",
		Short: "Receive a message from a queue (destructive read)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doReceive(cmd, args, backend, true)
		},
	}

	cmd.Flags().Float32P("timeout", "t", 0.1, "Seconds to wait")
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("wait", "w", false, "Wait (endless) for a message to arrive")

	return cmd
}

func doReceive(cmd *cobra.Command, args []string, backend backends.QueueBackend, acknowledge bool) error {
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	wait, _ := cmd.Flags().GetBool("wait")
	quiet, _ := cmd.Flags().GetBool("quiet")

	withHeaderAndProperties := log.IsVerbose
	withApplicationProperties := !quiet || log.IsVerbose

	opts := backends.ReceiveOptions{
		Queue:                     args[0],
		Timeout:                   timeout,
		Wait:                      wait,
		Acknowledge:               acknowledge,
		WithHeaderAndProperties:   withHeaderAndProperties,
		WithApplicationProperties: withApplicationProperties,
	}

	message, err := backend.Receive(context.Background(), opts)
	if err != nil {
		if err == context.DeadlineExceeded {
			return nil // no message when timeout occurs -> no error
		}
		return err
	}
	if message == nil {
		return fmt.Errorf("no message available")
	}

	// Display: internal metadata only if verbose, properties always (unless quiet)
	return displayMessage(message, opts.WithHeaderAndProperties, !quiet)
}

func displayMessage(message *backends.Message, withHeader, withProps bool) error {
	if withHeader && len(message.InternalMetadata) > 0 {
		for k, v := range message.InternalMetadata {
			fmt.Fprintf(os.Stderr, "%s: %v\n", k, v)
		}
	}

	if withProps && len(message.Properties) > 0 {
		propertiesString := ""
		for k, v := range message.Properties {
			if propertiesString != "" {
				propertiesString += ","
			}
			propertiesString += fmt.Sprintf("%s=%v", k, v)
		}
		fmt.Fprintf(os.Stderr, "Properties: %s\n", propertiesString)
	}

	// Always print message data
	fmt.Print(string(message.Data))
	// Add newline if stdout just for better readability
	if log.IsStdout {
		fmt.Println()
	}

	return nil
}
