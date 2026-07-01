package ibmmq

import (
	"encoding/hex"
	"strings"
)

// mqIDToString renders an MQ message/correlation ID (a fixed 24-byte field)
// for display. User-set IDs are printable strings padded with trailing NULs
// and round-trip unchanged; broker-generated IDs contain binary bytes and are
// hex-encoded (the full field, matching MQ tooling conventions) so they stay
// valid UTF-8 in normal output and NDJSON records.
//
// This file carries no ibmmq build tag on purpose: it has no cgo dependency,
// so it stays testable without the proprietary MQ client libraries.
func mqIDToString(id []byte) string {
	trimmed := strings.TrimRight(string(id), "\x00")
	if trimmed == "" {
		return ""
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] < 0x20 || trimmed[i] > 0x7e {
			return hex.EncodeToString(id)
		}
	}
	return trimmed
}
