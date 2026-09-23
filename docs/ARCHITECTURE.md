# ARFABIT — Architecture

**A**utomatic **R**ipping **F**or **A**pple TV **B**itstream **I**nterchange **T**ool

Put a disc in. Get a file your Apple TV plays without thinking about it.

---

## 0. Invariants — read this first

If you read nothing else in this document, read this. These are settled decisions.
Do not re-litigate them, work around them, or "improve" them without being asked.

1. **Go. One static binary.** No Python, no Node, no Docker, no runtime dependency.
2. **Output is MP4 / HEVC main10 / AAC / `mov_text`.** Apple TV 4K native playback is
   the only target that matters.
3. **`os/exec` directly against `makemkvcon` and `ffmpeg`.** No wrapper libraries.
4. **State is plain files.** No database. Any index is a disposable cache.
5. **HDR metadata must be re-injected on every HDR re-encode.** MakeMKV preserves it
   in the Master (it only remuxes), and ffmpeg forwards the basic color tags — but
   mastering-display and MaxCLL are **not** carried to x265 automatically, and those
   are the two that drive tone mapping. Missing them produces a grey, washed-out file
   that does not error. Copy paths are unaffected. See §9.
6. **ARFABIT never removes user files.** No cleanup jobs, no retention timers.
7. **Never invent an error explanation.** Show the raw output. See §15.
8. **Subtitles are SRT, never ASS.** ASS forces a playback transcode. See §10.
9. **The UI never presents a frightening choice**, so it never needs reassuring
   language. See §15 tone table.

### Where to look

| Working on…             | Read                |
| ----------------------- | ------------------- |
| Anything at all         | §0, §2 terminology  |
| Ripping / disc handling | §5, §8, §17         |
| Encoding                | §9 — especially HDR |
| Subtitles               | §10                 |
| Naming / metadata       | §6, §11             |
| Web UI                  | §14, §15            |
| Files, config, state    | §6, §7              |

### Highest-consequence code

Three places where a bug is silent rather than loud:

- **HDR metadata propagation (§9)** — wrong output, no error, looks like a disc problem.
- **Glyph clustering (§10)** — a bad cluster merge corrupts every line using that glyph.
- **Atomic file writes (§6)** — a partial write to shared state is invisible until read.

---

## 1. What this is

A single binary that watches an optical drive, rips what you insert, encodes it to
something an Apple TV 4K plays natively, and files it where Plex expects it.

The web UI is the whole interface. No terminal stays open. No Docker required.

**Day-1 scope:** one machine, one profile, movies only (DVD / Blu-ray / UHD).

---

## 2. Terminology

These words are used consistently in code, UI, and logs. No synonyms.

| Term         | Meaning                                                             |
| ------------ | ------------------------------------------------------------------- |
| **Node**     | One machine running ARFABIT. Has an ID and a friendly name.         |
| **Drive**    | One optical drive on a node. Calibrated independently.              |
| **Disc**     | The physical thing. Has a type (dvd/bluray/uhd) and a volume label. |
| **Title**    | One playable item MakeMKV found on the disc.                        |
| **Profile**  | A named ruleset: container, encoder settings, audio, subtitles.     |
| **Plan**     | What is about to happen to _this_ disc. Auto-built, user-editable.  |
| **Master**   | The raw, untouched `.mkv` MakeMKV produced.                         |
| **Delivery** | The finished `.mp4` plus its sidecar subtitles.                     |
| **Job**      | One disc's trip through the pipeline. Has a `job.json`.             |
| **Library**  | Where Deliveries land, in Plex layout.                              |

### Pipeline stages

```
SCAN  →  PLAN  →  RIP  →  OCR  →  PACKAGE  →  DELIVER  →  EJECT
```

Stage names appear verbatim in logs and as filter chips in the UI.

---

## 3. Stack decisions

