//go:build pulsar

package pulsar

import "testing"

func TestResolveTarget(t *testing.T) {
	tests := []struct {
		name          string
		isTopic       bool
		to            string
		tenant        string
		namespace     string
		nonPersistent bool
		want          string
		wantErr       bool
	}{
		// Passthrough: fully-qualified addresses
		{"persistent passthrough", false, "persistent://my-tenant/my-ns/q1", "public", "default", false, "persistent://my-tenant/my-ns/q1", false},
		{"non-persistent passthrough", true, "non-persistent://public/default/events", "public", "default", false, "non-persistent://public/default/events", false},

		// Default tenant/namespace
		{"bare queue", false, "orders", "public", "default", false, "persistent://public/default/orders", false},
		{"bare topic", true, "events", "public", "default", false, "persistent://public/default/events", false},

		// Custom tenant/namespace
		{"custom tenant", false, "orders", "acme", "default", false, "persistent://acme/default/orders", false},
		{"custom namespace", true, "events", "public", "staging", false, "persistent://public/staging/events", false},
		{"custom both", false, "q1", "acme", "prod", false, "persistent://acme/prod/q1", false},

		// Non-persistent
		{"non-persistent queue", false, "orders", "public", "default", true, "non-persistent://public/default/orders", false},
		{"non-persistent topic", true, "events", "public", "default", true, "non-persistent://public/default/events", false},
		{"non-persistent with custom tenant", false, "q1", "acme", "prod", true, "non-persistent://acme/prod/q1", false},

		// Passthrough ignores non-persistent flag
		{"passthrough ignores flag", true, "persistent://a/b/c", "public", "default", true, "persistent://a/b/c", false},

		// Errors
		{"empty queue", false, "", "public", "default", false, "", true},
		{"empty topic", true, "", "public", "default", false, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveTarget(tt.isTopic, tt.to, tt.tenant, tt.namespace, tt.nonPersistent)
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
