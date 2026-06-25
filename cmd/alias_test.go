package cmd

import "testing"

func TestExpandAlias(t *testing.T) {
	aliases := map[string]string{
		"drain":  "receive $1 -n 0 --ndjson | send $1-dlq --ndjson",
		"peek5":  "peek $1 -n 5",
		"showall": "receive $@ -n 0",
	}

	tests := []struct {
		name    string
		line    string
		want    string
	}{
		{"simple substitution", "peek5 orders", "peek orders -n 5"},
		{"multi-use of $1", "drain orders", "receive orders -n 0 --ndjson | send orders-dlq --ndjson"},
		{"$@ expands all args", "showall q1 -w -t 5s", "receive q1 -w -t 5s -n 0"},
		{"not an alias", "send q1 hello", "send q1 hello"},
		{"empty aliases", "send q1", "send q1"},
		{"alias with no args", "peek5", "peek  -n 5"},
		{"pipeline in alias", "drain myq", "receive myq -n 0 --ndjson | send myq-dlq --ndjson"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandAlias(tt.line, aliases)
			if got != tt.want {
				t.Errorf("expandAlias(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestExpandAlias_NilMap(t *testing.T) {
	got := expandAlias("drain orders", nil)
	if got != "drain orders" {
		t.Errorf("expandAlias with nil map should pass through, got %q", got)
	}
}

func TestIsAlias(t *testing.T) {
	aliases := map[string]string{
		"drain": "receive $1 -n 0",
	}

	if !isAlias("drain orders", aliases) {
		t.Error("isAlias should return true for known alias")
	}
	if isAlias("send q1 hi", aliases) {
		t.Error("isAlias should return false for non-alias")
	}
	if isAlias("drain orders", nil) {
		t.Error("isAlias should return false for nil map")
	}
}
