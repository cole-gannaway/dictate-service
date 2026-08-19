package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempAudioFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recording.wav")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("failed to write temp audio file: %v", err)
	}
	return path
}

func TestTranscribeAudioSuccess(t *testing.T) {
	const audioContents = "fake wav bytes"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		// "file" matches the OpenAI-compatible transcription API convention
		// -- regression test for a mismatch that previously caused 422s
		// against OpenAI-compatible endpoints (which reject "audio").
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatalf(`FormFile("file"): %v`, err)
		}
		defer file.Close()
		if header.Filename != "recording.wav" {
			t.Errorf("filename = %q, want recording.wav", header.Filename)
		}
		body, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("reading uploaded file: %v", err)
		}
		if string(body) != audioContents {
			t.Errorf("uploaded contents = %q, want %q", body, audioContents)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text": "hello world"}`))
	}))
	defer server.Close()

	path := writeTempAudioFile(t, audioContents)

	text, err := transcribeAudio(path, server.URL+"/transcribe")
	if err != nil {
		t.Fatalf("transcribeAudio() error = %v", err)
	}
	if text != "hello world" {
		t.Fatalf("transcribeAudio() = %q, want %q", text, "hello world")
	}
}

func TestTranscribeAudioServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("model not loaded"))
	}))
	defer server.Close()

	path := writeTempAudioFile(t, "fake wav bytes")

	_, err := transcribeAudio(path, server.URL+"/transcribe")
	if err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
	if !strings.Contains(err.Error(), "model not loaded") {
		t.Fatalf("error %q does not mention the server's response body", err)
	}
}

func TestTranscribeAudioMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	path := writeTempAudioFile(t, "fake wav bytes")

	if _, err := transcribeAudio(path, server.URL+"/transcribe"); err == nil {
		t.Fatal("expected an error for a malformed JSON response")
	}
}

func TestTranscribeAudioMissingFile(t *testing.T) {
	if _, err := transcribeAudio("/no/such/file.wav", "http://example.invalid/transcribe"); err == nil {
		t.Fatal("expected an error for a missing audio file")
	}
}
