# ARFABIT — notes for AI agents

You are working on ARFABIT: a single Go binary that watches an optical drive, rips
the disc, encodes it for native Apple TV 4K playback, and files it for Plex.

**Read `docs/ARCHITECTURE.md` §0 before writing any code.** It lists nine settled
decisions. They are not open questions. If a task seems to require breaking one,
stop and say so rather than working around it.

---

## The short version

| | |
|---|---|
| Language | Go. One static binary. No Python, Node, Docker, or runtime deps. |
| External tools | `makemkvcon` and `ffmpeg`, driven with `os/exec`. No wrapper libs. |
| Output | MKV · picture and sound copied or converted as the user chooses · SRT sidecars + embedded SRT |
| State | Plain JSON/JSONL files. No database. |
| UI | One HTML page, plain JavaScript over a JSON API, SSE for live updates. No npm, no framework, no build step. |
| Platforms | macOS and Windows are the real targets. Linux is supported. |

---

## How to work here

**Match the document, not your instincts.** This project has an unusually opinionated
design, arrived at through a long technical discussion. Where `docs/ARCHITECTURE.md`
specifies something, follow it exactly — the schemas, the stage names, the terminology,
the folder layout. Where it is silent, use judgment and say what you assumed.

**Use the terminology in §2 verbatim.** Node, Drive, Disc, Title, Defaults, Blueprint, Plan,
Master, Package, Line item, Delivery, Edition, Job, Library. Stage names are `SCAN PLAN RIP EJECT OCR QUEUED
PACKAGE DELIVER`, plus `LAB` for test clips, uppercase, in code and logs alike. No synonyms — not "convert" for PACKAGE,
not "source" for Master.

**Known shortcut.** `internal/config/toml.go` is a hand-written subset parser,
written only because the machine this was built on could not reach the Go module
proxy. Replace it with a real TOML library the moment that is possible; do not
extend it.

**Prefer boring code.** This is a wrapper around two external binaries. The value is
in correctness and UX, not cleverness. Straight-line subprocess handling beats an
abstraction almost every time.

**On other people's code.** `temp/` is gitignored and may hold clones of existing
rippers, kept for study. Read them to understand a problem — how `makemkvcon` output
behaves, which disc quirks are real, what a subprocess wrapper has to survive.

Then close them and write our version yourself. Not a reworded copy: our own code,
reached by understanding the problem rather than transcribing a solution. Shared
concepts are unavoidable and fine — there is one sensible way to parse a CSV-ish
robot-mode line — but the code is ours.

Never name those projects in anything git tracks. Not in source comments, not in
docs, not in commit messages, not in issues. They are a reading room, not a
dependency, and nothing here credits or references them.

---

## Things that are easy to get wrong

**`makemkvcon info disc:9999` tells you nothing about a disc.** It enumerates
drives, then tries to open disc 9999, which does not exist. So it always ends
with "failed to open disc", always exits non-zero, and always prints "opened in
OS access mode". None of that is about any real disc, and all three have been
mistaken for it — reporting a broken disc, then no drive at all, then a slow
drive, each on a machine where nothing was wrong. `drivesLocked` therefore
returns drives and nothing else. Do not reach past it for the messages.

**HDR metadata (§9).** An HDR source encoded without `--master-display` / `--max-cll`
produces a valid file that plays back grey. It does not error. Any change near the
encoder needs this verified, not assumed.

**What plays where (§4).** The target is the Plex app on an Apple TV 4K, and §4
holds what was tested to play there directly, without the server converting
anything. MKV, HEVC, H.264, and every sound format tried play directly except
Dolby TrueHD, which Plex converts on every play. PGS subtitles force the picture
to be converted. Every constraint in this project descends from that table. A
format not in it is untested, not assumed to work — the same rule as §15. The
table informs; it never decides. Changing a track because of a device is the
user's choice, and the page only says what each choice costs.

**Never remove user files.** No cleanup, no retention timers, no temp-file reaping
that could catch a Master. If disk space is short, say so and stop.

**Never invent an error explanation (§15).** Plain-language messages appear only on a
tested signature match. Everything else shows raw output verbatim. A wrong explanation
is worse than none — it is the specific failure that motivated this project.

**Atomic writes.** All state writes are write-temp-then-`rename`. Data may live on a
NAS; a node writes only inside its own `nodes/<id>/` directory.

---

## Writing user-facing text

The audience is a person who wants their disc on their TV. Assume no technical
vocabulary and no patience for jargon.

- Calm, short, concrete. Say what happened and what happens next.
- **Never mention risk or danger.** The UI does not present frightening choices,
  so it never needs to reassure. Do not write "don't worry" or "this is safe".
- Word choices: *remove* not delete · *replace* not overwrite · *did not finish*
  not failed · *stop* not abort or kill.
- Explain in units people feel: "about 6 hours", "22 GB", "roughly 1 in 10 discs".
- No exclamation marks. No emoji in the product UI.

The install instructions should work for an eight-year-old. If a step assumes
knowledge, it is a bug.

---

## Scope discipline

Phase 1 is **one node, movies only**. A master is always a copy of the disc as it is.
A Package — what to make from it — starts from the defaults or from a blueprint, a
recipe that makes line items the user can then change; the job never looks it up
again (§8). The architecture accommodates more (blueprint matchers, multichannel
audio, music CDs, Dolby Vision, a NAS controller), and
structures exist so those are data rather than rewrites — but do not build them yet.
See §16 for what belongs in which phase.

When a task is ambiguous, prefer the smaller version and name what you left out.
