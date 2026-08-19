// Command dictated is a small always-on daemon: run it once in a terminal,
// then trigger it over HTTP (bound to a hotkey) to start/stop recording,
// transcribe via the existing local transcription endpoint, and paste the
// result. State lives entirely in this process's memory -- no PID files,
// no polling the OS process table.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	device := flag.String("device", DefaultAudioDevice(),
		`audio input device: avfoundation index (e.g. ":1") on macOS, or a pulse source name on Linux`)
	transcribeURL := flag.String("transcribe-url", "http://127.0.0.1:8000/transcribe",
		"URL of the local transcription endpoint")
	port := flag.Int("port", 8090,
		"port the daemon listens on (and the client commands below connect to -- must match between the two)")
	listDevices := flag.Bool("list-devices", false,
		"list available audio input devices and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.BoolVar(showVersion, "v", false, "print version and exit (shorthand)")
	toggle := flag.Bool("toggle", false, "toggle recording on the running daemon, then exit (bind this to a hotkey)")
	start := flag.Bool("start", false, "start recording on the running daemon, then exit")
	stopCmd := flag.Bool("stop", false, "stop recording on the running daemon, then exit")
	status := flag.Bool("status", false, "print the running daemon's recording status, then exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	if *listDevices {
		printAudioDevices()
		return
	}

	listenAddr := fmt.Sprintf("127.0.0.1:%d", *port)

	action, err := clientAction(*toggle, *start, *stopCmd, *status)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if action != "" {
		runClient(action, listenAddr)
		return
	}

	recorder := NewRecorder(*device)

	mux := http.NewServeMux()
	mux.HandleFunc("/toggle", func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		if recorder.IsRecording() {
			handleStop(w, recorder, *transcribeURL)
		} else {
			handleStart(w, recorder)
		}
	})
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		if requirePost(w, r) {
			handleStart(w, recorder)
		}
	})
	mux.HandleFunc("/stop", func(w http.ResponseWriter, r *http.Request) {
		if requirePost(w, r) {
			handleStop(w, recorder, *transcribeURL)
		}
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"recording": recorder.IsRecording()})
	})

	server := &http.Server{Addr: listenAddr, Handler: mux}

	go func() {
		log.Printf("dictate daemon %s listening on http://%s (pid %d)", version, listenAddr, os.Getpid())
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}

// printAudioDevices lists candidate -device values so a machine-specific
// mic index (avfoundation numbers devices by whatever's plugged in, so
// index 0 is not reliably the built-in mic) can be picked correctly.
func printAudioDevices() {
	if runtime.GOOS == "darwin" {
		out, _ := exec.Command("ffmpeg", "-f", "avfoundation", "-list_devices", "true", "-i", "").CombinedOutput()
		inAudioSection := false
		for _, line := range strings.Split(string(out), "\n") {
			if !inAudioSection {
				inAudioSection = strings.Contains(line, "AVFoundation audio devices")
				continue
			}
			fmt.Println(line)
		}
		fmt.Println(`Pass the index shown above, e.g. -device ":1"`)
		return
	}
	if _, err := exec.LookPath("pactl"); err == nil {
		out, _ := exec.Command("pactl", "list", "short", "sources").CombinedOutput()
		fmt.Print(string(out))
		fmt.Println(`Pass the source name shown above, e.g. -device "alsa_input.usb-..."`)
		return
	}
	fmt.Println("install pulseaudio-utils (for `pactl`) to list audio sources on this system")
}

// clientAction returns which single client command was requested (empty
// string if none), or an error if more than one was given at once.
func clientAction(toggle, start, stop, status bool) (string, error) {
	flags := map[string]bool{"toggle": toggle, "start": start, "stop": stop, "status": status}
	order := []string{"toggle", "start", "stop", "status"}
	action := ""
	for _, name := range order {
		if !flags[name] {
			continue
		}
		if action != "" {
			return "", fmt.Errorf("only one of -toggle, -start, -stop, -status may be given (got -%s and -%s)", action, name)
		}
		action = name
	}
	return action, nil
}

