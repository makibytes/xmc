//go:build pulsar

package pulsar

import (
	"bytes"
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

// adminRequest performs an HTTP request against the Pulsar Admin REST API.
func adminRequest(method, endpoint string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(method, endpoint, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("admin API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("admin API returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// adminPutJSON performs a PUT with a JSON body against the Pulsar Admin REST API.
func adminPutJSON(endpoint string, body []byte) error {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("PUT", endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("admin API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusConflict {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("admin API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// CreateTopic creates a topic via the Admin REST API. If partitions > 0,
// a partitioned topic is created; otherwise a non-partitioned topic.
func CreateTopic(connArgs ConnArguments, adminPort int, topic, tenant, namespace string, nonPersistent bool, partitions int) error {
	adminURL := buildAdminURL(connArgs.Server, adminPort)
	persistence := persistenceScheme(nonPersistent)
	if partitions > 0 {
		endpoint := fmt.Sprintf("%s/admin/v2/%s/%s/%s/%s/partitions", adminURL, persistence, tenant, namespace, url.PathEscape(topic))
		body := fmt.Sprintf("%d", partitions)
		return adminPutJSON(endpoint, []byte(body))
	}
	endpoint := fmt.Sprintf("%s/admin/v2/%s/%s/%s/%s", adminURL, persistence, tenant, namespace, url.PathEscape(topic))
	return adminRequest("PUT", endpoint)
}

// DeleteTopic deletes a topic via the Admin REST API.
func DeleteTopic(connArgs ConnArguments, adminPort int, topic, tenant, namespace string, nonPersistent bool) error {
	adminURL := buildAdminURL(connArgs.Server, adminPort)
	persistence := persistenceScheme(nonPersistent)
	endpoint := fmt.Sprintf("%s/admin/v2/%s/%s/%s/%s", adminURL, persistence, tenant, namespace, url.PathEscape(topic))
	return adminRequest("DELETE", endpoint)
}

// ListTopics lists topics in the given tenant/namespace via the Admin REST API.
func ListTopics(connArgs ConnArguments, adminPort int, tenant, namespace string, nonPersistent bool) ([]TopicInfo, error) {
	adminURL := buildAdminURL(connArgs.Server, adminPort)
	persistence := persistenceScheme(nonPersistent)
	endpoint := fmt.Sprintf("%s/admin/v2/%s/%s/%s", adminURL, persistence, tenant, namespace)

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

func persistenceScheme(nonPersistent bool) string {
	if nonPersistent {
		return "non-persistent"
	}
	return "persistent"
}
