//go:build ibmmq

package ibmmq

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ibm-messaging/mq-golang/v5/ibmmq"
	"github.com/makibytes/amc/log"
	"github.com/spf13/cobra"
)

// Broker implements the IBM MQ messaging broker
type Broker struct {
	connArgs ConnArguments
}

// NewBroker creates a new IBM MQ broker instance
func NewBroker() *Broker {
	return &Broker{
		connArgs: ConnArguments{},
	}
}

// RootCommand returns the root command for IBM MQ operations
func (b *Broker) RootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "amc",
		Short: "IBM MQ Messaging Client",
		Long:  "Command-line interface for IBM MQ messaging",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	// Add persistent connection flags with environment variable defaults
	var defaultMQServer = os.Getenv("AMC_SERVER")
	if defaultMQServer == "" {
		defaultMQServer = "ibmmq://localhost:1414/QM1?channel=SYSTEM.DEF.SVRCONN"
	}
	var defaultUser = os.Getenv("AMC_USER")
	var defaultPassword = os.Getenv("AMC_PASSWORD")
	var defaultQueueManager = os.Getenv("AMC_QUEUE_MANAGER")
	var defaultChannel = os.Getenv("AMC_CHANNEL")

	rootCmd.PersistentFlags().StringVarP(&b.connArgs.Server, "server", "s", defaultMQServer, "Server URL (ibmmq://host:port/QMGR?channel=CHANNEL)")
	rootCmd.PersistentFlags().StringVarP(&b.connArgs.User, "user", "u", defaultUser, "Username for authentication")
	rootCmd.PersistentFlags().StringVarP(&b.connArgs.Password, "password", "p", defaultPassword, "Password for authentication")
	rootCmd.PersistentFlags().StringVarP(&b.connArgs.QueueManager, "qmgr", "m", defaultQueueManager, "Queue manager name (overrides URL)")
	rootCmd.PersistentFlags().StringVarP(&b.connArgs.Channel, "channel", "c", defaultChannel, "Channel name (overrides URL)")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	// Add subcommands
	rootCmd.AddCommand(b.putCommand())
	rootCmd.AddCommand(b.getCommand())
	rootCmd.AddCommand(b.peekCommand())

	return rootCmd
}

func (b *Broker) putCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "put <queue> [message]",
		Short: "Send a message to an IBM MQ queue",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.doPut(cmd, args)
		},
	}

	cmd.Flags().StringP("contenttype", "T", "text/plain", "MIME type of message data")
	cmd.Flags().StringP("correlationid", "C", "", "Correlation ID for request/response")
	cmd.Flags().StringP("messageid", "I", "", "Message ID")
	cmd.Flags().IntP("priority", "Y", 4, "Priority of the message (0-9)")
	cmd.Flags().BoolP("persistent", "d", false, "Make message persistent")
	cmd.Flags().StringP("replyto", "R", "", "Reply to queue for request/response")
	cmd.Flags().StringSliceP("property", "P", []string{}, "Message properties in key=value format")

	return cmd
}

func (b *Broker) getCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <queue>",
		Short: "Receive a message from an IBM MQ queue",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.doGet(cmd, args)
		},
	}

	cmd.Flags().IntP("number", "n", 1, "Number of messages to fetch")
	cmd.Flags().Float32P("timeout", "t", 0.1, "Seconds to wait")
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("wait", "w", false, "Wait (endless) for a message to arrive")

	return cmd
}

func (b *Broker) peekCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peek <queue>",
		Short: "Browse a message in an IBM MQ queue without removing it",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return b.doPeek(cmd, args)
		},
	}

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
	messageid, _ := cmd.Flags().GetString("messageid")
	priority, _ := cmd.Flags().GetInt("priority")
	persistent, _ := cmd.Flags().GetBool("persistent")
	replyto, _ := cmd.Flags().GetString("replyto")

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

	var persistence int
	if persistent {
		persistence = 1
	} else {
		persistence = 0
	}

	// Create send arguments
	putArgs := SendArguments{
		Queue:         args[0],
		Message:       data,
		Properties:    properties,
		ContentType:   contenttype,
		CorrelationID: correlationid,
		MessageID:     messageid,
		Priority:      priority,
		Persistence:   persistence,
		ReplyTo:       replyto,
	}

	// Connect to IBM MQ
	qMgr, err := Connect(b.connArgs)
	if err != nil {
		return err
	}
	defer qMgr.Disc()

	return SendMessage(qMgr, putArgs)
}