// runClient sends action ("toggle", "start", "stop", or "status") to the
// running daemon over HTTP and prints its response. This is what lets
// `dictated -toggle` be bound directly to a hotkey -- no PID files, no
// separate wrapper script, just a request to the daemon that's already
// listening.
func runClient(action, listenAddr string) {
	output, err := doClientRequest(action, listenAddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(output)
}

// doClientRequest holds the actual request/response logic behind runClient,
// kept separate (and free of os.Exit) so it can be exercised in tests
// against a fake HTTP server.
func doClientRequest(action, listenAddr string) (string, error) {
	method := http.MethodPost
	// stop/toggle now do the transcribe+clipboard+paste work synchronously
	// before responding (see handleStop), so their request can legitimately
	// take a while on a slow transcription backend -- status and start
	// return near-instantly and keep the short timeout.
	timeout := 3 * time.Second
	if action == "status" {
		method = http.MethodGet
	} else if action == "stop" || action == "toggle" {
		timeout = 5 * time.Minute
	}

	req, err := http.NewRequest(method, "http://"+listenAddr+"/"+action, nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("dictated daemon not reachable at %s: %w\nstart it first with: dictated", listenAddr, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", errors.New(strings.TrimSpace(string(body)))
	}

	if action == "status" {
		var s struct {
			Recording bool `json:"recording"`
		}
		if err := json.Unmarshal(body, &s); err == nil {
			if s.Recording {
				return "recording\n", nil
			}
			return "idle\n", nil
		}
	}
	return string(body), nil
}

func requirePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func handleStart(w http.ResponseWriter, recorder *Recorder) {
	if err := recorder.Start(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	log.Println("recording started")
	fmt.Fprintln(w, "recording")
}

// handleStop finalizes the audio file, then transcribes, copies to the
// clipboard, and pastes -- all synchronously, before responding. Doing
// this within the request instead of a detached goroutine means each
// -stop/-toggle call is self-contained: there's no way for a slow result to
// land after a later recording has already started, because nothing about
// it outlives the request that triggered it. The response body is the
// transcribed text itself (or an error), so the client -- and whatever
// hotkey tool invoked it -- can see the real result, not just "stopped".
func handleStop(w http.ResponseWriter, recorder *Recorder, transcribeURL string) {
	if err := recorder.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	log.Println("recording stopped, transcribing...")

	text, err := processRecording(transcribeURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintln(w, text)
}

// processRecording transcribes the just-recorded audio and copies/pastes
// the result, returning the transcribed text. notify() is reserved for
// problems only (no audio, transcription failure, auto-paste unavailable)
// -- the successful path is silent, since a working dictation is already
// self-evident from the pasted text landing where you were typing.
func processRecording(transcribeURL string) (string, error) {
	defer os.Remove(audioPath)

	info, err := os.Stat(audioPath)
	if err != nil || info.Size() == 0 {
		notify("No audio captured")
		return "", errors.New("no audio captured")
	}

	start := time.Now()
	text, err := transcribeAudio(audioPath, transcribeURL)
	log.Printf("transcribe request took %s", time.Since(start))
	if err != nil {
		notify("Transcription failed")
		return "", fmt.Errorf("transcription failed: %w", err)
	}

	start = time.Now()
	clipboardOK := copyToClipboard(text)
	log.Printf("clipboard copy took %s (ok=%v)", time.Since(start), clipboardOK)

	start = time.Now()
	pasteOK := clipboardOK && paste()
	log.Printf("paste took %s (ok=%v)", time.Since(start), pasteOK)

	if !pasteOK {
		notify("Copied to clipboard (auto-paste unavailable)")
	}
	log.Printf("transcribed: %s", text)
	return text, nil
}