| Choice    | Decision                              | Why                                                                                             |
| --------- | ------------------------------------- | ----------------------------------------------------------------------------------------------- |
| Language  | **Go**                                | One static binary. No runtime, no venv, no PATH surgery. Cross-compiles to macOS/Windows/Linux. |
| Web UI    | **Server-rendered HTML + HTMX + SSE** | No npm, no bundler, no build step. Assets via `go:embed`.                                       |
| State     | **Plain files** (JSON / JSONL)        | Human-readable, rsync-able, safe on a NAS. See §6.                                              |
| Config    | **TOML**, layered                     | See §7.                                                                                         |
| Rip       | `makemkvcon` via `os/exec`            | Robot mode (`-r`) is machine-parseable. No wrapper libs.                                        |
| Encode    | `ffmpeg` + `libx265` via `os/exec`    | `-progress pipe:1` gives structured progress.                                                   |
| OCR       | glyph clustering + small OCR model    | See §10. Under 20 MB, fully offline.                                                            |
| Metadata  | **Offline IMDb index**                | No network at rip time. See §11.                                                                |
| Autostart | Written by the binary itself          | LaunchAgent / Task Scheduler / systemd user unit. See §13.                                      |
| Docker    | **Not used**                          | Cannot pass an optical drive through Docker Desktop on macOS or Windows.                        |

---

## 4. The output target

Apple TV 4K plays, natively:

- **Video:** H.264, HEVC (8/10-bit) up to 2160p60. **Not** MPEG-2, **not** VC-1.
- **Audio:** AAC, AC-3, E-AC-3. **Not** TrueHD, **not** DTS / DTS-HD MA.
- **Container:** MP4 / M4V. **Not** MKV.
- **Subtitles:** `mov_text` (timed text). **Not** PGS, **not** VOBSUB.

Everything in this document follows from that list.

Note that "no transcode" describes the _playback_ goal, not the pipeline: DVD (MPEG-2)
and some older Blu-rays (VC-1) must be re-encoded because the Apple TV cannot decode
them at all. Day 1 encodes everything to HEVC, with UHD offering a direct-copy option.

---

## 5. Repository layout

```
arfabit/
  cmd/arfabit/main.go          entrypoint, flags, startup
  internal/
    disc/                      detection + backends
      backend.go               DiscBackend interface
      makemkv/                 makemkvcon driver + robot-mode parser
    plan/                      profile matching, Plan construction
    pipeline/                  stage runner, job lifecycle
      rip.go  ocr.go  package.go  deliver.go
    encode/
      x265.go                  ffmpeg arg construction
      hdr.go                   HDR10 metadata probe + propagation
    subs/
      pgs.go                   PGS/VOBSUB decode to bitmaps
      cluster.go               glyph clustering
      recognize.go             OCR model driver
      correct.go               dictionary + bigram context pass
      srt.go                   SRT + mov_text writers
    meta/
      imdb.go                  offline index build + lookup
      naming.go                Plex naming rules
    estimate/                  per-drive/per-node calibration model
    store/                     file-backed state (§6)
    config/                    layered TOML (§7)
    doctor/                    dependency checks + fixes
    autostart/                 per-platform service install
    web/
      server.go  routes.go  sse.go
      templates/  static/
  docs/
  temp/                        reference clones (gitignored)
```

---

## 6. State on disk

Files are the source of truth. Any index is a disposable cache with a Rebuild button.

**The one rule that makes multi-node safe: a node writes only inside its own directory.**
No locking, no SQLite-over-NFS corruption, no coordination.

```
<data-dir>/                       # default ~/Library/Application Support/arfabit
  config.toml                     # shared settings (may live on a NAS)
  nodes/
    <node-id>/
      node.json                   # name, platform, drives
      calibration.json            # estimator model (§12)
      jobs/<job-id>.json
      log/<job-id>.txt
  library/
    index.jsonl                   # append-only, one line per Delivery
```

All writes are write-temp-then-`rename`, which is atomic everywhere.
A node renders the global view by reading every `nodes/*/` directory.

### Media layout

Masters are kept separate so they are one folder to manage.

**ARFABIT never removes a file it did not just create, and never removes a Master at
all.** Masters accumulate under `masters/`. When you want the space back, you delete
that folder yourself, in Finder or Explorer. There is no retention timer, no cleanup
job, and no "are you sure" dialog, because the program simply does not have that power.

