package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// runLoggedTimeout is a safety net against a genuinely wedged process (e.g.
// ydotoold not responding). It is deliberately generous -- runLogged
// redirects output to a real file rather than a pipe specifically so that a
// command forking a background helper (xclip and similar X11/Wayland
// clipboard tools do this after a successful copy, as standard selection
// ownership semantics) can't make Wait() block on that helper, so this
// timeout should only ever fire for an actual hang, not the expected case.
const runLoggedTimeout = 10 * time.Second

var isMac = runtime.GOOS == "darwin"

func notify(message string) {
	if isMac {
		exec.Command("osascript", "-e", fmt.Sprintf(`display notification %q with title "Dictation"`, message)).Run()
		return
	}
	if path, err := exec.LookPath("notify-send"); err == nil {
		exec.Command(path, "Dictation", message).Run()
		return
	}
	log.Println(message)
}

// runLogged runs cmd and logs its combined output on failure so permission
// errors (e.g. macOS Accessibility/TCC denials) show up in the daemon's log
// instead of failing silently.
//
// Output is captured to a temp file rather than an in-memory pipe (what
// CombinedOutput uses under the hood). That distinction matters: a command
// that forks a background helper -- xclip and similar clipboard tools do
// this on success, forking a process to keep serving the selection, which
// inherits the invoking process's stdout/stderr descriptors -- would leave
// a pipe's write end open indefinitely, and cmd.Wait() blocks until every
// holder of a pipe closes it, not just the direct child. A plain *os.File
// carries no such obligation, so Wait() only needs the direct child's exit
// status and returns immediately regardless of what any detached
// descendant does afterward. runLoggedTimeout is kept as a safety net for
// an actual hang, not as the normal path.
func runLogged(name string, args []string, input string) bool {
	cmd := exec.Command(name, args...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}

	outFile, err := os.CreateTemp("", "dictate-runlogged-*")
	if err != nil {
		log.Printf("%s failed to start: %v", name, err)
		return false
	}
	defer os.Remove(outFile.Name())
	defer outFile.Close()
	cmd.Stdout = outFile
	cmd.Stderr = outFile

	if err := cmd.Start(); err != nil {
		log.Printf("%s failed to start: %v", name, err)
		return false
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			out, _ := os.ReadFile(outFile.Name())
			log.Printf("%s failed: %v: %s", name, err, strings.TrimSpace(string(out)))
			return false
		}
		return true
	case <-time.After(runLoggedTimeout):
		cmd.Process.Kill()
		log.Printf("%s: killed after not exiting within %s", name, runLoggedTimeout)
		return false
	}
}

// candidateCommand tries each candidate (binary name followed by its args)
// in order, running the first one found on PATH that also succeeds. That
// second condition matters: a tool can be installed but non-functional for
// the current session (e.g. xdotool present but the session is Wayland,
// where it has no display to talk to), so stopping at the first one merely
// *found* would wrongly treat that as the end of the line instead of
// falling back to the next candidate.
//
// If a found candidate is run and fails, run (runLogged) already logs the
// underlying error. But if none of the candidates are even on PATH, the
// loop below has nothing to log by itself -- so that case is logged here
// explicitly. Without it, a machine missing every candidate (e.g. a fresh
// Linux box with neither xdotool nor ydotool installed) fails completely
// silently, which is indistinguishable from the daemon not running at all.
func candidateCommand(label string, candidates [][]string, input string, lookPath func(string) (string, error), run func(name string, args []string, input string) bool) bool {
	found := false
	for _, c := range candidates {
		if _, err := lookPath(c[0]); err != nil {
			continue
		}
		found = true
		if run(c[0], c[1:], input) {
			log.Printf("%s succeeded via %s", label, c[0])
			return true
		}
	}
	if !found {
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = c[0]
		}
		log.Printf("%s failed: none of %s found on PATH", label, strings.Join(names, ", "))
	}
	return false
}

func copyToClipboard(text string) bool {
	if isMac {
		return runLogged("pbcopy", nil, text)
	}
	candidates := [][]string{
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
		{"wl-copy"},
	}
	return candidateCommand("clipboard copy", candidates, text, exec.LookPath, runLogged)
}

// linuxPasteCommand is ydotool alone -- xdotool used to be tried first here,
// but it can exit 0 without actually delivering the keystroke under
// Wayland (it can still reach XWayland's nested X server even though the
// focused window is a native Wayland client that never sees the event),
// and reliably detecting Wayland to gate around that turned out not to be
// worth it (XDG_SESSION_TYPE isn't always propagated to the shell a daemon
// gets started from). ydotool works via /dev/uinput at the kernel input
// level, so it doesn't depend on X11 vs Wayland at all and doesn't have
// this failure mode.
var linuxPasteCommand = []string{"ydotool", "key", "ctrl+v"}

func paste() bool {
	if isMac {
		return runLogged("osascript", []string{"-e",
			`tell application "System Events" to keystroke "v" using command down`}, "")
	}
	return candidateCommand("paste", [][]string{linuxPasteCommand}, "", exec.LookPath, runLogged)
}
