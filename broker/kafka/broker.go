//go:build kafka

package kafka

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/makibytes/amc/log"
	"github.com/segmentio/kafka-go"
	"github.com/spf13/cobra"
)

// Broker implements the Kafka messaging broker
type Broker struct {
	connArgs ConnArguments
}

// NewBroker creates a new Kafka broker instance
func NewBroker() *Broker {
	return &Broker{
		connArgs: ConnArguments{},
	}
}

// RootCommand returns the root command for Kafka operations
func (b *Broker) RootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "amc",
		Short: "Apache Kafka Messaging Client",
		Long:  "Command-line interface for Apache Kafka messaging",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	// Add persistent connection flags with environment variable defaults
	var defaultKafkaServer = os.Getenv("AMC_SERVER")
	if defaultKafkaServer == "" {
		defaultKafkaServer = "kafka://localhost:9092"
	}
	var defaultSaslUser = os.Getenv("AMC_USER")
	var defaultSaslPassword = os.Getenv("AMC_PASSWORD")

	rootCmd.PersistentFlags().StringVarP(&b.connArgs.Server, "server", "s", defaultKafkaServer, "Server URL (kafka://broker1:9092 or kafka://broker1:9092,broker2:9092)")
	rootCmd.PersistentFlags().StringVarP(&b.connArgs.User, "user", "u", defaultSaslUser, "Username for SASL authentication")
	rootCmd.PersistentFlags().StringVarP(&b.connArgs.Password, "password", "p", defaultSaslPassword, "Password for SASL authentication")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	// Add subcommands
	rootCmd.AddCommand(b.putCommand())
	rootCmd.AddCommand(b.getCommand())
	rootCmd.AddCommand(b.peekCommand())

	return rootCmd
}

func (b *Broker) putCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "put <topic> [message]",
		Short: "Publish a message to a Kafka topic",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.doPut(cmd, args)
		},
	}

	cmd.Flags().StringP("contenttype", "T", "text/plain", "MIME type of message data")
	cmd.Flags().StringP("correlationid", "C", "", "Correlation ID for request/response")
	cmd.Flags().StringP("key", "K", "", "Message key for partitioning")
	cmd.Flags().StringP("messageid", "I", "", "Message ID")
	cmd.Flags().StringSliceP("property", "P", []string{}, "Message headers in key=value format")

	return cmd
}

func (b *Broker) getCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <topic>",
		Short: "Subscribe and receive a message from a Kafka topic",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.doGet(cmd, args)
		},
	}

	cmd.Flags().StringP("group", "g", "amc-consumer-group", "Consumer group ID")
	cmd.Flags().IntP("number", "n", 1, "Number of messages to fetch")
	cmd.Flags().Float32P("timeout", "t", 0.1, "Seconds to wait")
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("wait", "w", false, "Wait (endless) for a message to arrive")

	return cmd
}

func (b *Broker) peekCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peek <topic>",
		Short: "Peek at messages in a Kafka topic (Note: Kafka will commit offset)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.doPeek(cmd, args)
		},
	}

	cmd.Flags().StringP("group", "g", "amc-peek-group", "Consumer group ID")
	cmd.Flags().IntP("number", "n", 1, "Number of messages to peek")
	cmd.Flags().BoolP("wait", "w", false, "Wait (endless) for a message to arrive")
	cmd.Flags().Float32P("timeout", "t", 0.1, "Seconds to wait")

	return cmd
}

func (b *Broker) doPut(cmd *cobra.Command, args []string) error {
	// Get message content (from args or stdin)
	var data []byte
	if len(args) > 1 {
		data = []byte(args[1])
	} else {
		var err error
		data, err = b.dataFromStdin()
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
	properties := make(map[string]string)
	propertySlice, _ := cmd.Flags().GetStringSlice("property")
	for _, property := range propertySlice {
		keyValue := strings.SplitN(property, "=", 2)
		if len(keyValue) == 2 {
			properties[keyValue[0]] = keyValue[1]
		} else {
			return fmt.Errorf("invalid property: %s", property)
		}
	}

	// Create send arguments
	putArgs := SendArguments{
		Topic:         args[0],
		Message:       data,
		Key:           key,
		Properties:    properties,
		ContentType:   contenttype,
		CorrelationID: correlationid,
		MessageID:     messageid,
	}

	return PublishMessage(context.Background(), b.connArgs, putArgs)
}

func (b *Broker) doGet(cmd *cobra.Command, args []string) error {
	// Parse command flags
	groupID, _ := cmd.Flags().GetString("group")
	number, _ := cmd.Flags().GetInt("number")
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	wait, _ := cmd.Flags().GetBool("wait")
	quiet, _ := cmd.Flags().GetBool("quiet")

	withHeaderAndProperties := log.IsVerbose
	withApplicationProperties := !quiet || log.IsVerbose

	getArgs := ReceiveArguments{
		Topic:                     args[0],
		Timeout:                   timeout,
		Wait:                      wait,
		Number:                    number,
		GroupID:                   groupID,
		WithHeaderAndProperties:   withHeaderAndProperties,
		WithApplicationProperties: withApplicationProperties,
	}

	message, err := SubscribeMessage(context.Background(), b.connArgs, getArgs)
	if err != nil {
		if err == context.DeadlineExceeded {
			return nil // no message when timeout occurs -> no error
		}
		return err
	}
	if message == nil {
		return fmt.Errorf("no message available")
	}

	return b.handleMessage(message, getArgs)
}

func (b *Broker) doPeek(cmd *cobra.Command, args []string) error {
	// Parse command flags
	groupID, _ := cmd.Flags().GetString("group")
	number, _ := cmd.Flags().GetInt("number")
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	wait, _ := cmd.Flags().GetBool("wait")

	peekArgs := ReceiveArguments{
		Topic:                     args[0],
		Timeout:                   timeout,
		Wait:                      wait,
		Number:                    number,
		GroupID:                   groupID,
		WithHeaderAndProperties:   true,
		WithApplicationProperties: true,
	}

	message, err := SubscribeMessage(context.Background(), b.connArgs, peekArgs)
	if err != nil {
		if err == context.DeadlineExceeded {
			return nil // no message when timeout occurs -> no error
		}
		return err
	}
	if message == nil {
		return fmt.Errorf("no message available")
	}

	return b.handleMessage(message, peekArgs)
}

func (b *Broker) handleMessage(message *kafka.Message, args ReceiveArguments) error {
	if args.WithHeaderAndProperties {
		fmt.Fprintf(os.Stderr, "Topic: %s, Partition: %d, Offset: %d\n", message.Topic, message.Partition, message.Offset)
		if message.Key != nil && len(message.Key) > 0 {
			fmt.Fprintf(os.Stderr, "Key: %s\n", string(message.Key))
		}
		fmt.Fprintf(os.Stderr, "Time: %s\n", message.Time)
	}

	if args.WithApplicationProperties && len(message.Headers) > 0 {
		propertiesString := ""
		for _, h := range message.Headers {
			if propertiesString != "" {
				propertiesString += ","
			}
			propertiesString += fmt.Sprintf("%s=%s", h.Key, string(h.Value))
		}
		fmt.Fprintf(os.Stderr, "Headers: %s\n", propertiesString)
	}

	// Always print message data
	fmt.Print(string(message.Value))
	// Add newline if stdout just for better readability
	if log.IsStdout {
		fmt.Println()
	}

	return nil
}

func (b *Broker) dataFromStdin() ([]byte, error) {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		return io.ReadAll(os.Stdin)
	} else {
		return nil, fmt.Errorf("no message provided and no data in stdin")
	}
}