package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const customInstallConfirmation = "INSTALL CUSTOM SOFTWARE"

var ownerRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type customInstallPlan struct {
	DownloadURL     string
	FinalURL        string
	InstallerBytes  string
	InstallerSHA256 string
	GitURL          string
	Branch          string
	DeviceType      string
	MigratedBranch  string
	RemoteHead      string
	OpenpilotExists bool
	Raw             string
}

func runCustomSoftwareInstall(ctx context.Context, cfg Config, report *RunReport, reader *bufio.Reader) error {
	target, err := chooseSingleTarget(report.DeviceReports, reader, cfg.CustomSoftwareURL == "", "install custom software")
	if err != nil {
		return err
	}

	customURL := strings.TrimSpace(cfg.CustomSoftwareURL)
	if customURL == "" {
		customURL, err = promptCustomSoftwareURL(reader)
		if err != nil {
			return err
		}
	} else {
		customURL, err = normalizeCustomSoftwareURL(customURL)
		if err != nil {
			return err
		}
	}

	fmt.Fprintln(output, "Custom software URL install replacement")
	fmt.Fprintln(output, "---------------------------------------")
	fmt.Fprintln(output, "WARNING: Only use this when directed by knowledgeable openpilot/comma users.")
	fmt.Fprintln(output, "This bypasses the setup installer UI, writes /data/openpilot, and writes /data/continue.sh.")
	fmt.Fprintln(output, "It may replace an existing /data/openpilot checkout after explicit confirmation.")
	fmt.Fprintf(output, "Target device: %s\n", target.IP)
	fmt.Fprintf(output, "Custom software URL: %s\n\n", customURL)

	ip := net.ParseIP(target.IP)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("invalid custom install target IP %q", target.IP)
	}

	client, err := connectSSH(ctx, ip.To4(), cfg.Timeout, privateKey)
	if err != nil {
		return fmt.Errorf("failed to connect to %s for custom install: %w", target.IP, err)
	}
	defer client.Close()

	planOut, err := executeCommand(client, customSoftwarePlanCommand(customURL), 45*time.Second)
	plan := parseCustomInstallPlan(planOut)
	fmt.Fprintln(output, "Custom installer preflight:")
	fmt.Fprintln(output, indentBlock(plan.Raw, "  "))
	if err != nil {
		return fmt.Errorf("custom installer preflight failed: %w", err)
	}
	if plan.GitURL == "" || plan.MigratedBranch == "" {
		return fmt.Errorf("could not extract GitHub repo and branch from custom installer")
	}
	if !strings.HasPrefix(plan.RemoteHead, "OK") {
		return fmt.Errorf("git branch check failed: %s", plan.RemoteHead)
	}
	fmt.Fprintln(output)

	if plan.OpenpilotExists {
		replace, err := promptYesNo(reader, "Existing /data/openpilot found. Move it aside and install this custom software?", false)
		if err != nil {
			return err
		}
		if !replace {
			fmt.Fprintln(output, "Custom install aborted: existing /data/openpilot was left unchanged.")
			return nil
		}
	}

	fmt.Fprintf(output, "Type %q to install %s branch %s: ", customInstallConfirmation, plan.GitURL, plan.MigratedBranch)
	confirmation, _ := reader.ReadString('\n')
	if strings.TrimSpace(confirmation) != customInstallConfirmation {
		fmt.Fprintln(output, "Custom install aborted: confirmation did not match. No changes were made.")
		return nil
	}
	fmt.Fprintln(output)

	installOut, err := executeCommand(client, customSoftwareInstallCommand(customURL, plan.GitURL, plan.MigratedBranch), 45*time.Minute)
	fmt.Fprintln(output, "Custom install command output:")
	fmt.Fprintln(output, indentBlock(installOut, "  "))
	if err != nil {
		return fmt.Errorf("custom install command failed: %w", err)
	}

	fmt.Fprintln(output, "Custom install requested. Watch the device screen; openpilot should continue from /data/openpilot after the wrapper restarts.")
	fmt.Fprintln(output, "If it still fails, share this log and a fresh setup screen screenshot.")
	return nil
}

func promptCustomSoftwareURL(reader *bufio.Reader) (string, error) {
	for {
		fmt.Fprint(output, "Enter custom software URL, domain, or owner/repo: ")
		text, _ := reader.ReadString('\n')
		normalized, err := normalizeCustomSoftwareURL(text)
		if err == nil {
			return normalized, nil
		}
		fmt.Fprintf(output, "%v. Examples: openpilot-test.comma.ai or commaai/openpilot\n", err)
	}
}

func normalizeCustomSoftwareURL(input string) (string, error) {
	text := strings.TrimSpace(input)
	if text == "" {
		return "", fmt.Errorf("custom software URL cannot be empty")
	}
	if ownerRepoPattern.MatchString(text) {
		return "https://installer.comma.ai/" + text, nil
	}

	parsed, err := url.Parse(text)
	if err != nil {
		return "", fmt.Errorf("invalid custom software URL: %w", err)
	}
	if parsed.Scheme == "" {
		text = "https://" + text
		parsed, err = url.Parse(text)
		if err != nil {
			return "", fmt.Errorf("invalid custom software URL: %w", err)
		}
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("custom software URL needs a host")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("custom software URL must use http or https")
	}
	return parsed.String(), nil
}

