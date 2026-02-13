//go:build artemis

package artemis

import (
	"github.com/Azure/go-amqp"
	"github.com/makibytes/amc/broker/amqpcommon"
)

// ConnArguments wraps the common AMQP connection arguments
type ConnArguments = amqpcommon.ConnArguments

// Connect establishes an AMQP 1.0 connection to Artemis
func Connect(args ConnArguments) (*amqp.Conn, *amqp.Session, error) {
	return amqpcommon.Connect(args)
}
