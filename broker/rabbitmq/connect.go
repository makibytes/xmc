//go:build rabbitmq

package rabbitmq

import (
	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/broker/amqpcommon"
)

// ConnArguments wraps the common AMQP connection arguments
type ConnArguments = amqpcommon.ConnArguments

// Connect establishes an AMQP 1.0 connection to RabbitMQ
func Connect(args ConnArguments) (*amqp.Conn, *amqp.Session, error) {
	return amqpcommon.Connect(args)
}
