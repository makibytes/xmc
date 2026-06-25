package cmd

import (
	"testing"

	"github.com/makibytes/xmc/broker/backends"
)

func TestSubscribeCommand_PassesGroupID(t *testing.T) {
	mock := &mockTopicBackend{subscribeMsg: &backends.Message{Data: []byte("x")}}
	cmd := NewSubscribeCommand(mock, nil, nil)
	cmd.SetArgs([]string{"test-topic", "-n", "1", "-g", "my-group"})

	_ = captureStdout(t, func() {
		err := cmd.Execute()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if mock.lastSubscribeOpts.GroupID != "my-group" {
		t.Errorf("group = %q, want %q", mock.lastSubscribeOpts.GroupID, "my-group")
	}
}
