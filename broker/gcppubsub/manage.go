//go:build google

package gcppubsub

import (
	"context"

	"google.golang.org/api/iterator"

	"github.com/makibytes/xmc/broker/backends"
)

func ListTopics(args ConnArguments) ([]backends.TopicInfo, error) {
	client, err := Connect(context.Background(), args)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var topics []backends.TopicInfo
	it := client.Topics(context.Background())
	for {
		t, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		topics = append(topics, backends.TopicInfo{Name: t.ID()})
	}
	return topics, nil
}

func ListSubscriptions(args ConnArguments) ([]backends.QueueInfo, error) {
	client, err := Connect(context.Background(), args)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var subs []backends.QueueInfo
	it := client.Subscriptions(context.Background())
	for {
		s, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		subs = append(subs, backends.QueueInfo{Name: s.ID()})
	}
	return subs, nil
}
