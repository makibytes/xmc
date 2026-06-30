//go:build artemis

package artemis

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/makibytes/xmc/broker/backends"
)

// ManagementArgs holds parameters for Artemis management operations
type ManagementArgs struct {
	Server     string // AMQP server URL - we derive the HTTP management URL from it
	User       string
	Password   string
	BrokerName string // Artemis broker name for Jolokia MBean paths (default "0.0.0.0")
}

// brokerMBean returns the Jolokia-encoded broker= prefix for MBean paths.
// Uses args.BrokerName if set, otherwise defaults to "0.0.0.0".
func (args ManagementArgs) brokerMBean() string {
	name := args.BrokerName
	if name == "" {
		name = "0.0.0.0"
	}
	return "%22" + name + "%22"
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

// ListQueues lists all queues via Jolokia
func ListQueues(args ManagementArgs) ([]QueueInfo, error) {
	base, err := jolokiaURL(args.Server)
	if err != nil {
		return nil, err
	}

	// Query for all queue MBeans (broker=* matches any broker name)
	path := "/read/org.apache.activemq.artemis:broker=*,component=addresses,address=*,subcomponent=queues,routing-type=%22anycast%22,queue=*"
	body, err := backends.MgmtGet(base+path, args.User, args.Password)
	if err != nil {
		// Try alternative: search for queue names
		path = "/search/org.apache.activemq.artemis:broker=*,component=addresses,address=*,subcomponent=queues,routing-type=*,queue=*"
		body, err = backends.MgmtGet(base+path, args.User, args.Password)
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
	path := fmt.Sprintf("/exec/org.apache.activemq.artemis:broker=%s,component=addresses,address=%%22%s%%22,subcomponent=queues,routing-type=%%22anycast%%22,queue=%%22%s%%22/removeAllMessages()", args.brokerMBean(), queue, queue)
	body, err := backends.MgmtGet(base+path, args.User, args.Password)
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

	path := fmt.Sprintf("/read/org.apache.activemq.artemis:broker=%s,component=addresses,address=%%22%s%%22,subcomponent=queues,routing-type=%%22anycast%%22,queue=%%22%s%%22", args.brokerMBean(), queue, queue)
	body, err := backends.MgmtGet(base+path, args.User, args.Password)
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

// CreateQueue creates an ANYCAST address and queue via Jolokia.
func CreateQueue(args ManagementArgs, queue string) error {
	base, err := jolokiaURL(args.Server)
	if err != nil {
		return err
	}

	// createQueue(name, address, routingType) — routingType ANYCAST
	path := fmt.Sprintf(
		"/exec/org.apache.activemq.artemis:broker=%s/createQueue(java.lang.String,java.lang.String,java.lang.String)/%s/%s/ANYCAST",
		args.brokerMBean(), url.PathEscape(queue), url.PathEscape(queue))
	body, err := backends.MgmtGet(base+path, args.User, args.Password)
	if err != nil {
		return err
	}

	return checkJolokiaError(body)
}

// DeleteQueue destroys an ANYCAST queue and its address via Jolokia.
func DeleteQueue(args ManagementArgs, queue string) error {
	base, err := jolokiaURL(args.Server)
	if err != nil {
		return err
	}

	// destroyQueue(name, removeConsumers, autoDeleteAddress) — true, true
	path := fmt.Sprintf(
		"/exec/org.apache.activemq.artemis:broker=%s/destroyQueue(java.lang.String,boolean,boolean)/%s/true/true",
		args.brokerMBean(), url.PathEscape(queue))
	body, err := backends.MgmtGet(base+path, args.User, args.Password)
	if err != nil {
		return err
	}

	return checkJolokiaError(body)
}

// CreateTopic creates a MULTICAST address via Jolokia.
func CreateTopic(args ManagementArgs, topic string) error {
	return CreateAddress(args, topic, "MULTICAST")
}

// DeleteTopic deletes a MULTICAST address via Jolokia.
func DeleteTopic(args ManagementArgs, topic string) error {
	return DeleteAddress(args, topic)
}

// CreateAddress creates a bare address with the given routing type via Jolokia.
// routingType may be "ANYCAST", "MULTICAST", or "ANYCAST,MULTICAST".
func CreateAddress(args ManagementArgs, name, routingType string) error {
	base, err := jolokiaURL(args.Server)
	if err != nil {
		return err
	}
	if routingType == "" {
		routingType = "ANYCAST"
	}
	path := fmt.Sprintf(
		"/exec/org.apache.activemq.artemis:broker=%s/createAddress(java.lang.String,java.lang.String)/%s/%s",
		args.brokerMBean(), url.PathEscape(name), url.PathEscape(routingType))
	body, err := backends.MgmtGet(base+path, args.User, args.Password)
	if err != nil {
		return err
	}
	return checkJolokiaError(body)
}

// DeleteAddress deletes an address (and all its queues) via Jolokia.
func DeleteAddress(args ManagementArgs, name string) error {
	base, err := jolokiaURL(args.Server)
	if err != nil {
		return err
	}
	path := fmt.Sprintf(
		"/exec/org.apache.activemq.artemis:broker=%s/deleteAddress(java.lang.String,boolean)/%s/true",
		args.brokerMBean(), url.PathEscape(name))
	body, err := backends.MgmtGet(base+path, args.User, args.Password)
	if err != nil {
		return err
	}
	return checkJolokiaError(body)
}

// ListAddresses lists all MULTICAST addresses (topics) via Jolokia.
func ListAddresses(args ManagementArgs) ([]backends.ObjectNode, error) {
	base, err := jolokiaURL(args.Server)
	if err != nil {
		return nil, err
	}

	// Search for all address MBeans.
	path := "/read/org.apache.activemq.artemis:broker=*,component=addresses,address=*"
	body, err := backends.MgmtGet(base+path, args.User, args.Password)
	if err != nil {
		// Fall back to search.
		path = "/search/org.apache.activemq.artemis:broker=*,component=addresses,address=*"
		body, err = backends.MgmtGet(base+path, args.User, args.Password)
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

	// Try parsing as array of MBean names (search result).
	var mbeanNames []string
	if err := json.Unmarshal(result.Value, &mbeanNames); err == nil {
		seen := make(map[string]bool)
		var out []backends.ObjectNode
		for _, name := range mbeanNames {
			addr := parseAddressFromMBean(name)
			if addr == "" || seen[addr] {
				continue
			}
			seen[addr] = true
			out = append(out, backends.ObjectNode{Name: addr})
		}
		return out, nil
	}

	// Try parsing as map of MBean name → attributes.
	var mbeanMap map[string]map[string]any
	if err := json.Unmarshal(result.Value, &mbeanMap); err == nil {
		seen := make(map[string]bool)
		var out []backends.ObjectNode
		for name, attrs := range mbeanMap {
			addr := parseAddressFromMBean(name)
			if addr == "" || seen[addr] {
				continue
			}
			seen[addr] = true
			node := backends.ObjectNode{Name: addr}
			if rt, ok := attrs["RoutingTypes"].(string); ok && rt != "" {
				node.Kind = normalizeRoutingType(rt)
			}
			out = append(out, node)
		}
		return out, nil
	}

	return nil, fmt.Errorf("unexpected Jolokia response format")
}

// parseAddressFromMBean extracts the address= value from a JMX MBean name.
func parseAddressFromMBean(name string) string {
	parts := strings.Split(name, ",")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 && kv[0] == "address" {
			return strings.Trim(kv[1], "\"")
		}
	}
	return ""
}

// checkJolokiaError inspects a Jolokia response for an error status.
func checkJolokiaError(body []byte) error {
	var result struct {
		Status int    `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil // can't parse — assume success (status was 200)
	}
	if result.Status != 200 {
		return fmt.Errorf("Jolokia error: %s", result.Error)
	}
	return nil
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

// normalizeRoutingType maps Jolokia RoutingTypes values to short lowercase labels.
func normalizeRoutingType(rt string) string {
	rt = strings.TrimSpace(rt)
	lower := strings.ToLower(rt)
	switch {
	case lower == "anycast":
		return "anycast"
	case lower == "multicast":
		return "multicast"
	case strings.Contains(lower, "anycast") && strings.Contains(lower, "multicast"):
		return "any/multi"
	default:
		return lower
	}
}
