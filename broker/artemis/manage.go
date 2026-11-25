//go:build artemis

package artemis

import (
	"cmp"
	"encoding/json"
	"errors"
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

// brokerName returns args.BrokerName, defaulting to "0.0.0.0".
func (args ManagementArgs) brokerName() string {
	return cmp.Or(args.BrokerName, "0.0.0.0")
}

// brokerMBean returns the Jolokia-encoded broker= value for MBean paths in
// GET URLs.
func (args ManagementArgs) brokerMBean() string {
	return "%22" + args.brokerName() + "%22"
}

// brokerMBeanName returns the full server-control MBean name with literal
// quotes, for use in POST request bodies (no URL encoding).
func (args ManagementArgs) brokerMBeanName() string {
	return `org.apache.activemq.artemis:broker="` + args.brokerName() + `"`
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

// mbeanAttrs is one MBean returned by a Jolokia read or search.
type mbeanAttrs struct {
	name  string
	attrs map[string]any // nil for names returned by a search
}

// jolokiaQuery reads readPath and, if that fails, falls back to searchPath
// (which yields MBean names only). A read answers with a map of MBean name to
// attributes, a search with an array of MBean names; both are returned in one
// shape.
func jolokiaQuery(args ManagementArgs, readPath, searchPath string) ([]mbeanAttrs, error) {
	base, err := jolokiaURL(args.Server)
	if err != nil {
		return nil, err
	}
	body, err := backends.MgmtGet(base+readPath, args.User, args.Password)
	if err != nil {
		if body, err = backends.MgmtGet(base+searchPath, args.User, args.Password); err != nil {
			return nil, err
		}
	}

	var result struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse Jolokia response: %w", err)
	}

	var names []string
	if err := json.Unmarshal(result.Value, &names); err == nil {
		out := make([]mbeanAttrs, len(names))
		for i, name := range names {
			out[i] = mbeanAttrs{name: name}
		}
		return out, nil
	}
	var byName map[string]map[string]any
	if err := json.Unmarshal(result.Value, &byName); err == nil {
		out := make([]mbeanAttrs, 0, len(byName))
		for name, attrs := range byName {
			out = append(out, mbeanAttrs{name: name, attrs: attrs})
		}
		return out, nil
	}
	return nil, errors.New("unexpected Jolokia response format")
}

// ListQueues lists all queues via Jolokia
func ListQueues(args ManagementArgs) ([]QueueInfo, error) {
	// Read the ANYCAST queue MBeans with their attributes; the search fallback
	// covers every routing type but carries no counts.
	found, err := jolokiaQuery(args,
		"/read/org.apache.activemq.artemis:broker=*,component=addresses,address=*,subcomponent=queues,routing-type=%22anycast%22,queue=*",
		"/search/org.apache.activemq.artemis:broker=*,component=addresses,address=*,subcomponent=queues,routing-type=*,queue=*")
	if err != nil {
		return nil, err
	}

	var queues []QueueInfo
	for _, m := range found {
		qi := parseMBeanName(m.name)
		if qi.Name == "" {
			continue
		}
		if count, ok := m.attrs["MessageCount"].(float64); ok {
			qi.MessageCount = int64(count)
		}
		if count, ok := m.attrs["ConsumerCount"].(float64); ok {
			qi.ConsumerCount = int(count)
		}
		queues = append(queues, qi)
	}
	return queues, nil
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
	// destroyQueue(name, removeConsumers, autoDeleteAddress) — true, true
	_, err := jolokiaExec(args, args.brokerMBeanName(), "destroyQueue(java.lang.String,boolean,boolean)", queue, true, true)
	return err
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
	_, err := jolokiaExec(args, args.brokerMBeanName(), "createAddress(java.lang.String,java.lang.String)", name, cmp.Or(routingType, "ANYCAST"))
	return err
}

// DeleteAddress deletes an address (and all its queues) via Jolokia.
func DeleteAddress(args ManagementArgs, name string) error {
	_, err := jolokiaExec(args, args.brokerMBeanName(), "deleteAddress(java.lang.String,boolean)", name, true)
	return err
}

// ListAddresses lists all addresses via Jolokia, with their routing type when
// the read (rather than the search fallback) succeeded.
func ListAddresses(args ManagementArgs) ([]backends.ObjectNode, error) {
	const pattern = "org.apache.activemq.artemis:broker=*,component=addresses,address=*"
	found, err := jolokiaQuery(args, "/read/"+pattern, "/search/"+pattern)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var out []backends.ObjectNode
	for _, m := range found {
		addr := parseAddressFromMBean(m.name)
		if addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true
		node := backends.ObjectNode{Name: addr}
		if rt := routingTypesString(m.attrs["RoutingTypes"]); rt != "" {
			node.Kind = normalizeRoutingType(rt)
		}
		out = append(out, node)
	}
	return out, nil
}

// parseAddressFromMBean extracts the address= value from a JMX MBean name.
func parseAddressFromMBean(name string) string {
	parts := strings.SplitSeq(stripMBeanDomain(name), ",")
	for part := range parts {
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
	if _, after, ok := strings.Cut(name, ":"); ok {
		return after
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
		return fmt.Errorf("jolokia error: %s", result.Error)
	}
	return nil
}

// QueueInfo and QueueStats are aliases for the backends types (rather than
// broker-local shadow types): Artemis's fields — including RoutingType,
// which the JMS-derived ANYCAST/MULTICAST concept isn't unique to Artemis —
// are exactly backends.QueueInfo/QueueStats, so callers (including the MCP
// tool wiring in broker/artemis.go) can use either name interchangeably.
type QueueInfo = backends.QueueInfo

// QueueStats is an alias for backends.QueueStats; see the QueueInfo comment above.
type QueueStats = backends.QueueStats

func parseMBeanName(name string) QueueInfo {
	qi := QueueInfo{}
	parts := strings.SplitSeq(stripMBeanDomain(name), ",")
	for part := range parts {
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
