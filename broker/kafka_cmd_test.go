//go:build kafka

package broker

import (
	"strings"
	"testing"
)

func TestKafkaManageUpdateTopicRejectsNonPositivePartitions(t *testing.T) {
	tests := [][]string{
		{"manage", "update-topic", "orders", "--partitions", "0"},
		{"manage", "update-topic", "orders", "--partitions", "-1"},
	}

	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := GetRootCommand()
			cmd.SetArgs(args)
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected error for non-positive --partitions")
			}
			if !strings.Contains(err.Error(), "invalid --partitions") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
