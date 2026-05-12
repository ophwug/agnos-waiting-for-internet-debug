package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	openpilotURL     = "https://openpilot.comma.ai"
	defaultParallel  = 64
	defaultScanDelay = 750 * time.Millisecond
)

var (
	output      io.Writer = os.Stdout
	errorOutput io.Writer = os.Stderr
)

type Config struct {
	IP                string
	CIDR              string
	Parallel          int
	Timeout           time.Duration
	JSON              bool
	LogPath           string
	RepairDongleID    string
	OverwriteDongleID bool
	NoReboot          bool
}

type RunReport struct {
	StartedAt     time.Time      `json:"started_at"`
	Debugger      string         `json:"debugger"`
	LogPath       string         `json:"log_path,omitempty"`
	OpenpilotURL  string         `json:"openpilot_url"`
	LAN           *LANInfo       `json:"lan,omitempty"`
	Targets       []string       `json:"targets"`
	Skipped       []SkippedIP    `json:"skipped,omitempty"`
	DeviceReports []DeviceReport `json:"device_reports"`
}

func main() {
	cfg := parseFlags()
	defer waitForExit()

	if cfg.JSON && cfg.RepairDongleID != "" {
		fmt.Fprintf(errorOutput, "Error: --json cannot be combined with --repair-dongle-id\n")
		os.Exit(1)
	}

	logFile, err := setupTeeLog(&cfg)
	if err != nil {
		fmt.Fprintf(errorOutput, "Warning: could not create diagnosis log: %v\n", err)
	} else if logFile != nil {
		defer logFile.Close()
	}

	fmt.Fprintln(output, "openpilot setup internet debugger")
	fmt.Fprintf(output, "Version: %s\n", debuggerVersion())
	if cfg.LogPath != "" {
		fmt.Fprintf(output, "Diagnosis log: %s\n", cfg.LogPath)
	}
	fmt.Fprintln(output, "-----------------------------------")
	fmt.Fprintf(output, "This tool checks what AGNOS/openpilot setup sees when it says \"Waiting for internet\" and can run a guarded dongle_id repair when directed by knowledgeable users.\n\n")

	if cfg.IP == "" && cfg.CIDR == "" && !cfg.JSON {
		cfg = promptStartupMenu(cfg)
		fmt.Fprintln(output)
	}

	ctx := context.Background()
	report, targets, err := discoverTargets(ctx, cfg)
	if err != nil {
		fmt.Fprintf(errorOutput, "Error: %v\n", err)
		os.Exit(1)
	}

	probeDevices(ctx, cfg, report, targets)

	if cfg.JSON {
		diagnoseDevices(ctx, cfg, report)
		enc := json.NewEncoder(output)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(errorOutput, "Error writing JSON: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if reachableCount(report.DeviceReports) == 0 {
		printTextReport(report)
		return
	}

	action := "diagnosis"
	reader := bufio.NewReader(os.Stdin)
	if cfg.RepairDongleID != "" {
		action = "repair"
	} else {
		action = promptActionMenu(reader)
	}

	switch action {
	case "repair":
		if err := runDongleIDRepair(ctx, cfg, report, reader); err != nil {
			fmt.Fprintf(errorOutput, "Error: %v\n", err)
			os.Exit(1)
		}
	default:
		diagnoseDevices(ctx, cfg, report)
		printTextReport(report)
	}
}

func parseFlags() Config {
	cfg := Config{}
	flag.StringVar(&cfg.IP, "ip", "", "debug one known device IP instead of scanning")
	flag.StringVar(&cfg.CIDR, "cidr", "", "scan a specific IPv4 CIDR instead of auto-detecting the active LAN")
	flag.IntVar(&cfg.Parallel, "parallel", defaultParallel, "maximum concurrent SSH probes")
	flag.DurationVar(&cfg.Timeout, "timeout", defaultScanDelay, "timeout for each SSH probe, e.g. 750ms or 2s")
	flag.BoolVar(&cfg.JSON, "json", false, "print machine-readable JSON")
	flag.StringVar(&cfg.LogPath, "log", "", "write a tee-style diagnosis log to this file; default is diagnosis-<timestamp>.txt next to the executable")
	flag.StringVar(&cfg.RepairDongleID, "repair-dongle-id", "", "repair /persist/comma/dongle_id with this 16-character hex dongle ID")
	flag.BoolVar(&cfg.OverwriteDongleID, "overwrite-dongle-id", false, "allow replacing an existing /persist/comma/dongle_id during repair")
	flag.BoolVar(&cfg.NoReboot, "no-reboot", false, "do not prompt to reboot after a successful dongle ID repair")
	flag.Parse()

	cfg.IP = strings.TrimSpace(cfg.IP)
	cfg.CIDR = strings.TrimSpace(cfg.CIDR)
	cfg.RepairDongleID = strings.TrimSpace(cfg.RepairDongleID)
	if cfg.Parallel < 1 {
		cfg.Parallel = defaultParallel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultScanDelay
	}
	return cfg
}

func setupTeeLog(cfg *Config) (*os.File, error) {
	if cfg.LogPath == "-" {
		return nil, nil
	}

	path := strings.TrimSpace(cfg.LogPath)
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			exe = "."
		}
		dir := filepath.Dir(exe)
		path = filepath.Join(dir, "diagnosis-"+time.Now().Format("20060102-150405")+".txt")
		cfg.LogPath = path
	}

	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	output = io.MultiWriter(os.Stdout, file)
	errorOutput = io.MultiWriter(os.Stderr, file)
	return file, nil
}

