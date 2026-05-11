package main

import (
	"net"
	"testing"
)

func TestTargetsFromCIDRSkipsNetworkBroadcastSelfGateway(t *testing.T) {
	targets, skipped, err := targetsFromCIDR("192.168.1.0/29", []net.IP{
		net.ParseIP("192.168.1.2"),
		net.ParseIP("192.168.1.1"),
	})
	if err != nil {
		t.Fatal(err)
	}

	gotTargets := ipsToStrings(targets)
	wantTargets := []string{"192.168.1.3", "192.168.1.4", "192.168.1.5", "192.168.1.6"}
	if len(gotTargets) != len(wantTargets) {
		t.Fatalf("targets = %#v, want %#v", gotTargets, wantTargets)
	}
	for i := range wantTargets {
		if gotTargets[i] != wantTargets[i] {
			t.Fatalf("targets = %#v, want %#v", gotTargets, wantTargets)
		}
	}

	reasons := map[string]string{}
	for _, skip := range skipped {
		reasons[skip.IP] = skip.Reason
	}
	for ip, reason := range map[string]string{
		"192.168.1.0": "network",
		"192.168.1.1": "gateway",
		"192.168.1.2": "self",
		"192.168.1.7": "broadcast",
	} {
		if reasons[ip] != reason {
			t.Fatalf("skip reason for %s = %q, want %q; all=%#v", ip, reasons[ip], reason, skipped)
		}
	}
}

func TestParseWindowsRouteChoosesLowestMetricDefaultRoute(t *testing.T) {
	out := []byte(`
IPv4 Route Table
===========================================================================
Active Routes:
Network Destination        Netmask          Gateway       Interface  Metric
          0.0.0.0          0.0.0.0       10.0.0.1      10.0.0.44     35
          0.0.0.0          0.0.0.0    192.168.7.1   192.168.7.22     25
`)
	route, err := parseWindowsRoute(out)
	if err != nil {
		t.Fatal(err)
	}
	if route.gateway.String() != "192.168.7.1" {
		t.Fatalf("gateway = %s", route.gateway)
	}
	if route.src.String() != "192.168.7.22" {
		t.Fatalf("src = %s", route.src)
	}
}

func TestParseDarwinRoute(t *testing.T) {
	out := []byte(`
   route to: default
destination: default
       mask: default
    gateway: 192.168.4.1
  interface: en0
`)
	route, err := parseDarwinRoute(out)
	if err != nil {
		t.Fatal(err)
	}
	if route.iface != "en0" {
		t.Fatalf("iface = %q", route.iface)
	}
	if route.gateway.String() != "192.168.4.1" {
		t.Fatalf("gateway = %s", route.gateway)
	}
}

func TestParseLinuxRoute(t *testing.T) {
	out := []byte(`default via 172.16.5.1 dev wlan0 proto dhcp src 172.16.5.99 metric 600`)
	route, err := parseLinuxRoute(out)
	if err != nil {
		t.Fatal(err)
	}
	if route.iface != "wlan0" {
		t.Fatalf("iface = %q", route.iface)
	}
	if route.gateway.String() != "172.16.5.1" {
		t.Fatalf("gateway = %s", route.gateway)
	}
	if route.src.String() != "172.16.5.99" {
		t.Fatalf("src = %s", route.src)
	}
}
