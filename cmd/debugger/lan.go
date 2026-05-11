package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type LANInfo struct {
	Interface string `json:"interface"`
	IP        net.IP `json:"ip"`
	CIDR      string `json:"cidr"`
	Gateway   net.IP `json:"gateway,omitempty"`
	Source    string `json:"source"`
}

type SkippedIP struct {
	IP     string `json:"ip"`
	Reason string `json:"reason"`
}

type routeInfo struct {
	iface   string
	gateway net.IP
	src     net.IP
}

func discoverActiveLAN(ctx context.Context) (*LANInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var route routeInfo
	var err error

	switch runtime.GOOS {
	case "windows":
		route, err = discoverWindowsRoute(ctx)
	case "darwin":
		route, err = discoverDarwinRoute(ctx)
	case "linux":
		route, err = discoverLinuxRoute(ctx)
	default:
		err = fmt.Errorf("automatic subnet detection is not implemented for %s", runtime.GOOS)
	}

	if err == nil {
		if lan, err := lanFromRoute(route, runtime.GOOS); err == nil {
			return lan, nil
		}
	}

	lan, fallbackErr := firstPrivateIPv4LAN()
	if fallbackErr == nil {
		lan.Source = "fallback-interface"
		return lan, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, fallbackErr
}

func discoverWindowsRoute(ctx context.Context) (routeInfo, error) {
	out, err := exec.CommandContext(ctx, "route", "print", "-4", "0.0.0.0").Output()
	if err != nil {
		return routeInfo{}, err
	}
	return parseWindowsRoute(out)
}

func discoverDarwinRoute(ctx context.Context) (routeInfo, error) {
	out, err := exec.CommandContext(ctx, "route", "-n", "get", "default").Output()
	if err != nil {
		return routeInfo{}, err
	}
	return parseDarwinRoute(out)
}

func discoverLinuxRoute(ctx context.Context) (routeInfo, error) {
	out, err := exec.CommandContext(ctx, "ip", "route", "show", "default").Output()
	if err != nil {
		return routeInfo{}, err
	}
	return parseLinuxRoute(out)
}

func parseWindowsRoute(out []byte) (routeInfo, error) {
	scanner := bufio.NewScanner(bytes.NewReader(out))
	bestMetric := int(^uint(0) >> 1)
	var best routeInfo

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 || fields[0] != "0.0.0.0" || fields[1] != "0.0.0.0" {
			continue
		}
		gateway := net.ParseIP(fields[2])
		src := net.ParseIP(fields[3])
		metric, _ := strconv.Atoi(fields[4])
		if gateway == nil || src == nil || gateway.To4() == nil || src.To4() == nil {
			continue
		}
		if metric < bestMetric {
			bestMetric = metric
			best = routeInfo{gateway: gateway.To4(), src: src.To4()}
		}
	}

	if best.src == nil {
		return routeInfo{}, fmt.Errorf("could not parse Windows default IPv4 route")
	}
	return best, nil
}

func parseDarwinRoute(out []byte) (routeInfo, error) {
	var route routeInfo
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "gateway:") {
			route.gateway = net.ParseIP(strings.TrimSpace(strings.TrimPrefix(line, "gateway:")))
		}
		if strings.HasPrefix(line, "interface:") {
			route.iface = strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
		}
	}
	if route.iface == "" {
		return routeInfo{}, fmt.Errorf("could not parse macOS default route interface")
	}
	if route.gateway != nil {
		route.gateway = route.gateway.To4()
	}
	return route, nil
}

func parseLinuxRoute(out []byte) (routeInfo, error) {
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || fields[0] != "default" {
			continue
		}
		var route routeInfo
		for i := 1; i < len(fields)-1; i++ {
			switch fields[i] {
			case "via":
				route.gateway = net.ParseIP(fields[i+1])
			case "dev":
				route.iface = fields[i+1]
			case "src":
				route.src = net.ParseIP(fields[i+1])
			}
		}
		if route.iface == "" {
			return routeInfo{}, fmt.Errorf("could not parse Linux default route interface")
		}
		if route.gateway != nil {
			route.gateway = route.gateway.To4()
		}
		if route.src != nil {
			route.src = route.src.To4()
		}
		return route, nil
	}
	return routeInfo{}, fmt.Errorf("could not parse Linux default IPv4 route")
}

