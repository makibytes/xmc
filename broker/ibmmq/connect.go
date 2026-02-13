//go:build ibmmq

package ibmmq

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/ibm-messaging/mq-golang/v5/ibmmq"
)

type ConnArguments struct {
	Server       string
	User         string
	Password     string
	QueueManager string
	Channel      string
}

// Connect establishes a connection to IBM MQ
func Connect(args ConnArguments) (ibmmq.MQQueueManager, error) {
	// Parse server URL to extract host and port
	host, port, channel, qmName, err := parseIBMMQURL(args.Server)
	if err != nil {
		return ibmmq.MQQueueManager{}, err
	}

	// Override with explicit args if provided
	if args.Channel != "" {
		channel = args.Channel
	}
	if args.QueueManager != "" {
		qmName = args.QueueManager
	}

	// Create connection object
	cno := ibmmq.NewMQCNO()
	csp := ibmmq.NewMQCSP()

	// Set authentication
	if args.User != "" {
		csp.AuthenticationType = ibmmq.MQCSP_AUTH_USER_ID_AND_PWD
		csp.UserId = args.User
		csp.Password = args.Password
	}
	cno.SecurityParms = csp

	// Set client connection options
	cd := ibmmq.NewMQCD()
	cd.ChannelName = channel
	cd.ConnectionName = fmt.Sprintf("%s(%s)", host, port)

	cno.ClientConn = cd
	cno.Options = ibmmq.MQCNO_CLIENT_BINDING

	// Connect to queue manager
	qMgr, err := ibmmq.Connx(qmName, cno)
	if err != nil {
		return ibmmq.MQQueueManager{}, fmt.Errorf("failed to connect to queue manager: %w", err)
	}

	return qMgr, nil
}

// parseIBMMQURL parses IBM MQ connection URL
// Format: ibmmq://host:port/qmgr?channel=CHANNEL
func parseIBMMQURL(serverURL string) (host, port, channel, qmName string, err error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", "", "", "", fmt.Errorf("invalid server URL: %w", err)
	}

	// Extract host and port
	hostPort := u.Host
	if strings.Contains(hostPort, ":") {
		parts := strings.Split(hostPort, ":")
		host = parts[0]
		port = parts[1]
	} else {
		host = hostPort
		port = "1414" // Default IBM MQ port
	}

	// Extract queue manager name from path
	qmName = strings.TrimPrefix(u.Path, "/")
	if qmName == "" {
		qmName = "QM1" // Default queue manager name
	}

	// Extract channel from query parameter
	channel = u.Query().Get("channel")
	if channel == "" {
		channel = "SYSTEM.DEF.SVRCONN" // Default channel
	}

	return host, port, channel, qmName, nil
}
