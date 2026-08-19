package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
)

const (
	audioPath        = "/tmp/dictate-recording.wav"
	maxRecordSeconds = 1800 // hard cap so a missed stop can't record forever
)

var (
	errAlreadyRecording = errors.New("already recording")
	errNotRecording     = errors.New("not recording")
)

// Recorder owns the ffmpeg subprocess. Unlike a PID file, there's nothing to
// go stale here: the process handle lives only in memory for as long as this
// daemon runs, and a background goroutine clears it the moment ffmpeg exits
// (whether from our stop signal or a crash), so IsRecording() is always
// accurate.
type Recorder struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{}
	stderr   bytes.Buffer
	stopping bool   // true once Stop() has signaled ffmpeg, to suppress the "unexpected exit" log
	device   string // avfoundation index (macOS, e.g. ":1") or pulse source name (Linux)
}

// DefaultAudioDevice is a starting guess only -- avfoundation numbers audio
// devices by whatever's plugged in, so index 0 can just as easily be a
// virtual/loopback device (capturing desktop audio) as the built-in mic. Use
// -list-devices to find the right index for a given machine.
func DefaultAudioDevice() string {
	return defaultAudioDeviceFor(runtime.GOOS)
}

func defaultAudioDeviceFor(goos string) string {
	if goos == "darwin" {
		return ":0"
	}
	return "default"
}

func NewRecorder(device string) *Recorder {
	return &Recorder{device: device}
}

func (r *Recorder) IsRecording() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cmd != nil
}

func (r *Recorder) recordArgs() []string {
	return recordArgsFor(runtime.GOOS, r.device)
}

func recordArgsFor(goos, device string) []string {
	args := []string{"-y"}
	if goos == "darwin" {
		args = append(args, "-f", "avfoundation", "-i", device)
	} else {
		args = append(args, "-f", "pulse", "-i", device)
	}
	args = append(args,
		"-ar", "16000", "-ac", "1", "-acodec", "pcm_s16le",
		"-t", fmt.Sprintf("%d", maxRecordSeconds), audioPath,
	)
	return args
}

func (r *Recorder) Start() error {
	r.mu.Lock()
	if r.cmd != nil {
		r.mu.Unlock()
		return errAlreadyRecording
	}

	cmd := exec.Command("ffmpeg", r.recordArgs()...)
	r.stderr.Reset()
	cmd.Stderr = &r.stderr
	if err := cmd.Start(); err != nil {
		r.mu.Unlock()
		return err
	}

	done := make(chan struct{})
	r.cmd = cmd
	r.done = done
	r.mu.Unlock()

	go func() {
		waitErr := cmd.Wait()
		r.mu.Lock()
		wasStopping := r.stopping
		r.cmd = nil
		r.stopping = false
		r.mu.Unlock()
		if waitErr != nil && !wasStopping {
			log.Printf("ffmpeg exited unexpectedly: %v\n%s", waitErr, r.stderr.String())
		}
		close(done)
	}()
	return nil
}

// Stop signals ffmpeg to finish writing the file and blocks until it exits.
func (r *Recorder) Stop() error {
	r.mu.Lock()
	if r.cmd == nil {
		r.mu.Unlock()
		return errNotRecording
	}
	proc := r.cmd.Process
	done := r.done
	r.stopping = true
	r.mu.Unlock()

	if err := proc.Signal(syscall.SIGINT); err != nil {
		return err
	}
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("timed out waiting for ffmpeg to stop")
	}
}
