package backends

// Defaults shared by the CLI flags and the MCP tools, so both surfaces agree.
const (
	// DefaultGroupID is the consumer group used when none is specified.
	DefaultGroupID = "xmc-consumer-group"
	// DefaultPriority is the message priority used when none is specified.
	DefaultPriority = 4
	// DefaultContentType is the MIME type used when none is specified.
	DefaultContentType = "text/plain"
)
