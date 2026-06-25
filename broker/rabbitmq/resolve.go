//go:build rabbitmq

package rabbitmq

import (
	"fmt"
	"strings"
)

// ResolveTarget maps the routing flags (-e/-q/<to>) into an AMQP 1.0 v2
// address for RabbitMQ 4.x. The resolution rules are:
//
//  1. <to> starting with "/" is an AMQP 1.0 v2 address — used verbatim.
//  2. -e and -q are mutually exclusive (caller enforces this).
//  3. -q <queue> → /queues/<queue>.
//  4. -e <exchange> with <to> → /exchanges/<exchange>/<to>.
//     -e <exchange> without <to> → /exchanges/<exchange>.
//  5. No flags, <to> not a v2 address:
//     send/receive  → /queues/<to>   (queue by name, default exchange).
//     publish/subscribe → /exchanges/amq.topic/<to>  (topic exchange).
func ResolveTarget(isTopic bool, to, exchange, queue string) (string, error) {
	// Rule 1: explicit v2 address has highest precedence.
	if strings.HasPrefix(to, "/") {
		return to, nil
	}

	// Rule 3: -q <queue> → /queues/<queue>.
	if queue != "" {
		return "/queues/" + queue, nil
	}

	// Rule 4: -e <exchange> [<to>].
	if exchange != "" {
		if to != "" {
			return "/exchanges/" + exchange + "/" + to, nil
		}
		return "/exchanges/" + exchange, nil
	}

	// Rule 5: bare <to> — apply defaults.
	if to == "" {
		if isTopic {
			return "", fmt.Errorf("requires a topic argument, or use --exchange / --queue")
		}
		return "", fmt.Errorf("requires a queue argument, or use --exchange / --queue")
	}

	if isTopic {
		// Default topic exchange for pub/sub topology.
		return "/exchanges/amq.topic/" + to, nil
	}
	// Default queue routing for queue topology.
	return "/queues/" + to, nil
}
