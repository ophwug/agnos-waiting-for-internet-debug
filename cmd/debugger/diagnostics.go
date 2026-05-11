package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type DeviceReport struct {
	IP           string       `json:"ip"`
	SSHReachable bool         `json:"ssh_reachable"`
	SSHError     string       `json:"ssh_error,omitempty"`
	Diagnostics  *Diagnostics `json:"diagnostics,omitempty"`
}

type Diagnostics struct {
	Hostname          string      `json:"hostname,omitempty"`
	Version           string      `json:"version,omitempty"`
	Model             string      `json:"model,omitempty"`
	CurrentTime       string      `json:"current_time,omitempty"`
	IPAddresses       string      `json:"ip_addresses,omitempty"`
	DefaultRoute      string      `json:"default_route,omitempty"`
	DNS               string      `json:"dns,omitempty"`
	HTTPChecks        []HTTPCheck `json:"http_checks"`
	OverallStatus     string      `json:"overall_status"`
	LikelySetupScreen string      `json:"likely_setup_screen"`
	Hint              string      `json:"hint,omitempty"`
}

type HTTPCheck struct {
	Method   string `json:"method"`
	OK       bool   `json:"ok"`
	Category string `json:"category"`
	Detail   string `json:"detail,omitempty"`
	Raw      string `json:"raw,omitempty"`
}

func scanAndDiagnose(ctx context.Context, targets []net.IP, parallel int, timeout time.Duration, key []byte) []DeviceReport {
	jobs := make(chan net.IP)
	results := make(chan DeviceReport, len(targets))

	var wg sync.WaitGroup
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ip := range jobs {
				results <- diagnoseTarget(ctx, ip, timeout, key)
			}
		}()
	}

	for _, ip := range targets {
		jobs <- ip
	}
	close(jobs)
	wg.Wait()
	close(results)

	out := make([]DeviceReport, 0, len(targets))
	for res := range results {
		out = append(out, res)
	}
	return out
}

func diagnoseTarget(ctx context.Context, ip net.IP, timeout time.Duration, key []byte) DeviceReport {
	report := DeviceReport{IP: ip.String()}

	client, err := connectSSH(ctx, ip, timeout, key)
	if err != nil {
		report.SSHError = compactError(err)
		return report
	}
	defer client.Close()

	report.SSHReachable = true
	diag := &Diagnostics{}
	report.Diagnostics = diag

	diag.Hostname = runRemoteField(client, "hostname", 2*time.Second)
	diag.Version = runRemoteField(client, "cat /VERSION 2>/dev/null || true", 2*time.Second)
	diag.Model = runRemoteField(client, "tr -d '\\000' </sys/firmware/devicetree/base/model 2>/dev/null || true", 2*time.Second)
	diag.CurrentTime = runRemoteField(client, "date -Is 2>/dev/null || date 2>/dev/null || true", 2*time.Second)
	diag.IPAddresses = runRemoteField(client, "ip -4 addr show 2>/dev/null || ifconfig 2>/dev/null || true", 3*time.Second)
	diag.DefaultRoute = runRemoteField(client, "ip route show default 2>/dev/null || route -n 2>/dev/null || true", 2*time.Second)
	diag.DNS = runRemoteField(client, "cat /etc/resolv.conf 2>/dev/null || true", 2*time.Second)

	httpOut, err := executeCommand(client, remoteHTTPScript(), 6*time.Second)
	if err != nil && strings.TrimSpace(httpOut) == "" {
		diag.HTTPChecks = []HTTPCheck{{
			Method:   "HEAD",
			OK:       false,
			Category: "UNKNOWN",
			Detail:   compactError(err),
		}}
	} else {
		diag.HTTPChecks = parseHTTPChecks(httpOut)
	}
	if len(diag.HTTPChecks) == 0 {
		diag.HTTPChecks = []HTTPCheck{{
			Method:   "HEAD",
			OK:       false,
			Category: "UNKNOWN",
			Detail:   "no HTTP check output",
			Raw:      strings.TrimSpace(httpOut),
		}}
	}

	diag.OverallStatus, diag.LikelySetupScreen, diag.Hint = classifyDiagnostics(diag.HTTPChecks)
	return report
}

