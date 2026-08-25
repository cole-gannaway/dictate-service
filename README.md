# dictate

A small always-on daemon for hotkey dictation: press a hotkey to start
recording, press it again to stop — your speech gets transcribed via a local
transcription endpoint, copied to your clipboard, and auto-pasted wherever
your cursor is. Single static binary, macOS and Linux.

## Requirements

**`ffmpeg` is required on both platforms** — it's what actually records
audio.

- macOS: `brew install ffmpeg`
- Linux (Debian/Ubuntu): `sudo apt install ffmpeg`

Everything else below is looked up at runtime and skipped gracefully if
missing — `dictate` still runs, but you'll want these installed for the
full experience (notifications, clipboard, auto-paste):

| Purpose | macOS | Linux |
|---|---|---|
| Record audio | `ffmpeg` (avfoundation) | `ffmpeg` (pulse) |
| Notifications | `osascript` (built in) | `notify-send` (`libnotify-bin`) |
| Clipboard | `pbcopy` (built in) | `xclip`, `xsel`, or `wl-copy` |
| Auto-paste | `osascript` (built in) | `ydotool` |

Notifications are reserved for problems only (no audio captured, transcription
failed, auto-paste unavailable) — a successful dictation is silent, since the
pasted text landing where you were typing is confirmation enough. If a step's
tool is missing, `dictate` degrades gracefully and logs it — e.g. no
clipboard tool found means you'll see "Copied to clipboard (auto-paste
unavailable)" instead of a silent failure. Check the daemon's log whenever a
step doesn't seem to work.

You'll also need a transcription server running (default expected at
`http://127.0.0.1:8000/transcribe`) — see `-transcribe-url` below if yours
lives elsewhere. `dictate` expects the OpenAI-compatible transcription
contract: `POST` with the audio as a multipart field named `file`, and a
JSON response shaped `{"text": "..."}`. If you're pointing at your own
server, make sure its handler's upload parameter is named `file` — a
mismatched field name is a common cause of a `422 Unprocessable Entity`
from FastAPI-based servers (it validates the multipart field names before
your handler code ever runs).

## How to use it

Run the daemon once and leave it running in a terminal:

```
./dictate
```

It logs each step as it happens — recording started/stopped, transcription,
paste — so you can see what's going on. Press Ctrl+C to stop it.

### Trigger it

Bind a hotkey (e.g. Cmd+Shift+D via `skhd` on macOS) directly to the binary
— no wrapper script needed:

```
dictate -toggle
```

First press starts recording and returns immediately. Second press stops it,
transcribes, copies to clipboard, and auto-pastes — and blocks until all of
that finishes, printing the transcribed text before exiting. `-toggle` is
just a thin HTTP client to the already-running daemon, so there's no process
or PID to manage beyond the one long-running daemon; it just doesn't return
on that second press until the paste itself has happened.

Also available: `dictate -start`, `-stop`, and `-status` (prints `recording`
or `idle`).

**On Ubuntu**, `skhd` doesn't exist — bind the hotkey through your desktop
environment instead: **Settings → Keyboard → Keyboard Shortcuts → add a
custom shortcut** running `dictate -toggle` (use the full path if
`dictate` isn't on the shortcut's `PATH`, e.g. `/usr/local/bin/dictate
-toggle`). This works under both X11 and Wayland since the DE handles the
key capture, not `dictate` itself. If you're on a tiling WM instead of
GNOME, `sxhkd` is the closest equivalent to `skhd`.

### Useful flags

Run `dictate -h` for the full list. The ones you're most likely to need:

| Flag | Purpose |
|---|---|
| `-device` | Audio input device (avfoundation index on macOS, pulse source name on Linux) |
| `-list-devices` | List available audio input devices and exit |
| `-transcribe-url` | URL of the transcription endpoint (default `http://127.0.0.1:8000/transcribe`) |
| `-port` | Port the daemon listens on (default `8090`) |
| `-version` / `-v` | Print version and exit |
| `-update` | Check GitHub for a newer release and self-update, then exit |

