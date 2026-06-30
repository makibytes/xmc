package cmd

import (
	"context"
	"io"
	"time"

	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// registerProduceFlags adds the common flags shared by send and publish commands.
func registerProduceFlags(cmd *cobra.Command) {
	cmd.Flags().StringP("content-type", "T", "text/plain", "MIME type of message data")
	cmd.Flags().StringP("correlation-id", "C", "", "Correlation ID for request/response")
	cmd.Flags().StringP("message-id", "I", "", "Message ID")
	cmd.Flags().IntP("priority", "Y", 4, "Priority of the message (0-9)")
	cmd.Flags().BoolP("persistent", "d", false, "Make message persistent")
	cmd.Flags().StringP("reply-to", "R", "", "Reply to address for request/response")
	cmd.Flags().StringSliceP("property", "P", []string{}, "Message properties in key=value format")
	cmd.Flags().IntP("count", "n", 1, "Number of times to send/publish the message")
	cmd.Flags().VarP(newDurationValue(0, time.Millisecond), "ttl", "E", "Message time-to-live (e.g. \"5s\", \"1m\"; 0 = no expiry)")
	cmd.Flags().BoolP("lines", "l", false, "Read stdin line by line, send each line as a separate message")
	cmd.Flags().Bool("ndjson", false, "Read newline-delimited JSON records from stdin (lossless import)")
	cmd.Flags().Float64("rate", 0, "Throttle to at most this many messages per second (0 = unlimited)")
	cmd.Flags().SetNormalizeFunc(aliasNormalize)
}

// produceFlags holds the parsed flag values common to both send and publish.
type produceFlags struct {
	contentType   string
	correlationID string
	messageID     string
	replyTo       string
	priority      int
	persistent    bool
	count         int
	ttl           int64
	lines         bool
	ndjson        bool
	properties    map[string]any
	limiter       *rateLimiter
}

func parseProduceFlags(cmd *cobra.Command) (produceFlags, error) {
	contenttype, _ := cmd.Flags().GetString("content-type")
	correlationid, _ := cmd.Flags().GetString("correlation-id")
	messageid, _ := cmd.Flags().GetString("message-id")
	priority, _ := cmd.Flags().GetInt("priority")
	persistent, _ := cmd.Flags().GetBool("persistent")
	replyto, _ := cmd.Flags().GetString("reply-to")
	count, _ := cmd.Flags().GetInt("count")
	ttl := getDuration(cmd, "ttl").Milliseconds()
	lines, _ := cmd.Flags().GetBool("lines")
	ndjson, _ := cmd.Flags().GetBool("ndjson")
	rate, _ := cmd.Flags().GetFloat64("rate")

	properties, err := parsePropertiesFlag(cmd.Flags())
	if err != nil {
		return produceFlags{}, err
	}

	return produceFlags{
		contentType:   contenttype,
		correlationID: correlationid,
		messageID:     messageid,
		replyTo:       replyto,
		priority:      priority,
		persistent:    persistent,
		count:         count,
		ttl:           ttl,
		lines:         lines,
		ndjson:        ndjson,
		properties:    properties,
		limiter:       newRateLimiter(rate),
	}, nil
}

// emitter is a function that sends a single message payload to the broker.
type emitter func(ctx context.Context, data []byte) error

// emitterFromRecord is a function that sends a pre-parsed NDJSON record to the
// broker, preserving its full metadata.
type emitterFromRecord func(ctx context.Context, rec messageRecord) error

// runProduce drives the produce loop shared by send and publish:
// NDJSON import, line-delimited mode, or a counted send of a single payload.
// The input reader is used for stdin-based modes (lines, ndjson, pipe).
func runProduce(ctx context.Context, input io.Reader, args []string, pf produceFlags,
	emit emitter, emitRecord emitterFromRecord, verb string,
) error {
	if pf.ndjson {
		sent, err := forEachRecord(input, func(rec messageRecord) error {
			pf.limiter.wait()
			return emitRecord(ctx, rec)
		})
		if err != nil {
			return err
		}
		log.Verbose("%s %d messages", verb, sent)
		return nil
	}

	if pf.lines {
		sent, err := forEachInputLine(input, func(line string) error {
			pf.limiter.wait()
			return emit(ctx, []byte(line))
		})
		if err != nil {
			return err
		}
		log.Verbose("%s %d messages", verb, sent)
		return nil
	}

	data, err := readCommandMessage(args, input)
	if err != nil {
		return err
	}

	for i := 0; i < pf.count; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		pf.limiter.wait()
		if err := emit(ctx, data); err != nil {
			return err
		}
		if pf.count > 1 {
			log.Verbose("%s message %d/%d", verb, i+1, pf.count)
		}
	}

	return nil
}
