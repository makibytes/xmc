package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// NewReplyCommand creates a reply command: the responder side of the
// request-reply pattern. It consumes requests from a queue and, for each one,
// sends a response to the request's reply-to destination. The response
// correlation ID is derived from the request (its correlation ID, falling back
// to its message ID) so the original requester can match the reply.
//
// This complements the request command, which only implements the requester
// side, and makes xmc a self-contained request-reply testing tool.
func NewReplyCommand(backend backends.QueueBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "reply <queue> [response]",
		Aliases: []string{"respond"},
		Short:   "Reply to requests on a queue; supports --for/--forever streaming",
		Long: `Listens on a queue and responds to each incoming request by sending a message
to the request's reply-to destination.

The response payload can be one of:
  - a fixed string (given as an argument or piped via stdin),
  - the request payload itself (--echo), or
  - the standard output of a shell command fed the request on stdin (--command).

The responder runs until interrupted (Ctrl-C) unless --count, --for, or
--forever sets a different bound. The reply's correlation ID is taken from the
request's correlation ID, falling back to the request's message ID.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doReply(cmd, args, backend)
		},
	}

	cmd.Flags().BoolP("echo", "e", false, "Echo the request payload back as the response")
	cmd.Flags().StringP("command", "x", "", "Run a shell command per request; its stdout becomes the response")
	cmd.Flags().StringP("reply-to", "R", "", "Fallback reply destination when a request carries no reply-to")
	cmd.Flags().StringP("content-type", "T", "text/plain", "MIME type of the response data")
	cmd.Flags().StringSliceP("property", "P", []string{}, "Response properties in key=value format")
	cmd.Flags().IntP("count", "n", 0, "Number of requests to serve (0 = serve until interrupted)")
	cmd.Flags().VarP(newDurationValue(0, time.Second), "timeout", "t", "Time to wait per request (e.g. \"5s\"; 0 = wait indefinitely)")
	cmd.Flags().BoolP("quiet", "q", false, "Suppress per-request logging")
	cmd.Flags().StringP("selector", "S", "", "Only handle requests matching this selector expression")
	cmd.Flags().String("for", "", "Run for a bounded duration then stop (e.g. \"30s\", \"5m\")")
	cmd.Flags().Bool("forever", false, "Run until interrupted (no time bound)")
	// Accept legacy concatenated spellings (--contenttype) as aliases of the
	// kebab-case names (--content-type).
	cmd.Flags().SetNormalizeFunc(aliasNormalize)

	return cmd
}

// replyConfig holds the resolved options for producing responses.
type replyConfig struct {
	echo        bool
	command     string
	staticBody  []byte
	replyTo     string
	contentType string
	properties  map[string]any
	quiet       bool
}

func doReply(cmd *cobra.Command, args []string, backend backends.QueueBackend) error {
	echo, _ := cmd.Flags().GetBool("echo")
	command, _ := cmd.Flags().GetString("command")
	fallbackReplyTo, _ := cmd.Flags().GetString("reply-to")
	contentType, _ := cmd.Flags().GetString("content-type")
	count, _ := cmd.Flags().GetInt("count")
	timeout := float32(getDuration(cmd, "timeout").Seconds())
	quiet, _ := cmd.Flags().GetBool("quiet")
	selector, _ := cmd.Flags().GetString("selector")

	sf, err := ParseStreamingFlags(cmd)
	if err != nil {
		return err
	}
	duration := sf.Duration
	follow := sf.Follow
	if (sf.Duration > 0 || sf.Forever) && !cmd.Flags().Changed("count") {
		count = 0
	}

	properties, err := parsePropertiesFlag(cmd.Flags())
	if err != nil {
		return err
	}

	if echo && command != "" {
		return fmt.Errorf("--echo and --command are mutually exclusive")
	}

	cfg := replyConfig{
		echo:        echo,
		command:     command,
		replyTo:     fallbackReplyTo,
		contentType: contentType,
		properties:  properties,
		quiet:       quiet,
	}

	// A fixed response body is only meaningful when not echoing or shelling out.
	if !echo && command == "" {
		body, err := readReplyBody(args)
		if err != nil {
			return err
		}
		cfg.staticBody = body
	}

	parentCtx := cmd.Context()
	ctx, stop := streamContext(duration, parentCtx)
	defer stop()

	// With no timeout we block until each request arrives; ctx cancellation
	// (Ctrl-C) is what ends an otherwise idle responder.
	wait := timeout <= 0

	served := 0
	for count == 0 || served < count {
		if ctx.Err() != nil {
			return finishReply(served)
		}

		message, err := backend.Receive(ctx, backends.ReceiveOptions{
			Queue:       args[0],
			Timeout:     timeout,
			Wait:        wait,
			Acknowledge: true,
			Verbosity:   backends.VerbosityNormal,
			Selector:    selector,
		})

		switch {
		case errors.Is(err, context.Canceled):
			return finishReply(served)
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, backends.ErrNoMessageAvailable), message == nil && err == nil:
			if follow || count == 0 {
				continue // keep waiting for the next request
			}
			return finishReply(served)
		case err != nil:
			return err
		}

		served++
		if err := respondToRequest(ctx, backend, message, cfg); err != nil {
			return err
		}
	}

	return finishReply(served)
}

func finishReply(served int) error {
	log.Verbose("served %d request(s)", served)
	return nil
}

// respondToRequest builds and sends the reply for a single request. A request
// that cannot be answered (no reply-to and no fallback) is skipped rather than
// aborting the responder.
func respondToRequest(ctx context.Context, backend backends.QueueBackend, request *backends.Message, cfg replyConfig) error {
	replyTo := request.ReplyTo
	if replyTo == "" {
		replyTo = cfg.replyTo
	}
	if replyTo == "" {
		log.Verbose("request has no reply-to and no --replyto fallback; skipping")
		return nil
	}

	body, err := replyBody(cfg, request)
	if err != nil {
		// A failing command should not tear down the whole responder.
		log.Error("reply command failed: %s\n", err)
		return nil
	}

	correlationID := request.CorrelationID
	if correlationID == "" {
		correlationID = request.MessageID
	}

	if !cfg.quiet {
		log.Verbose("replying to %s (correlation %q)", replyTo, correlationID)
	}

	return backend.Send(ctx, backends.SendOptions{
		Queue:         replyTo,
		Message:       body,
		Properties:    cfg.properties,
		CorrelationID: correlationID,
		ContentType:   cfg.contentType,
	})
}

func replyBody(cfg replyConfig, request *backends.Message) ([]byte, error) {
	switch {
	case cfg.echo:
		return request.Data, nil
	case cfg.command != "":
		return runShellCommand(cfg.command, request.Data)
	default:
		return cfg.staticBody, nil
	}
}

// runShellCommand executes command via the system shell, feeding the input to
// its stdin and returning its stdout. The command's stderr is inherited so its
// diagnostics are visible. Shared by the reply responder and forward --command.
func runShellCommand(command string, input []byte) ([]byte, error) {
	c := exec.Command("sh", "-c", command)
	c.Stdin = bytes.NewReader(input)
	c.Stderr = os.Stderr
	return c.Output()
}

// readReplyBody resolves the static response body from the command argument or,
// failing that, stdin. It is only called when neither --echo nor --command is set.
func readReplyBody(args []string) ([]byte, error) {
	if len(args) > 1 {
		return []byte(args[1]), nil
	}
	stat, err := os.Stdin.Stat()
	if err == nil && (stat.Mode()&os.ModeCharDevice) == 0 {
		return io.ReadAll(os.Stdin)
	}
	return nil, fmt.Errorf("no response provided: pass a response argument, --echo, or --command")
}