If you set `-port` or `-transcribe-url` when starting the daemon, pass the
same `-port` to the client commands (`-toggle`, `-start`, `-stop`,
`-status`) so they reach it:

```
dictate -port 9090          # start the daemon on a different port
dictate -port 9090 -toggle  # and hit that same port from the hotkey
```

Picking the right microphone matters — `ffmpeg`'s device numbering depends
on whatever's plugged in, so index `0` isn't reliably your built-in mic if
you have a headset or virtual audio device attached:

```
./dictate -list-devices
./dictate -device ":1"
```

(On Linux, `-device` takes a pulse source name instead — `-list-devices`
uses `pactl list short sources` if installed.)

## Other notes

### Linux specifics

- **Audio backend**: `-f pulse` is used for recording, which also works
  against PipeWire via `pipewire-pulse` — the default on Ubuntu 22.04+, so
  no extra setup needed there.
- **Auto-paste uses `ydotool` only**: `xdotool` used to be tried first, but
  it can exit successfully without actually delivering the keystroke under
  Wayland (it can still reach XWayland's nested X server even though the
  focused window is a native Wayland client that never sees the event) —
  and Wayland is the default session on GNOME/Ubuntu. Rather than trying to
  detect the session type reliably enough to guard against that, `dictate`
  only uses `ydotool`, which works via `/dev/uinput` at the kernel input
  level regardless of X11/Wayland. It needs its daemon running (`ydotoold`)
  and usually needs your user in the `input` group
  (`sudo usermod -aG input $USER`, then log out/in) since it writes to
  `/dev/uinput`. If it's not installed/working, transcribed text still
  lands on the clipboard — you just paste manually with Ctrl+V.
- **No Accessibility-permission equivalent**: the macOS
  Accessibility-permission dance doesn't apply on Linux; paste failures
  there are almost always a missing tool or a Wayland/`ydotoold` setup
  issue, not a permissions prompt.

### Troubleshooting

**Auto-paste does nothing / silently fails** — check the daemon's log, a
failed paste logs the real error, e.g.:

```
osascript failed: exit status 1: ... System Events got an error: osascript is not allowed to send keystrokes. (1002)
```

On macOS this is an Accessibility permission issue, not a bug. Fix:
**System Settings → Privacy & Security → Accessibility** — make sure the
terminal app you run `./dictate` from (Terminal.app, iTerm2, etc.) is in
the list and toggled **on**. If it's not there, click `+` and add it
manually. If it's there but still failing, remove it (`-`) and re-add it,
then restart the daemon.

**It's recording desktop audio / the wrong input instead of my mic** — see
[Useful flags](#useful-flags) above for `-list-devices` / `-device`.

## Developers

### Build from source

```
go build -o dictate .
./dictate
```

### Tests

```
go test ./...
```

Covers the transcription HTTP call, the ffmpeg arg-building and recorder
state machine, the clipboard/paste tool fallback logic, and the CLI client
plumbing (`-toggle`/`-start`/`-stop`/`-status`) — all against fakes
(`httptest`, injected lookups), so nothing here needs a real microphone,
display server, or transcription model. One test does require `ffmpeg` on
`PATH` (it verifies the recorder cleans up after ffmpeg exits unexpectedly)
and skips itself if it's missing.

### Build for distribution

```
./build.sh [version]
```

Cross-compiles `dictate` for macOS and Linux (amd64 + arm64) into `dist/`
from whatever platform you run it on — no cgo dependencies, so no Docker or
per-platform build machine is needed. It also drops a `dist/dictate`
symlink pointing at the binary for your current host.

### Cutting a release

```
./release.sh <version>   # e.g. ./release.sh 1.1.0 -- no leading "v"
```

Requires the GitHub CLI, authenticated (`brew install gh && gh auth login`).
Builds all four platform binaries, tags the commit, pushes the tag, and
creates a GitHub release with the binaries attached as
`dictate-<goos>-<goarch>` assets. That naming is what `dictate -update`
looks for when checking `https://github.com/cole-gannaway/dictate-service/releases`
for a newer version — installs on macOS and Linux can then self-update in
place by running `dictate -update`.
