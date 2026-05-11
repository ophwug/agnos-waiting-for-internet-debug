package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	openpilotURL     = "https://openpilot.comma.ai"
	defaultParallel  = 64
	defaultScanDelay = 750 * time.Millisecond
)

type Config struct {
	IP       string
	CIDR     string
	Parallel int
	Timeout  time.Duration
	JSON     bool
}

type RunReport struct {
	StartedAt     time.Time      `json:"started_at"`
	Debugger      string         `json:"debugger"`
	OpenpilotURL  string         `json:"openpilot_url"`
	LAN           *LANInfo       `json:"lan,omitempty"`
	Targets       []string       `json:"targets"`
	Skipped       []SkippedIP    `json:"skipped,omitempty"`
	DeviceReports []DeviceReport `json:"device_reports"`
}

func main() {
	cfg := parseFlags()
	defer waitForExit()

	fmt.Println("openpilot setup internet debugger")
	fmt.Printf("Version: %s\n", debuggerVersion())
	fmt.Println("-----------------------------------")
	fmt.Printf("This read-only tool checks what AGNOS/openpilot setup sees when it says \"Waiting for internet\".\n\n")

	if cfg.IP == "" && cfg.CIDR == "" && !cfg.JSON {
		cfg = promptStartupMenu(cfg)
		fmt.Println()
	}

	ctx := context.Background()
	report, err := run(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if cfg.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing JSON: %v\n", err)
			os.Exit(1)
		}
		return
	}

	printTextReport(report)
}

func parseFlags() Config {
	cfg := Config{}
	flag.StringVar(&cfg.IP, "ip", "", "debug one known device IP instead of scanning")
	flag.StringVar(&cfg.CIDR, "cidr", "", "scan a specific IPv4 CIDR instead of auto-detecting the active LAN")
	flag.IntVar(&cfg.Parallel, "parallel", defaultParallel, "maximum concurrent SSH probes")
	flag.DurationVar(&cfg.Timeout, "timeout", defaultScanDelay, "timeout for each SSH probe, e.g. 750ms or 2s")
	flag.BoolVar(&cfg.JSON, "json", false, "print machine-readable JSON")
	flag.Parse()

	cfg.IP = strings.TrimSpace(cfg.IP)
	cfg.CIDR = strings.TrimSpace(cfg.CIDR)
	if cfg.Parallel < 1 {
		cfg.Parallel = defaultParallel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultScanDelay
	}
	return cfg
}

func promptStartupMenu(cfg Config) Config {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println("How would you like to find the device?")
		fmt.Println("  1. Enter the device IP address shown in Advanced internet settings")
		fmt.Println("  2. Scan this computer's active local network")
		fmt.Println("  3. Enter a network range manually, such as 192.168.1.0/24")
		fmt.Print("Select 1, 2, or 3: ")

		choice, _ := reader.ReadString('\n')
		switch strings.TrimSpace(choice) {
		case "1":
			for {
				fmt.Print("Enter device IP address: ")
				ipText, _ := reader.ReadString('\n')
				ipText = strings.TrimSpace(ipText)
				ip := net.ParseIP(ipText)
				if ip != nil && ip.To4() != nil {
					cfg.IP = ip.To4().String()
					return cfg
				}
				fmt.Println("That does not look like an IPv4 address. Example: 192.168.129.5")
			}
		case "2", "":
			fmt.Println("Scanning the active local network.")
			return cfg
		case "3":
			for {
				fmt.Print("Enter IPv4 CIDR: ")
				cidr, _ := reader.ReadString('\n')
				cidr = strings.TrimSpace(cidr)
				if _, _, err := net.ParseCIDR(cidr); err == nil {
					cfg.CIDR = cidr
					return cfg
				}
				fmt.Println("That does not look like an IPv4 CIDR. Example: 192.168.129.0/24")
			}
		default:
			fmt.Println("Please choose 1, 2, or 3.")
		}
		fmt.Println()
	}
}

func run(ctx context.Context, cfg Config) (*RunReport, error) {
	report := &RunReport{
		StartedAt:    time.Now(),
		Debugger:     debuggerVersion(),
		OpenpilotURL: openpilotURL,
	}

	var targets []net.IP
	var skipped []SkippedIP

	switch {
	case cfg.IP != "":
		ip := net.ParseIP(cfg.IP)
		if ip == nil || ip.To4() == nil {
			return nil, fmt.Errorf("invalid IPv4 address for --ip: %q", cfg.IP)
		}
		targets = []net.IP{ip.To4()}
	case cfg.CIDR != "":
		var err error
		targets, skipped, err = targetsFromCIDR(cfg.CIDR, nil)
		if err != nil {
			return nil, err
		}
	default:
		lan, err := discoverActiveLAN(ctx)
		if err != nil {
			return nil, fmt.Errorf("%v\n\nTry passing --cidr 192.168.1.0/24 or --ip <device-ip> manually", err)
		}
		report.LAN = lan
		var err2 error
		targets, skipped, err2 = targetsFromCIDR(lan.CIDR, []net.IP{lan.IP, lan.Gateway})
		if err2 != nil {
			return nil, err2
		}
	}

	if len(targets) == 0 {
		return nil, fmt.Errorf("no scan targets found")
	}

	report.Skipped = skipped
	report.Targets = ipsToStrings(targets)

	if !cfg.JSON {
		if report.LAN != nil {
			fmt.Printf("Detected LAN: %s on %s", report.LAN.CIDR, report.LAN.Interface)
			if report.LAN.Gateway != nil {
				fmt.Printf(" (gateway %s)", report.LAN.Gateway)
			}
			fmt.Println()
		}
		fmt.Printf("Scanning %d target(s) with %d workers...\n\n", len(targets), cfg.Parallel)
	}

	report.DeviceReports = scanAndDiagnose(ctx, targets, cfg.Parallel, cfg.Timeout, privateKey)
	sort.Slice(report.DeviceReports, func(i, j int) bool {
		return ipLess(net.ParseIP(report.DeviceReports[i].IP), net.ParseIP(report.DeviceReports[j].IP))
	})

	return report, nil
}

func ipsToStrings(ips []net.IP) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		out = append(out, ip.String())
	}
	return out
}
