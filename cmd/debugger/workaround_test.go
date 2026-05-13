package main

import (
	"strings"
	"testing"
)

func TestParseModemStatePreflight(t *testing.T) {
	existing := parseModemStatePreflight("EXISTS\n-rw-r--r-- 1 root root 3 May 13 12:00 /dev/shm/modem\njson_parse=OK\n")
	if !existing.Exists {
		t.Fatalf("existing = %#v", existing)
	}

	missing := parseModemStatePreflight("MISSING\n")
	if missing.Exists {
		t.Fatalf("missing = %#v", missing)
	}
}

func TestModemStateWorkaroundCommandHasRequiredSteps(t *testing.T) {
	cmd := modemStateWorkaroundCommand()
	for _, want := range []string{
		"sudo tee /dev/shm/modem",
		"sudo chmod 0644 /dev/shm/modem",
		"json.load",
		"OK: /dev/shm/modem contains an empty JSON object",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("workaround command missing %q:\n%s", want, cmd)
		}
	}
}

func TestModemStatePreflightCommandDoesNotDumpRawContent(t *testing.T) {
	cmd := modemStatePreflightCommand()
	if strings.Contains(cmd, "cat /dev/shm/modem") {
		t.Fatalf("preflight command should not dump raw modem state:\n%s", cmd)
	}
	for _, want := range []string{"ls -lha /dev/shm/modem", "json_parse=OK", "json_keys="} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("preflight command missing %q:\n%s", want, cmd)
		}
	}
}