func promptStartupMenu(cfg Config) Config {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Fprintln(output, "How would you like to find the device?")
		fmt.Fprintln(output, "  1. Enter the device IP address shown in Advanced internet settings")
		fmt.Fprintln(output, "  2. Scan this computer's active local network")
		fmt.Fprintln(output, "  3. Enter a network range manually, such as 192.168.1.0/24")
		fmt.Fprint(output, "Select 1, 2, or 3: ")

		choice, _ := reader.ReadString('\n')
		switch strings.TrimSpace(choice) {
		case "1":
			for {
				fmt.Fprint(output, "Enter device IP address: ")
				ipText, _ := reader.ReadString('\n')
				ipText = strings.TrimSpace(ipText)
				ip := net.ParseIP(ipText)
				if ip != nil && ip.To4() != nil {
					cfg.IP = ip.To4().String()
					return cfg
				}
				fmt.Fprintln(output, "That does not look like an IPv4 address. Example: 192.168.129.5")
			}
		case "2", "":
			fmt.Fprintln(output, "Scanning the active local network.")
			return cfg
		case "3":
			for {
				fmt.Fprint(output, "Enter IPv4 CIDR: ")
				cidr, _ := reader.ReadString('\n')
				cidr = strings.TrimSpace(cidr)
				if _, _, err := net.ParseCIDR(cidr); err == nil {
					cfg.CIDR = cidr
					return cfg
				}
				fmt.Fprintln(output, "That does not look like an IPv4 CIDR. Example: 192.168.129.0/24")
			}
		default:
			fmt.Fprintln(output, "Please choose 1, 2, or 3.")
		}
		fmt.Fprintln(output)
	}
}

func promptActionMenu(reader *bufio.Reader) string {
	for {
		fmt.Fprintln(output, "What would you like to do?")
		fmt.Fprintln(output, "  1. Run diagnosis")
		fmt.Fprintln(output, "  2. Repair missing/incorrect dongle_id")
		fmt.Fprint(output, "Select 1 or 2: ")

		choice, _ := reader.ReadString('\n')
		switch strings.TrimSpace(choice) {
		case "1", "":
			fmt.Fprintln(output, "Running diagnosis.")
			fmt.Fprintln(output)
			return "diagnosis"
		case "2":
			fmt.Fprintln(output, "Starting dongle_id repair.")
			fmt.Fprintln(output)
			return "repair"
		default:
			fmt.Fprintln(output, "Please choose 1 or 2.")
			fmt.Fprintln(output)
		}
	}
}

