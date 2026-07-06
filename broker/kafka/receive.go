//go:build kafka

package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/makibytes/xmc/log"
	"github.com/segmentio/kafka-go"
)

// fetchMessage fetches a single message from reader, which the caller owns and
// may reuse across calls (see TopicAdapter.getReader) — consumer-group joins are
// comparatively expensive (often 1-3s), and forward/bridge/drain-mode subscribe
// call this in a tight poll loop, so a fresh Reader per call would spend most of
// its time rejoining the group instead of fetching.
func fetchMessage(ctx context.Context, reader *kafka.Reader, args ReceiveArguments, brokers []string) (*kafka.Message, error) {
	// Apply timeout if specified
	if args.Timeout > 0 && !args.Wait {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(float64(args.Timeout)*float64(time.Second)))
		defer cancel()
	}

	log.Verbose("📩 subscribing to topic %s...", args.Topic)
	message, err := reader.FetchMessage(ctx)
	if err != nil {
		return nil, hintAdvertisedListeners(fmt.Errorf("failed to fetch message: %w", err), brokers)
	}

	// Commit the offset (acknowledge). Only group readers track offsets;
	// partition readers (--partition) have nothing to commit.
	if args.Partition < 0 && args.GroupID != "" {
		if err := reader.CommitMessages(ctx, message); err != nil {
			log.Verbose("⚠️  failed to commit message: %v", err)
		}
	}

	return &message, nil
}
