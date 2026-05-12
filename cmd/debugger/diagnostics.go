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
	SetupRuntime      string      `json:"setup_runtime,omitempty"`
	SetupProcesses    string      `json:"setup_processes,omitempty"`
	PythonEnvironment string      `json:"python_environment,omitempty"`
	StabilitySamples  string      `json:"stability_samples,omitempty"`
	PersistComma      string      `json:"persist_comma,omitempty"`
	SetupBinary       string      `json:"setup_binary,omitempty"`
	RecentSetupLogs   string      `json:"recent_setup_logs,omitempty"`
	IPAddresses       string      `json:"ip_addresses,omitempty"`
	DefaultRoute      string      `json:"default_route,omitempty"`
	DNS               string      `json:"dns,omitempty"`
	HTTPChecks        []HTTPCheck `json:"http_checks"`
	OverallStatus     string      `json:"overall_status"`
	LikelySetupScreen string      `json:"likely_setup_screen"`
	Hint              string      `json:"hint,omitempty"`
}

type HTTPCheck struct {
	Context  string `json:"context"`
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
	diag.SetupRuntime = runRemoteField(client, setupRuntimeCommand(), 5*time.Second)
	diag.SetupProcesses = runRemoteField(client, "ps -eo pid,comm,args 2>/dev/null | grep -Ei 'setup|tici_setup|mici_setup|installer|raylib|ui' | grep -v grep || true", 2*time.Second)
	diag.PythonEnvironment = runRemoteField(client, pythonEnvironmentCommand(), 6*time.Second)
	diag.StabilitySamples = runRemoteField(client, setupEnvStabilityCommand(), 22*time.Second)
	diag.PersistComma = runRemoteField(client, "ls -lha /persist/comma 2>&1 || true", 2*time.Second)
	diag.SetupBinary = runRemoteField(client, setupBinaryCommand(), 6*time.Second)
	diag.RecentSetupLogs = runRemoteField(client, recentSetupLogsCommand(), 5*time.Second)
	diag.IPAddresses = runRemoteField(client, "ip -4 addr show 2>/dev/null || ifconfig 2>/dev/null || true", 3*time.Second)
	diag.DefaultRoute = runRemoteField(client, "ip route show default 2>/dev/null || route -n 2>/dev/null || true", 2*time.Second)
	diag.DNS = runRemoteField(client, "cat /etc/resolv.conf 2>/dev/null || true", 2*time.Second)

	httpOut, err := executeCommand(client, remoteHTTPScript(), 6*time.Second)
	if err != nil && strings.TrimSpace(httpOut) == "" {
		diag.HTTPChecks = []HTTPCheck{{
			Context:  "plain",
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
			Context:  "plain",
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
import os
import socket
import ssl
import subprocess
import sys
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

CHECK_SCRIPT = r'''
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
      print("RESULT\t%s\tOK\t%d\t%s" % (method, response.status, response.geturl()))
  except Exception as e:
    category, detail = classify_error(e)
    print("RESULT\t%s\tFAIL\t%s\t%s" % (method, category, detail))
'''

def emit_check(context, method, ok, category, detail):
  state = "OK" if ok else "FAIL"
  print("HTTPCHECK\t%s\t%s\t%s\t%s\t%s" % (context, method, state, category, detail))

def run_plain():
  for method in ("HEAD", "GET"):
    try:
      req = urllib.request.Request(URL, method=method)
      with urllib.request.urlopen(req, timeout=2.0) as response:
        emit_check("plain", method, True, response.status, response.geturl())
    except Exception as e:
      category, detail = classify_error(e)
      emit_check("plain", method, False, category, detail)

def find_setup_process():
  for pid in os.listdir("/proc"):
    if not pid.isdigit():
      continue
    try:
      cmdline = open(f"/proc/{pid}/cmdline", "rb").read().replace(b"\0", b" ").decode("utf-8", "replace")
    except Exception:
      continue
    if "/usr/comma/setup" in cmdline or "tici_setup" in cmdline or "mici_setup" in cmdline:
      return pid
  return None

def run_setup_env():
  pid = find_setup_process()
  if pid is None:
    emit_check("setup-env", "HEAD", False, "NO_SETUP_PROCESS", "could not find running setup process")
    return
  try:
    env_items = open(f"/proc/{pid}/environ", "rb").read().split(b"\0")
    env = {}
    for item in env_items:
      if b"=" in item:
        key, value = item.split(b"=", 1)
        env[key.decode("utf-8", "replace")] = value.decode("utf-8", "replace")
  except Exception as e:
    emit_check("setup-env", "HEAD", False, "ENV_READ_ERROR", repr(e))
    return
  try:
    cwd = os.readlink(f"/proc/{pid}/cwd")
  except Exception:
    cwd = "/"
  try:
    exe = os.readlink(f"/proc/{pid}/exe")
  except Exception:
    exe = sys.executable
  setup_archive = "/usr/comma/setup"
  if os.path.exists(setup_archive):
    existing = env.get("PYTHONPATH", "")
    env["PYTHONPATH"] = setup_archive + ((":" + existing) if existing else "")

  candidates = [exe, env.get("PYTHON", ""), sys.executable, "python3"]
  last = None
  for py in [c for c in candidates if c]:
    try:
      proc = subprocess.run([py, "-c", CHECK_SCRIPT], cwd=cwd, env=env, text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=6)
      emitted = False
      for line in proc.stdout.splitlines():
        parts = line.split("\t", 4)
        if len(parts) == 5 and parts[0] == "RESULT":
          emit_check("setup-env", parts[1], parts[2] == "OK", parts[3], parts[4])
          emitted = True
      if proc.returncode == 0 and emitted:
        return
      last = proc.stdout.strip() or ("exit code %s" % proc.returncode)
    except Exception as e:
      last = repr(e)
  emit_check("setup-env", "HEAD", False, "SETUP_ENV_EXEC_ERROR", str(last))

run_plain()
run_setup_env()
PY`
}

func setupRuntimeCommand() string {
	return `python3 - <<'PY'
import os
import subprocess

def run(cmd):
  try:
    return subprocess.check_output(cmd, stderr=subprocess.STDOUT, timeout=1.5, text=True).strip()
  except Exception as e:
    return "ERROR:%r" % (e,)

print("python=%s" % run(["python3", "--version"]))
print("version_file=%s" % (open("/VERSION").read().strip() if os.path.exists("/VERSION") else "missing"))
model_path = "/sys/firmware/devicetree/base/model"
if os.path.exists(model_path):
  print("model=%s" % open(model_path, "rb").read().replace(b"\0", b"").decode("utf-8", "replace").strip())
print("networkctl=%s" % run(["sh", "-lc", "networkctl status wlan0 2>/dev/null | sed -n '1,18p'"]))
print("systemctl_setup=%s" % run(["sh", "-lc", "systemctl status setup 2>/dev/null | sed -n '1,12p' || true"]))
PY`
}

func setupBinaryCommand() string {
	return `sh -lc '
set +e
echo "setup_path=$(readlink -f /usr/comma/setup 2>/dev/null || echo /usr/comma/setup)"
echo "setup_file=$(file /usr/comma/setup 2>/dev/null || true)"
echo "setup_sha256=$(sha256sum /usr/comma/setup 2>/dev/null | awk "{print \$1}" || true)"
echo "setup_zip_matches:"
python3 - <<'"'"'PY'"'"'
import zipfile

path = "/usr/comma/setup"
patterns = [
  "Waiting for internet",
  "waiting for",
  "openpilot.comma.ai",
  "NetworkConnectivityMonitor",
  "network_connected",
  "get_network_type",
  "urlopen",
  "Request(",
  "internet",
  "CONNECTING",
]
try:
  with zipfile.ZipFile(path) as zf:
    names = zf.namelist()
    print("zip_entries=%d" % len(names))
    for name in names:
      if not name.endswith((".py", ".pyi", ".txt")):
        continue
      try:
        text = zf.read(name).decode("utf-8", "replace")
      except Exception:
        continue
      lower = text.lower()
      matched = [p for p in patterns if p.lower() in lower]
      if not matched:
        continue
      print("MATCH_FILE %s patterns=%s" % (name, ",".join(matched)))
      lines = text.splitlines()
      for idx, line in enumerate(lines):
        if any(p.lower() in line.lower() for p in patterns):
          start = max(0, idx - 3)
          end = min(len(lines), idx + 4)
          print("SNIPPET %s:%d" % (name, idx + 1))
          for line_no in range(start, end):
            print("%4d: %s" % (line_no + 1, lines[line_no][:220]))
          print("END_SNIPPET")
except Exception as e:
  print("zip_probe_error=%r" % (e,))
PY
echo "setup_strings:"
if command -v strings >/dev/null 2>&1; then
  strings -a /usr/comma/setup 2>/dev/null |
    grep -Eio "https?://[^[:space:]\"'"'"'<>]+" |
    sort -u |
    head -30
  strings -a /usr/comma/setup 2>/dev/null |
    grep -Ei "waiting|internet|network|openpilot|github|comma|urllib|curl|wget|HEAD|GET" |
    head -80
else
  echo "strings command not found"
fi
'`
}

func setupEnvStabilityCommand() string {
	return `python3 - <<'PY'
import os
import subprocess
import sys
import time

CHECK_SCRIPT = r'''
import socket
import ssl
import time
import urllib.error
import urllib.request

URL = "https://openpilot.comma.ai"
start = time.monotonic()
try:
  req = urllib.request.Request(URL, method="HEAD")
  with urllib.request.urlopen(req, timeout=2.0) as response:
    print("OK\t%d\t%s\t%.3f" % (response.status, response.geturl(), time.monotonic() - start))
except Exception as e:
  reason = getattr(e, "reason", e)
  text = repr(reason)
  if isinstance(reason, ssl.SSLCertVerificationError) or "CERTIFICATE_VERIFY_FAILED" in text:
    category = "TLS_CERT"
  elif isinstance(reason, socket.gaierror):
    category = "DNS"
  elif isinstance(reason, TimeoutError) or "timed out" in text.lower():
    category = "TIMEOUT"
  else:
    category = type(reason).__name__
  print("FAIL\t%s\t%s\t%.3f" % (category, text, time.monotonic() - start))
'''

def find_setup_process():
  for pid in os.listdir("/proc"):
    if not pid.isdigit():
      continue
    try:
      cmdline = open(f"/proc/{pid}/cmdline", "rb").read().replace(b"\0", b" ").decode("utf-8", "replace")
    except Exception:
      continue
    if "/usr/comma/setup" in cmdline or "tici_setup" in cmdline or "mici_setup" in cmdline:
      return pid
  return None

pid = find_setup_process()
if pid is None:
  print("stability_error=no setup process found")
  raise SystemExit

env = {}
try:
  for item in open(f"/proc/{pid}/environ", "rb").read().split(b"\0"):
    if b"=" in item:
      key, value = item.split(b"=", 1)
      env[key.decode("utf-8", "replace")] = value.decode("utf-8", "replace")
except Exception as e:
  print("stability_error=env read failed: %r" % (e,))
  raise SystemExit

try:
  cwd = os.readlink(f"/proc/{pid}/cwd")
except Exception:
  cwd = "/"
try:
  exe = os.readlink(f"/proc/{pid}/exe")
except Exception:
  exe = sys.executable
if os.path.exists("/usr/comma/setup"):
  existing = env.get("PYTHONPATH", "")
  env["PYTHONPATH"] = "/usr/comma/setup" + ((":" + existing) if existing else "")

print("setup-env stability: 12 HEAD samples, 1 second apart")
ok_count = 0
fail_count = 0
for i in range(12):
  proc = subprocess.run([exe, "-c", CHECK_SCRIPT], cwd=cwd, env=env, text=True,
                        stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=4)
  line = proc.stdout.strip().replace("\n", " | ")
  if line.startswith("OK\t"):
    ok_count += 1
  else:
    fail_count += 1
  print("sample %02d: %s" % (i + 1, line))
  if i != 11:
    time.sleep(1.0)
print("stability summary: ok=%d fail=%d" % (ok_count, fail_count))
PY`
}

func pythonEnvironmentCommand() string {
	return `python3 - <<'PY'
import os
import subprocess
import sys

def sh(cmd, timeout=2.0):
  try:
    return subprocess.check_output(cmd, shell=True, stderr=subprocess.STDOUT, timeout=timeout, text=True).strip()
  except Exception as e:
    return "ERROR:%r" % (e,)

print("current_python=%s" % sys.executable)
print("current_sys_path=%r" % sys.path)
print("current_pythonpath=%s" % os.environ.get("PYTHONPATH", ""))
print("import_openpilot=%s" % sh("python3 - <<'PY2'\ntry:\n import openpilot\n print(getattr(openpilot, '__file__', 'namespace-package'))\nexcept Exception as e:\n print(repr(e))\nPY2"))
print("import_openpilot_from_setup_zip=%s" % sh("PYTHONPATH=/usr/comma/setup python3 - <<'PY2'\ntry:\n import openpilot\n print(getattr(openpilot, '__file__', 'namespace-package'))\nexcept Exception as e:\n print(repr(e))\nPY2"))
print("openpilot_dirs=%s" % sh("find /data /usr /opt -maxdepth 5 -type d -name openpilot 2>/dev/null | head -40", timeout=4.0))

pids = []
for pid in os.listdir("/proc"):
  if not pid.isdigit():
    continue
  try:
    raw = open(f"/proc/{pid}/cmdline", "rb").read().replace(b"\0", b" ").decode("utf-8", "replace").strip()
  except Exception:
    continue
  if "setup" in raw or "tici_setup" in raw or "mici_setup" in raw:
    pids.append((int(pid), raw))

for pid, cmdline in sorted(pids):
  print("process_pid=%s" % pid)
  print("process_cmdline=%s" % cmdline)
  try:
    print("process_cwd=%s" % os.readlink(f"/proc/{pid}/cwd"))
  except Exception as e:
    print("process_cwd_error=%r" % (e,))
  try:
    print("process_exe=%s" % os.readlink(f"/proc/{pid}/exe"))
  except Exception as e:
    print("process_exe_error=%r" % (e,))
  try:
    env = open(f"/proc/{pid}/environ", "rb").read().split(b"\0")
    interesting = []
    for item in env:
      text = item.decode("utf-8", "replace")
      if text.startswith(("PYTHONPATH=", "PYTHONHOME=", "PATH=", "VIRTUAL_ENV=", "LD_LIBRARY_PATH=", "HOME=", "USER=")):
        interesting.append(text)
    print("process_python_env=%s" % " | ".join(interesting))
  except Exception as e:
    print("process_env_error=%r" % (e,))
PY`
}

func recentSetupLogsCommand() string {
	return `sh -lc '
(journalctl -n 250 --no-pager 2>/dev/null || logcat -d -t 250 2>/dev/null || true) |
grep -Ei "setup|waiting|internet|openpilot.comma.ai|github.com|urllib|traceback|exception|error|networkmanager|systemd-resolved|resolved|timesync|dns|ssl|certificate|wlan0|dhcp" |
tail -40
'`
}

func parseHTTPChecks(out string) []HTTPCheck {
	var checks []HTTPCheck
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 6)
		if len(parts) >= 5 && parts[0] == "HTTPCHECK" {
			check := HTTPCheck{
				Context:  parts[1],
				Method:   parts[2],
				OK:       parts[3] == "OK",
				Category: parts[4],
				Raw:      line,
			}
			if len(parts) == 6 {
				check.Detail = parts[5]
			}
			checks = append(checks, check)
			continue
		}
		legacyParts := strings.SplitN(line, "\t", 5)
		if len(legacyParts) >= 4 && legacyParts[0] == "HTTPCHECK" {
			check := HTTPCheck{
				Context:  "plain",
				Method:   legacyParts[1],
				OK:       legacyParts[2] == "OK",
				Category: legacyParts[3],
				Raw:      line,
			}
			if len(legacyParts) == 5 {
				check.Detail = legacyParts[4]
			}
			checks = append(checks, check)
		}
	}
	return checks
}

func classifyDiagnostics(checks []HTTPCheck) (status, screen, hint string) {
	head := findHTTPCheck(checks, "setup-env", "HEAD")
	if head == nil {
		head = findHTTPCheck(checks, "plain", "HEAD")
	}
	if head != nil && head.OK {
		context := "the device"
		if head.Context == "setup-env" {
			context = "the setup process environment"
		}
		return "PASS", "Continue or Continue without Wi-Fi", fmt.Sprintf("The current AGNOS setup internet check passed from %s. If the screen still says Waiting for internet, check the setup runtime/log lines for a UI state or setup-thread problem.", context)
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

func findHTTPCheck(checks []HTTPCheck, context, method string) *HTTPCheck {
	for i := range checks {
		if strings.EqualFold(checks[i].Context, context) && strings.EqualFold(checks[i].Method, method) {
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
	context := check.Context
	if context == "" {
		context = "plain"
	}
	if check.OK {
		return fmt.Sprintf("%s %s OK (%s %s)", context, check.Method, check.Category, check.Detail)
	}
	return fmt.Sprintf("%s %s FAIL (%s %s)", context, check.Method, check.Category, check.Detail)
}
