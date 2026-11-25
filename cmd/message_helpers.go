package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/pflag"
)

func parsePropertiesFlag(flags *pflag.FlagSet) (map[string]any, error) {
	values, err := flags.GetStringSlice("property")
	if err != nil {
		return nil, err
	}

	properties := make(map[string]any, len(values))
	for _, property := range values {
		key, value, ok := strings.Cut(property, "=")
		if !ok {
			return nil, fmt.Errorf("invalid property: %s", property)
		}
		properties[key] = value
	}

	return properties, nil
}

func readCommandMessage(args []string, in io.Reader) ([]byte, error) {
	if len(args) > 1 {
		return []byte(args[1]), nil
	}

	return readFromReader(in)
}

// readFromReader reads all available data from the given reader. When the
// reader is an *os.File it checks whether it is a terminal (char device) and
// returns an error if no piped data is available.
func readFromReader(r io.Reader) ([]byte, error) {
	if f, ok := r.(*os.File); ok {
		stat, err := f.Stat()
		if err != nil {
			return nil, fmt.Errorf("inspect input: %w", err)
		}
		if (stat.Mode() & os.ModeCharDevice) != 0 {
			return nil, fmt.Errorf("no message provided and no data in stdin")
		}
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// maxInputLine bounds one input line (or NDJSON record), which may carry a
// large (e.g. base64) payload.
const maxInputLine = 16 * 1024 * 1024

// newLineScanner returns a line scanner tolerant of very long lines; the
// default bufio.Scanner limit (64 KB) rejects realistic payloads.
func newLineScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxInputLine)
	return scanner
}

func forEachInputLine(r io.Reader, visit func(string) error) (int, error) {
	scanner := newLineScanner(r)
	processed := 0
	for scanner.Scan() {
		if err := visit(scanner.Text()); err != nil {
			return processed, err
		}
		processed++
	}
	if err := scanner.Err(); err != nil {
		return processed, fmt.Errorf("error reading input: %w", err)
	}

	return processed, nil
}
