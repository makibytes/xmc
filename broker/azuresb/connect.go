//go:build azure

package azuresb

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"
)

type ConnArguments struct {
	ConnectionString string
	Namespace        string
}

func Connect(args ConnArguments) (*azservicebus.Client, error) {
	if args.ConnectionString != "" {
		client, err := azservicebus.NewClientFromConnectionString(args.ConnectionString, nil)
		if err != nil {
			return nil, fmt.Errorf("connecting via connection string: %w", err)
		}
		return client, nil
	}
	if args.Namespace != "" {
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("creating Azure credential: %w", err)
		}
		client, err := azservicebus.NewClient(args.Namespace, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("connecting to namespace %s: %w", args.Namespace, err)
		}
		return client, nil
	}
	return nil, fmt.Errorf("set --connection-string or --namespace")
}

func AdminClient(args ConnArguments) (*admin.Client, error) {
	if args.ConnectionString != "" {
		adm, err := admin.NewClientFromConnectionString(args.ConnectionString, nil)
		if err != nil {
			return nil, fmt.Errorf("creating admin client: %w", err)
		}
		return adm, nil
	}
	if args.Namespace != "" {
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("creating Azure credential: %w", err)
		}
		adm, err := admin.NewClient(args.Namespace, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("creating admin client for %s: %w", args.Namespace, err)
		}
		return adm, nil
	}
	return nil, fmt.Errorf("set --connection-string or --namespace")
}

func ensureQueue(ctx context.Context, adm *admin.Client, name string) error {
	if isSubQueue(name) {
		return nil // sub-queues (e.g. /$deadletterqueue) are not top-level entities
	}
	_, err := adm.GetQueue(ctx, name, nil)
	if err == nil {
		return nil
	}
	_, err = adm.CreateQueue(ctx, name, nil)
	if err != nil {
		return fmt.Errorf("creating queue %s: %w", name, err)
	}
	return nil
}

// isSubQueue returns true for Azure Service Bus sub-queue paths such as
// "myqueue/$deadletterqueue" or "myqueue/$DeadLetterQueue". These are
// not standalone entities and must not be created or looked up via the
// admin API.
func isSubQueue(name string) bool {
	return strings.Contains(name, "/$")
}

func ensureTopic(ctx context.Context, adm *admin.Client, topic string) error {
	_, err := adm.GetTopic(ctx, topic, nil)
	if err == nil {
		return nil
	}
	_, err = adm.CreateTopic(ctx, topic, nil)
	if err != nil {
		return fmt.Errorf("creating topic %s: %w", topic, err)
	}
	return nil
}

func ensureTopicAndSub(ctx context.Context, adm *admin.Client, topic, sub string) error {
	if err := ensureTopic(ctx, adm, topic); err != nil {
		return err
	}

	_, err := adm.GetSubscription(ctx, topic, sub, nil)
	if err != nil {
		if _, createErr := adm.CreateSubscription(ctx, topic, sub, nil); createErr != nil {
			return fmt.Errorf("creating subscription %s/%s: %w", topic, sub, createErr)
		}
	}
	return nil
}
