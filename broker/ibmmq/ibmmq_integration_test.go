//go:build ibmmq && integration

package ibmmq

import (
	"context"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/test/integration"
)

var testServer string
var testUser string
var testPass string

func TestMain(m *testing.M) {
	ctx := context.Background()
	broker, err := integration.StartIBMMQ(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "skipping IBM MQ integration tests: failed to start IBM MQ container: %v\n", err)
		fmt.Fprintf(os.Stderr, "note: IBM MQ tests require CGO_ENABLED=1 and the IBM MQ client SDK\n")
		os.Exit(0)
	}
	defer broker.Terminate(ctx)

	// Parse URL: ibmmq://admin:passw0rd@host:port/QM1
	u, err := url.Parse(broker.URL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse IBM MQ URL %q: %v\n", broker.URL, err)
		os.Exit(1)
	}
	testUser = u.User.Username()
	testPass, _ = u.User.Password()
	testServer = fmt.Sprintf("ibmmq://%s", u.Host)

	err = integration.WaitForBroker(func() error {
		qMgr, err := Connect(makeConnArgs())
		if err != nil {
			return err
		}
		qMgr.Disc() //nolint:errcheck
		return nil
	}, 60*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "IBM MQ not ready: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func makeConnArgs() ConnArguments {
	return ConnArguments{
		Server:       testServer,
		User:         testUser,
		Password:     testPass,
		QueueManager: "QM1",
		Channel:      "DEV.APP.SVRCONN",
	}
}

func randomSuffix() string { return fmt.Sprintf("%d", rand.Int63()) }

func TestIBMMQ_QueueSendReceive(t *testing.T) {
	t.Parallel()
	queue := "DEV.QUEUE.1." + randomSuffix()
	connArgs := makeConnArgs()

	sender, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter (sender): %v", err)
	}
	defer sender.Close()

	err = sender.Send(context.Background(), backends.SendOptions{
		Queue:   queue,
		Message: []byte("hello-ibmmq"),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	receiver, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter (receiver): %v", err)
	}
	defer receiver.Close()

	msg, err := receiver.Receive(context.Background(), backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: true,
		Timeout:     5,
	})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(msg.Data) != "hello-ibmmq" {
		t.Errorf("expected %q, got %q", "hello-ibmmq", string(msg.Data))
	}
}

func TestIBMMQ_QueuePeek(t *testing.T) {
	t.Parallel()
	queue := "DEV.QUEUE.1." + randomSuffix()
	connArgs := makeConnArgs()

	sender, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter (sender): %v", err)
	}
	defer sender.Close()

	err = sender.Send(context.Background(), backends.SendOptions{
		Queue:   queue,
		Message: []byte("peek-me"),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// First peek (non-destructive)
	peeker, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter (peeker): %v", err)
	}
	defer peeker.Close()

	msg, err := peeker.Receive(context.Background(), backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: false,
		Timeout:     5,
	})
	if err != nil {
		t.Fatalf("Peek (first): %v", err)
	}
	if string(msg.Data) != "peek-me" {
		t.Errorf("expected %q, got %q", "peek-me", string(msg.Data))
	}

	// Second peek should still find the message
	peeker2, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter (peeker2): %v", err)
	}
	defer peeker2.Close()

	msg2, err := peeker2.Receive(context.Background(), backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: false,
		Timeout:     5,
	})
	if err != nil {
		t.Fatalf("Peek (second): %v", err)
	}
	if string(msg2.Data) != "peek-me" {
		t.Errorf("message gone after peek; expected %q, got %q", "peek-me", string(msg2.Data))
	}
}

func TestIBMMQ_QueueProperties(t *testing.T) {
	t.Parallel()
	queue := "DEV.QUEUE.1." + randomSuffix()
	connArgs := makeConnArgs()

	sender, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter (sender): %v", err)
	}
	defer sender.Close()

	props := map[string]any{
		"color": "red",
		"count": int32(7),
	}
	err = sender.Send(context.Background(), backends.SendOptions{
		Queue:      queue,
		Message:    []byte("props-message"),
		Properties: props,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	receiver, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter (receiver): %v", err)
	}
	defer receiver.Close()

	msg, err := receiver.Receive(context.Background(), backends.ReceiveOptions{
		Queue:                     queue,
		Acknowledge:               true,
		Timeout:                   5,
		WithApplicationProperties: true,
	})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if msg.Properties == nil {
		t.Fatal("expected Properties map, got nil")
	}
	if msg.Properties["color"] != "red" {
		t.Errorf("expected color=%q, got %v", "red", msg.Properties["color"])
	}
}
