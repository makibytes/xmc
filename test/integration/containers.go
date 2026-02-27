//go:build integration

// Package integration provides testcontainer helpers for XMC integration tests.
// Build with: -tags integration
package integration

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/modules/pulsar"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"github.com/testcontainers/testcontainers-go/wait"
)

// BrokerContainer holds a running test broker container.
type BrokerContainer struct {
	Container testcontainers.Container
	URL       string
}

func (b *BrokerContainer) Terminate(ctx context.Context) {
	if b.Container != nil {
		b.Container.Terminate(ctx) //nolint:errcheck
	}
}

// StartArtemis starts an Apache Artemis container and returns its AMQP URL.
func StartArtemis(ctx context.Context) (*BrokerContainer, error) {
	req := testcontainers.ContainerRequest{
		Image:        "apache/activemq-artemis:latest-alpine",
		ExposedPorts: []string{"5672/tcp"},
		Env: map[string]string{
			"ARTEMIS_USER":     "artemis",
			"ARTEMIS_PASSWORD": "artemis",
		},
		WaitingFor: wait.ForListeningPort("5672/tcp").WithStartupTimeout(60 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("starting Artemis: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		container.Terminate(ctx) //nolint:errcheck
		return nil, err
	}
	port, err := container.MappedPort(ctx, "5672")
	if err != nil {
		container.Terminate(ctx) //nolint:errcheck
		return nil, err
	}

	return &BrokerContainer{
		Container: container,
		URL:       fmt.Sprintf("amqp://%s:%s", host, port.Port()),
	}, nil
}

// StartRabbitMQ starts a RabbitMQ container using the testcontainers module
// and returns its AMQP URL.
func StartRabbitMQ(ctx context.Context) (*BrokerContainer, error) {
	c, err := rabbitmq.Run(ctx, "rabbitmq:4-management-alpine")
	if err != nil {
		return nil, fmt.Errorf("starting RabbitMQ: %w", err)
	}

	url, err := c.AmqpURL(ctx)
	if err != nil {
		c.Terminate(ctx) //nolint:errcheck
		return nil, err
	}

	return &BrokerContainer{Container: c, URL: url}, nil
}

// StartKafka starts a Kafka container using the testcontainers module and
// returns the broker address as a kafka:// URL.
func StartKafka(ctx context.Context) (*BrokerContainer, error) {
	c, err := kafka.Run(ctx, "confluentinc/cp-kafka:7.6.1")
	if err != nil {
		return nil, fmt.Errorf("starting Kafka: %w", err)
	}

	brokers, err := c.Brokers(ctx)
	if err != nil {
		c.Terminate(ctx) //nolint:errcheck
		return nil, err
	}

	return &BrokerContainer{Container: c, URL: "kafka://" + brokers[0]}, nil
}

// StartNATS starts a NATS container with JetStream enabled using the
// testcontainers module and returns its connection URL.
func StartNATS(ctx context.Context) (*BrokerContainer, error) {
	c, err := nats.Run(ctx, "nats:latest", nats.WithArgument("--js", ""))
	if err != nil {
		return nil, fmt.Errorf("starting NATS: %w", err)
	}

	url, err := c.ConnectionString(ctx)
	if err != nil {
		c.Terminate(ctx) //nolint:errcheck
		return nil, err
	}

	return &BrokerContainer{Container: c, URL: url}, nil
}

// StartMosquitto starts an Eclipse Mosquitto MQTT broker using a generic
// container (no dedicated testcontainers module exists).
func StartMosquitto(ctx context.Context) (*BrokerContainer, error) {
	req := testcontainers.ContainerRequest{
		Image:        "eclipse-mosquitto:latest",
		ExposedPorts: []string{"1883/tcp"},
		Cmd:          []string{"mosquitto", "-c", "/mosquitto-no-auth.conf"},
		WaitingFor:   wait.ForListeningPort("1883/tcp").WithStartupTimeout(30 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("starting Mosquitto: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		container.Terminate(ctx) //nolint:errcheck
		return nil, err
	}
	port, err := container.MappedPort(ctx, "1883")
	if err != nil {
		container.Terminate(ctx) //nolint:errcheck
		return nil, err
	}

	return &BrokerContainer{
		Container: container,
		URL:       fmt.Sprintf("tcp://%s:%s", host, port.Port()),
	}, nil
}

// StartPulsar starts an Apache Pulsar container using the testcontainers module
// and returns its broker URL.
func StartPulsar(ctx context.Context) (*BrokerContainer, error) {
	c, err := pulsar.Run(ctx, "apachepulsar/pulsar:3.3.0")
	if err != nil {
		return nil, fmt.Errorf("starting Pulsar: %w", err)
	}

	url, err := c.BrokerURL(ctx)
	if err != nil {
		c.Terminate(ctx) //nolint:errcheck
		return nil, err
	}

	return &BrokerContainer{Container: c, URL: url}, nil
}
