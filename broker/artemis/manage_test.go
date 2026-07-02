//go:build artemis

package artemis

import "testing"

// Jolokia canonicalizes MBean property keys alphabetically, so "address" is
// always the first key and arrives glued to the domain prefix.
const (
	addressMBean = `org.apache.activemq.artemis:address="orders",broker="0.0.0.0",component=addresses`
	queueMBean   = `org.apache.activemq.artemis:address="orders",broker="0.0.0.0",component=addresses,queue="orders",routing-type="anycast",subcomponent=queues`
)

func TestParseAddressFromMBean(t *testing.T) {
	if got := parseAddressFromMBean(addressMBean); got != "orders" {
		t.Errorf("parseAddressFromMBean(address MBean) = %q, want %q", got, "orders")
	}
	if got := parseAddressFromMBean(queueMBean); got != "orders" {
		t.Errorf("parseAddressFromMBean(queue MBean) = %q, want %q", got, "orders")
	}
	if got := parseAddressFromMBean("no-properties-here"); got != "" {
		t.Errorf("parseAddressFromMBean(garbage) = %q, want empty", got)
	}
}

func TestParseMBeanName(t *testing.T) {
	qi := parseMBeanName(queueMBean)
	if qi.Name != "orders" {
		t.Errorf("Name = %q, want %q", qi.Name, "orders")
	}
	if qi.RoutingType != "anycast" {
		t.Errorf("RoutingType = %q, want %q", qi.RoutingType, "anycast")
	}
}

func TestRoutingTypesString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{[]any{"ANYCAST"}, "ANYCAST"},
		{[]any{"MULTICAST", "ANYCAST"}, "MULTICAST,ANYCAST"},
		{"ANYCAST", "ANYCAST"},
		{nil, ""},
		{42, ""},
	}
	for _, c := range cases {
		if got := routingTypesString(c.in); got != c.want {
			t.Errorf("routingTypesString(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
