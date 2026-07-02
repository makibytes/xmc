package ibmmq

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestMQIDToStringPrintable(t *testing.T) {
	id := make([]byte, 24)
	copy(id, "my-correlation-id")
	if got := mqIDToString(id); got != "my-correlation-id" {
		t.Errorf("printable ID = %q, want %q", got, "my-correlation-id")
	}
}

func TestMQIDToStringEmpty(t *testing.T) {
	if got := mqIDToString(make([]byte, 24)); got != "" {
		t.Errorf("all-NUL ID = %q, want empty", got)
	}
}

func TestMQIDToStringBinary(t *testing.T) {
	// Broker-generated style: "AMQ " + queue manager name + binary suffix.
	id := make([]byte, 24)
	copy(id, "AMQ QM1 ")
	id[8], id[9], id[10] = 0x01, 0xfe, 0x99
	got := mqIDToString(id)
	want := hex.EncodeToString(id)
	if got != want {
		t.Errorf("binary ID = %q, want full hex %q", got, want)
	}
	if strings.ContainsAny(got, "\x00\x01") {
		t.Error("binary ID output must not contain raw control bytes")
	}
}