func discoverTargets(ctx context.Context, cfg Config) (*RunReport, []net.IP, error) {
	report := &RunReport{
		StartedAt:    time.Now(),
		Debugger:     debuggerVersion(),
		LogPath:      cfg.LogPath,
		OpenpilotURL: openpilotURL,
	}

	var targets []net.IP
	var skipped []SkippedIP

	switch {
	case cfg.IP != "":
		ip := net.ParseIP(cfg.IP)
		if ip == nil || ip.To4() == nil {
			return nil, nil, fmt.Errorf("invalid IPv4 address for --ip: %q", cfg.IP)
		}
		targets = []net.IP{ip.To4()}
	case cfg.CIDR != "":
		var err error
		targets, skipped, err = targetsFromCIDR(cfg.CIDR, nil)
		if err != nil {
			return nil, nil, err
		}
	default:
		lan, err := discoverActiveLAN(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("%v\n\nTry passing --cidr 192.168.1.0/24 or --ip <device-ip> manually", err)
		}
		report.LAN = lan
		var err2 error
		targets, skipped, err2 = targetsFromCIDR(lan.CIDR, []net.IP{lan.IP, lan.Gateway})
		if err2 != nil {
			return nil, nil, err2
		}
	}

	if len(targets) == 0 {
		return nil, nil, fmt.Errorf("no scan targets found")
	}

	report.Skipped = skipped
	report.Targets = ipsToStrings(targets)

	if !cfg.JSON {
		if report.LAN != nil {
			fmt.Fprintf(output, "Detected LAN: %s on %s", report.LAN.CIDR, report.LAN.Interface)
			if report.LAN.Gateway != nil {
				fmt.Fprintf(output, " (gateway %s)", report.LAN.Gateway)
			}
			fmt.Fprintln(output)
		}
		fmt.Fprintf(output, "Scanning %d target(s) with %d workers...\n\n", len(targets), cfg.Parallel)
	}

	return report, targets, nil
}

func probeDevices(ctx context.Context, cfg Config, report *RunReport, targets []net.IP) {
	report.DeviceReports = scanSSHReachability(ctx, targets, cfg.Parallel, cfg.Timeout, privateKey)
	sortDeviceReports(report.DeviceReports)

	if cfg.JSON {
		return
	}

	reachable := reachableCount(report.DeviceReports)
	fmt.Fprintln(output, "SSH probe complete.")
	fmt.Fprintf(output, "Targets probed: %d\n", len(report.Targets))
	fmt.Fprintf(output, "SSH reachable devices: %d\n", reachable)
	for _, device := range report.DeviceReports {
		if device.SSHReachable {
			fmt.Fprintf(output, "  - %s\n", device.IP)
		}
	}
	fmt.Fprintln(output)
}

func diagnoseDevices(ctx context.Context, cfg Config, report *RunReport) {
	report.DeviceReports = diagnoseReachableDevices(ctx, report.DeviceReports, cfg.Parallel, cfg.Timeout, privateKey)
	sortDeviceReports(report.DeviceReports)
}

func sortDeviceReports(reports []DeviceReport) {
	sort.Slice(reports, func(i, j int) bool {
		return ipLess(net.ParseIP(reports[i].IP), net.ParseIP(reports[j].IP))
	})
}

func reachableCount(reports []DeviceReport) int {
	reachable := 0
	for _, device := range reports {
		if device.SSHReachable {
			reachable++
		}
	}
	return reachable
}

func run(ctx context.Context, cfg Config) (*RunReport, error) {
	report, targets, err := discoverTargets(ctx, cfg)
	if err != nil {
		return nil, err
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
