//go:build artemis

package artemis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/amc/log"
	"github.com/spf13/cobra"
)

type Broker struct {
	connArgs ConnArguments
}

// NewBroker creates a new Artemis broker
func NewBroker() *Broker {
	return &Broker{
		connArgs: ConnArguments{},
	}
}

func (b *Broker) RootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "amc",
		Short: "Apache Artemis Messaging Client",
		Long:  "Command-line interface for Apache Artemis messaging",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	// Add persistent connection flags with environment variable defaults
	var defaultAmqpServer = os.Getenv("AMC_SERVER")
	if defaultAmqpServer == "" {
		defaultAmqpServer = "amqp://localhost:5672"
	}
	var defaultSaslUser = os.Getenv("AMC_USER")
	var defaultSaslPassword = os.Getenv("AMC_PASSWORD")

	rootCmd.PersistentFlags().StringVarP(&b.connArgs.Server, "server", "s", defaultAmqpServer, "Server URL")
	rootCmd.PersistentFlags().StringVarP(&b.connArgs.User, "user", "u", defaultSaslUser, "Username for SASL PLAIN login")
	rootCmd.PersistentFlags().StringVarP(&b.connArgs.Password, "password", "p", defaultSaslPassword, "Password for SASL PLAIN login")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	// Add commands
	rootCmd.AddCommand(b.putCommand())
	rootCmd.AddCommand(b.getCommand())
	rootCmd.AddCommand(b.peekCommand())

	return rootCmd
}

func (b *Broker) putCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "put <address> [message]",
		Short: "Send a message to an address",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.doPut(cmd, args)
		},
	}

	// Add put-specific flags (same as the original cmd/put.go)
	cmd.Flags().StringP("contenttype", "T", "text/plain", "MIME type of message data")
	cmd.Flags().StringP("correlationid", "C", "", "Correlation ID for request/response")
	cmd.Flags().BoolP("durable", "d", false, "Create durable address if it doesn't exist")
	cmd.Flags().StringP("messageid", "I", "", "Message ID")
	cmd.Flags().BoolP("multicast", "m", false, "Send to a multicast address, default is anycast")
	cmd.Flags().Uint8P("priority", "Y", 4, "Priority of the message (0-9)")
	cmd.Flags().StringSliceP("property", "P", []string{}, "Message properties in key=value format")
	cmd.Flags().StringP("replyto", "R", "", "Reply to address for request/response")
	cmd.Flags().String("subject", "", "Subject")
	cmd.Flags().String("to", "", "Intended destination node")

	return cmd
}

func (b *Broker) getCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <queue>",
		Short: "Fetch a message from a queue",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.doGet(cmd, args)
		},
	}

	// Add get-specific flags (same as the original cmd/get.go)
	cmd.Flags().BoolP("durable", "d", false, "Create durable queue if it doesn't exist")
	cmd.Flags().BoolP("multicast", "m", false, "Multicast: subscribe to address, default is anycast: get from queue")
	cmd.Flags().IntP("number", "n", 1, "Number of messages to fetch, 0 = all")
	cmd.Flags().Float32P("timeout", "t", 0.1, "Seconds to wait")
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("wait", "w", false, "Wait (endless) for a message to arrive")

	return cmd
}

func (b *Broker) peekCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peek <queue>",
		Short: "Look into a message, but let it stay in the queue",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.doPeek(cmd, args)
		},
	}

	// Add peek-specific flags (same as the original cmd/peek.go)
	cmd.Flags().BoolP("durable", "d", true, "Create durable queue if it doesn't exist")
	cmd.Flags().IntP("number", "n", 1, "Number of messages to fetch, 0 = all")
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
	durable, _ := cmd.Flags().GetBool("durable")
	messageid, _ := cmd.Flags().GetString("messageid")
	multicast, _ := cmd.Flags().GetBool("multicast")
	priority, _ := cmd.Flags().GetUint8("priority")
	replyto, _ := cmd.Flags().GetString("replyto")
	subject, _ := cmd.Flags().GetString("subject")
	to, _ := cmd.Flags().GetString("to")

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

	// Create send arguments
	putArgs := SendArguments{
		Address:       args[0],
		ContentType:   contenttype,
		CorrelationID: correlationid,
		Durable:       durable,
		Message:       data,
		MessageID:     messageid,
		Multicast:     multicast,
		Priority:      priority,
		Properties:    properties,
		ReplyTo:       replyto,
		Subject:       subject,
		To:            to,
	}

	// Connect and send
	connection, session, err := Connect(b.connArgs)
	if err != nil {
		return err
	}
	defer connection.Close()
	defer session.Close(context.Background())

	return SendMessage(context.Background(), session, putArgs)
}

func (b *Broker) doGet(cmd *cobra.Command, args []string) error {
	// Parse command flags
	number, _ := cmd.Flags().GetInt("number")
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	wait, _ := cmd.Flags().GetBool("wait")
	if wait {
		timeout = 0
	}
	multicast, _ := cmd.Flags().GetBool("multicast")
	durable, _ := cmd.Flags().GetBool("durable")
	quiet, _ := cmd.Flags().GetBool("quiet")

	withHeaderAndProperties := log.IsVerbose
	withApplicationProperties := !quiet || log.IsVerbose

	getArgs := ReceiveArguments{
		Acknowledge:               true,
		Durable:                   durable,
		Multicast:                 multicast,
		Number:                    number,
		Queue:                     args[0],
		Timeout:                   timeout,
		Wait:                      wait,
		WithHeaderAndProperties:   withHeaderAndProperties,
		WithApplicationProperties: withApplicationProperties,
	}

	// Connect and receive
	connection, session, err := Connect(b.connArgs)
	if err != nil {
		return err
	}
	defer connection.Close()
	defer session.Close(context.Background())

	message, err := ReceiveMessage(session, getArgs)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
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
	number, _ := cmd.Flags().GetInt("number")
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	wait, _ := cmd.Flags().GetBool("wait")
	if wait {
		timeout = 0
	}
	durable, _ := cmd.Flags().GetBool("durable")

	peekArgs := ReceiveArguments{
		Acknowledge: false, // peek doesn't acknowledge (remove) messages
		Durable:     durable,
		Number:      number,
		Queue:       args[0],
		Timeout:     timeout,
		Wait:        wait,
	}

	// Connect and peek
	connection, session, err := Connect(b.connArgs)
	if err != nil {
		return err
	}
	defer connection.Close()
	defer session.Close(context.Background())

	message, err := ReceiveMessage(session, peekArgs)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil // no message when timeout occurs -> no error
		}
		return err
	}
	if message == nil {
		return fmt.Errorf("no message available")
	}

	return b.handleMessage(message, peekArgs)
}

func (b *Broker) handleMessage(message *amqp.Message, args ReceiveArguments) error {
	if args.WithHeaderAndProperties {
		fmt.Fprintf(os.Stderr, "Header: %+v\n", message.Header)
		fmt.Fprintf(os.Stderr, "MessageProperties: %+v\n", message.Properties)
	}
	if args.WithApplicationProperties && len(message.ApplicationProperties) > 0 {
		propertiesString := ""
		for k, v := range message.ApplicationProperties {
			if propertiesString != "" {
				propertiesString += ","
			}
			propertiesString += fmt.Sprintf("%s=%s", k, v)
		}
		fmt.Fprintf(os.Stderr, "Properties: %s\n", propertiesString)
	}

	// Always print message data
	fmt.Print(string(message.GetData()))
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
