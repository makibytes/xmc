# AMC - Artemis Messaging Client

[![Build Status](https://travis-ci.org/makibytes/amc.svg?branch=master)](https://travis-ci.org/makibytes/amc)
[![Go Report Card](https://goreportcard.com/badge/github.com/makibytes/amc)](https://goreportcard.com/report/github.com/makibytes/amc)
[![GoDoc](https://godoc.org/github.com/makibytes/amc?status.svg)](https://godoc.org/github.com/makibytes/amc)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](https://github.com/makibytes/amc/blob/main/LICENSE)

This project provides a unified command-line interface (CLI) for sending and receiving messages to/from different Message and Streaming Brokers. The broker backend is selected at **build time** using Go build tags:

## Building for Different Brokers

Build the `amc` binary for your specific broker:

|Build Command                    |Broker          |Protocol     |
|---------------------------------|----------------|-------------|
|`go build -tags artemis`        |Apache Artemis  |AMQP 1.0     |
|`go build -tags ibmmq`          |IBM MQ          |IBM MQ       |
|`go build -tags kafka`          |Apache Kafka    |Kafka        |
|`go build -tags mqtt`           |MQTT Brokers    |MQTT         |
|`go build -tags rabbitmq`       |RabbitMQ        |AMQP 0.9.1   |

The goal of this project is to support a common set of features across the different
protocols and brokers, comparable to the JMS API. See broker/BROKERS.md for more details.

## Usage

After building for your chosen broker, the `amc` binary provides the same interface regardless of backend:

```sh
amc put <queue-name> <message>
```

You can receive a message with the following command:

```sh
amc get <queue-name>
```

This will print the payload (data) to stdout and remove the message from the
queue. Use `peek` instead of `get` to keep it in the queue.

You can wait for a message with the `-w` flag:

```sh
amc get -w <queue-name>
```

The following parameters and environment variables can be used for all commands:

```sh
  -s, --server string      server URL of the broker    [$AMC_SERVER]
  -u, --user string        username for SASL login     [$AMC_USER]
  -p, --password string    password for SASL login     [$AMC_PASSWORD]
```

Notice: the environment variables are prefixed with `AMC_`.

## Advanced Usage

You can set properties (metadata) for the message with the following flags:

```sh
amc put <queue-name> -P <key1>=<value1> -P <key2>=<value2> <message>
```

If a message has properties, the `get` command will show them automatically.
You can suppress this behaviour with the `-q` flag:

```sh
  -q, --quiet    quiet about properties, show data only
```

Note that in the context of the AMQP 1.0 protocol, the properties are called
"Application Properties". The protocol also defines a structure called
"Properties" with a finite list of fields like message-id, user-id, etc. In
`amc` we call them "MessageProperties" to avoid confusion. You can see them
in verbose mode only.

## Working with Files, Redirection of STDOUT

The message can be read from file:

```sh
amc put <queue-name> < message.dat
```

By redirecting the output of `get` the message data (and only the data) will
be written to a file:

```sh
amc get <queue-name> > message.dat
```

The file will be exactly the same as it was sent! Without redirection `amc`
adds a newline character at the end of the message data for better readability.

## Testing

The tests are based on the [bats testing framework](https://github.com/bats-core/bats-core)
(included) and depend on a local Artemis broker with its default settings.

If you have Docker you can spin up an Artemis container like so:

```sh
docker run --name artemis -d \
    -p 8161:8161 -p 5672:5672 \
    apache/activemq-artemis:latest-alpine
```

Port 5672 is the default port of the AMQP 1.0 protocol. Port 8161 provides access to the Artemis web console,
where you can check the queues and messages manually. Default credentials are artemis/artemis.

Then you can start the tests:

```sh
./run-tests.sh
```

## Contributing

Contributions are welcome. Please open an issue or submit a pull request.
Use the latest version of Go and run tests with Artemis.

## License

This project is licensed under the MIT License - see the [LICENSE.md](LICENSE.md) file for details
