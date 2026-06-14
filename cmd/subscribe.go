package cmd

import (
	"context"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/spf13/cobra"
)

// NewSubscribeCommand creates a subscribe command for topic-based brokers
func NewSubscribeCommand(backend backends.TopicBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subscribe <topic>",
		Short: "Subscribe and receive a message from a topic",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doSubscribe(cmd, args, backend)
		},
	}

	cmd.Flags().StringP("group", "g", "xmc-consumer-group", "Consumer group ID")
	cmd.Flags().VarP(newDurationValue(100*time.Millisecond, time.Second), "timeout", "t", "Time to wait for a message (e.g. \"100ms\", \"5s\")")
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("wait", "w", true, "Wait (endless) for a message to arrive")
	cmd.Flags().IntP("count", "n", 1, "Number of messages to receive (0 = until interrupted)")
	cmd.Flags().BoolP("json", "J", false, "Output messages as JSON")
	cmd.Flags().StringP("format", "F", "", "Output format string, e.g. \"%i %s\\n\" (overrides --json)")
	cmd.Flags().Bool("ndjson", false, "Output one lossless JSON record per line (overrides --format/--json)")
	cmd.Flags().StringP("selector", "S", "", "Filter messages by property expression (e.g. \"color='red'\")")
	cmd.Flags().BoolP("durable", "D", false, "Create a durable subscription that survives disconnection")
	cmd.Flags().String("for", "", "Stream for a bounded duration then stop (e.g. \"30s\", \"5m\")")
	cmd.Flags().Bool("stats", false, "Print live throughput statistics to stderr while streaming")

	return cmd
}

func doSubscribe(cmd *cobra.Command, args []string, backend backends.TopicBackend) error {
	groupID, _ := cmd.Flags().GetString("group")
	timeout := float32(getDuration(cmd, "timeout").Seconds())
	wait, _ := cmd.Flags().GetBool("wait")
	quiet, _ := cmd.Flags().GetBool("quiet")
	count, _ := cmd.Flags().GetInt("count")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	selector, _ := cmd.Flags().GetString("selector")
	durable, _ := cmd.Flags().GetBool("durable")
	format, _ := cmd.Flags().GetString("format")
	ndjson, _ := cmd.Flags().GetBool("ndjson")
	forStr, _ := cmd.Flags().GetString("for")
	stats, _ := cmd.Flags().GetBool("stats")

	duration, err := parseDurationFlag(forStr)
	if err != nil {
		return err
	}
	follow := duration > 0 || stats

	opts := backends.SubscribeOptions{
		Topic:     args[0],
		GroupID:   groupID,
		Timeout:   timeout,
		Wait:      wait,
		Verbosity: commandVerbosity(quiet),
		Selector:  selector,
		Durable:   durable,
	}

	return runConsume(func(ctx context.Context) (*backends.Message, error) {
		return backend.Subscribe(ctx, opts)
	}, consumeConfig{
		count:      count,
		jsonOutput: jsonOutput,
		verbosity:  opts.Verbosity,
		format:     format,
		ndjson:     ndjson,
		follow:     follow,
	}, duration, stats)
}
