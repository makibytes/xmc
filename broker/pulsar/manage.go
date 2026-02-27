//go:build pulsar

package pulsar

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TopicInfo holds information about a Pulsar topic.
type TopicInfo struct {
	Name string
}

// ListTopics lists persistent topics in the public/default namespace via the Admin REST API.
func ListTopics(connArgs ConnArguments, adminPort int) ([]TopicInfo, error) {
	adminURL := buildAdminURL(connArgs.Server, adminPort)
	endpoint := fmt.Sprintf("%s/admin/v2/persistent/public/default", adminURL)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("querying Pulsar admin API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("admin API returned %d: %s", resp.StatusCode, string(body))
	}

	var topics []string
	if err := json.NewDecoder(resp.Body).Decode(&topics); err != nil {
		return nil, fmt.Errorf("decoding admin response: %w", err)
	}

	result := make([]TopicInfo, len(topics))
	for i, t := range topics {
		result[i] = TopicInfo{Name: t}
	}
	return result, nil
}

// buildAdminURL derives the Pulsar admin URL from the broker URL.
// pulsar://host:6650 → http://host:8080
// pulsar+ssl://host:6651 → https://host:8443
func buildAdminURL(brokerURL string, adminPort int) string {
	u, err := url.Parse(brokerURL)
	if err != nil {
		return fmt.Sprintf("http://localhost:%d", adminPort)
	}

	scheme := "http"
	if strings.Contains(u.Scheme, "ssl") || strings.Contains(u.Scheme, "tls") {
		scheme = "https"
	}

	host := u.Hostname()
	if host == "" {
		host = "localhost"
	}

	return fmt.Sprintf("%s://%s:%d", scheme, host, adminPort)
}
