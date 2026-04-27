package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

func parsePropertiesFlag(cmd flagValueGetter) (map[string]any, error) {
	values, err := cmd.GetStringSlice("property")
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

func readCommandMessage(args []string) ([]byte, error) {
	if len(args) > 1 {
		return []byte(args[1]), nil
	}

	return readFromStdin()
}

func readFromStdin() ([]byte, error) {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect stdin: %w", err)
	}
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return nil, fmt.Errorf("no message provided and no data in stdin")
	}

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func forEachInputLine(visit func(string) error) (int, error) {
	scanner := bufio.NewScanner(os.Stdin)
	processed := 0
	for scanner.Scan() {
		if err := visit(scanner.Text()); err != nil {
			return processed, err
		}
		processed++
	}
	if err := scanner.Err(); err != nil {
		return processed, fmt.Errorf("error reading stdin: %w", err)
	}

	return processed, nil
}