func (b *Broker) doGet(cmd *cobra.Command, args []string) error {
	// Parse command flags
	number, _ := cmd.Flags().GetInt("number")
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	wait, _ := cmd.Flags().GetBool("wait")
	quiet, _ := cmd.Flags().GetBool("quiet")

	withHeaderAndProperties := log.IsVerbose
	withApplicationProperties := !quiet || log.IsVerbose

	getArgs := ReceiveArguments{
		Queue:                     args[0],
		Timeout:                   timeout,
		Wait:                      wait,
		Number:                    number,
		Acknowledge:               true, // get = destructive read
		WithHeaderAndProperties:   withHeaderAndProperties,
		WithApplicationProperties: withApplicationProperties,
	}

	// Connect to IBM MQ
	qMgr, err := Connect(b.connArgs)
	if err != nil {
		return err
	}
	defer qMgr.Disc()

	md, message, err := ReceiveMessage(qMgr, getArgs)
	if err != nil {
		if strings.Contains(err.Error(), "timeout") {
			return nil // no message when timeout occurs -> no error
		}
		return err
	}
	if message == nil {
		return fmt.Errorf("no message available")
	}

	return b.handleMessage(md, message, getArgs)
}

func (b *Broker) doPeek(cmd *cobra.Command, args []string) error {
	// Parse command flags
	number, _ := cmd.Flags().GetInt("number")
	timeout, _ := cmd.Flags().GetFloat32("timeout")
	wait, _ := cmd.Flags().GetBool("wait")

	peekArgs := ReceiveArguments{
		Queue:                     args[0],
		Timeout:                   timeout,
		Wait:                      wait,
		Number:                    number,
		Acknowledge:               false, // peek = browse (non-destructive)
		WithHeaderAndProperties:   true,
		WithApplicationProperties: true,
	}

	// Connect to IBM MQ
	qMgr, err := Connect(b.connArgs)
	if err != nil {
		return err
	}
	defer qMgr.Disc()

	md, message, err := ReceiveMessage(qMgr, peekArgs)
	if err != nil {
		if strings.Contains(err.Error(), "timeout") {
			return nil // no message when timeout occurs -> no error
		}
		return err
	}
	if message == nil {
		return fmt.Errorf("no message available")
	}

	return b.handleMessage(md, message, peekArgs)
}

func (b *Broker) handleMessage(md *ibmmq.MQMD, message []byte, args ReceiveArguments) error {
	if args.WithHeaderAndProperties {
		fmt.Fprintf(os.Stderr, "Format: %s\n", strings.TrimSpace(md.Format))
		fmt.Fprintf(os.Stderr, "Priority: %d\n", md.Priority)
		fmt.Fprintf(os.Stderr, "Persistence: %d\n", md.Persistence)

		// Print message ID if set
		msgId := strings.TrimRight(string(md.MsgId[:]), "\x00")
		if msgId != "" {
			fmt.Fprintf(os.Stderr, "MessageID: %x\n", md.MsgId)
		}

		// Print correlation ID if set
		correlId := strings.TrimRight(string(md.CorrelId[:]), "\x00")
		if correlId != "" {
			fmt.Fprintf(os.Stderr, "CorrelationID: %x\n", md.CorrelId)
		}

		// Print reply-to queue if set
		if md.ReplyToQ != "" {
			fmt.Fprintf(os.Stderr, "ReplyToQ: %s\n", strings.TrimSpace(md.ReplyToQ))
		}
	}

	// Always print message data
	fmt.Print(string(message))
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