```
~/Downloads/arfabit/
  masters/
    Blade Runner (1982)/
      master.mkv
      eng-sdh.srt   eng-forced.srt
      job.json      log.txt
  library/
    Blade Runner (1982)/
      Blade Runner (1982) {edition-Archive}.mp4
      Blade Runner (1982) {edition-Archive}.en.sdh.srt
      Blade Runner (1982) {edition-Archive}.en.forced.srt
```

The edition tag is **always** written, derived from the profile name. It lets a
re-encode coexist in Plex as a selectable edition instead of overwriting anything.
Sidecar names match the video filename exactly, which is what Plex requires;
`.sdh` and `.forced` are flags Plex understands.

---

## 7. Configuration

```
built-in defaults
  → <data-dir>/config.toml      shared across nodes
    → <local>/config.toml       this machine: paths, drives, node name
      → Plan override           this disc only
```

Last one wins. The UI shows provenance next to every setting
("5.1 E-AC-3 · inherited from shared config") so it is never a mystery which
file to edit.

No environment variables. They exist to serve Docker and systemd; we use neither.

---

## 8. Profiles

A profile is a named ruleset. A matcher decides which profile a disc gets,
evaluated most-specific-first.

```toml
[profile.Archive]
container   = "mp4"
video       = { codec = "hevc", encoder = "x265", preset = "slow", crf = 20, profile = "main10" }
audio       = [
  { codec = "aac", bitrate = "256k", layout = "stereo", default = true },
]
subtitles   = { ocr = true, formats = ["srt", "mov_text"], include = ["sdh", "forced"] }
chapters    = true
keep_master = "forever"

[match]
uhd    = { profile = "Archive", crf = 20, allow_copy = true }
bluray = { profile = "Archive", crf = 20 }
dvd    = { profile = "Archive", crf = 18 }
```

### Space check

The Plan will not start a job it cannot finish. Before offering a Plan, compare the
estimated total (Master + Delivery, from §12) against free space on the target volume.

ARFABIT does not monitor disk space in the background and shows no space meter during
normal use. It speaks up at exactly one moment: when the disc in the drive will not
fit. That message states four numbers and nothing else:

- what this rip is estimated to need
- how much room is left on the drive
- how large `masters/` currently is
- how large `library/` currently is

Those last two are there because they are almost always the answer — the user has
Masters they no longer need, and this is the moment they would want to know it.
ARFABIT offers to open that folder. It does not offer to remove anything (§0.6).

Within about 10% of the threshold, the Plan shows the same four numbers as a note
rather than a stop, since an estimate can run low.

Matcher dimensions:

- `source` — `uhd` / `bluray` / `dvd`
- `height` — 2160 / 1080 / 720 / 576 / 480
- `aspect` — from `ffmpeg cropdetect` on sampled frames

Aspect matters mostly for DVDs, where 4:3 and letterboxed 16:9 want different
treatment. Cropping is **never** automatic — cropdetect misfires on dark scenes —
but the Plan offers it with the savings shown.

**Day 1 ships exactly one profile.** The matcher structure exists from the start so
that adding more is data, not code.

---

## 9. Encoding

### Defaults

| Source        | CRF | Preset | Profile |
| ------------- | --- | ------ | ------- |
| UHD 2160p     | 20  | slow   | main10  |
| Blu-ray 1080p | 20  | slow   | main10  |
| DVD 480/576p  | 18  | slow   | main10  |

Presets offered: `superfast, medium, slow, slower, veryslow`. Default `slow`.
No hardware encoders — quality per bit is meaningfully worse, and this is an archive.

**main10 always**, even for SDR sources. 10-bit internal precision reduces banding
in gradients at essentially no size cost, and Apple TV plays HEVC Main10 natively.

### UHD: copy or re-encode

UHD discs are already HEVC, so the Plan offers both, with sizes:

```
○ Copy the picture exactly          54.2 GB  ·  2 min
● Re-encode HEVC CRF 20 slow       ~22 GB    ·  ~6 hr
```

Copy size is exact from the scan. Encode size comes from the estimator (§12).

