package main

import (
	"os/exec"
	"reflect"
	"testing"
	"time"
)

func TestDefaultAudioDeviceFor(t *testing.T) {
	cases := map[string]string{
		"darwin":  ":0",
		"linux":   "default",
		"windows": "default", // unsupported platform, but should still fall through sanely
	}
	for goos, want := range cases {
		if got := defaultAudioDeviceFor(goos); got != want {
			t.Errorf("defaultAudioDeviceFor(%q) = %q, want %q", goos, got, want)
		}
	}
}

func TestRecordArgsForDarwinUsesAVFoundation(t *testing.T) {
	args := recordArgsFor("darwin", ":1")

	want := []string{
		"-y", "-f", "avfoundation", "-i", ":1",
		"-ar", "16000", "-ac", "1", "-acodec", "pcm_s16le",
		"-t", "1800", audioPath,
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("recordArgsFor(darwin, \":1\") = %v, want %v", args, want)
	}
}

func TestRecordArgsForLinuxUsesPulse(t *testing.T) {
	args := recordArgsFor("linux", "alsa_input.usb-foo")

	want := []string{
		"-y", "-f", "pulse", "-i", "alsa_input.usb-foo",
		"-ar", "16000", "-ac", "1", "-acodec", "pcm_s16le",
		"-t", "1800", audioPath,
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("recordArgsFor(linux, ...) = %v, want %v", args, want)
	}
}

func TestRecorderIsRecording(t *testing.T) {
	r := NewRecorder(":0")
	if r.IsRecording() {
		t.Fatal("new recorder should not report recording")
	}
	r.cmd = &exec.Cmd{}
	if !r.IsRecording() {
		t.Fatal("recorder with a live cmd should report recording")
	}
}

func TestRecorderStartRejectsWhenAlreadyRecording(t *testing.T) {
	r := NewRecorder(":0")
	r.cmd = &exec.Cmd{} // simulate an in-flight recording without touching ffmpeg

	if err := r.Start(); err != errAlreadyRecording {
		t.Fatalf("Start() = %v, want errAlreadyRecording", err)
	}
}

func TestRecorderStopRejectsWhenNotRecording(t *testing.T) {
	r := NewRecorder(":0")

	if err := r.Stop(); err != errNotRecording {
		t.Fatalf("Stop() = %v, want errNotRecording", err)
	}
}

// TestRecorderCleansUpAfterProcessExit exercises the background goroutine
// that clears Recorder.cmd when ffmpeg exits on its own (crash, bad device,
// etc.), not just when Stop() signals it -- that's the subtle concurrency
// path most likely to regress silently. It only needs ffmpeg to exist and
// fail fast on a bogus device; it doesn't need a working microphone.
func TestRecorderCleansUpAfterProcessExit(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	r := NewRecorder("this-device-definitely-does-not-exist-12345")
	if err := r.Start(); err != nil {
		t.Fatalf("Start() = %v, want nil (ffmpeg should launch even if the device is bad)", err)
	}
	if !r.IsRecording() {
		t.Fatal("expected IsRecording() to be true immediately after Start()")
	}

	select {
	case <-r.done:
		// ffmpeg exited (as expected, given the bogus device).
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ffmpeg to exit on a bad device")
	}

	if r.IsRecording() {
		t.Fatal("expected IsRecording() to be false after ffmpeg exits unexpectedly")
	}
}
