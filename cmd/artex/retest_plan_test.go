package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/retestplan"
)

func retestFixture(t *testing.T) []byte {
	t.Helper()
	input, err := os.ReadFile("../../retestplan/testdata/policy-change.json")
	if err != nil {
		t.Fatal(err)
	}
	return input
}

// Exercise the actual startup route in a subprocess, including explicit empty
// flag values. Invalid server addresses and config make accidental startup fail.
func TestRetestPlanOfflineCommand(t *testing.T) {
	if os.Getenv("ARTEX_RETEST_TEST_HELPER") == "1" {
		flag.CommandLine = flag.NewFlagSet("artex", flag.ExitOnError)
		for i, arg := range os.Args {
			if arg == "--" {
				os.Args = append([]string{"artex"}, os.Args[i+1:]...)
				os.Exit(run())
			}
		}
		os.Exit(99)
	}
	input := retestFixture(t)
	temp := t.TempDir()
	inputFile := filepath.Join(temp, "input.json")
	if err := os.WriteFile(inputFile, input, 0600); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(temp, "invalid-config.json")
	if err := os.WriteFile(configFile, []byte("not a config"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARTEX_RETEST_TEST_HELPER", "1")
	t.Setenv("ARTEX_CONFIG", configFile)
	t.Setenv("ARTEX_PG_DSN", "")
	for _, tc := range []struct {
		name, filename, stdin string
		extra                 []string
		wantCode              int
	}{
		{name: "file", filename: inputFile},
		{name: "stdin", filename: "-", stdin: string(input)},
		{name: "empty-filename", filename: "", wantCode: 2},
		{name: "missing-file", filename: filepath.Join(temp, "missing"), wantCode: 2},
		{name: "bad-json", filename: "-", stdin: "{", wantCode: 2},
		{name: "invalid-plan", filename: "-", stdin: "{}", wantCode: 2},
		{name: "extra-argument", filename: inputFile, extra: []string{"unexpected"}, wantCode: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := filepath.Join(temp, tc.name+"-data")
			args := []string{"-test.run=^TestRetestPlanOfflineCommand$", "--", "-data", dataDir, "-addr", "invalid address", "-proxy", "invalid address", "-retest-plan", tc.filename}
			args = append(args, tc.extra...)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], args...)
			cmd.Dir = temp
			cmd.Stdin = strings.NewReader(tc.stdin)
			var out, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &stderr
			err := cmd.Run()
			code := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatal(err)
				}
				code = exitErr.ExitCode()
			}
			if ctx.Err() != nil || code != tc.wantCode {
				t.Fatalf("exit=%d, want %d; stdout=%s stderr=%s", code, tc.wantCode, &out, &stderr)
			}
			if tc.wantCode == 0 {
				var plan retestplan.Plan
				if err := json.Unmarshal(out.Bytes(), &plan); err != nil || len(plan.Items) != 4 || plan.AllocatedUnits != 3 || stderr.Len() != 0 {
					t.Fatalf("expected only a JSON plan: %s; stderr=%s; error=%v", &out, &stderr, err)
				}
			} else if out.Len() != 0 || !strings.HasPrefix(stderr.String(), "retest-plan:") {
				t.Fatalf("input errors must go only to stderr: stdout=%s stderr=%s", &out, &stderr)
			}
			if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("offline mode touched the backend data directory: %v", err)
			}
		})
	}
}

func TestRetestInputRejectsMalformedAndOversizedDocuments(t *testing.T) {
	for name, text := range map[string]string{
		"trailing-document": "{} {}",
		"trailing-junk":     "{} invalid",
		"unknown-field":     `{"silently_ignore_this": true}`,
		"oversized":         "{}" + strings.Repeat(" ", maxRetestInputBytes),
		"empty":             "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readRetestInput(strings.NewReader(text)); err == nil {
				t.Fatal("invalid document accepted")
			}
		})
	}
	input := append(retestFixture(t), []byte(" \n\t")...)
	if _, err := readRetestInput(bytes.NewReader(input)); err != nil {
		t.Fatal(err)
	}
}

type failingPlanWriter struct{}

func (failingPlanWriter) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestRetestPlanReportsOutputFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := runRetestPlan("-", bytes.NewReader(retestFixture(t)), failingPlanWriter{}, &stderr); code != 1 || !strings.Contains(stderr.String(), "write output") {
		t.Fatalf("code=%d, stderr=%s", code, &stderr)
	}
}