func lanFromRoute(route routeInfo, source string) (*LANInfo, error) {
	if route.src != nil && route.src.To4() != nil {
		if lan, err := lanForIP(route.src.To4()); err == nil {
			lan.Gateway = route.gateway
			lan.Source = source
			return lan, nil
		}
	}
	if route.iface != "" {
		iface, err := net.InterfaceByName(route.iface)
		if err != nil {
			return nil, err
		}
		lan, err := lanForInterface(iface)
		if err != nil {
			return nil, err
		}
		lan.Gateway = route.gateway
		lan.Source = source
		return lan, nil
	}
	return nil, fmt.Errorf("default route did not include an interface or source IP")
}

func firstPrivateIPv4LAN() (*LANInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for i := range ifaces {
		if ifaces[i].Flags&net.FlagUp == 0 || ifaces[i].Flags&net.FlagLoopback != 0 {
			continue
		}
		lan, err := lanForInterface(&ifaces[i])
		if err == nil && isPrivateIPv4(lan.IP) {
			return lan, nil
		}
	}
	return nil, fmt.Errorf("could not find an active private IPv4 interface")
}

func lanForIP(ip net.IP) (*LANInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for i := range ifaces {
		lan, err := lanForInterface(&ifaces[i])
		if err == nil && lan.IP.Equal(ip) {
			return lan, nil
		}
	}
	return nil, fmt.Errorf("could not find local interface for %s", ip)
}

func lanForInterface(iface *net.Interface) (*LANInfo, error) {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil || ip[0] == 127 {
			continue
		}
		ones, bits := ipNet.Mask.Size()
		if bits != 32 {
			continue
		}
		_ = ones
		return &LANInfo{
			Interface: iface.Name,
			IP:        ip,
			CIDR:      (&net.IPNet{IP: ip.Mask(ipNet.Mask), Mask: ipNet.Mask}).String(),
			Source:    "interface",
			Gateway:   nil,
		}, nil
	}
	return nil, fmt.Errorf("interface %s has no IPv4 address", iface.Name)
}

func targetsFromCIDR(cidr string, skipIPs []net.IP) ([]net.IP, []SkippedIP, error) {
	networkIP, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	networkIP = networkIP.To4()
	if networkIP == nil {
		return nil, nil, fmt.Errorf("only IPv4 CIDRs are supported: %q", cidr)
	}
	ones, bits := ipNet.Mask.Size()
	if bits != 32 {
		return nil, nil, fmt.Errorf("only IPv4 CIDRs are supported: %q", cidr)
	}

	network := ipv4ToUint32(networkIP.Mask(ipNet.Mask))
	size := uint32(1) << uint32(32-ones)
	broadcast := network + size - 1

	skip := map[uint32]string{
		network:   "network",
		broadcast: "broadcast",
	}
	for _, ip := range skipIPs {
		if ip == nil || ip.To4() == nil {
			continue
		}
		reason := "skip"
		if ip.Equal(skipIPs[0]) {
			reason = "self"
		}
		if len(skipIPs) > 1 && ip.Equal(skipIPs[1]) {
			reason = "gateway"
		}
		skip[ipv4ToUint32(ip.To4())] = reason
	}

	targets := []net.IP{}
	skipped := []SkippedIP{}
	for current := network; current <= broadcast; current++ {
		if reason, ok := skip[current]; ok {
			skipped = append(skipped, SkippedIP{IP: uint32ToIPv4(current).String(), Reason: reason})
		} else {
			targets = append(targets, uint32ToIPv4(current))
		}
		if current == broadcast {
			break
		}
	}
	return targets, skipped, nil
}

func isPrivateIPv4(ip net.IP) bool {
	ip = ip.To4()
	if ip == nil {
		return false
	}
	return ip[0] == 10 ||
		(ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) ||
		(ip[0] == 192 && ip[1] == 168)
}

func ipv4ToUint32(ip net.IP) uint32 {
	return binary.BigEndian.Uint32(ip.To4())
}

func uint32ToIPv4(v uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, v)
	return ip
}

func ipLess(a, b net.IP) bool {
	if a == nil {
		return true
	}
	if b == nil {
		return false
	}
	return ipv4ToUint32(a.To4()) < ipv4ToUint32(b.To4())
}
