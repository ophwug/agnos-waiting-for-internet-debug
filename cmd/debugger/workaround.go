package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const modemWorkaroundConfirmation = "APPLY WORKAROUND"

type modemStatePreflight struct {
	Exists bool
	Raw    string
}

func runModemStateWorkaround(ctx context.Context, cfg Config, report *RunReport, reader *bufio.Reader) error {
	target, err := chooseSingleTarget(report.DeviceReports, reader, !cfg.WorkaroundModem, "apply workaround")
	if err != nil {
		return err
	}

	fmt.Fprintln(output, "Temporary Waiting for Internet workaround")
	fmt.Fprintln(output, "---------------------------------------")
	fmt.Fprintln(output, "WARNING: Only use this when directed by knowledgeable openpilot/comma users.")
	fmt.Fprintln(output, "This writes a temporary empty modem state file to /dev/shm/modem.")
	fmt.Fprintln(output, "It is intended for the setup bug where internet checks pass but setup remains stuck because modem state is missing.")
	fmt.Fprintln(output, "The file is on tmpfs and disappears after reboot.")
	fmt.Fprintf(output, "Target device: %s\n\n", target.IP)

	ip := net.ParseIP(target.IP)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("invalid workaround target IP %q", target.IP)
	}

	client, err := connectSSH(ctx, ip.To4(), cfg.Timeout, privateKey)
	if err != nil {
		return fmt.Errorf("failed to connect to %s for workaround: %w", target.IP, err)
	}
	defer client.Close()

	preflight, err := readRemoteModemState(client)
	if err != nil {
		return err
	}
	fmt.Fprintln(output, "Preflight /dev/shm/modem:")
	fmt.Fprintln(output, indentBlock(preflight.Raw, "  "))
	if preflight.Exists {
		replace, err := promptYesNo(reader, "A /dev/shm/modem file already exists. Replace it with an empty JSON object?", false)
		if err != nil {
			return err
		}
		if !replace {
			fmt.Fprintln(output, "Workaround aborted: existing /dev/shm/modem was left unchanged.")
			return nil
		}
	}
	fmt.Fprintln(output)

	fmt.Fprintf(output, "Type %q to write /dev/shm/modem: ", modemWorkaroundConfirmation)
	confirmation, _ := reader.ReadString('\n')
	if strings.TrimSpace(confirmation) != modemWorkaroundConfirmation {
		fmt.Fprintln(output, "Workaround aborted: confirmation did not match. No changes were made.")
		return nil
	}
	fmt.Fprintln(output)

	workaroundOut, err := executeCommand(client, modemStateWorkaroundCommand(), 8*time.Second)
	fmt.Fprintln(output, "Workaround command output:")
	fmt.Fprintln(output, indentBlock(workaroundOut, "  "))
	if err != nil {
		return fmt.Errorf("workaround command failed: %w", err)
	}

	verify, err := readRemoteModemState(client)
	if err != nil {
		return err
	}
	fmt.Fprintln(output, "Post-write verification:")
	fmt.Fprintln(output, indentBlock(verify.Raw, "  "))
	if !verify.Exists {
		return fmt.Errorf("verification failed: /dev/shm/modem is still missing")
	}

	fmt.Fprintln(output, "Workaround applied. Watch the setup screen now; the Continue button may appear after the next setup internet check.")
	fmt.Fprintln(output, "If it still says Waiting for internet, share this log and a fresh setup screen screenshot.")
	return nil
}

func readRemoteModemState(client *ssh.Client) (modemStatePreflight, error) {
	out, err := executeCommand(client, modemStatePreflightCommand(), 4*time.Second)
	preflight := parseModemStatePreflight(out)
	if err != nil {
		return preflight, fmt.Errorf("failed to read current /dev/shm/modem state: %w", err)
	}
	return preflight, nil
}

func parseModemStatePreflight(out string) modemStatePreflight {
	raw := strings.TrimSpace(out)
	preflight := modemStatePreflight{Raw: raw}
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "EXISTS" {
			preflight.Exists = true
			return preflight
		}
	}
	return preflight
}

func modemStatePreflightCommand() string {
	return `if [ -e /dev/shm/modem ]; then
  echo EXISTS
  ls -lha /dev/shm/modem 2>/dev/null || true
  python3 - <<'PY'
import json
from pathlib import Path

path = Path("/dev/shm/modem")
data = path.read_bytes()
print(f"size_bytes={len(data)}")
try:
  obj = json.loads(data.decode("utf-8"))
  print("json_parse=OK")
  if isinstance(obj, dict):
    print("json_type=object")
    print("json_keys=" + ",".join(sorted(str(k) for k in obj.keys())[:20]))
    if "connected" in obj:
      print(f"connected={obj.get('connected')!r}")
  else:
    print("json_type=" + type(obj).__name__)
except Exception as e:
  print(f"json_parse=FAIL {e!r}")
PY
else
  echo MISSING
fi`
}

func modemStateWorkaroundCommand() string {
	return `set -u
echo "Step: write temporary empty modem state to /dev/shm/modem"
printf '{}\n' | sudo tee /dev/shm/modem >/dev/null || { echo "ERROR: write failed"; exit 1; }
sudo chmod 0644 /dev/shm/modem 2>/dev/null || true
echo "Step: verify /dev/shm/modem parses as JSON object"
VERIFY="$(python3 - <<'PY'
import json
try:
  with open("/dev/shm/modem") as f:
    obj = json.load(f)
  print("OK" if isinstance(obj, dict) else "NOT_OBJECT")
except Exception as e:
  print("ERROR:%r" % (e,))
PY
)"
if [ "$VERIFY" != "OK" ]; then
  echo "ERROR: verify failed; got '$VERIFY'"
  exit 1
fi
ls -lha /dev/shm/modem 2>/dev/null || true
echo "OK: /dev/shm/modem contains an empty JSON object"`
}
