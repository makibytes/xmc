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

// brokerMBeanName returns the full server-control MBean name with literal
// quotes, for use in POST request bodies (no URL encoding).
func (args ManagementArgs) brokerMBeanName() string {
	name := args.BrokerName
	if name == "" {
		name = "0.0.0.0"
	}
	return `org.apache.activemq.artemis:broker="` + name + `"`
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

// jolokiaExec performs a Jolokia exec operation via POST. POST avoids the
// URL-escaping pitfalls of GET /exec paths for arguments containing slashes,
// quotes, or JSON (e.g. selector filters and QueueConfiguration JSON).
func jolokiaExec(args ManagementArgs, mbean, operation string, arguments ...any) ([]byte, error) {
	base, err := jolokiaURL(args.Server)
	if err != nil {
		return nil, err
	}
	if arguments == nil {
		arguments = []any{}
	}
	body, err := json.Marshal(map[string]any{
		"type":      "exec",
		"mbean":     mbean,
		"operation": operation,
		"arguments": arguments,
	})
	if err != nil {
		return nil, err
	}
	resp, err := backends.MgmtPost(base, body, args.User, args.Password)
	if err != nil {
		return nil, err
	}
	return resp, checkJolokiaError(resp)
}

// resolveQueueMBean finds the full MBean name of a queue by searching across
// all addresses and routing types. A queue created with --address lives under
// an address different from its own name, so callers must not assume the
// address component equals the queue name.
func resolveQueueMBean(args ManagementArgs, queue string) (string, error) {
	base, err := jolokiaURL(args.Server)
	if err != nil {
		return "", err
	}
	path := fmt.Sprintf(
		"/search/org.apache.activemq.artemis:broker=%s,component=addresses,address=*,subcomponent=queues,routing-type=*,queue=%%22%s%%22",
		args.brokerMBean(), url.PathEscape(queue))
	body, err := backends.MgmtGet(base+path, args.User, args.Password)
	if err != nil {
		return "", err
	}

	var result struct {
		Value []string `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse Jolokia response: %w", err)
	}
	if len(result.Value) == 0 {
		return "", fmt.Errorf("queue %q not found", queue)
	}
	return result.Value[0], nil
}

// PurgeQueue removes all messages from a queue via Jolokia
func PurgeQueue(args ManagementArgs, queue string) (int64, error) {
	mbean, err := resolveQueueMBean(args, queue)
	if err != nil {
		return 0, err
	}
	body, err := jolokiaExec(args, mbean, "removeAllMessages()")
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

	mbean, err := resolveQueueMBean(args, queue)
	if err != nil {
		return nil, err
	}
	path := "/read/" + url.PathEscape(mbean)
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

// CreateQueue creates an ANYCAST address and queue of the same name.
func CreateQueue(args ManagementArgs, queue string) error {
	return CreateQueueWithConfig(args, QueueConfig{"name": queue})
}

// QueueConfig collects Artemis QueueConfiguration attributes (kebab-case JSON
// keys as defined by the Artemis QueueConfiguration contract: "address",
// "routing-type", "filter-string", "durable", "max-consumers",
// "purge-on-no-consumers", "exclusive", "last-value", "last-value-key",
// "non-destructive", "ring-size", ...). Only the keys present are sent, so
// broker defaults apply to everything else.
type QueueConfig map[string]any

// CreateQueueWithConfig creates a queue from QueueConfiguration attributes via
// the server control's createQueue(String queueConfiguration, boolean
// ignoreIfExists) operation. The address defaults to the queue name and the
// routing type to ANYCAST; the address is auto-created when missing.
func CreateQueueWithConfig(args ManagementArgs, config QueueConfig) error {
	name, _ := config["name"].(string)
	if name == "" {
		return fmt.Errorf("queue config needs a name")
	}
	if _, ok := config["address"]; !ok {
		config["address"] = name
	}
	if _, ok := config["routing-type"]; !ok {
		config["routing-type"] = "ANYCAST"
	}
	config["auto-create-address"] = true

	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}
	_, err = jolokiaExec(args, args.brokerMBeanName(),
		"createQueue(java.lang.String,boolean)", string(configJSON), false)
	return err
}

// UpdateQueueConfig changes settings of an existing queue via the server
// control's updateQueue(String queueConfiguration) operation. config must
// contain "name"; only the supplied keys are changed (an explicit empty
// "filter-string" removes the filter).
func UpdateQueueConfig(args ManagementArgs, config QueueConfig) error {
	name, _ := config["name"].(string)
	if name == "" {
		return fmt.Errorf("queue config needs a name")
	}
	// Artemis' updateQueue treats an absent filter as "remove the filter",
	// while every other absent attribute means "leave unchanged" (verified
	// against 2.x for both the JSON and the positional overloads). Preserve
	// the current filter explicitly when the caller does not set one.
	if _, ok := config["filter-string"]; !ok {
		filter, err := getQueueFilter(args, name)
		if err != nil {
			return err
		}
		if filter != "" {
			config["filter-string"] = filter
		}
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}
	_, err = jolokiaExec(args, args.brokerMBeanName(),
		"updateQueue(java.lang.String)", string(configJSON))
	return err
}

// getQueueFilter reads a queue's current filter expression ("" when none).
func getQueueFilter(args ManagementArgs, queue string) (string, error) {
	mbean, err := resolveQueueMBean(args, queue)
	if err != nil {
		return "", err
	}
	base, err := jolokiaURL(args.Server)
	if err != nil {
		return "", err
	}
	body, err := backends.MgmtGet(base+"/read/"+url.PathEscape(mbean)+"/Filter", args.User, args.Password)
	if err != nil {
		return "", err
	}
	var result struct {
		Value *string `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse Jolokia response: %w", err)
	}
	if result.Value == nil {
		return "", nil
	}
	return *result.Value, nil
}

// EnableQueue re-enables message dispatch on a queue.
func EnableQueue(args ManagementArgs, queue string) error {
	mbean, err := resolveQueueMBean(args, queue)
	if err != nil {
		return err
	}
	_, err = jolokiaExec(args, mbean, "enable()")
	return err
}

// DisableQueue disables message dispatch on a queue: messages accumulate but
// are not delivered to consumers until the queue is enabled again.
func DisableQueue(args ManagementArgs, queue string) error {
	mbean, err := resolveQueueMBean(args, queue)
	if err != nil {
		return err
	}
	_, err = jolokiaExec(args, mbean, "disable()")
	return err
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
			if rt := routingTypesString(attrs["RoutingTypes"]); rt != "" {
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
	parts := strings.Split(stripMBeanDomain(name), ",")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 && kv[0] == "address" {
			return strings.Trim(kv[1], "\"")
		}
	}
	return ""
}

// stripMBeanDomain removes the "org.apache.activemq.artemis:" domain prefix
// from a JMX MBean name. Jolokia canonicalizes property keys alphabetically,
// so "address" comes first and would otherwise be glued to the domain
// ("org.apache.activemq.artemis:address=...") and never match.
func stripMBeanDomain(name string) string {
	if i := strings.Index(name, ":"); i >= 0 {
		return name[i+1:]
	}
	return name
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
	parts := strings.Split(stripMBeanDomain(name), ",")
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

// routingTypesString flattens the RoutingTypes attribute, which Jolokia
// returns as a JSON array of strings (e.g. ["ANYCAST","MULTICAST"]), into a
// comma-joined string. A plain string value is passed through.
func routingTypesString(v any) string {
	switch rt := v.(type) {
	case string:
		return rt
	case []any:
		parts := make([]string, 0, len(rt))
		for _, e := range rt {
			if s, ok := e.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ",")
	default:
		return ""
	}
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