func parseCustomInstallPlan(out string) customInstallPlan {
	raw := strings.TrimSpace(out)
	plan := customInstallPlan{Raw: raw}
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		switch key {
		case "DOWNLOAD_URL":
			plan.DownloadURL = value
		case "FINAL_URL":
			plan.FinalURL = value
		case "INSTALLER_BYTES":
			plan.InstallerBytes = value
		case "INSTALLER_SHA256":
			plan.InstallerSHA256 = value
		case "GIT_URL":
			plan.GitURL = value
		case "BRANCH_STR":
			plan.Branch = value
		case "DEVICE_TYPE":
			plan.DeviceType = value
		case "MIGRATED_BRANCH":
			plan.MigratedBranch = value
		case "GIT_REMOTE_HEAD":
			plan.RemoteHead = value
		case "OPENPILOT_EXISTS":
			plan.OpenpilotExists = value == "yes"
		}
	}
	return plan
}

func migrateInstallerBranch(branch, deviceType string) string {
	switch deviceType {
	case "tici":
		switch branch {
		case "release3", "release-tici", "release3-staging", "nightly", "nightly-dev":
			return "release-tici"
		case "master":
			return "master-tici"
		}
	case "tizi":
		switch branch {
		case "release3":
			return "release-tizi"
		case "release3-staging":
			return "release-tizi-staging"
		}
	case "mici":
		switch branch {
		case "release3":
			return "release-mici"
		case "release3-staging":
			return "release-mici-staging"
		}
	}
	return branch
}

func customSoftwarePlanCommand(customURL string) string {
	return `set -u
CUSTOM_URL=` + shellQuote(customURL) + `
python3 - <<'PY'
import hashlib
import os
import re
import subprocess
import sys
import urllib.request
from pathlib import Path

custom_url = os.environ.get("CUSTOM_URL", "")
if not custom_url:
  custom_url = ` + pythonStringLiteral(customURL) + `

def read_text(path, default=""):
  try:
    return Path(path).read_bytes().replace(b"\0", b"").decode("utf-8", "replace").strip()
  except Exception:
    return default

model = read_text("/sys/firmware/devicetree/base/model")
model_l = model.lower()
if "tizi" in model_l:
  device_type = "tizi"
elif "tici" in model_l:
  device_type = "tici"
elif "mici" in model_l:
  device_type = "mici"
else:
  device_type = model.split()[-1].lower() if model.split() else "unknown"

version = read_text("/VERSION", "unknown")
serial = read_text("/proc/device-tree/serial-number")
headers = {
  "User-Agent": "AGNOSSetup-" + version,
  "X-openpilot-device-type": device_type,
}
if serial:
  headers["X-openpilot-serial"] = serial

print("DOWNLOAD_URL\t" + custom_url)
print("DEVICE_MODEL\t" + model)
print("DEVICE_TYPE\t" + device_type)
print("AGNOS_VERSION\t" + version)
print("EXISTING_OPENPILOT\t" + ("yes" if Path("/data/openpilot").exists() else "no"))
print("OPENPILOT_EXISTS\t" + ("yes" if Path("/data/openpilot").exists() else "no"))
if Path("/data/openpilot").exists():
  try:
    desc = subprocess.check_output("cd /data/openpilot && git rev-parse --abbrev-ref HEAD && git rev-parse --short HEAD", shell=True, stderr=subprocess.STDOUT, timeout=4, text=True).strip().replace("\n", " ")
    print("EXISTING_OPENPILOT_GIT\t" + desc)
  except Exception as e:
    print("EXISTING_OPENPILOT_GIT\tERROR:%r" % (e,))
print("CONTINUE_SH\t" + ("yes" if Path("/data/continue.sh").exists() else "no"))
try:
  print("DATA_DISK\t" + subprocess.check_output("df -h /data | tail -1", shell=True, stderr=subprocess.STDOUT, timeout=4, text=True).strip())
except Exception as e:
  print("DATA_DISK\tERROR:%r" % (e,))

req = urllib.request.Request(custom_url, headers=headers)
try:
  with urllib.request.urlopen(req, timeout=20) as resp:
    data = resp.read(30 * 1024 * 1024)
    print("FINAL_URL\t" + resp.geturl())
except Exception as e:
  print("ERROR\tdownload failed: %r" % (e,))
  sys.exit(1)

print("INSTALLER_BYTES\t%d" % len(data))
print("INSTALLER_SHA256\t" + hashlib.sha256(data).hexdigest())
if data[:4] != b"\x7fELF":
  print("ERROR\tdownloader response is not an ELF installer")
  sys.exit(1)

git_match = re.search(rb"https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\.git", data)
if not git_match:
  print("ERROR\tcould not find GitHub clone URL in installer")
  sys.exit(1)
git_url = git_match.group(0).decode("ascii")

branch = ""
for match in re.finditer(rb"([A-Za-z0-9._/-]{2,80})\?[\x00 ]{1,128}", data):
  if match.start() <= git_match.end():
    continue
  candidate = match.group(1).decode("ascii", "ignore")
  if candidate.startswith("http") or candidate.startswith("/"):
    continue
  branch = candidate
  break
if not branch:
  print("ERROR\tcould not find branch string in installer")
  sys.exit(1)

def migrate(branch, device_type):
  if device_type == "tici":
    if branch in ("release3", "release-tici", "release3-staging", "nightly", "nightly-dev"):
      return "release-tici"
    if branch == "master":
      return "master-tici"
  if device_type == "tizi":
    if branch == "release3":
      return "release-tizi"
    if branch == "release3-staging":
      return "release-tizi-staging"
  if device_type == "mici":
    if branch == "release3":
      return "release-mici"
    if branch == "release3-staging":
      return "release-mici-staging"
  return branch

migrated = migrate(branch, device_type)
if not re.match(r"^[A-Za-z0-9._/-]+$", migrated):
  print("ERROR\tunsafe migrated branch: %r" % (migrated,))
  sys.exit(1)
print("GIT_URL\t" + git_url)
print("BRANCH_STR\t" + branch)
print("MIGRATED_BRANCH\t" + migrated)
try:
  subprocess.check_output(["git", "ls-remote", "--exit-code", "--heads", git_url, migrated], stderr=subprocess.STDOUT, timeout=20, text=True)
  print("GIT_REMOTE_HEAD\tOK " + git_url + " " + migrated)
except Exception as e:
  print("GIT_REMOTE_HEAD\tFAIL %r" % (e,))
  sys.exit(1)
PY`
}