### HDR

**This is the highest-consequence part of the encoder.** An HDR source encoded
without its color metadata produces a valid file that plays back washed-out and
grey. It does not error. It just looks wrong.

MakeMKV does not cause this problem — it remuxes, so the Master's HEVC bitstream still
carries its HDR10 SEI messages intact. The loss happens at **our** re-encode: once frames
are decoded and handed to x265, the metadata survives only as ffmpeg stream side-data.
ffmpeg forwards `color_primaries` / `color_trc` / `colorspace` to libx265 on its own, but
**mastering-display and content-light-level are not forwarded** — and those are precisely
the fields a TV uses to tone map. They must be probed and passed explicitly.

Probe the source with `ffprobe` (stream `side_data`), then propagate every field:

```
--profile main10 --output-depth 10
--colorprim bt2020 --transfer smpte2084 --colormatrix bt2020nc
--master-display "G(...)B(...)R(...)WP(...)L(...)"    # from source MDCV
--max-cll "<MaxCLL>,<MaxFALL>"                        # from source CLL
--hdr10 --hdr10-opt
```

If the source declares HDR but the metadata cannot be read, the job **stops and
asks** rather than producing a grey file.

On the **UHD direct-copy** path none of this applies — the bitstream is never decoded,
so the metadata travels untouched. Still verify it landed in the MP4's `mdcv` and `clli`
boxes after muxing; a copy that silently drops them fails the same way.

**Dolby Vision and HDR10+ are deferred.** UHD discs carry DV Profile 7 (dual layer);
Apple TV wants Profile 5 or 8.1, and converting needs `dovi_tool` plus a class of
failure modes we do not want on day 1. Every DV disc has an HDR10 base layer that
looks excellent. The Plan says so plainly when it applies.

### Audio

**Copy when the codec is already native, encode when it is not** — the same rule as
video (§4). Apple TV decodes AC-3, E-AC-3 and AAC, and most Blu-rays carry a DD 5.1
track alongside the lossless one, so surround usually costs nothing.

| Source track | Action |
| --- | --- |
| AC-3 / E-AC-3 / AAC | **copy** (`-c:a copy`) — bit-perfect, no encode time |
| TrueHD / DTS-HD MA / DTS / PCM | encode |

**An AAC stereo track is always added as a fallback**, whether or not a copy was
available.

| Target | Codec | Bitrate |
| --- | --- | --- |
| Stereo fallback (always present) | AAC-LC | 256k VBR |
| 5.1 / 7.1 when encoding is required (later) | E-AC-3 | 768k |
| 5.1 alternate (later) | AC-3 | 640k (format ceiling) |

Track order matters: Apple TV selects the first compatible track, so multichannel
is listed first when present, with the stereo fallback after it.

---

## 10. Subtitles

**SRT, not ASS.** ASS requires a rendering engine, so Plex burns it into the picture,
which forces a full video transcode at playback — the exact thing this project exists
to avoid. SRT is handed to the client as text and direct-plays. There is also no
fidelity to gain: the source is bitmaps, so any styling would be invented.

Output: **SRT sidecars** (for Plex) **plus an embedded `mov_text` track** (for playing
the file directly on an Apple TV). Both are cheap.

### OCR pipeline

PGS subtitles use one font for an entire disc. Exploit that.

**Stage 1 — glyph clustering.** Render every subtitle image, segment into individual
glyph bitmaps, cluster identical shapes. ~60,000 glyph instances in a film collapse to
roughly **100–150 unique shapes**. This is pixel matching, not recognition, so it is exact.

**Stage 2 — recognize ~150 images, not 60,000.** A small OCR model labels the cluster
representatives. Every occurrence inherits the answer, so there is no per-line drift —
an `I` never becomes an `l` in one scene and not another.

**Stage 3 — context pass.** Hunspell dictionary + bigram table over the assembled SRT
fixes genuine ambiguities (`rn`/`m`, spacing, line joins).

**Stage 4 — flag the rest.** Anything still uncertain is surfaced in the UI so you
eyeball ten lines, not fifteen hundred.

