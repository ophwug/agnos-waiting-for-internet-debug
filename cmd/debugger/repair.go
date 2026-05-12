package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const repairConfirmation = "WRITE DONGLE ID"

type dongleRepairPreflight struct {
	Exists bool
	Value  string
	Raw    string
}

func runDongleIDRepair(ctx context.Context, cfg Config, report *RunReport, reader *bufio.Reader) error {
	target, err := chooseRepairTarget(report.DeviceReports, reader, cfg.RepairDongleID == "")
	if err != nil {
		return err
	}

	dongleID := strings.TrimSpace(cfg.RepairDongleID)
	if dongleID == "" {
		dongleID, err = promptDongleID(reader)
		if err != nil {
			return err
		}
	} else {
		dongleID, err = normalizeDongleID(dongleID)
		if err != nil {
			return err
		}
	}

	fmt.Fprintln(output, "Dongle ID repair")
	fmt.Fprintln(output, "----------------")
	fmt.Fprintln(output, "WARNING: Only use this repair when directed by knowledgeable openpilot/comma users.")
	fmt.Fprintln(output, "This writes persistent device identity state to /persist/comma/dongle_id.")
	fmt.Fprintf(output, "Target device: %s\n", target.IP)
	fmt.Fprintf(output, "Requested dongle_id: %s\n\n", dongleID)

	ip := net.ParseIP(target.IP)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("invalid repair target IP %q", target.IP)
	}

	client, err := connectSSH(ctx, ip.To4(), cfg.Timeout, privateKey)
	if err != nil {
		return fmt.Errorf("failed to connect to %s for repair: %w", target.IP, err)
	}
	defer client.Close()

	preflight, err := readRemoteDongleID(client)
	if err != nil {
		return err
	}
	fmt.Fprintln(output, "Preflight /persist/comma/dongle_id:")
	fmt.Fprintln(output, indentBlock(preflight.Raw, "  "))
	if preflight.Exists {
		fmt.Fprintf(output, "Existing dongle_id: %s\n", preflight.Value)
		if !cfg.OverwriteDongleID {
			overwrite, err := promptYesNo(reader, "Existing dongle_id found. Overwrite it?", false)
			if err != nil {
				return err
			}
			if !overwrite {
				fmt.Fprintln(output, "Repair aborted: existing dongle_id was left unchanged.")
				return nil
			}
		}
		fmt.Fprintf(output, "Overwrite approved. Old dongle_id: %s\n", preflight.Value)
		fmt.Fprintf(output, "New dongle_id: %s\n", dongleID)
	}
	fmt.Fprintln(output)

	fmt.Fprintf(output, "Type %q to write /persist/comma/dongle_id: ", repairConfirmation)
	confirmation, _ := reader.ReadString('\n')
	if strings.TrimSpace(confirmation) != repairConfirmation {
		fmt.Fprintln(output, "Repair aborted: confirmation did not match. No changes were made.")
		return nil
	}
	fmt.Fprintln(output)

	repairOut, err := executeCommand(client, dongleIDRepairCommand(dongleID), 12*time.Second)
	fmt.Fprintln(output, "Repair command output:")
	fmt.Fprintln(output, indentBlock(repairOut, "  "))
	if err != nil {
		return fmt.Errorf("repair command failed: %w", err)
	}

	verify, err := readRemoteDongleID(client)
	if err != nil {
		return err
	}
	fmt.Fprintln(output, "Post-write verification:")
	fmt.Fprintln(output, indentBlock(verify.Raw, "  "))
	if !verify.Exists || verify.Value != dongleID {
		return fmt.Errorf("verification failed: dongle_id is %q, expected %q", verify.Value, dongleID)
	}
	fmt.Fprintf(output, "Repair verified. /persist/comma/dongle_id is now %s\n", verify.Value)

	if cfg.NoReboot {
		fmt.Fprintln(output, "Reboot skipped by --no-reboot. Reboot the device before expecting setup/registration state to refresh.")
		return nil
	}

	reboot, err := promptYesNo(reader, "Reboot the device now?", true)
	if err != nil {
		return err
	}
	if !reboot {
		fmt.Fprintln(output, "Reboot skipped. Reboot the device before expecting setup/registration state to refresh.")
		return nil
	}

	fmt.Fprintln(output, "Requesting device reboot...")
	rebootOut, rebootErr := executeCommand(client, "sudo reboot", 3*time.Second)
	if strings.TrimSpace(rebootOut) != "" {
		fmt.Fprintln(output, indentBlock(rebootOut, "  "))
	}
	if rebootErr != nil {
		fmt.Fprintf(output, "Reboot command returned after connection closed or failed: %v\n", rebootErr)
	} else {
		fmt.Fprintln(output, "Reboot requested.")
	}
	return nil
}

