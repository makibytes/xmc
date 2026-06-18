//go:build azmc

package azuresb

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"

	"github.com/makibytes/xmc/broker/backends"
)

func ListQueues(args ConnArguments) ([]backends.QueueInfo, error) {
	adm, err := AdminClient(args)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	var queues []backends.QueueInfo

	pager := adm.NewListQueuesPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing queues: %w", err)
		}
		for _, q := range page.Queues {
			info := backends.QueueInfo{Name: q.QueueName}

			props, err := adm.GetQueueRuntimeProperties(ctx, q.QueueName, nil)
			if err == nil {
				info.MessageCount = int64(props.ActiveMessageCount)
			}

			queues = append(queues, info)
		}
	}

	return queues, nil
}

func ListTopics(args ConnArguments) ([]backends.TopicInfo, error) {
	adm, err := AdminClient(args)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	var topics []backends.TopicInfo

	pager := adm.NewListTopicsPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing topics: %w", err)
		}
		for _, t := range page.Topics {
			topics = append(topics, backends.TopicInfo{Name: t.TopicName})
		}
	}

	return topics, nil
}

func GetQueueStats(args ConnArguments, queue string) (*backends.QueueStats, error) {
	adm, err := AdminClient(args)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	props, err := adm.GetQueueRuntimeProperties(ctx, queue, nil)
	if err != nil {
		return nil, fmt.Errorf("getting queue stats: %w", err)
	}

	return &backends.QueueStats{
		Name:          queue,
		MessageCount:  int64(props.ActiveMessageCount),
		ConsumerCount: 0,
		EnqueueCount:  int64(props.TotalMessageCount),
	}, nil
}

func PurgeQueue(args ConnArguments, queue string) (int64, error) {
	client, err := Connect(args)
	if err != nil {
		return 0, err
	}
	defer client.Close(context.Background()) //nolint:errcheck

	adm, err := AdminClient(args)
	if err != nil {
		return 0, err
	}

	ctx := context.Background()
	props, err := adm.GetQueueRuntimeProperties(ctx, queue, nil)
	var approxCount int64
	if err == nil {
		approxCount = int64(props.ActiveMessageCount)
	}

	recv, err := client.NewReceiverForQueue(queue, &azservicebus.ReceiverOptions{
		ReceiveMode: azservicebus.ReceiveModeReceiveAndDelete,
	})
	if err != nil {
		return 0, fmt.Errorf("creating purge receiver: %w", err)
	}
	defer recv.Close(ctx) //nolint:errcheck

	var purged int64
	for {
		msgs, err := recv.ReceiveMessages(ctx, 100, nil)
		if err != nil || len(msgs) == 0 {
			break
		}
		purged += int64(len(msgs))
	}

	if purged == 0 {
		purged = approxCount
	}

	return purged, nil
}

// ensure admin import is used
var _ *admin.Client