Footprint: PaddleOCR mobile recognition (~10 MB) + dictionary (~5 MB). **Under 20 MB**,
shipped in the binary, fully offline. Per-disc glyph dictionaries are cached and reused.

**Forced subtitles** are identified separately and default to **on**. On most Blu-rays
the alien-language and signage translations are a distinct PGS track flagged `forced`,
not burned into the picture. They are usually under 100 lines — the cheapest possible
place to spend OCR effort, and the highest value.

---

## 11. Metadata — offline

No network at rip time.

**Source:** IMDb `title.basics.tsv.gz`, filtered to `movie` + `tvMovie`, keeping
`tconst / primaryTitle / originalTitle / startYear / runtimeMinutes`.
About 700k rows, **~60 MB** on disk.

**Matching** uses two signals the disc hands us:

1. **Runtime** from the MakeMKV scan, matched ±2 min. Narrows 700k titles to a few hundred.
2. **Volume label**, cleaned and fuzzy-matched against that narrowed set.

The Plan shows the top three candidates with years and runtimes, best pre-selected.
You confirm or type your own. Never a silent wrong rename.

**Updates:** IMDb publishes no deltas, only full files. So:

- Default is **never auto-update**. Titles and runtimes of existing films do not change.
- Settings has a manual **Refresh** button showing the current index date,
  and an optional monthly check, **off** by default.
- **TV checkbox**, off by default, roughly triples the index.
- **Optional online lookup**, off by default, for discs the offline index cannot pin down.

Licensing: IMDb datasets are free for personal, non-commercial use. The index is
therefore **downloaded on first run**, not bundled in the released binary.

---

## 12. The estimator

After a few jobs, ARFABIT knows how long _your_ hardware takes and how big _your_
files get. No other ripper does this.

The model is keyed on **(node, drive, profile, source type)** — because rip speed is a
property of the drive and encode speed is a property of the CPU, they calibrate
independently. Two drives of different speeds on one machine converge separately.

```jsonc
// calibration.json
{
  "drives": {
    "/dev/disk4": { "rip_mbps": { "bluray": 22.4, "dvd": 8.1 }, "samples": 7 },
  },
  "encode": {
    "x265/slow/1080p": { "fps": 4.7, "bpp": 0.061, "samples": 5 },
  },
}
```

- **Time** = source duration x frames x observed fps.
- **Size** = bits-per-pixel observed at this CRF x resolution x duration.

Seeded with conservative priors so estimates exist from job one, then weighted toward
recent observations. The UI shows confidence honestly: _"about 6 hours"_ after five
samples, _"roughly 4–9 hours"_ after one.

---

## 13. Running as a service

Autostart is a toggle in the web UI. The binary writes the right thing per platform.

| Platform | Mechanism                                        | Note                                                                       |
| -------- | ------------------------------------------------ | -------------------------------------------------------------------------- |
| macOS    | LaunchAgent in `~/Library/LaunchAgents`          | **Agent, not Daemon** — needs the user session for drive and folder access |
| Windows  | Task Scheduler, at-logon                         | More reliable than a Run key; restarts on failure                          |
| Linux    | systemd **user** unit + `loginctl enable-linger` | Lingering is what survives logout                                          |

The toggle also shows live status ("Running, started 3 days ago") and a Restart button.

---

## 14. The web UI

### Logging is a feature, not an afterthought

- **Append-only over SSE.** The page never reloads. New lines append to a virtualized
  list. Scroll position, text selection, and search are never disturbed.
- **Follow toggle**, on by default, auto-disengages the moment you scroll up,
  re-engages via "Jump to live".
- **Filter box that persists** across navigation, plus level and stage chips
  (SCAN / RIP / OCR / PACKAGE).
- **Stable line IDs** so re-rendering a filter never scrambles anything.
- **Per-job plain-text log on disk**, with a Download button. Copy-paste always works.

### Doctor

First screen on a fresh install. Checks `makemkvcon`, `ffmpeg`, MakeMKV license and
expiry, drive presence, free disk space, and the metadata index. Each failure gets a
copy-pasteable fix and a "Fix this for me" button where that is safe.

