//go:build pulsar

package pulsar

import (
	"fmt"
	"strings"
)

// ResolveTarget maps a bare topic/queue name into a fully-qualified Pulsar
// topic address. Resolution rules:
//
//  1. Names starting with "persistent://" or "non-persistent://" pass through verbatim.
//  2. Otherwise build <scheme>://<tenant>/<namespace>/<name>.
//     scheme defaults to "persistent" unless nonPersistent is true.
func ResolveTarget(isTopic bool, to, tenant, namespace string, nonPersistent bool) (string, error) {
	if to == "" {
		if isTopic {
			return "", fmt.Errorf("requires a topic argument")
		}
		return "", fmt.Errorf("requires a queue argument")
	}

	if strings.HasPrefix(to, "persistent://") || strings.HasPrefix(to, "non-persistent://") {
		return to, nil
	}

	scheme := "persistent"
	if nonPersistent {
		scheme = "non-persistent"
	}

	return fmt.Sprintf("%s://%s/%s/%s", scheme, tenant, namespace, to), nil
}
