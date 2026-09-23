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
| Output | MP4 · HEVC main10 · AAC 256k stereo · SRT sidecars + `mov_text` |
| State | Plain JSON/JSONL files. No database. |
| UI | Server-rendered HTML + HTMX + SSE. No npm, no build step. |
| Platforms | macOS and Windows are the real targets. Linux is supported. |

---

## How to work here

**Match the document, not your instincts.** This project has an unusually opinionated
design, arrived at through a long technical discussion. Where `docs/ARCHITECTURE.md`
specifies something, follow it exactly — the schemas, the stage names, the terminology,
the folder layout. Where it is silent, use judgment and say what you assumed.

**Use the terminology in §2 verbatim.** Node, Drive, Disc, Title, Profile, Plan,
Master, Delivery, Job, Library. Stage names are `SCAN PLAN RIP OCR PACKAGE DELIVER
EJECT`, uppercase, in code and logs alike. No synonyms — not "convert" for PACKAGE,
not "source" for Master.

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

**HDR metadata (§9).** An HDR source encoded without `--master-display` / `--max-cll`
produces a valid file that plays back grey. It does not error. Any change near the
encoder needs this verified, not assumed.

**Apple TV codec limits (§4).** It cannot decode MPEG-2, VC-1, TrueHD, or DTS in any
form, and it cannot open MKV or read PGS subtitles. Every constraint in this project
descends from that list.

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

Phase 1 is **one node, one profile, movies only**. The architecture accommodates more
(multiple profiles, multichannel audio, music CDs, Dolby Vision, a NAS controller), and
structures exist so those are data rather than rewrites — but do not build them yet.
See §16 for what belongs in which phase.

When a task is ambiguous, prefer the smaller version and name what you left out.
