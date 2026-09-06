//go:build linux && amd64

package monitor

import (
	"os"
	"strings"
	"testing"
)

func TestCollector(t *testing.T) {
	c := NewCollector()
	info, err := c.GetBasicInfo()
	if err != nil {
		t.Fatalf("GetBasicInfo failed: %v", err)
	}
	t.Logf("BasicInfo: %+v", info)

	report, err := c.GetReport()
	if err != nil {
		t.Fatalf("GetReport failed: %v", err)
	}
	t.Logf("Report: %+v", report)
}

func TestIsAllDigits(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1", true},
		{"12345", true},
		{"0", true},
		{"", false},
		{"12a", false},
		{"a12", false},
		{"net", false},
		{"-1", false},
		{" ", false},
	}
	for _, tc := range tests {
		if got := isAllDigits(tc.input); got != tc.want {
			t.Errorf("isAllDigits(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestCountLinesInFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Non-existent file
	if got := countLinesInFile(tmpDir + "/nonexistent"); got != 0 {
		t.Errorf("expected 0 for nonexistent file, got %d", got)
	}

	// Empty file
	emptyFile := tmpDir + "/empty"
	if err := os.WriteFile(emptyFile, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	if got := countLinesInFile(emptyFile); got != 0 {
		t.Errorf("expected 0 for empty file, got %d", got)
	}

	// Header only with newline
	headerFile := tmpDir + "/header_only"
	if err := os.WriteFile(headerFile, []byte("sl  local_address rem_address   st\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := countLinesInFile(headerFile); got != 0 {
		t.Errorf("expected 0 for header only file, got %d", got)
	}

	// Header + 3 connections
	connsFile := tmpDir + "/conns"
	content := "header\nline1\nline2\nline3\n"
	if err := os.WriteFile(connsFile, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if got := countLinesInFile(connsFile); got != 3 {
		t.Errorf("expected 3 connections, got %d", got)
	}

	// Large file (10000 lines) to verify buffer handling across multiple chunks
	largeFile := tmpDir + "/large"
	var buf strings.Builder
	buf.WriteString("header\n")
	for i := 0; i < 10000; i++ {
		buf.WriteString("0123456789: 0100007F:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000\n")
	}
	if err := os.WriteFile(largeFile, []byte(buf.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if got := countLinesInFile(largeFile); got != 10000 {
		t.Errorf("expected 10000 connections, got %d", got)
	}
}