func chooseRepairTarget(reports []DeviceReport, reader *bufio.Reader, allowPrompt bool) (DeviceReport, error) {
	reachable := make([]DeviceReport, 0, len(reports))
	for _, report := range reports {
		if report.SSHReachable {
			reachable = append(reachable, report)
		}
	}
	if len(reachable) == 0 {
		return DeviceReport{}, fmt.Errorf("no SSH-reachable device found for repair")
	}
	if len(reachable) == 1 {
		return reachable[0], nil
	}
	if !allowPrompt {
		return DeviceReport{}, fmt.Errorf("multiple SSH-reachable devices found; pass --ip <device-ip> for repair")
	}

	for {
		fmt.Fprintln(output, "Choose the device to repair:")
		for i, report := range reachable {
			fmt.Fprintf(output, "  %d. %s\n", i+1, report.IP)
		}
		fmt.Fprintf(output, "Select 1-%d: ", len(reachable))
		choice, _ := reader.ReadString('\n')
		idx, err := strconv.Atoi(strings.TrimSpace(choice))
		if err == nil && idx >= 1 && idx <= len(reachable) {
			fmt.Fprintf(output, "Repair target selected: %s\n\n", reachable[idx-1].IP)
			return reachable[idx-1], nil
		}
		fmt.Fprintln(output, "Please choose one of the listed devices.")
		fmt.Fprintln(output)
	}
}

func promptDongleID(reader *bufio.Reader) (string, error) {
	for {
		fmt.Fprint(output, "Enter 16-character dongle_id: ")
		text, _ := reader.ReadString('\n')
		dongleID, err := normalizeDongleID(text)
		if err == nil {
			return dongleID, nil
		}
		fmt.Fprintf(output, "%v. Example: 9d09cc205c254c4b\n", err)
	}
}

func normalizeDongleID(text string) (string, error) {
	dongleID := strings.ToLower(strings.TrimSpace(text))
	if len(dongleID) != 16 {
		return "", fmt.Errorf("dongle_id must be exactly 16 hex characters")
	}
	for _, ch := range dongleID {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return "", fmt.Errorf("dongle_id must contain only hex characters")
		}
	}
	return dongleID, nil
}

func promptYesNo(reader *bufio.Reader, question string, defaultYes bool) (bool, error) {
	suffix := " [Y/n]: "
	if !defaultYes {
		suffix = " [y/N]: "
	}
	for {
		fmt.Fprint(output, question+suffix)
		answer, _ := reader.ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer == "" {
			return defaultYes, nil
		}
		switch answer {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintln(output, "Please answer yes or no.")
		}
	}
}

func readRemoteDongleID(client *ssh.Client) (dongleRepairPreflight, error) {
	out, err := executeCommand(client, dongleIDPreflightCommand(), 4*time.Second)
	preflight := parseDongleIDPreflight(out)
	if err != nil {
		return preflight, fmt.Errorf("failed to read current dongle_id: %w", err)
	}
	return preflight, nil
}

func parseDongleIDPreflight(out string) dongleRepairPreflight {
	raw := strings.TrimSpace(out)
	preflight := dongleRepairPreflight{Raw: raw}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "EXISTS\t") {
			preflight.Exists = true
			preflight.Value = strings.TrimSpace(strings.TrimPrefix(line, "EXISTS\t"))
			return preflight
		}
		if line == "MISSING" {
			return preflight
		}
	}
	return preflight
}

func dongleIDPreflightCommand() string {
	return `if [ -f /persist/comma/dongle_id ]; then printf 'EXISTS\t'; cat /persist/comma/dongle_id; else echo MISSING; fi`
}

func dongleIDRepairCommand(dongleID string) string {
	quoted := shellQuote(dongleID)
	return `set -u
DONGLE_ID=` + quoted + `
echo "Step: remount /persist read-write"
sudo mount -o remount,rw /persist || { echo "ERROR: remount rw failed"; exit 1; }
trap 'sudo mount -o remount,ro /persist >/dev/null 2>&1 || true' EXIT
echo "Step: ensure /persist/comma exists"
sudo mkdir -p /persist/comma || { echo "ERROR: mkdir failed"; exit 1; }
sudo chown comma:comma /persist/comma 2>/dev/null || true
echo "Step: write /persist/comma/dongle_id"
printf '%s\n' "$DONGLE_ID" | sudo tee /persist/comma/dongle_id >/dev/null || { echo "ERROR: write failed"; exit 1; }
sudo chown comma:comma /persist/comma/dongle_id 2>/dev/null || true
echo "Step: sync"
sync || { echo "ERROR: sync failed"; exit 1; }
echo "Step: remount /persist read-only"
sudo mount -o remount,ro /persist || { echo "ERROR: remount ro failed"; exit 1; }
trap - EXIT
echo "Step: verify"
VERIFY="$(cat /persist/comma/dongle_id 2>/dev/null || true)"
if [ "$VERIFY" != "$DONGLE_ID" ]; then
  echo "ERROR: verify failed; got '$VERIFY'"
  exit 1
fi
echo "OK: dongle_id verified as $VERIFY"`
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
