# Security policy

This document is the security perimeter of `nimbleterminal.dev/ntvideo`: what
each playback and extraction path trusts, what OS confinement covers after
`WithWorker` or `HeadlessOptions.Sandbox`, and what remains the host's job.
OS enforcement comes from
[`ntmediabox`](https://github.com/NimbleTerminal/ntmediabox) and
[`ntconfine`](https://github.com/NimbleTerminal/ntconfine). Their own
`SECURITY.md` files define the Landlock, seccomp, and Seatbelt rules and the
hostile-peer wire bounds this library relies on. This file states the choices
ntvideo makes on top of those libraries.

## Reporting a vulnerability

Do not report security vulnerabilities in public GitHub issues. Send reports
privately to **`security@neomantra.net`** with the affected version, platform,
and a minimal reproduction.

## Threat model

ntvideo is a library that plays and inspects media chosen by the host. The
default native path loads libmpv in the host process. OS confinement is opt-in.
The browser backend is a DOM media element and is outside this perimeter.

| In scope when confinement is enabled | Outside this perimeter |
| --- | --- |
| A hostile local media file or caption sidecar that exploits libmpv or FFmpeg inside the worker or a confined headless tool | The default in-process libmpv path, and headless tools run with a nil `Sandbox`. File-size and dimension checks are not a memory sandbox |
| A worker that has already been compromised and sends hostile bytes on its socket | Untrusted helper binaries. `WorkerPath`, `StagePath`, `WithLibraryPath`, `PlayerOptions.LibraryPath`, `FFprobe`, and `FFmpeg` are trusted host settings |
| Path tricks that would make a decoder open something other than the staged regular file (URLs, mpv protocols, UNC paths, devices, directories, symlink escape) | Kernel exploits. The worker shares the host kernel |
| Terminal control sequences in worker status, captions, or cue metadata | Browser sources. The DOM backend follows the page's URL, CORS, and CSP rules |
| Oversized files, frames, cue documents, and tool output | `AllowUnsandboxed: true`, which runs the helper with no OS confinement when enforcement is unavailable |
| | macOS address-space limits. Darwin does not enforce `RLIMIT_AS`; deadlines, output caps, and wire bounds are the controls on that platform |

A file that passes `validateLocalFile` can still change before the decoder
reads it. Native decoding can allocate before metadata arrives. Presentation
is capped at 960×540 pixels; that cap does not bound decoder allocations.

## Local files

Native `Load`, `Player`, and the headless file APIs accept local regular files
only. `validateLocalFile` rejects an empty path, an embedded NUL, `://`, a UNC
or `//` path, and a path whose non-volume portion contains `:`. The path is
made absolute, symlinks are evaluated, and the result must be a regular file
no larger than the active file limit. Limits default to 2 GiB per file, 4096
pixels per side, and 4096×2160 pixels. Zero or negative limit fields use those
defaults.

In-process libmpv is started with `config=no`, `load-scripts=no`, `ytdl=no`,
no default input bindings, no OSC, `sub-auto=no`, `audio-file-auto=no`,
`access-references=no`, and lavf protocol allowlists of `file`. Those settings
stop ordinary network and playlist features. They do not confine a compromised
decoder.

## Out-of-process playback

`WithWorker` decodes in a separate process. Without it, `New` and `NewPlayer`
decode in-process. The worker plays with software decoding and audio disabled.
Hardware decoding and audio flags in `PlayerOptions` do not turn those back on
inside the worker.

On load, the host creates a `0700` staging directory and exposes the video
there under a bare name (`video` plus a short sanitized extension). The staged
entry is a symlink to the validated file, a hard link when symlink creation
fails, or a bounded `0600` copy if both link methods fail. The original file stays
on the worker's read policy for the life of the helper. Later writes to it are
visible through a symlink or hard link; copy fallback presents the copied bytes.
Up to 16 caption sidecars are copied into the directory (mode `0600`, at most 4 MiB
each, and never more than the active file limit). Only those bare names cross
the ntmediabox connection. Names that are empty, longer than 255 bytes, not
`filepath.Base` of themselves, contain `/`, `\`, or `:`, are `.` or `..`,
start with `-`, or contain C0 or C1 controls are rejected.

The process policy is `ntmediabox.DefaultPolicy` for the worker binary, the
staging directory, the resolved video, and an explicitly configured libmpv file.
On macOS, ntvideo also grants read access to the regular system file
`/private/etc/ssl/openssl.cnf` for LibreSSL initialization, never its enclosing
directory. Connection failures may include the helper's bounded, control-stripped
stderr. That policy adds ntconfine's default library, loader, and font directories, sets no writable paths, and leaves
`AllowUnsandboxed` false unless the host set it. Directory entries on the read
list are readable as trees; file entries are the literal path. The staging
directory is also an argument to the worker, so it is visible in process
arguments as well as in the policy. The resolved video path is in the policy
and is not sent on the wire. An empty `WorkerPath` resolves `ntvideo-worker`
beside the host executable and never searches `PATH`. An explicit path is
checked with `VerifyBinary` (regular executable file; on Unix, not group- or
world-writable, no setuid or setgid, owned by the effective uid or root, parent
directory not world-writable unless it is sticky). Linux needs `ntconfine-stage`
beside the host, or `WorkerConfig.StagePath`. macOS uses `sandbox-exec`. If OS enforcement
is unavailable and `AllowUnsandboxed` is false, startup returns
`ntmediabox.ErrUnavailable`. Windows has no OS confinement, so it takes that
path unless `AllowUnsandboxed` is set.

On Unix the helper starts with an empty environment and its own session, without
a controlling terminal. The explicitly unsandboxed Windows fallback retains
loader-related system and `PATH` environment variables and has no Unix process
group guarantees. ntvideo does not set ntconfine's resource-limit fields, so
Linux uses ntconfine's defaults: core dumps off, 1024 open files, a 16 GiB
address space, and 4096 processes per uid. macOS `sandbox-exec` does not enforce
`RLIMIT_AS`. The streaming helper has no playback deadline. It runs until the
host closes it, the process dies, or the wire protocol fails. A missing worker
binary fails `Load` immediately. A missing default libmpv inside a running worker
fails later, as status `Failed`.

`WithLibraryPath` also applies to worker playback. The host resolves that trusted
path to a regular file, adds the literal file to the helper's read policy, and
writes its absolute path in a bounded version-1 settings file (mode `0600`) in
the private staging directory. The path is not sent in a media wire request and
no library directory is added implicitly. Shared-library dependencies still need
to be in the permitted runtime directories. `NewWorkerDecoder` reads these
settings before opening libmpv; malformed settings fail the load. Without staged
settings it uses `PlayerOptions.LibraryPath`, or the default loader search when
that is empty. Direct `Player` also accepts `PlayerOptions.LibraryPath`. Library
paths are trusted host settings, not verified executable binaries. An explicitly
configured missing library fails host `Load`; an invalid library file fails in
the helper without falling back to the default libmpv.

The host reads the worker with `NewClientWithKill`. A protocol violation, I/O
error, EOF, or client close runs the kill callback, which closes the socket and
the helper. Helper close terminates the worker (process-group `SIGKILL` on
Unix) and reaps it. `workerDecoder.Unload` releases the in-worker libmpv
instance when the client stops playback. Status and frames from an older session, or from a
generation older than the latest request, are dropped. Frames larger than the
current widget, and frames that arrive while the widget is hidden, are dropped.
Widget dimensions sent to the worker are clamped to ntmediabox's `MaxFrameSide`
(4096). Opening another source increments the session and destroys the previous
staging directory.

`OpenRequest.Limits` may narrow the worker's `PlayerOptions.Limits`. A request
cannot raise those limits. A zero request field leaves the worker maximum in
place.

Showing a hidden widget does not resume playback. That is host-owned control,
independent of the sandbox. Hiding sends a `1×1` resize to reduce worker rendering
work; visible frame requests restore the requested size. Frames that arrive
while the widget is hidden are dropped. Volume and mute status reflect the
host's requested preferences, but worker audio remains disabled even when
`Status.Muted` is false.

Confinement bounds file, execute, and network access. It does not authenticate
pixels or cue text. A confined worker can still send any frame that passes the
wire bounds and any cue text that passes `decodeWorkerCues`.

## Caption cue metadata

With `WithCaptionEvents(true)`, staged `.srt` and `.vtt` sidecars are parsed in
a second, short-lived confined helper using the same policy. The helper speaks
the ntmediabox render protocol (`ServeWorker` selects it from the frame type).
The host uses `NewRenderClientWithKill` with a 15-second timeout and accepts
metadata only. That timeout is not `HeadlessOptions.Timeout`.

The helper rejects any request that is not kind `video-caption-cues`, has a
non-bare name, or carries inline `Data` or `Meta`. It checks the staged file
again, parses at most 4 MiB and 10,000 cues, and returns version-1 JSON. The
host rejects metadata that is empty, larger than 8 MiB, or not UTF-8, and
rejects a document unless:

- `version` is 1 and `cues` is present
- the cue count is at most 10,000 and the combined text is at most 4 MiB
- each cue ID is its decimal index, `TrackID` is empty, `Start >= 0`, and `End > Start`
- each cue's text is already equal to `captionPlainText` of itself (ANSI
  sequences, Unicode control characters, bidi overrides, and isolates removed,
  at most 4096 runes)

`captionPlainText` removes `U+200E`, `U+200F`, `U+061C`, `U+202A`–`U+202E`, and
`U+2066`–`U+2069`. The metadata validator rejects text containing those scalars;
in-process caption parsing strips them too. Cue metadata travels in raw
`RenderResponse.Meta` bytes, so it needs this application-level validation.

A response that includes an image or an end flag is rejected. There is no
fallback that parses the sidecar in the host. A metadata or parse failure sets
`Status.CaptionCueErr` for that sidecar and leaves video playback running.
Embedded subtitle tracks, ASS/bitmap sidecars, and extensionless sidecars do not
produce cue timelines.
Unsupported filename extensions are not rewritten to `.srt`.

The decoder identifies subtitle sources by mpv's `external-filename`, not by
track-list position or counts. A versioned `Track.Kind` marker carries the staged
bare name (or an empty source for embedded subtitles) through ntmediabox; the
host restores the public `subtitles` kind. Only a known staged name can select a
cue timeline. Missing or unknown source identity reports `CaptionCueErr` instead
of guessing. This does not authenticate an already compromised helper's claims.

`ServeWorker` serves both the media protocol and this metadata protocol.
`NewWorkerDecoder` with `ntmediabox.Serve` serves media only. Rebuild the
worker with the host after an upgrade so both sides share the metadata version.

In-process playback parses the same sidecar limits in the host. `ParseCues`
does too. WebVTT timestamp maps are rejected. Errors return no partial
transcript. Parsing errors do not stop playback.

## Headless probe, frames, and transcripts

`Probe`, `FrameAt`, `Frames`, and container `Cues` run ffprobe and ffmpeg.
Sidecar `Cues` and `ParseCues` parse text in the calling process and do not
spawn those tools. Each tool invocation gets a deadline (15 seconds by default,
or `HeadlessOptions.Timeout`, and the caller's context), a stdout budget, and a
64 KiB stderr budget. A write past either budget fails the operation.
`protocol_whitelist=file` is set on every tool invocation. That flag is an
application restriction; it confines the process only when `Sandbox` is set.

`Frames` accepts at most 16 timestamps and a 16 Mi pixel output budget. The
default output box is 640×360. Each distinct timestamp is an input-side seek.
One failure, including a timestamp at or past the end, returns no images.
Duplicate timestamps produce independent images.

A nil `Sandbox` runs the tools directly. `FFprobe` and `FFmpeg` then name
executables on `PATH`, or absolute paths supplied by the host, with no
`VerifyBinary` check.

A non-nil `Sandbox` uses the same ntmediabox policy as the worker, with the
tool binary and the one input file as the extra paths. The zero
`HeadlessSandbox` requires OS enforcement. `AllowUnsandboxed` is the explicit
opt-out when enforcement is unavailable. Linux uses `StagePath` or
`ntconfine-stage` beside the host. The tool path is resolved and passed through
`VerifyBinary` before launch. Cancellation calls `KillHelper` on the helper
pid. The same policy applies to confined container transcripts. Sidecar parsing
stays in-process either way.

Tool failures include a bounded stderr excerpt. That excerpt is not stripped of
terminal controls. Sanitize it before displaying it. The example player does
this because a browser build can take the source from the page query string.

## Browser backend

The browser build does not apply native file limits or ntmediabox. Sources,
including blob URLs, are owned by the page. Transcript links are rendered only
when `new URL(value, document.baseURI)` is `http:` or `https:`; other schemes
are dropped. The example's development server listens on loopback.

## What hosts do

- Use `WithWorker` or `HeadlessOptions.Sandbox` for media you do not trust.
  Leave `AllowUnsandboxed` false unless losing confinement is an accepted
  fallback (Windows has no OS enforcement in ntconfine).
- Configure `WithLibraryPath` only from trusted host settings. Worker playback
  grants that resolved library file read access; dependencies must remain in
  permitted runtime directories. Direct `Player` and custom worker decoders can
  use `PlayerOptions.LibraryPath`.
- Ship `ntvideo-worker` and, on Linux, `ntconfine-stage` next to the host, or
  pass verified paths. Do not point `WorkerPath`, `StagePath`, `FFprobe`, or
  `FFmpeg` at a location an untrusted user can replace.
- Call `ServeWorker` from the worker's `ntmediabox.WorkerConn()`, and rebuild
  that binary with the host.
- Keep driving the Bubble Tea loop after close, and do not treat a hidden
  widget as paused-by-sandbox. Visibility does not resume playback.
- A host that sandboxes `Player` itself must still allow the process to read
  libmpv and its libraries. libmpv is loaded on the first `Load`.
- Treat headless errors as untrusted text.

## Invariants

When changing worker, headless, or cue-metadata code, keep all of the following:

- [ ] Default native playback stays in-process. `WithWorker` and `HeadlessOptions.Sandbox` stay opt-in, and a nil sandbox still means direct tool execution.
- [ ] The zero `WorkerConfig` and the zero `HeadlessSandbox` fail closed with `ErrUnavailable` when OS enforcement is missing. `AllowUnsandboxed` is never implied.
- [ ] Worker and confined-tool binaries pass `ResolveSibling` or `VerifyBinary`. Sibling lookup does not search `PATH`.
- [ ] The worker policy is `DefaultPolicy` of the helper, the staging directory, and the resolved video, with the optional resolved `WithLibraryPath` file and no writable paths. Headless confinement is `DefaultPolicy` of the tool and the one input file.
- [ ] The streaming worker has no playback deadline. Cue metadata and headless tools keep theirs.
- [ ] Only bare filenames are sent to a worker. Sidecar copies stay within 16 files and 4 MiB. Requests cannot widen `PlayerOptions.Limits`.
- [ ] `NewClientWithKill` and `NewRenderClientWithKill` remain the host clients, so a protocol failure kills the helper process group.
- [ ] Cue metadata is version 1, bounded, checked with `decodeWorkerCues`, and associated by explicit staged filename. Bidi controls are rejected. The host does not parse the sidecar or guess its track on failure. Playback continues.
- [ ] `ServeWorker` is what serves both protocols. A media-only `ntmediabox.Serve` wrapper does not grow a second, unconfined parser.
- [ ] Headless stdout and stderr budgets fail the write that would exceed them. Batch frames seek to the requested times, preserve order, and return all images or an error.
- [ ] `task ci` passes before release. Confinement changes also need `task ci-native` where libmpv and FFmpeg are installed.
