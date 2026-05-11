package main

import "testing"

func TestParseHTTPChecks(t *testing.T) {
	out := "noise\nHTTPCHECK\tplain\tHEAD\tFAIL\tDNS\tgaierror\nHTTPCHECK\tsetup-env\tGET\tOK\t200\thttps://openpilot.comma.ai\n"
	checks := parseHTTPChecks(out)
	if len(checks) != 2 {
		t.Fatalf("len(checks) = %d", len(checks))
	}
	if checks[0].Context != "plain" || checks[0].Method != "HEAD" || checks[0].OK || checks[0].Category != "DNS" || checks[0].Detail != "gaierror" {
		t.Fatalf("unexpected HEAD check: %#v", checks[0])
	}
	if checks[1].Context != "setup-env" || checks[1].Method != "GET" || !checks[1].OK || checks[1].Category != "200" {
		t.Fatalf("unexpected GET check: %#v", checks[1])
	}
}

func TestClassifyDiagnosticsPass(t *testing.T) {
	status, screen, hint := classifyDiagnostics([]HTTPCheck{{Context: "setup-env", Method: "HEAD", OK: true, Category: "200"}})
	if status != "PASS" {
		t.Fatalf("status = %q", status)
	}
	if screen != "Continue or Continue without Wi-Fi" {
		t.Fatalf("screen = %q", screen)
	}
	if hint == "" {
		t.Fatal("expected hint")
	}
}

func TestClassifyDiagnosticsDNSFailure(t *testing.T) {
	status, screen, hint := classifyDiagnostics([]HTTPCheck{{Context: "setup-env", Method: "HEAD", OK: false, Category: "DNS"}})
	if status != "FAIL" {
		t.Fatalf("status = %q", status)
	}
	if screen != "Waiting for internet" {
		t.Fatalf("screen = %q", screen)
	}
	if hint == "" {
		t.Fatal("expected hint")
	}
}

func TestClassifyDiagnosticsUnknownWithoutHead(t *testing.T) {
	status, screen, _ := classifyDiagnostics([]HTTPCheck{{Context: "setup-env", Method: "GET", OK: true, Category: "200"}})
	if status != "UNKNOWN" {
		t.Fatalf("status = %q", status)
	}
	if screen != "Waiting for internet" {
		t.Fatalf("screen = %q", screen)
	}
}
