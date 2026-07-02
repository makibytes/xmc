//go:build nats

package nats

import "testing"

func TestStreamName_Idempotent(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"srptest_q", "XMC_Q_SRPTEST_Q"},
		{"srptest-q", "XMC_Q_SRPTEST_Q"},
		{"xmc.reply", "XMC_Q_XMC_REPLY"},
		// Already-resolved names (e.g. relisted from JetStream by the AI shell
		// sidebar and fed straight back into Send/Receive/Purge) must not be
		// re-prefixed into a nonexistent "XMC_Q_XMC_Q_..." stream.
		{"XMC_Q_SRPTEST_Q", "XMC_Q_SRPTEST_Q"},
	}
	for _, c := range cases {
		if got := streamName(c.in); got != c.want {
			t.Errorf("streamName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Applying streamName twice must always be a no-op, for any input.
	for _, c := range cases {
		once := streamName(c.in)
		twice := streamName(once)
		if once != twice {
			t.Errorf("streamName not idempotent for %q: once=%q twice=%q", c.in, once, twice)
		}
	}
}
