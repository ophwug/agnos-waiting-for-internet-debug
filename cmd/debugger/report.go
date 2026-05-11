package main

import (
	"fmt"
	"strings"
)

func printTextReport(report *RunReport) {
	reachable := 0
	for _, device := range report.DeviceReports {
		if device.SSHReachable {
			reachable++
		}
	}

	fmt.Println("Scan complete.")
	fmt.Printf("Targets probed: %d\n", len(report.Targets))
	fmt.Printf("SSH reachable devices: %d\n\n", reachable)

	if reachable == 0 {
		fmt.Println("No comma/openpilot device accepted SSH with the embedded comma key.")
		fmt.Println("Make sure the device is powered on, on the same network, and still exposes SSH as user comma.")
		fmt.Println()
		fmt.Println("Please share a screenshot of the setup screen along with this output.")
		return
	}

	for _, device := range report.DeviceReports {
		if !device.SSHReachable {
			continue
		}
		diag := device.Diagnostics
		fmt.Printf("Device %s\n", device.IP)
		fmt.Println(strings.Repeat("-", len("Device ")+len(device.IP)))
		if diag.Hostname != "" {
			fmt.Printf("Hostname: %s\n", oneLine(diag.Hostname))
		}
		if diag.Version != "" {
			fmt.Printf("OS version: %s\n", oneLine(diag.Version))
		}
		if diag.Model != "" {
			fmt.Printf("Model: %s\n", oneLine(diag.Model))
		}
		if diag.CurrentTime != "" {
			fmt.Printf("Device time: %s\n", oneLine(diag.CurrentTime))
		}
		if diag.SetupRuntime != "" {
			fmt.Printf("Setup runtime: %s\n", oneLine(diag.SetupRuntime))
		}
		if diag.DefaultRoute != "" {
			fmt.Printf("Default route: %s\n", oneLine(diag.DefaultRoute))
		}
		if diag.DNS != "" {
			fmt.Printf("DNS: %s\n", oneLine(diag.DNS))
		}
		fmt.Println("Setup internet checks:")
		for _, check := range diag.HTTPChecks {
			fmt.Printf("  - %s\n", summarizeCheck(check))
		}
		fmt.Printf("Result: %s\n", diag.OverallStatus)
		fmt.Printf("Likely setup screen: %s\n", diag.LikelySetupScreen)
		if diag.Hint != "" {
			fmt.Printf("Hint: %s\n", diag.Hint)
		}
		if diag.SetupProcesses != "" {
			fmt.Println("Setup/UI processes:")
			fmt.Println(indentBlock(diag.SetupProcesses, "  "))
		}
		if diag.RecentSetupLogs != "" {
			fmt.Println("Recent setup/network log lines:")
			fmt.Println(indentBlock(diag.RecentSetupLogs, "  "))
		}
		fmt.Println()
	}

	fmt.Println("Please share a screenshot of the setup screen along with this output.")
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 180 {
		return s[:177] + "..."
	}
	return s
}

func indentBlock(s, prefix string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := range lines {
		lines[i] = prefix + strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, "\n")
}
