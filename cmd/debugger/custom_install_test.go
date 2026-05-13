package main

import (
	"strings"
	"testing"
)

func TestNormalizeCustomSoftwareURL(t *testing.T) {
	tests := map[string]string{
		"commaai/openpilot":               "https://installer.comma.ai/commaai/openpilot",
		"openpilot-test.comma.ai":         "https://openpilot-test.comma.ai",
		"https://openpilot-test.comma.ai": "https://openpilot-test.comma.ai",
		"http://example.test/path":        "http://example.test/path",
	}
	for input, want := range tests {
		got, err := normalizeCustomSoftwareURL(input)
		if err != nil {
			t.Fatalf("normalizeCustomSoftwareURL(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("normalizeCustomSoftwareURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeCustomSoftwareURLRejectsBadInput(t *testing.T) {
	for _, input := range []string{"", "   ", "ftp://example.test/path"} {
		if _, err := normalizeCustomSoftwareURL(input); err == nil {
			t.Fatalf("normalizeCustomSoftwareURL(%q) succeeded, want error", input)
		}
	}
}

func TestMigrateInstallerBranch(t *testing.T) {
	tests := []struct {
		branch string
		device string
		want   string
	}{
		{"release3", "tizi", "release-tizi"},
		{"release3-staging", "tizi", "release-tizi-staging"},
		{"release3", "mici", "release-mici"},
		{"release3-staging", "mici", "release-mici-staging"},
		{"release3-staging", "tici", "release-tici"},
		{"master", "tici", "master-tici"},
		{"custom", "tizi", "custom"},
	}
	for _, tt := range tests {
		if got := migrateInstallerBranch(tt.branch, tt.device); got != tt.want {
			t.Fatalf("migrateInstallerBranch(%q, %q) = %q, want %q", tt.branch, tt.device, got, tt.want)
		}
	}
}

func TestParseCustomInstallPlan(t *testing.T) {
	plan := parseCustomInstallPlan(strings.Join([]string{
		"DOWNLOAD_URL\thttps://openpilot-test.comma.ai",
		"FINAL_URL\thttps://commadist.blob.core.windows.net/installer",
		"INSTALLER_BYTES\t1234",
		"INSTALLER_SHA256\tabc",
		"GIT_URL\thttps://github.com/commaai/openpilot.git",
		"BRANCH_STR\trelease3-staging",
		"DEVICE_TYPE\ttizi",
		"MIGRATED_BRANCH\trelease-tizi-staging",
		"GIT_REMOTE_HEAD\tOK https://github.com/commaai/openpilot.git release-tizi-staging",
		"OPENPILOT_EXISTS\tyes",
	}, "\n"))
	if plan.GitURL != "https://github.com/commaai/openpilot.git" || plan.MigratedBranch != "release-tizi-staging" || !plan.OpenpilotExists {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestCustomSoftwarePlanCommandHasRequiredChecks(t *testing.T) {
	cmd := customSoftwarePlanCommand("https://openpilot-test.comma.ai")
	for _, want := range []string{
		"User-Agent",
		"AGNOSSetup-",
		"X-openpilot-device-type",
		"X-openpilot-serial",
		"INSTALLER_SHA256",
		"GIT_URL",
		"BRANCH_STR",
		"MIGRATED_BRANCH",
		"git\", \"ls-remote\"",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("plan command missing %q:\n%s", want, cmd)
		}
	}
}

func TestCustomSoftwareInstallCommandHasRequiredSteps(t *testing.T) {
	cmd := customSoftwareInstallCommand("https://openpilot-test.comma.ai", "https://github.com/commaai/openpilot.git", "release-tizi-staging")
	for _, want := range []string{
		"git clone --progress \"$GIT_URL\" -b \"$MIGRATED_BRANCH\" --depth=1 --recurse-submodules /data/tmppilot",
		"git submodule update --init --recursive",
		"mv /data/openpilot \"$BACKUP\"",
		"mv /data/tmppilot /data/openpilot",
		"cat > /data/continue.sh",
		"exec ./launch_openpilot.sh",
		"tmux new-session -s comma -d /usr/comma/comma.sh",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("install command missing %q:\n%s", want, cmd)
		}
	}
}
