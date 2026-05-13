package main

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestNormalizeDongleID(t *testing.T) {
	got, err := normalizeDongleID(" 9D09CC205C254C4B\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != "9d09cc205c254c4b" {
		t.Fatalf("dongle_id = %q", got)
	}

	for _, bad := range []string{"9d09", "9d09cc205c254c4x", "9d09cc205c254c4b00"} {
		if _, err := normalizeDongleID(bad); err == nil {
			t.Fatalf("normalizeDongleID(%q) succeeded, want error", bad)
		}
	}
}

func TestChooseRepairTargetMultipleRequiresExplicitTargetWithoutPrompt(t *testing.T) {
	_, err := chooseRepairTarget([]DeviceReport{
		{IP: "192.168.1.5", SSHReachable: true},
		{IP: "192.168.1.6", SSHReachable: true},
	}, bufio.NewReader(strings.NewReader("")), false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--ip") {
		t.Fatalf("error = %q, want --ip guidance", err)
	}
}

func TestChooseRepairTargetPromptsForOneOfMultipleDevices(t *testing.T) {
	oldOutput := output
	output = io.Discard
	defer func() { output = oldOutput }()

	got, err := chooseRepairTarget([]DeviceReport{
		{IP: "192.168.1.5", SSHReachable: true},
		{IP: "192.168.1.6", SSHReachable: true},
	}, bufio.NewReader(strings.NewReader("2\n")), true)
	if err != nil {
		t.Fatal(err)
	}
	if got.IP != "192.168.1.6" {
		t.Fatalf("target = %s", got.IP)
	}
}

func TestParseDongleIDPreflight(t *testing.T) {
	existing := parseDongleIDPreflight("EXISTS\t9d09cc205c254c4b\n")
	if !existing.Exists || existing.Value != "9d09cc205c254c4b" {
		t.Fatalf("existing = %#v", existing)
	}

	missing := parseDongleIDPreflight("MISSING\n")
	if missing.Exists || missing.Value != "" {
		t.Fatalf("missing = %#v", missing)
	}
}

func TestDongleIDRepairCommandHasRequiredSteps(t *testing.T) {
	cmd := dongleIDRepairCommand("9d09cc205c254c4b")
	for _, want := range []string{
		"sudo mount -o remount,rw /persist",
		"sudo mkdir -p /persist/comma",
		"sudo tee /persist/comma/dongle_id",
		"sync",
		"sudo mount -o remount,ro /persist",
		"VERIFY=\"$(cat /persist/comma/dongle_id",
		"OK: dongle_id verified",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("repair command missing %q:\n%s", want, cmd)
		}
	}
}

func TestPromptActionMenu(t *testing.T) {
	oldOutput := output
	output = io.Discard
	defer func() { output = oldOutput }()

	if got := promptActionMenu(bufio.NewReader(bytes.NewBufferString("\n"))); got != "diagnosis" {
		t.Fatalf("default action = %q", got)
	}
	if got := promptActionMenu(bufio.NewReader(bytes.NewBufferString("2\n"))); got != "repair" {
		t.Fatalf("repair action = %q", got)
	}
	if got := promptActionMenu(bufio.NewReader(bytes.NewBufferString("3\n"))); got != "modem-workaround" {
		t.Fatalf("workaround action = %q", got)
	}
	if got := promptActionMenu(bufio.NewReader(bytes.NewBufferString("4\n"))); got != "custom-install" {
		t.Fatalf("custom install action = %q", got)
	}
}
