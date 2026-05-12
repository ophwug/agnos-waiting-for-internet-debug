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

	fmt.Fprintln(output, "Scan complete.")
	fmt.Fprintf(output, "Targets probed: %d\n", len(report.Targets))
	fmt.Fprintf(output, "SSH reachable devices: %d\n\n", reachable)

	if reachable == 0 {
		fmt.Fprintln(output, "No comma/openpilot device accepted SSH with the embedded comma key.")
		fmt.Fprintln(output, "Make sure the device is powered on, on the same network, and still exposes SSH as user comma.")
		fmt.Fprintln(output)
		if report.LogPath != "" {
			fmt.Fprintf(output, "Diagnosis log written to: %s\n", report.LogPath)
		}
		fmt.Fprintln(output, "Please share a screenshot of the setup screen along with this output.")
		return
	}

	for _, device := range report.DeviceReports {
		if !device.SSHReachable {
			continue
		}
		diag := device.Diagnostics
		fmt.Fprintf(output, "Device %s\n", device.IP)
		fmt.Fprintln(output, strings.Repeat("-", len("Device ")+len(device.IP)))
		if diag.Hostname != "" {
			fmt.Fprintf(output, "Hostname: %s\n", oneLine(diag.Hostname))
		}
		if diag.Version != "" {
			fmt.Fprintf(output, "OS version: %s\n", oneLine(diag.Version))
		}
		if diag.Model != "" {
			fmt.Fprintf(output, "Model: %s\n", oneLine(diag.Model))
		}
		if diag.CurrentTime != "" {
			fmt.Fprintf(output, "Device time: %s\n", oneLine(diag.CurrentTime))
		}
		if diag.SetupRuntime != "" {
			fmt.Fprintf(output, "Setup runtime: %s\n", oneLine(diag.SetupRuntime))
		}
		if diag.DefaultRoute != "" {
			fmt.Fprintf(output, "Default route: %s\n", oneLine(diag.DefaultRoute))
		}
		if diag.DNS != "" {
			fmt.Fprintf(output, "DNS: %s\n", oneLine(diag.DNS))
		}
		fmt.Fprintln(output, "Setup internet checks:")
		for _, check := range diag.HTTPChecks {
			fmt.Fprintf(output, "  - %s\n", summarizeCheck(check))
		}
		fmt.Fprintf(output, "Result: %s\n", diag.OverallStatus)
		fmt.Fprintf(output, "Likely setup screen: %s\n", diag.LikelySetupScreen)
		if diag.Hint != "" {
			fmt.Fprintf(output, "Hint: %s\n", diag.Hint)
		}
		if diag.SetupProcesses != "" {
			fmt.Fprintln(output, "Setup/UI processes:")
			fmt.Fprintln(output, indentBlock(diag.SetupProcesses, "  "))
		}
		if diag.PythonEnvironment != "" {
			fmt.Fprintln(output, "Python environment clues:")
			fmt.Fprintln(output, indentBlock(diag.PythonEnvironment, "  "))
		}
		if diag.StabilitySamples != "" {
			fmt.Fprintln(output, "Setup-env connectivity stability:")
			fmt.Fprintln(output, indentBlock(diag.StabilitySamples, "  "))
		}
		if diag.NetworkState != "" {
			fmt.Fprintln(output, "Network state clues:")
			fmt.Fprintln(output, indentBlock(diag.NetworkState, "  "))
		}
		if diag.PersistComma != "" {
			fmt.Fprintln(output, "Persist comma directory:")
			fmt.Fprintln(output, indentBlock(diag.PersistComma, "  "))
		}
		if diag.SetupBinary != "" {
			fmt.Fprintln(output, "Setup binary/script clues:")
			fmt.Fprintln(output, indentBlock(diag.SetupBinary, "  "))
		}
		if diag.RecentSetupLogs != "" {
			fmt.Fprintln(output, "Recent setup/network log lines:")
			fmt.Fprintln(output, indentBlock(diag.RecentSetupLogs, "  "))
		}
		fmt.Fprintln(output)
	}

	if report.LogPath != "" {
		fmt.Fprintf(output, "Diagnosis log written to: %s\n", report.LogPath)
	}
	fmt.Fprintln(output, "Please share a screenshot of the setup screen along with this output.")
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
