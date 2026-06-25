package cmd

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/makibytes/xmc/broker/backends"
)

// formatMessage renders a message according to a kcat-style format string.
//
// Field tokens (substituted from the message):
//
//	%s        message payload (data)
//	%S        payload length in bytes
//	%i        message ID
//	%c        correlation ID
//	%r        reply-to
//	%y        content type
//	%P        priority
//	%u        persistent flag (true/false)
//	%h        all application properties as sorted key=value pairs, comma-separated
//	%p{key}   value of application property "key" (empty if absent)
//	%m{key}   value of internal metadata "key" (empty if absent)
//	%%        a literal percent sign
//
// C-style escapes in the literal text are also interpreted: \n \t \r \\.
// Any unrecognized token is emitted verbatim so that format strings fail soft
// rather than silently dropping characters.
func formatMessage(message *backends.Message, format string) string {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		switch format[i] {
		case '\\':
			i = writeEscape(&b, format, i)
		case '%':
			i = writeToken(&b, message, format, i)
		default:
			b.WriteByte(format[i])
		}
	}
	return b.String()
}

// writeEscape handles a backslash escape starting at index i (format[i] == '\\')
// and returns the index of the last byte it consumed.
func writeEscape(b *strings.Builder, format string, i int) int {
	if i+1 >= len(format) {
		b.WriteByte('\\')
		return i
	}
	switch format[i+1] {
	case 'n':
		b.WriteByte('\n')
	case 't':
		b.WriteByte('\t')
	case 'r':
		b.WriteByte('\r')
	case '\\':
		b.WriteByte('\\')
	default:
		b.WriteByte('\\')
		b.WriteByte(format[i+1])
	}
	return i + 1
}

// writeToken handles a percent token starting at index i (format[i] == '%')
// and returns the index of the last byte it consumed.
func writeToken(b *strings.Builder, message *backends.Message, format string, i int) int {
	if i+1 >= len(format) {
		b.WriteByte('%')
		return i
	}
	switch format[i+1] {
	case 's':
		b.Write(message.Data)
	case 'S':
		b.WriteString(strconv.Itoa(len(message.Data)))
	case 'i':
		b.WriteString(message.MessageID)
	case 'c':
		b.WriteString(message.CorrelationID)
	case 'r':
		b.WriteString(message.ReplyTo)
	case 'y':
		b.WriteString(message.ContentType)
	case 'P':
		b.WriteString(strconv.Itoa(message.Priority))
	case 'u':
		b.WriteString(strconv.FormatBool(message.Persistent))
	case 'h':
		b.WriteString(formatProperties(message.Properties))
	case 'p':
		if key, end, ok := braceArg(format, i+2); ok {
			b.WriteString(lookupValue(message.Properties, key))
			return end
		}
		b.WriteString("%p")
	case 'm':
		if key, end, ok := braceArg(format, i+2); ok {
			b.WriteString(lookupValue(message.InternalMetadata, key))
			return end
		}
		b.WriteString("%m")
	case '%':
		b.WriteByte('%')
	default:
		b.WriteByte('%')
		b.WriteByte(format[i+1])
	}
	return i + 1
}

// braceArg parses a {name} argument beginning at index j (expecting format[j] == '{').
// On success it returns the inner name, the index of the closing '}', and true.
func braceArg(format string, j int) (string, int, bool) {
	if j >= len(format) || format[j] != '{' {
		return "", 0, false
	}
	rel := strings.IndexByte(format[j:], '}')
	if rel < 0 {
		return "", 0, false
	}
	closeIdx := j + rel
	return format[j+1 : closeIdx], closeIdx, true
}

func lookupValue(values map[string]any, key string) string {
	if v, ok := values[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// formatProperties renders all application properties as sorted key=value pairs.
func formatProperties(properties map[string]any) string {
	if len(properties) == 0 {
		return ""
	}
	names := slices.Sorted(maps.Keys(properties))
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, fmt.Sprintf("%s=%v", name, properties[name]))
	}
	return strings.Join(pairs, ",")
}

// displayMessageFormat writes the message rendered through the format string to
// the given writer. Unlike displayMessage it adds no trailing newline: the
// format string is responsible for all output, including line breaks via \n.
func displayMessageFormat(w io.Writer, message *backends.Message, format string) error {
	_, err := fmt.Fprint(w, formatMessage(message, format))
	return err
}
