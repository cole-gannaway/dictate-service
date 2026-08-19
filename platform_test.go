package main

import (
	"bytes"
	"errors"
	"log"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCandidateCommandFallsBackWhenFirstIsFoundButFails(t *testing.T) {
	// Regression test for a real bug: paste() used to stop at the first
	// binary *found* on PATH, not the first that actually worked, so an
	// installed-but-non-functional xdotool (e.g. under Wayland) silently
	// blocked the ydotool fallback. Both candidates here are "found"; only
	// the second succeeds.
	var ran []string
	lookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	run := func(name string, args []string, input string) bool {
		ran = append(ran, name)
		return name == "second"
	}

	ok := candidateCommand("test", [][]string{{"first"}, {"second"}}, "", lookPath, run)

	if !ok {
		t.Fatal("expected candidateCommand to succeed via fallback")
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(ran, want) {
		t.Fatalf("ran = %v, want %v", ran, want)
	}
}

func TestCandidateCommandSkipsBinariesNotOnPath(t *testing.T) {
	var ran []string
	lookPath := func(name string) (string, error) {
		if name == "missing" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}
	run := func(name string, args []string, input string) bool {
		ran = append(ran, name)
		return true
	}

	ok := candidateCommand("test", [][]string{{"missing"}, {"present"}}, "", lookPath, run)

	if !ok {
		t.Fatal("expected candidateCommand to succeed")
	}
	if want := []string{"present"}; !reflect.DeepEqual(ran, want) {
		t.Fatalf("ran = %v, want %v (missing binary should never be run)", ran, want)
	}
}

func TestCandidateCommandAllFail(t *testing.T) {
	lookPath := func(string) (string, error) { return "/usr/bin/x", nil }
	run := func(string, []string, string) bool { return false }

	if candidateCommand("test", [][]string{{"a"}, {"b"}}, "", lookPath, run) {
		t.Fatal("expected candidateCommand to report failure when every candidate fails")
	}
}

func TestCandidateCommandLogsWhenNoneOnPath(t *testing.T) {
	// Regression test: previously, if no candidate was even found on PATH,
	// candidateCommand returned false without logging anything, making the
	// failure indistinguishable from the daemon not running at all.
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	lookPath := func(string) (string, error) { return "", errors.New("not found") }
	run := func(string, []string, string) bool { return true }

	if candidateCommand("paste", [][]string{{"xdotool"}, {"ydotool"}}, "", lookPath, run) {
		t.Fatal("expected candidateCommand to report failure when nothing is on PATH")
	}
	if got := buf.String(); !strings.Contains(got, "paste failed") || !strings.Contains(got, "xdotool") || !strings.Contains(got, "ydotool") {
		t.Fatalf("expected log to mention paste failure and both candidates, got %q", got)
	}
}

func TestCandidateCommandPassesArgsAndInput(t *testing.T) {
	var gotName string
	var gotArgs []string
	var gotInput string
	lookPath := func(name string) (string, error) { return "/usr/bin/" + name, nil }
	run := func(name string, args []string, input string) bool {
		gotName, gotArgs, gotInput = name, args, input
		return true
	}

	candidateCommand("test", [][]string{{"xclip", "-selection", "clipboard"}}, "hello", lookPath, run)

	if gotName != "xclip" {
		t.Errorf("name = %q, want xclip", gotName)
	}
	if want := []string{"-selection", "clipboard"}; !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("args = %v, want %v", gotArgs, want)
	}
	if gotInput != "hello" {
		t.Errorf("input = %q, want hello", gotInput)
	}
}

func TestRunLoggedSuccess(t *testing.T) {
	if !runLogged("true", nil, "") {
		t.Fatal("expected runLogged(\"true\") to report success")
	}
}

func TestRunLoggedFailure(t *testing.T) {
	if runLogged("false", nil, "") {
		t.Fatal("expected runLogged(\"false\") to report failure")
	}
}

func TestRunLoggedDoesNotHangOnDetachedGrandchild(t *testing.T) {
	// Regression test for a real bug: xclip (and similar tools) fork a
	// background helper after a successful clipboard copy, which inherits
	// our stdout/stderr descriptors. Capturing output via an in-memory pipe
	// (as CombinedOutput does) makes cmd.Wait() block until every holder of
	// that pipe closes it -- including the lingering grandchild -- even
	// though the command's own work already finished successfully.
	// Simulate that shape portably: the direct child ("sh") exits
	// immediately, but backgrounds a subprocess that inherits its stdout
	// and lingers well past runLoggedTimeout. Capturing to a real file
	// instead of a pipe (see runLogged) means this should return almost
	// immediately rather than waiting out runLoggedTimeout.
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found on PATH")
	}

	start := time.Now()
	ok := runLogged("sh", []string{"-c", "(sleep 5 &) ; exit 0"}, "")
	elapsed := time.Since(start)

	if !ok {
		t.Fatal("expected runLogged to report success despite the lingering grandchild")
	}
	if elapsed >= 1*time.Second {
		t.Fatalf("runLogged took %s, expected it to return almost immediately instead of waiting on the detached grandchild", elapsed)
	}
}

func TestRunLoggedWritesStdin(t *testing.T) {
	if !runLogged("grep", []string{"-q", "hello"}, "hello world") {
		t.Fatal("expected grep to find the match in stdin")
	}
	if runLogged("grep", []string{"-q", "hello"}, "goodbye world") {
		t.Fatal("expected grep to report no match")
	}
}
