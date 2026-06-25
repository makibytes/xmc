//go:build redis

package redis

import "testing"

func TestResolveTarget(t *testing.T) {
	tests := []struct {
		name    string
		isTopic bool
		to      string
		prefix  string
		want    string
		wantErr bool
	}{
		// Default prefix
		{"bare queue", false, "orders", "xmc", "xmc:queue:orders", false},
		{"bare topic", true, "events", "xmc", "xmc:topic:events", false},

		// Custom prefix
		{"custom prefix queue", false, "orders", "myapp", "myapp:queue:orders", false},
		{"custom prefix topic", true, "events", "myapp", "myapp:topic:events", false},

		// Passthrough: full keys (contain ":")
		{"full queue key", false, "xmc:queue:orders", "xmc", "xmc:queue:orders", false},
		{"full topic key", true, "xmc:topic:events", "xmc", "xmc:topic:events", false},
		{"custom full key", false, "myapp:queue:q1", "xmc", "myapp:queue:q1", false},
		{"arbitrary key with colon", false, "namespace:key", "xmc", "namespace:key", false},

		// Errors
		{"empty queue", false, "", "xmc", "", true},
		{"empty topic", true, "", "xmc", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveTarget(tt.isTopic, tt.to, tt.prefix)
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
