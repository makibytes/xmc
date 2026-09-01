// Package rc defines the return-code string constants used across broker commands.
package rc

// Return codes returned by broker commands, mirroring the JMS-style error taxonomy.
const (
	Error               = "error"
	CmdLineParsingError = "command line parsing error"
	AMQPConnectionError = "AMQP connection error"
	Unauthorized        = "unauthorized"
	PermissionDenied    = "permission denied"
	NoSuchAddress       = "no such address"
	BrokerError         = "broker error"
	NoMessage           = "no message"
)
