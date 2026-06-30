package backends

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestRandomSuffix(t *testing.T) {
	s1 := RandomSuffix()
	s2 := RandomSuffix()
	if len(s1) != 12 {
		t.Errorf("RandomSuffix() length = %d, want 12", len(s1))
	}
	if s1 == s2 {
		t.Error("two calls to RandomSuffix returned identical values")
	}
}

func TestSubscriptionNameGroup(t *testing.T) {
	name, ephemeral := SubscriptionName(SubscribeOptions{GroupID: "my-group"})
	if name != "my-group" {
		t.Errorf("name = %q, want %q", name, "my-group")
	}
	if ephemeral {
		t.Error("group subscription should not be ephemeral")
	}
}

func TestSubscriptionNameDurable(t *testing.T) {
	name, ephemeral := SubscriptionName(SubscribeOptions{
		Durable: true,
		Topic:   "orders",
	})
	if !strings.HasPrefix(name, "xmc-durable-") || !strings.Contains(name, "orders") {
		t.Errorf("durable name = %q, want xmc-durable-orders-*", name)
	}
	if ephemeral {
		t.Error("durable subscription should not be ephemeral")
	}
}

func TestSubscriptionNameEphemeral(t *testing.T) {
	name, ephemeral := SubscriptionName(SubscribeOptions{Topic: "events"})
	if !strings.HasPrefix(name, "xmc-sub-") {
		t.Errorf("ephemeral name = %q, want xmc-sub-*", name)
	}
	if !ephemeral {
		t.Error("default subscription should be ephemeral")
	}
}

func TestStringifyProps(t *testing.T) {
	in := map[string]any{
		"str":   "hello",
		"int":   42,
		"bool":  true,
		"float": 3.14,
	}
	out := StringifyProps(in)
	if len(out) != len(in) {
		t.Errorf("len = %d, want %d", len(out), len(in))
	}
	if out["str"] != "hello" {
		t.Errorf("str = %q, want %q", out["str"], "hello")
	}
	if out["int"] != "42" {
		t.Errorf("int = %q, want %q", out["int"], "42")
	}
}

func TestStringifyPropsEmpty(t *testing.T) {
	out := StringifyProps(nil)
	if out == nil {
		t.Error("StringifyProps(nil) should return an empty map, not nil")
	}
	if len(out) != 0 {
		t.Errorf("len = %d, want 0", len(out))
	}
}

func TestTimeoutDurationWait(t *testing.T) {
	d := TimeoutDuration(5, true)
	if d < 23*time.Hour {
		t.Errorf("waiting timeout = %v, want ~24h", d)
	}
}

func TestTimeoutDurationNegative(t *testing.T) {
	d := TimeoutDuration(-1, false)
	if d != 5*time.Second {
		t.Errorf("negative timeout = %v, want 5s", d)
	}
}

func TestTimeoutDurationZero(t *testing.T) {
	d := TimeoutDuration(0, false)
	if d != 5*time.Second {
		t.Errorf("zero timeout = %v, want 5s", d)
	}
}

func TestTimeoutDurationExplicit(t *testing.T) {
	d := TimeoutDuration(10, false)
	if d != 10*time.Second {
		t.Errorf("explicit timeout = %v, want 10s", d)
	}
}

func TestEnvOrMissing(t *testing.T) {
	key := "XMC_TEST_ENV_OR"
	os.Unsetenv(key)
	if got := envOr(key, "fallback"); got != "fallback" {
		t.Errorf("envOr = %q, want %q", got, "fallback")
	}
}

func TestEnvOrPresent(t *testing.T) {
	key := "XMC_TEST_ENV_OR_SET"
	os.Setenv(key, "from-env")
	defer os.Unsetenv(key)
	if got := envOr(key, "fallback"); got != "from-env" {
		t.Errorf("envOr = %q, want %q", got, "from-env")
	}
}

func TestEnvOrEmpty(t *testing.T) {
	key := "XMC_TEST_ENV_OR_EMPTY"
	os.Setenv(key, "")
	if got := envOr(key, "fallback"); got != "fallback" {
		t.Errorf("envOr on empty = %q, want %q", got, "fallback")
	}
}

func TestPropConstants(t *testing.T) {
	if PropContentType != "content-type" {
		t.Errorf("PropContentType = %q", PropContentType)
	}
	if PropCorrelationID != "correlation-id" {
		t.Errorf("PropCorrelationID = %q", PropCorrelationID)
	}
	if PropMessageID != "message-id" {
		t.Errorf("PropMessageID = %q", PropMessageID)
	}
	if PropReplyTo != "reply-to" {
		t.Errorf("PropReplyTo = %q", PropReplyTo)
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	if ErrNoMessageAvailable == ErrBrowseUnsupported {
		t.Error("sentinel errors should be distinct")
	}
}

func TestIsOKStatusEmpty(t *testing.T) {
	if !isOKStatus(200, nil) {
		t.Error("isOKStatus(200, nil) should be true")
	}
	if isOKStatus(201, nil) {
		t.Error("isOKStatus(201, nil) should be false")
	}
	if isOKStatus(404, nil) {
		t.Error("isOKStatus(404, nil) should be false")
	}
}

func TestIsOKStatusCustom(t *testing.T) {
	ok := []int{200, 201, 204}
	if !isOKStatus(200, ok) {
		t.Error("200 should be OK")
	}
	if !isOKStatus(201, ok) {
		t.Error("201 should be OK")
	}
	if !isOKStatus(204, ok) {
		t.Error("204 should be OK")
	}
	if isOKStatus(202, ok) {
		t.Error("202 should not be OK")
	}
	if isOKStatus(404, ok) {
		t.Error("404 should not be OK")
	}
}
