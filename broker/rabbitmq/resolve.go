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
		return "/queues/" + escapeName(queue), nil
	}

	// Rule 4: -e <exchange> [<to>].
	if exchange != "" {
		if to != "" {
			return "/exchanges/" + escapeName(exchange) + "/" + escapeName(to), nil
		}
		return "/exchanges/" + escapeName(exchange), nil
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
		return "/exchanges/amq.topic/" + escapeName(to), nil
	}
	// Default queue routing for queue topology.
	return "/queues/" + escapeName(to), nil
}

// escapeName percent-encodes a queue/exchange name or routing key for use in
// an AMQP 1.0 v2 address. RabbitMQ requires percent-encoding for characters
// outside the unreserved set [A-Za-z0-9-._~].
func escapeName(s string) string {
	isUnreserved := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~'
	}

	needsEscape := false
	for i := 0; i < len(s); i++ {
		if !isUnreserved(s[i]) {
			needsEscape = true
			break
		}
	}
	if !needsEscape {
		return s
	}

	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		if c := s[i]; isUnreserved(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