MakeMKV beta keys expire about every 60 days. Doctor checks expiry at launch and
refreshes silently — the failure that ARM users hit constantly never surfaces.
If auto-fetch fails, Settings links the purchase page, the forum, and r/makemkv,
plus the exact path to `settings.conf` on this platform.

### Transcode Lab

Operates on an existing Master. Pick a timestamp (`01:23:45`) and a duration
(5 / 10 / 15 / 30 / 60 / 120 / 600 s), then
render that same clip under several settings. The Lab produces **files you watch on
your own TV** — it does not pick a winner and does not change your settings. Judging
is yours.

Alongside the files, a comparison table. Every column is normalised so the best entry
reads 100%, with the basis stated per column:

| Column | Basis for 100% |
|---|---|
| File size | the smallest result |
| Size per hour | extrapolated from clip length, smallest result |
| Encode time | the fastest result |
| Average bitrate | the lowest result |
| VMAF | a theoretical bit-perfect 100, not the best entry |

VMAF is deliberately scored against the absolute rather than the field, so a row
reading 94% means "94% of perfect", not "best of a bad set".

A single clip lies — a dark, static scene flatters every bitrate; falling snow
destroys them all. Auto-sampling three clips by scene complexity is **phase 2**.

---

## 15. Error policy

> **Never replace. Always augment. Never guess.**

ARM's central mistake is substituting a guessed explanation for the real error, and
users burn hours chasing the guess. So:

- A plain-language line appears **only** on a tested signature match — exact exit code
  plus a verified stderr pattern.
- The verbatim output is always one click away under "Technical details", always complete.
- Anything unmatched gets an honest generic: _"ARFABIT did not finish this disc.
  Here is exactly what the ripper reported:"_ followed by raw output. No invented cause.
- Every message says what ARFABIT **did**: _"The master file is still in your cache.
  Nothing was removed."_

The signature list stays small and every entry earns its place. A wrong explanation
costs more than no explanation.

### Tone

Safety by construction, not reassurance. Nothing in the UI mentions risk, because
nothing in the UI presents a scary choice. Word substitutions:

| Not this     | This           |
| ------------ | -------------- |
| delete       | remove         |
| overwrite    | replace        |
| failed       | did not finish |
| abort / kill | stop           |

---

## 16. Roadmap

**Phase 1 — day 1**
Single node. One profile. Movies. MP4 / HEVC main10 / AAC stereo / OCR'd SRT.
makemkvcon-based detection. Offline IMDb naming. Manual Transcode Lab. Autostart.

**Phase 2**
Multiple profiles and matchers · multichannel E-AC-3 · OS-level disc detection ·
auto-sample Transcode Lab · "fix my subtitle file" standalone tool · TV series naming.

**Phase 3**
Music CDs (separate backend — MakeMKV cannot read CDDA; needs libcdio + MusicBrainz) ·
Dolby Vision (P7 → P8.1 via `dovi_tool`) · HDR10+ · `arfabit-control` light web
controller for NAS targets · local text LLM for the OCR context pass.

---

## Appendix A. makemkvcon robot mode

Observed from `makemkvcon -r --cache=1 info disc:N` (v1.18.4). This is a field guide,
not a spec — MakeMKV publishes no formal one. Extend it as new discs reveal attributes.

### Line grammar

```
TYPE:field,field,...        fields are CSV; strings are quoted; quotes escaped as \"
```

| Line | Shape |
|---|---|
| `MSG` | `code,flags,argcount,"rendered","format",args...` |
| `DRV` | `index,visible,?,flags,"drive name","disc label","device"` |
| `TCOUNT` | `count` — titles **above `--minlength`**, default 120s |
| `CINFO` | `attr,code,"value"` — disc level |
| `TINFO` | `title,attr,code,"value"` |
| `SINFO` | `title,stream,attr,code,"value"` |

`MSG` carries a **stable numeric code plus a format string with arguments broken out
separately**. Error handling keys on the code, never on the rendered English (§15) —
that survives wording changes and localization.

### Message codes seen