func runRemoteField(client *ssh.Client, command string, timeout time.Duration) string {
	out, _ := executeCommand(client, command, timeout)
	return strings.TrimSpace(out)
}

func remoteHTTPScript() string {
	return `python3 - <<'PY'
import socket
import ssl
import urllib.error
import urllib.request

URL = "https://openpilot.comma.ai"

def classify_error(err):
  if isinstance(err, urllib.error.HTTPError):
    return "HTTP_ERROR", str(err.code)
  reason = getattr(err, "reason", err)
  text = repr(reason)
  if isinstance(reason, ssl.SSLCertVerificationError) or "CERTIFICATE_VERIFY_FAILED" in text:
    return "TLS_CERT", text
  if isinstance(reason, socket.gaierror):
    return "DNS", text
  if isinstance(reason, TimeoutError) or "timed out" in text.lower():
    return "TIMEOUT", text
  if isinstance(reason, ConnectionRefusedError):
    return "CONNECTION_REFUSED", text
  return type(reason).__name__, text

for method in ("HEAD", "GET"):
  try:
    req = urllib.request.Request(URL, method=method)
    with urllib.request.urlopen(req, timeout=2.0) as response:
      print("HTTPCHECK\t%s\tOK\t%d\t%s" % (method, response.status, response.geturl()))
  except Exception as e:
    category, detail = classify_error(e)
    print("HTTPCHECK\t%s\tFAIL\t%s\t%s" % (method, category, detail))
PY`
}

func parseHTTPChecks(out string) []HTTPCheck {
	var checks []HTTPCheck
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) >= 4 && parts[0] == "HTTPCHECK" {
			check := HTTPCheck{
				Method:   parts[1],
				OK:       parts[2] == "OK",
				Category: parts[3],
				Raw:      line,
			}
			if len(parts) == 5 {
				check.Detail = parts[4]
			}
			checks = append(checks, check)
		}
	}
	return checks
}

func classifyDiagnostics(checks []HTTPCheck) (status, screen, hint string) {
	head := findHTTPCheck(checks, "HEAD")
	if head != nil && head.OK {
		return "PASS", "Continue or Continue without Wi-Fi", "The current AGNOS setup internet check passed from the device."
	}
	if head == nil {
		return "UNKNOWN", "Waiting for internet", "Could not run the current AGNOS HEAD check."
	}

	switch head.Category {
	case "DNS":
		hint = "DNS lookup failed on the device. Check router DNS, captive portal state, or whether the device received valid DNS servers."
	case "TLS_CERT":
		hint = "TLS certificate verification failed. On setup devices this often means the system clock is wrong or time sync cannot reach the internet."
	case "TIMEOUT":
		hint = "The device timed out reaching openpilot.comma.ai. Check firewall, captive portal, router isolation, or upstream internet."
	case "HTTP_ERROR":
		hint = "The device reached openpilot.comma.ai but got an HTTP error. Save the status code and share it with the screenshot."
	case "CONNECTION_REFUSED":
		hint = "The device resolved the host but the TCP connection was refused."
	default:
		hint = "The device could not complete the same internet check setup uses. Share this output and the setup screenshot."
	}
	return "FAIL", "Waiting for internet", hint
}

func findHTTPCheck(checks []HTTPCheck, method string) *HTTPCheck {
	for i := range checks {
		if strings.EqualFold(checks[i].Method, method) {
			return &checks[i]
		}
	}
	return nil
}

func compactError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	text = strings.ReplaceAll(text, "\n", "; ")
	if len(text) > 220 {
		return text[:217] + "..."
	}
	return text
}

func summarizeCheck(check HTTPCheck) string {
	if check.OK {
		return fmt.Sprintf("%s OK (%s %s)", check.Method, check.Category, check.Detail)
	}
	return fmt.Sprintf("%s FAIL (%s %s)", check.Method, check.Category, check.Detail)
}
