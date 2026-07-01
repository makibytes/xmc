package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// durationValue is a pflag.Value for time-valued flags. It accepts either a Go
// duration string ("100ms", "5s", "1m30s") or a bare number, which is
// interpreted in a legacy unit (bareUnit) so existing numeric usage keeps
// working. Because Go duration strings always carry a unit suffix while bare
// numbers never do, the two forms are unambiguous.
//
// This lets every time flag (--timeout, --interval, --ttl) accept the same
// human-readable format as --for, without breaking scripts that pass plain
// numbers (seconds for timeout/interval, milliseconds for ttl).
type durationValue struct {
	d        time.Duration
	bareUnit time.Duration
}

func newDurationValue(def, bareUnit time.Duration) *durationValue {
	return &durationValue{d: def, bareUnit: bareUnit}
}

func (v *durationValue) Set(s string) error {
	s = strings.TrimSpace(s)
	if d, err := time.ParseDuration(s); err == nil {
		if d < 0 {
			return fmt.Errorf("duration must not be negative: %q", s)
		}
		v.d = d
		return nil
	}
	// Backward-compatible fallback: a bare number in the legacy unit.
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("invalid duration %q (use e.g. %q, %q, or a plain number)", s, "100ms", "5s")
	}
	if n < 0 {
		return fmt.Errorf("duration must not be negative: %q", s)
	}
	v.d = time.Duration(n * float64(v.bareUnit))
	return nil
}

func (v *durationValue) Type() string { return "duration" }

func (v *durationValue) String() string {
	if v == nil || v.d == 0 {
		return "0s"
	}
	return v.d.String()
}

// getDuration reads a durationValue flag's resolved value. Invalid input is
// rejected by Set at parse time, so this never needs to return an error.
func getDuration(cmd *cobra.Command, name string) time.Duration {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		return 0
	}
	if dv, ok := f.Value.(*durationValue); ok {
		return dv.d
	}
	return 0
}

// aliasNormalize maps legacy flag spellings to their canonical form, so both
// spellings refer to the same flag (e.g. --contenttype and --content-type, or
// the deprecated --queue-name and --queue on read commands). Registering it
// keeps existing scripts working while the canonical names are shown in help.
func aliasNormalize(f *pflag.FlagSet, name string) pflag.NormalizedName {
	switch name {
	case "contenttype":
		name = "content-type"
	case "correlationid":
		name = "correlation-id"
	case "messageid":
		name = "message-id"
	case "replyto":
		name = "reply-to"
	case "queue-name":
		name = "queue"
	}
	return pflag.NormalizedName(name)
}
