//go:build redis

package redis

import (
	"fmt"
	"strings"
)

// ResolveTarget maps a bare queue/topic name into a fully-qualified Redis
// stream key. Resolution rules:
//
//  1. Names containing ":" pass through verbatim (assumed to be full keys).
//  2. Otherwise build <prefix>:queue:<name> or <prefix>:topic:<name>.
func ResolveTarget(isTopic bool, to, prefix string) (string, error) {
	if to == "" {
		if isTopic {
			return "", fmt.Errorf("requires a topic argument")
		}
		return "", fmt.Errorf("requires a queue argument")
	}

	if strings.Contains(to, ":") {
		return to, nil
	}

	if isTopic {
		return prefix + ":topic:" + to, nil
	}
	return prefix + ":queue:" + to, nil
}
