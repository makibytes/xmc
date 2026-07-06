//go:build mqtt

package mqtt

import "testing"

func TestTopicMatchesFilter(t *testing.T) {
	cases := []struct {
		filter, topic string
		want          bool
	}{
		{"a/b", "a/b", true},
		{"a/b", "a/c", false},
		{"a/+", "a/b", true},
		{"a/+", "a/b/c", false},
		{"a/+/c", "a/b/c", true},
		{"a/#", "a/b/c", true},
		{"a/#", "a", true}, // '#' includes the parent level per spec
		{"#", "anything/at/all", true},
		{"a/+", "a", false},
		{"a", "a/b", false},
		{"queue/orders", "queue/orders", true},
	}
	for _, c := range cases {
		if got := topicMatchesFilter(c.filter, c.topic); got != c.want {
			t.Errorf("topicMatchesFilter(%q, %q) = %v, want %v", c.filter, c.topic, got, c.want)
		}
	}
}
