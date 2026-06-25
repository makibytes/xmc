package backends

// Metric is a key-value pair displayed alongside an object node in the TUI
// sidebar. Label is a short identifier (e.g. "msgs", "consumers", "partitions")
// and Value is the numeric quantity.
type Metric struct {
	Label string
	Value int64
}

// ObjectNode represents a single broker object (queue, topic, exchange, stream,
// subscription, consumer group, …). Flat types populate only Name+Metrics;
// hierarchical types (exchange→binding→queue, topic→subscription,
// stream→consumer) also fill Children.
type ObjectNode struct {
	Name     string       // display name
	Kind     string       // child sub-label: "binding", "consumer", etc. (empty for top-level)
	Metrics  []Metric     // structured → sortable AND displayable
	Children []ObjectNode // nil for flat types; populated for expanded hierarchy
}
