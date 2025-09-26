//go:build artemis

package broker

type ReceiveArguments struct {
	Acknowledge               bool
	Durable                   bool
	Multicast                 bool
	Number                    int
	Queue                     string
	Timeout                   float32
	Wait                      bool
	WithHeaderAndProperties   bool
	WithApplicationProperties bool
}
