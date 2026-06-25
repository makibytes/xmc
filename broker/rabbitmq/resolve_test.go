//go:build rabbitmq

package rabbitmq

import (
	"testing"
)

func TestResolveTarget(t *testing.T) {
	tests := []struct {
		name     string
		isTopic  bool
		to       string
		exchange string
		queue    string
		want     string
		wantErr  bool
	}{
		// Rule 1: v2 address passthrough
		{"v2 queue address", false, "/queues/q1", "", "", "/queues/q1", false},
		{"v2 exchange address", true, "/exchanges/amq.topic/orders.eu", "", "", "/exchanges/amq.topic/orders.eu", false},
		{"v2 takes precedence over -e", true, "/exchanges/custom/key", "ignored", "", "/exchanges/custom/key", false},

		// Rule 3: -q <queue>
		{"queue flag send", false, "", "", "q1", "/queues/q1", false},
		{"queue flag publish", true, "", "", "q1", "/queues/q1", false},

		// Rule 4: -e <exchange> with <to>
		{"exchange with routing key", true, "orders.eu", "amq.topic", "", "/exchanges/amq.topic/orders.eu", false},
		{"exchange with routing key (send)", false, "key1", "amq.direct", "", "/exchanges/amq.direct/key1", false},
		// Rule 4: -e <exchange> without <to> (fanout/headers)
		{"exchange only fanout", true, "", "amq.fanout", "", "/exchanges/amq.fanout", false},
		{"exchange only headers", true, "", "amq.headers", "", "/exchanges/amq.headers", false},

		// Rule 5: bare <to> defaults
		{"bare topic default", true, "orders.eu", "", "", "/exchanges/amq.topic/orders.eu", false},
		{"bare queue default", false, "q1", "", "", "/queues/q1", false},
		{"bare topic wildcard", true, "orders.#", "", "", "/exchanges/amq.topic/orders.#", false},

		// Errors
		{"no args topic", true, "", "", "", "", true},
		{"no args queue", false, "", "", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveTarget(tt.isTopic, tt.to, tt.exchange, tt.queue)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveTarget() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ResolveTarget() = %q, want %q", got, tt.want)
			}
		})
	}
}
