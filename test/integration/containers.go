//go:build integration

// Package integration provides testcontainer helpers for XMC integration tests.
// Build with: -tags integration
package integration

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
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

// StartKafka starts a Redpanda container (Kafka-compatible API, arm64 native)
// and returns the broker address as a kafka:// URL.
func StartKafka(ctx context.Context) (*BrokerContainer, error) {
	c, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	if err != nil {
		return nil, fmt.Errorf("starting Kafka (Redpanda): %w", err)
	}

	brokers, err := c.KafkaSeedBroker(ctx)
	if err != nil {
		c.Terminate(ctx) //nolint:errcheck
		return nil, err
	}

	return &BrokerContainer{Container: c, URL: "kafka://" + brokers}, nil
}

// StartNATS starts a NATS container with JetStream enabled using the
// testcontainers module and returns its connection URL.
func StartNATS(ctx context.Context) (*BrokerContainer, error) {
	// Use a config file to enable JetStream; WithArgument always appends flag + value
	// which would pass an empty string argument when only a flag is needed.
	c, err := nats.Run(ctx, "nats:latest",
		nats.WithConfigFile(strings.NewReader("jetstream: true\n")),
	)
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
		// Write a minimal config that allows anonymous connections with MQTT 5 support.
		Files: []testcontainers.ContainerFile{
			{
				Reader:            strings.NewReader("listener 1883\nallow_anonymous true\n"),
				ContainerFilePath: "/mosquitto/config/mosquitto.conf",
				FileMode:          0o644,
			},
		},
		WaitingFor: wait.ForListeningPort("1883/tcp").WithStartupTimeout(30 * time.Second),
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

// StartIBMMQ starts an IBM MQ container and returns its connection URL.
// The URL format is: ibmmq://admin:passw0rd@host:port/QM1
// Note: tests require the IBM MQ client libraries (CGO_ENABLED=1, MQ SDK installed).
func StartIBMMQ(ctx context.Context) (*BrokerContainer, error) {
	req := testcontainers.ContainerRequest{
		Image:        "icr.io/ibm-messaging/mq:latest",
		ExposedPorts: []string{"1414/tcp", "9443/tcp"},
		Env: map[string]string{
			"LICENSE":      "accept",
			"MQ_QMGR_NAME": "QM1",
			"MQ_APP_PASSWORD": "passw0rd",
		},
		WaitingFor: wait.ForLog("AMQ5026I").WithStartupTimeout(90 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("starting IBM MQ: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		container.Terminate(ctx) //nolint:errcheck
		return nil, err
	}
	port, err := container.MappedPort(ctx, "1414")
	if err != nil {
		container.Terminate(ctx) //nolint:errcheck
		return nil, err
	}

	return &BrokerContainer{
		Container: container,
		URL:       fmt.Sprintf("ibmmq://admin:passw0rd@%s:%s/QM1", host, port.Port()),
	}, nil
}

// StartPulsar starts an Apache Pulsar container and returns its broker URL.
// Uses a GenericContainer to avoid the testcontainers Pulsar module's log-based
// wait strategy which doesn't work with Pulsar 3.x images.
func StartPulsar(ctx context.Context) (*BrokerContainer, error) {
	const pulsarCmd = "/bin/bash -c '/pulsar/bin/apply-config-from-env.py /pulsar/conf/standalone.conf && bin/pulsar standalone --no-functions-worker -nss'"
	req := testcontainers.ContainerRequest{
		Image:        "apachepulsar/pulsar:3.3.0",
		ExposedPorts: []string{"6650/tcp", "8080/tcp"},
		Cmd:          []string{"/bin/bash", "-c", "bin/pulsar standalone --no-functions-worker -nss"},
		WaitingFor: wait.ForHTTP("/admin/v2/clusters").
			WithPort("8080/tcp").
			WithStartupTimeout(120 * time.Second).
			WithResponseMatcher(func(r io.Reader) bool {
				respBytes, _ := io.ReadAll(r)
				return strings.Contains(string(respBytes), "standalone")
			}),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("starting Pulsar: %w", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		container.Terminate(ctx) //nolint:errcheck
		return nil, err
	}
	port, err := container.MappedPort(ctx, "6650")
	if err != nil {
		container.Terminate(ctx) //nolint:errcheck
		return nil, err
	}

	return &BrokerContainer{
		Container: container,
		URL:       fmt.Sprintf("pulsar://%s:%s", host, port.Port()),
	}, nil
}
