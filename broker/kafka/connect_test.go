//go:build kafka

package kafka

import (
	"errors"
	"strings"
	"testing"
)

func TestHintAdvertisedListeners(t *testing.T) {
	brokers := []string{"localhost:9092"}

	t.Run("nil error", func(t *testing.T) {
		if got := hintAdvertisedListeners(nil, brokers); got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})

	t.Run("non-DNS error unchanged", func(t *testing.T) {
		err := errors.New("dial tcp 127.0.0.1:9092: connect: connection refused")
		got := hintAdvertisedListeners(err, brokers)
		if got != err {
			t.Fatalf("expected error to be returned unchanged, got %v", got)
		}
	})

	t.Run("DNS failure on advertised host gets hint", func(t *testing.T) {
		err := errors.New("failed to dial: failed to open connection to kafka:9092: dial tcp: lookup kafka: no such host")
		got := hintAdvertisedListeners(err, brokers)
		if got == err {
			t.Fatalf("expected wrapped error with hint, got original unchanged")
		}
		if !errors.Is(got, err) {
			t.Errorf("expected wrapped error to still match original via errors.Is")
		}
		if !strings.Contains(got.Error(), "kafka") {
			t.Errorf("expected hint to mention the unreachable host %q, got: %v", "kafka", got)
		}
		if !strings.Contains(got.Error(), "advertised.listeners") {
			t.Errorf("expected hint to mention advertised.listeners, got: %v", got)
		}
	})

	t.Run("DNS failure on bootstrap host unchanged", func(t *testing.T) {
		err := errors.New("dial tcp: lookup localhost: no such host")
		got := hintAdvertisedListeners(err, brokers)
		if got != err {
			t.Fatalf("expected error to be returned unchanged when failing host is a bootstrap host, got %v", got)
		}
	})

	t.Run("DNS failure with resolver suffix still matches", func(t *testing.T) {
		err := errors.New("dial tcp: lookup kafka on 127.0.0.1:53: no such host")
		got := hintAdvertisedListeners(err, brokers)
		if got == err {
			t.Fatalf("expected wrapped error with hint, got original unchanged")
		}
	})

	t.Run("multiple bootstrap brokers, failing host not among them", func(t *testing.T) {
		multi := []string{"localhost:9092", "127.0.0.1:9093"}
		err := errors.New("dial tcp: lookup broker2: no such host")
		got := hintAdvertisedListeners(err, multi)
		if got == err {
			t.Fatalf("expected wrapped error with hint, got original unchanged")
		}
	})
}

func TestHostOnly(t *testing.T) {
	cases := map[string]string{
		"localhost:9092": "localhost",
		"kafka:9092":     "kafka",
		"127.0.0.1:9092": "127.0.0.1",
		"noport":         "noport",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}