func customSoftwareInstallCommand(customURL, gitURL, branch string) string {
	return `set -eu
CUSTOM_URL=` + shellQuote(customURL) + `
GIT_URL=` + shellQuote(gitURL) + `
MIGRATED_BRANCH=` + shellQuote(branch) + `
echo "Step: verify remote branch still exists"
git ls-remote --exit-code --heads "$GIT_URL" "$MIGRATED_BRANCH" >/dev/null || { echo "ERROR: remote branch check failed"; exit 1; }
echo "Step: stop setup/openpilot wrapper"
tmux kill-session -t comma >/dev/null 2>&1 || true
python3 - <<'PY' || true
import os
import signal

needles = ["/usr/comma/" + "setup", "/usr/comma/" + "comma.sh", "/tmp/" + "installer"]
own = {os.getpid(), os.getppid()}
for pid in os.listdir("/proc"):
  if not pid.isdigit() or int(pid) in own:
    continue
  try:
    raw = open(f"/proc/{pid}/cmdline", "rb").read().replace(b"\0", b" ").decode("utf-8", "replace")
  except Exception:
    continue
  if any(needle in raw for needle in needles):
    try:
      os.kill(int(pid), signal.SIGTERM)
      print(f"killed pid {pid}: {raw[:160]}")
    except Exception as e:
      print(f"could not kill pid {pid}: {e!r}")
PY
sleep 1
echo "Step: prepare /data/tmppilot"
rm -rf /data/tmppilot
mkdir -p /data
echo "Step: clone $GIT_URL branch $MIGRATED_BRANCH"
git clone --progress "$GIT_URL" -b "$MIGRATED_BRANCH" --depth=1 --recurse-submodules /data/tmppilot 2>&1
cd /data/tmppilot
git checkout "$MIGRATED_BRANCH"
git reset --hard "origin/$MIGRATED_BRANCH"
git submodule update --init --recursive
echo "Step: move checkout into /data/openpilot"
STAMP="$(date -u +%Y%m%d-%H%M%S)"
if [ -e /data/openpilot ]; then
  BACKUP="/data/openpilot.backup-$STAMP"
  mv /data/openpilot "$BACKUP"
  echo "Existing /data/openpilot moved to $BACKUP"
fi
mv /data/tmppilot /data/openpilot
echo "Step: write /data/continue.sh"
cat > /data/continue.sh <<'EOF'
#!/usr/bin/env bash
cd /data/openpilot
exec ./launch_openpilot.sh
EOF
chmod +x /data/continue.sh
printf '%s\n' "$CUSTOM_URL" > /data/custom_software_url
sync
echo "Step: verify install"
test -d /data/openpilot/.git || { echo "ERROR: /data/openpilot/.git missing"; exit 1; }
test -x /data/continue.sh || { echo "ERROR: /data/continue.sh missing or not executable"; exit 1; }
cd /data/openpilot
echo "Installed branch: $(git rev-parse --abbrev-ref HEAD)"
echo "Installed commit: $(git rev-parse --short HEAD)"
echo "Step: restart wrapper"
tmux new-session -s comma -d /usr/comma/comma.sh || echo "WARNING: could not start tmux wrapper; reboot or launch /usr/comma/comma.sh manually"
echo "OK: custom software install replacement complete"`
}

func pythonStringLiteral(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`).Replace(s) + `"`
}
