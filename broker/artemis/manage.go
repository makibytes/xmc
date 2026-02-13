//go:build artemis

package artemis

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/makibytes/xmc/log"
)

// ManagementArgs holds parameters for Artemis management operations
type ManagementArgs struct {
	Server   string // AMQP server URL - we derive the HTTP management URL from it
	User     string
	Password string
}

// jolokiaURL converts the AMQP server URL to Jolokia HTTP URL
func jolokiaURL(amqpServer string) (string, error) {
	u, err := url.Parse(amqpServer)
	if err != nil {
		return "", err
	}
	host := u.Hostname()
	// Artemis management console defaults to port 8161
	return fmt.Sprintf("http://%s:8161/console/jolokia", host), nil
}

func jolokiaGet(baseURL, path, user, password string) ([]byte, error) {
	fullURL := baseURL + path
	log.Verbose("requesting %s", fullURL)

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return nil, err
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	req.Header.Set("Origin", baseURL)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("management API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("management API returned status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// ListQueues lists all queues via Jolokia
func ListQueues(args ManagementArgs) ([]QueueInfo, error) {
	base, err := jolokiaURL(args.Server)
	if err != nil {
		return nil, err
	}

	// Query for all queue MBeans
	path := "/read/org.apache.activemq.artemis:broker=%220.0.0.0%22,component=addresses,address=*,subcomponent=queues,routing-type=%22anycast%22,queue=*"
	body, err := jolokiaGet(base, path, args.User, args.Password)
	if err != nil {
		// Try alternative: search for queue names
		path = "/search/org.apache.activemq.artemis:broker=*,component=addresses,address=*,subcomponent=queues,routing-type=*,queue=*"
		body, err = jolokiaGet(base, path, args.User, args.Password)
		if err != nil {
			return nil, err
		}
	}

	var result struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse Jolokia response: %w", err)
	}

	// Try parsing as array of MBean names (search result)
	var mbeanNames []string
	if err := json.Unmarshal(result.Value, &mbeanNames); err == nil {
		var queues []QueueInfo
		for _, name := range mbeanNames {
			qi := parseMBeanName(name)
			if qi.Name != "" {
				queues = append(queues, qi)
			}
		}
		return queues, nil
	}

	// Try parsing as map of MBean name -> attributes (read result)
	var mbeanMap map[string]map[string]any
	if err := json.Unmarshal(result.Value, &mbeanMap); err == nil {
		var queues []QueueInfo
		for name, attrs := range mbeanMap {
			qi := parseMBeanName(name)
			if count, ok := attrs["MessageCount"].(float64); ok {
				qi.MessageCount = int64(count)
			}
			if count, ok := attrs["ConsumerCount"].(float64); ok {
				qi.ConsumerCount = int(count)
			}
			queues = append(queues, qi)
		}
		return queues, nil
	}

	return nil, fmt.Errorf("unexpected Jolokia response format")
}

// PurgeQueue removes all messages from a queue via Jolokia
func PurgeQueue(args ManagementArgs, queue string) (int64, error) {
	base, err := jolokiaURL(args.Server)
	if err != nil {
		return 0, err
	}

	// Exec removeAllMessages operation
	path := fmt.Sprintf("/exec/org.apache.activemq.artemis:broker=%%220.0.0.0%%22,component=addresses,address=%%22%s%%22,subcomponent=queues,routing-type=%%22anycast%%22,queue=%%22%s%%22/removeAllMessages()", queue, queue)
	body, err := jolokiaGet(base, path, args.User, args.Password)
	if err != nil {
		return 0, err
	}

	var result struct {
		Value float64 `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("failed to parse purge response: %w", err)
	}

	return int64(result.Value), nil
}

// GetQueueStats returns statistics for a queue via Jolokia
func GetQueueStats(args ManagementArgs, queue string) (*QueueStats, error) {
	base, err := jolokiaURL(args.Server)
	if err != nil {
		return nil, err
	}

	path := fmt.Sprintf("/read/org.apache.activemq.artemis:broker=%%220.0.0.0%%22,component=addresses,address=%%22%s%%22,subcomponent=queues,routing-type=%%22anycast%%22,queue=%%22%s%%22", queue, queue)
	body, err := jolokiaGet(base, path, args.User, args.Password)
	if err != nil {
		return nil, err
	}

	var result struct {
		Value map[string]any `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse stats response: %w", err)
	}

	stats := &QueueStats{Name: queue}
	if v, ok := result.Value["MessageCount"].(float64); ok {
		stats.MessageCount = int64(v)
	}
	if v, ok := result.Value["ConsumerCount"].(float64); ok {
		stats.ConsumerCount = int(v)
	}
	if v, ok := result.Value["MessagesAdded"].(float64); ok {
		stats.EnqueueCount = int64(v)
	}
	if v, ok := result.Value["MessagesAcknowledged"].(float64); ok {
		stats.DequeueCount = int64(v)
	}

	return stats, nil
}

type QueueInfo struct {
	Name          string
	RoutingType   string
	MessageCount  int64
	ConsumerCount int
}

type QueueStats struct {
	Name          string
	MessageCount  int64
	ConsumerCount int
	EnqueueCount  int64
	DequeueCount  int64
}

func parseMBeanName(name string) QueueInfo {
	qi := QueueInfo{}
	parts := strings.Split(name, ",")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := kv[0]
		val := strings.Trim(kv[1], "\"")
		switch key {
		case "queue":
			qi.Name = val
		case "routing-type":
			qi.RoutingType = val
		}
	}
	return qi
}
