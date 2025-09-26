package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/makibytes/amc/broker/backends"
	"github.com/spf13/cobra"
)

// NewPublishCommand creates a publish command for topic-based brokers
func NewPublishCommand(backend backends.TopicBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "publish <topic> [message]",
		Short: "Publish a message to a topic",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doPublish(cmd, args, backend)
		},
	}

	cmd.Flags().StringP("contenttype", "T", "text/plain", "MIME type of message data")
	cmd.Flags().StringP("correlationid", "C", "", "Correlation ID for request/response")
	cmd.Flags().StringP("key", "K", "", "Message key for partitioning")
	cmd.Flags().StringP("messageid", "I", "", "Message ID")
	cmd.Flags().StringSliceP("property", "P", []string{}, "Message properties in key=value format")

	return cmd
}

func doPublish(cmd *cobra.Command, args []string, backend backends.TopicBackend) error {
	// Get message content (from args or stdin)
	var data []byte
	if len(args) > 1 {
		data = []byte(args[1])
	} else {
		var err error
		data, err = readFromStdin()
		if err != nil {
			return err
		}
	}

	// Parse command flags
	contenttype, _ := cmd.Flags().GetString("contenttype")
	correlationid, _ := cmd.Flags().GetString("correlationid")
	key, _ := cmd.Flags().GetString("key")
	messageid, _ := cmd.Flags().GetString("messageid")

	// Parse properties
	properties := make(map[string]any)
	propertySlice, _ := cmd.Flags().GetStringSlice("property")
	for _, property := range propertySlice {
		keyValue := strings.SplitN(property, "=", 2)
		if len(keyValue) == 2 {
			properties[keyValue[0]] = keyValue[1]
		} else {
			return fmt.Errorf("invalid property: %s", property)
		}
	}

	// Create publish options
	opts := backends.PublishOptions{
		Topic:         args[0],
		Message:       data,
		Key:           key,
		Properties:    properties,
		MessageID:     messageid,
		CorrelationID: correlationid,
		ContentType:   contenttype,
	}

	return backend.Publish(context.Background(), opts)
}