| Code | Meaning | Handling |
|---|---|---|
| 1005 | version banner | informational |
| 1009 | "default profile missing" | **suppress** — appears every run, harmless |
| 1011 | "Using LibreDrive mode" | good: raw access engaged |
| 2010 | "opened in OS access mode" | degraded access; usually another process holds the drive |
| 5010 | "Failed to open disc" | **expected and ignorable after a `disc:9999` probe**; a real error otherwise |

`disc:9999` is a pseudo-index that enumerates drives, then fails to open disc 9999.
Treating its trailing 5010 as an error is a bug.

### DRV

Sixteen slots always print. **`visible == 256` means an empty slot, not a drive.**
Filter on it or the UI shows sixteen phantom drives.

### CINFO (disc)

| Attr | Meaning | Example |
|---|---|---|
| 1 | disc type | `Blu-ray disc` |
| 2 | **disc name** | `The Sheep Detectives` |
| 28 / 29 | language code / name | `eng` / `English` |
| 30 | display name | |
| 32 | **volume label** | `THE_SHEEP_DETECTIVES` |

Attr 2 is already a human-readable title, cleaner than the volume label. §11 should try
attr 2 first and fall back to attr 32.

### TINFO (title)

| Attr | Meaning | Example |
|---|---|---|
| 2 | name | `The Sheep Detectives` |
| 8 | chapter count | `16` |
| 9 | duration | `1:49:04` |
| 10 | size, human | `31.1 GB` |
| 11 | **size, bytes** | `33457569792` |
| 16 | source playlist | `00001.mpls` |
| 27 | suggested output filename | `..._t00.mkv` |
| 30 | summary | `... - 16 chapter(s) , 31.1 GB` |

Attr 11 and 9 drive main-feature selection and the space check (§8).
Attr 16 is how playlist obfuscation is spotted: many titles sharing or cycling `.mpls`
names with near-identical durations.

### SINFO (stream)

| Attr | Meaning | Example |
|---|---|---|
| 1 | stream type | `Video` / `Audio` / `Subtitles` |
| 2 | layout | `Surround 7.1` |
| 3 / 4 | language code / name | `eng` / `English` |
| 5 | **codec id** | `V_MPEG4/ISO/AVC`, `A_TRUEHD` |
| 6 / 7 | codec short / long | `TrueHD` / `TrueHD Atmos` |
| 13 | bitrate | `128 Kb/s` |
| 14 | channel count | `8` |
| 17 / 18 | sample rate / bit depth | `48000` / `24` |
| 19 | **resolution** | `1920x1080` |
| 20 | aspect | `16:9` |
| 21 | frame rate | `23.976 (120000/5005)` |
| 30 | summary | `TrueHD Surround 7.1 English` |
| 38 / 39 | flag chars / names | `d` / `Default` |
| 40 | channel layout | `7.1` |
| 42 | conversion note | `( Lossless conversion )` |

**Trap: attr 28/29 are not the stream's language.** On a French subtitle stream, attr 3/4
correctly read `fra`/`French` while attr 28/29 read `eng`/`English` — the *title's*
language leaking into the stream record. **Always use attr 3/4 for stream language.**
Using 28/29 silently tags every track as English.

**Duplicate tracks are normal and indistinguishable at scan time.** A disc may list two
identical-looking `PGS English` streams and four `PGS French`. Every exposed attribute
matches; the real difference is standard vs SDH, or France vs Canadian French. Resolve
it by content, not metadata:

1. Before OCR, compare bitmap count, byte size, and first/last timestamps. Exact
   duplicates are dropped without OCR cost.
2. After OCR, detect SDH by `[SOUND EFFECT]` markers, musical notes, and `SPEAKER:`
   prefixes, and label the track accordingly — which is what the Plex sidecar name
   needs anyway.

**Open problem: forced-subtitle detection.** MakeMKV marks forced tracks only inside the
attr 30 summary string — `PGS English  (forced only)`, with two spaces. There is no
dedicated attribute. Parsing English prose is exactly what §15 warns against, so treat a
match as a *hint*, corroborate with attr 38/39 flags and track size, and let the Plan
show what was inferred rather than asserting it.
