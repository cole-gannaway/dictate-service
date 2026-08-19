package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"
)

func TestClientAction(t *testing.T) {
	cases := []struct {
		name                        string
		toggle, start, stop, status bool
		want                        string
		wantErr                     bool
	}{
		{name: "none set", want: ""},
		{name: "toggle", toggle: true, want: "toggle"},
		{name: "start", start: true, want: "start"},
		{name: "stop", stop: true, want: "stop"},
		{name: "status", status: true, want: "status"},
		{name: "toggle and start conflict", toggle: true, start: true, wantErr: true},
		{name: "all four conflict", toggle: true, start: true, stop: true, status: true, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := clientAction(c.toggle, c.start, c.stop, c.status)
			if c.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("clientAction() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRequirePost(t *testing.T) {
	rec := httptest.NewRecorder()
	if requirePost(rec, httptest.NewRequest(http.MethodGet, "/toggle", nil)) {
		t.Fatal("expected requirePost to reject a GET request")
	}
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}

	rec2 := httptest.NewRecorder()
	if !requirePost(rec2, httptest.NewRequest(http.MethodPost, "/toggle", nil)) {
		t.Fatal("expected requirePost to accept a POST request")
	}
}

func TestDoClientRequestToggle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/toggle" {
			t.Errorf("got %s %s, want POST /toggle", r.Method, r.URL.Path)
		}
		w.Write([]byte("recording\n"))
	}))
	defer server.Close()

	output, err := doClientRequest("toggle", serverAddr(t, server))
	if err != nil {
		t.Fatalf("doClientRequest() error = %v", err)
	}
	if output != "recording\n" {
		t.Fatalf("output = %q, want %q", output, "recording\n")
	}
}

func TestDoClientRequestStatusUsesGETAndParsesJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"recording": true}`))
	}))
	defer server.Close()

	output, err := doClientRequest("status", serverAddr(t, server))
	if err != nil {
		t.Fatalf("doClientRequest() error = %v", err)
	}
	if output != "recording\n" {
		t.Fatalf("output = %q, want %q", output, "recording\n")
	}
}

func TestDoClientRequestErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "already recording", http.StatusConflict)
	}))
	defer server.Close()

	_, err := doClientRequest("start", serverAddr(t, server))
	if err == nil {
		t.Fatal("expected an error for a non-2xx response")
	}
	if !strings.Contains(err.Error(), "already recording") {
		t.Fatalf("error %q does not mention the server's response body", err)
	}
}

func TestDoClientRequestDaemonNotReachable(t *testing.T) {
	// Nothing listens here; use a server that we've already closed to get a
	// deterministic connection-refused without depending on port scanning.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := serverAddr(t, server)
	server.Close()

	_, err := doClientRequest("status", addr)
	if err == nil {
		t.Fatal("expected an error when the daemon isn't reachable")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("error %q does not explain that the daemon is unreachable", err)
	}
}

func TestHandleStartConflict(t *testing.T) {
	r := NewRecorder(":0")
	r.cmd = &exec.Cmd{} // simulate already recording, without touching ffmpeg

	rec := httptest.NewRecorder()
	handleStart(rec, r)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestHandleStopConflict(t *testing.T) {
	r := NewRecorder(":0") // not recording

	rec := httptest.NewRecorder()
	handleStop(rec, r, "http://example.invalid/transcribe")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func serverAddr(t *testing.T, server *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parsing test server URL: %v", err)
	}
	return u.Host
}
