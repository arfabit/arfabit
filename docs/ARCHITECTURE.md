# ARFABIT — Architecture

**A**utomatic **R**ipping **F**or **A**pple TV **B**itstream **I**nterchange **T**ool

Put a disc in. Get a file your Apple TV plays without thinking about it.

---

## 0. Invariants — read this first

If you read nothing else in this document, read this. These are settled decisions.
Do not re-litigate them, work around them, or "improve" them without being asked.

1. **Go. One static binary.** No Python, no Node, no Docker, no runtime dependency.
2. **Output is MKV / HEVC main10 / sound that plays directly / SRT.** Direct play in
   the Plex app on an Apple TV 4K is the target that matters, as tested (§4). MP4 is
   made only when a project asks for one, for players that cannot open MKV, and
   nothing about how it plays is assumed (§4, §8).
3. **`os/exec` directly against `makemkvcon` and `ffmpeg`.** No wrapper libraries.
4. **State is plain files.** No database. Any index is a disposable cache.
5. **HDR metadata must be re-injected on every HDR re-encode.** MakeMKV preserves it
   in the Original (it only remuxes), and ffmpeg forwards the basic color tags — but
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
- **Reading subtitles (§10)** — a line read wrong still looks like a line. Lines of
  low confidence are listed on the task and in the log, never passed off as read.
- **Atomic file writes (§6)** — a partial write to shared state is invisible until read.

---

## 1. What this is

A single binary that watches an optical drive, rips what you insert, encodes it to
something an Apple TV 4K plays natively, and files it where Plex expects it.

The web UI is the whole interface. No terminal stays open. No Docker required.

**Day-1 scope:** one machine, movies only (DVD / Blu-ray / UHD). Plans start from
the defaults or from a blueprint chosen by hand (§8); no automatic matching yet.

---

## 2. Terminology

These words are used consistently in code, UI, and logs. No synonyms.

| Term          | Meaning                                                                  |
| ------------- | ------------------------------------------------------------------------ |
| **Node**      | One machine running ARFABIT. Has an ID and a friendly name.              |
| **Drive**     | One optical drive on a node. Calibrated independently.                   |
| **Disc**      | The physical thing. Has a type (dvd/bluray/uhd) and a volume label.      |
| **Title**     | One playable item MakeMKV found on the disc.                             |
| **Defaults**  | The settings every Plan starts from when no blueprint is used.           |
| **Blueprint** | A recipe that fills a Project in against what an Original holds: named settings that override the defaults. Once used, only its line items remain; never part of a Job. |
| **Plan**      | What is about to happen, for _this_ job. Auto-built, user-editable.      |
| **Original**  | The disc as it is: every track, subtitles still inside, as MakeMKV copies it. Kept beside its films as the edition `Original` (§6). Always kept; only the user removes one (§0.6). |
| **Project**   | What you set up to be done: an original, and what to make from it — line items, and the containers to make them into, one file each. Starting it makes tasks. |
| **Line item** | One track of the Original in a Project, and what to do with it: copy as is, or convert. |
| **Delivery**  | The finished `.mkv` plus its sidecar subtitles.                          |
| **Low confidence** | A subtitle OCR may have read wrong: nothing was read, what was read holds a mark known to be a misreading, or a second reading says other words. ARFABIT's own measure; no reader's score goes into it (§10). |
| **Edition**   | A Plan's version name, written as `{edition-...}` in the Delivery's filename. Plex's word. Blank means none. |
| **Task**      | One step a project becomes, in the queue: a copy of a disc to its Original, one subtitle track of an Original read into text (OCR), or a package (a film) or a clip made from an Original. A film planned with its disc is a task of its own that waits for the copy. In code, and in the files on disk, a **job**. |
| **Library**   | Where originals and Deliveries land, one folder per film, in Plex layout (§6). |

### What waits on what

Two things are scarce, and they are scarce for different reasons.

**The drive** can read one disc at a time. It is held during SCAN and RIP and
released at EJECT, which is why ejecting comes early: from that point the next
disc can go in while the last one converts.

**The processor** could in principle run several conversions at once, and
should not. x265 already uses every core, so a second conversion finishes both
later than running them in turn would, and makes a nonsense of both estimates.
Packaging and lab clips therefore share a small number of slots, one by default
(`machine.max_conversions`), and whatever cannot start waits at stage QUEUED.

The result is the arrangement that matters for ripping a shelf of discs: the
drive never idles, and the processor works through a queue behind it.

### Pipeline stages

```
copy:       SCAN  →  PLAN  →  RIP  →  EJECT          the original, renamed into place
OCR:                                    OCR           one task per chosen subtitle track
package:                                QUEUED  →  PACKAGE  →  DELIVER
```

Stage names appear verbatim in the logs and on the page.

**Copy includes OCR.** Once the disc is copied, each subtitle track the Plan
chose is read into text from the original, as a task of its own at stage OCR,
into the SRT beside the original (§10). Reading takes a minute or two and
little processor, so an OCR task takes no processor slot, and any number run
at once. A problem reading them never costs the film: it is still delivered,
and the task says what happened.

**A package takes the original's SRTs at DELIVER,** not before, so a fix made
while it was converting is in the film. A converted subtitle line item names
its track; if that track is still being read, DELIVER waits for it, and if
nobody has read it, it is read then.

**A disc is two jobs.** The rip makes the Original and ends when the disc is out.
If the Plan asked for a transcode, pressing Start also makes a second job, which
waits in QUEUED ("Waiting for its original") until the rip finishes, then makes
the Delivery from the Original. They are planned together only because the Plan
is where the disc's contents are known; they need different things (the drive,
the processor) and are stopped separately. The transcode records its rip as
`from`. A rip that is stopped or does not finish takes its waiting transcode
off the queue, saying why.

**Each step stays open to change until it starts.** The copy can start the
moment the disc is read, since the scan already knows every track, and the Plan
stays on the page while it runs. The film's name and the subtitle tracks to
read can be changed until the copy is finished: that is when the original is
named, and its subtitles begin. A name changed while copying moves the copy to
the folder for that name when it is renamed, and the folder made for the old
name goes if nothing else is in it. The film — its line items, its edition,
and whether to make one at all — can be changed until it leaves the line for
the processor; a change made after the original exists is pointed at the
original's tracks as the first one was. Each step takes what the Plan says
under the same lock a change is made under, so a change is either in the step
or refused with a sentence saying that step has started.

**QUEUED** is waiting for a processor slot, shown as a stage of its own because
waiting and working otherwise look the same. A job that gets a slot straight away
passes through it without stopping. Whatever waits, waits in a line that can be
rearranged from the page, and only the front of the line is given a slot, so the
order shown is the order things start in. A Project joins the line held for ten
seconds before it may start, so one added by mistake or in the wrong place can be
stopped or moved before anything has been done; the page calls this "Getting
things ready". **LAB** is the stage a package job making
test clips runs in (§14); it is not part of a disc's trip and takes a slot the
same way PACKAGE does.

**EJECT comes straight after RIP**, not at the end. Once the Original exists the
disc has nothing left to give, and everything after it happens on the copy.
Packaging takes hours; holding the disc through that keeps it for no reason and
keeps the drive spinning.

---

## 3. Stack decisions

| Choice    | Decision                              | Why                                                                                             |
| --------- | ------------------------------------- | ----------------------------------------------------------------------------------------------- |
| Language  | **Go**                                | One static binary. No runtime, no venv, no PATH surgery. Cross-compiles to macOS/Windows/Linux. |
| Web UI    | **One HTML page + plain JavaScript over a JSON API + SSE** | No npm, no bundler, no build step, no framework. Assets via `go:embed`.       |
| State     | **Plain files** (JSON / JSONL)        | Human-readable, rsync-able, safe on a NAS. See §6.                                              |
| Config    | **TOML**, layered                     | See §7.                                                                                         |
| Rip       | `makemkvcon` via `os/exec`            | Robot mode (`-r`) is machine-parseable. No wrapper libs.                                        |
| Encode    | `ffmpeg` + `libx265` via `os/exec`    | `-progress pipe:1` gives structured progress.                                                   |
| OCR       | A reader the computer already has     | Vision on macOS, Windows.Media.Ocr on Windows, Tesseract elsewhere if installed. See §10.       |
| Metadata  | **Offline IMDb index**                | No network at rip time. See §11.                                                                |
| Autostart | Written by the binary itself          | LaunchAgent / Task Scheduler / systemd user unit. See §13.                                      |
| Docker    | **Not used**                          | Cannot pass an optical drive through Docker Desktop on macOS or Windows.                        |

---

## 4. The output target

The goal is **direct play**: the file reaches the screen as it is, with nothing
converted by the media server on the way. What plays directly depends on the player
as much as the television, so this is a table of what was **tested**, per device and
player. A format absent from it is untested, not assumed to work or to fail — the
same rule as §15. Other devices are added as they are tested, never from a spec sheet.

### Plex app on Apple TV 4K

Tested 2026-09-25 with one-minute clips cut from real originals, read off Plex's
Dashboard while playing, with the server's video transcoding turned off.

| | Result |
|---|---|
| **Container** MKV | direct play |
| **Picture** HEVC main10 · H.264 as on the disc | direct play |
| **Sound, lossy** AAC stereo and 7.1 · AC-3 5.1 · E-AC-3 5.1 · DTS 5.1 | direct play |
| **Sound, lossless** DTS-HD MA 7.1 · FLAC 5.1 and 7.1 · ALAC 5.1 · PCM 5.1 | direct play |
| **Sound** Dolby TrueHD 5.1 | **converted by the server (to Opus) on every play** |
| **Subtitles** SRT in MKV | direct play |
| **Subtitles** PGS | plays only if the server converts the picture, to burn them in |

Not yet tested: MPEG-2 and VC-1 pictures (DVDs and some older Blu-rays), which
are re-encoded regardless; HDR; Dolby Atmos; MP4 at all, with its own subtitle
format or with SRT beside it; a sidecar SRT beside an MKV (SRT inside MKV is). Not
knowable from the Dashboard: whether the Apple TV plays DTS-HD MA in full or only
the lossy core every DTS-HD MA track carries inside it.

Everything in this document follows from that table.

Note that "no transcode" describes the _playback_ goal, not the pipeline: DVD (MPEG-2)
and some older Blu-rays (VC-1) are re-encoded, and so is every other picture today
(§9), with UHD offering a direct-copy option.

---

## 5. Repository layout

```
arfabit/
  cmd/arfabit/main.go          entrypoint, flags, startup, wiring
  internal/
    disc/                      Disc/Title types, main-feature selection
      makemkv/                 makemkvcon driver, robot-mode parser, rip, drive health
    drive/                     per-platform drive enumeration
    eject/                     per-platform eject
    pipeline/                  stage runner, job lifecycle
      runner.go                SCAN → DELIVER for a disc
      plan.go  audio.go        Plan construction, audio track rules
      package.go               package jobs: clips and whole-Original Deliveries, from line items
      reading.go               OCR tasks: a track of an original read into the SRT beside it
      subtitles.go             a track read into SRT, with low confidence; a clip's own subtitles
      recipe.go                blueprints as recipes; an Original's or a disc's tracks
    playback/                  what was tested to play where (§4), as notes
      slots.go                 processor slots (QUEUED)
      estimate.go  rate.go     calibration model (§12), read speed
      space.go                 space check (§8)
      resume.go                jobs left behind by a restart
      log.go                   per-job log
    ffmpeg/
      encode.go                ffmpeg/x265 arg construction
      hdr.go                   HDR10 metadata probe + propagation
      probe.go  run.go         ffprobe, subprocess + progress
    lab/                       clip rendering and the comparison table
    blueprints/                blueprints made on the page (blueprints.json)
    subs/                      PGS decode, a track read from an original, SRT writing
    ocr/                       the computer's own text reader (§10): Vision, Windows, Tesseract
    meta/
      index.go                 offline IMDb index build + lookup
      naming.go                Plex naming rules
    store/                     file-backed state (§6)
    config/                    layered TOML (§7)
    doctor/                    dependency checks
    autostart/                 per-platform service install
    restart/                   restarting ARFABIT in place, from the page
    web/
      server.go  pages.go  events.go  drives.go
      templates/  static/
  docs/
  temp/                        reference clones (gitignored)
```

---

## 6. State on disk

Files are the source of truth. Any index is a disposable cache.

**The one rule that makes multi-node safe: a node writes only inside its own directory.**
No locking, no SQLite-over-NFS corruption, no coordination.

```
<data-dir>/                       # default ~/Library/Application Support/arfabit
  config.toml                     # shared settings (may live on a NAS)
  config.local.toml               # this machine's settings (see §7)
  blueprints.json                 # blueprints made on the page (§8)
  titles.json                     # the offline film list (§11)
  nodes/
    <node-id>/
      calibration.json            # estimator model (§12)
      drives.json                 # what each drive does when a disc goes in (§14)
      jobs/<job-id>.json
      log/<job-id>.txt
  library/
    index.jsonl                   # append-only, one line per Delivery
```

Three files outside `nodes/` are written by a node, and each is an exception to
the rule above:

- `blueprints.json` is shared on purpose: blueprints are global, like
  `config.toml`. Two nodes saving at the same moment means the later save wins.
- `titles.json` is the film list, downloaded from the page. It is the same for
  every node.
- `library/index.jsonl` gets one line appended per Delivery, by whichever node
  made it.

The rule for every write is write-temp-then-`rename`, which is atomic everywhere.
Not every write follows it yet (§16).
A node renders the global view by reading every `nodes/*/` directory.

### Media layout

Originals and films share one folder per film, named for Plex. The original is
an edition of its own, `Original`, so to Plex it is one more version of the film:

```
~/Downloads/arfabit/
  library/
    Blade Runner (1982)/
      Blade Runner (1982) {edition-Original}.mkv      # the original
      Blade Runner (1982) {edition-Original}.en.srt   # its subtitles read into text (§10)
      Blade Runner (1982).mkv                         # a film, no edition
      Blade Runner (1982).en.srt                      # a copy of them
      Blade Runner (1982) {edition-Small}.mkv         # a film, edition "Small"
  clips/
    Blade Runner (1982)/
      Blade Runner (1982) {edition-Lab 001 - Defaults - 1h15m20s}.mkv
      Blade Runner (1982) {edition-Lab 001 - Small - 1h15m20s}.mkv
```

MakeMKV names its file its own way, in the film's folder; once the copy is
finished ARFABIT renames it to the original's name. A rename, never a removal:
if something already has that name, the copy keeps MakeMKV's, and the log says
so. A copy that is stopped part way stays under MakeMKV's name, and the task
says where. No film can take the edition `Original`.

The original keeps every track, its picture subtitles included. Plex plays it
without converting the picture unless those are switched on; its Dolby TrueHD
sound, where it has some, Plex converts on every play (§4). Choosing it is the
user's business.

**ARFABIT never removes a file it did not just create, and never removes an
Original at all.** When you want the space back, you remove an original
yourself, in Finder or Explorer. There is no retention timer, no cleanup job,
and no "are you sure" dialog, because the program simply does not have that
power.

The job record and log live in the data directory (above), not beside the
Original.

A Delivery carries its Plan's edition as a tag. "Edition" is Plex's word, and the
tag is how Plex keeps several versions of one film side by side as selectable
editions. A blank edition means no tag. Braces are left out of an edition, since
they would end the tag early. Lab clips use the same shape, with a run number, the blueprint
(or "Defaults") and the position as the edition, so a media manager pointed at `clips/` sees one title
with several editions to play in turn. The run number comes from what is already in
the folder, so it survives restarts.
Sidecar names match the video filename exactly, which is what Plex requires;
`.sdh` and `.forced` are flags Plex understands, which ARFABIT does not set yet
because it does not yet tell those tracks apart (Appendix A).

**The SRT beside the original is the SRT.** OCR writes it there, fixes are
made to it, and every film made from that original gets a copy (§10). A copy
ARFABIT made and nobody has changed since, proved by its SHA-256, is the one
file ARFABIT replaces, to bring it up to date with a fix; anything else is
never replaced. Before
removing an original, make sure its SRTs are where you want them: once it is
gone, that film's subtitles cannot be read or fixed again from their
pictures. The pictures of low-confidence subtitles are kept in the node's own
folder (`nodes/<id>/ocr/<task>/`), so they can still be shown after an
original is removed.

Any number of tracks in one language can be read beside an original. The
first picture track of a language, in the order the original holds them,
gets the plain name (`.en.srt`); the second and after are numbered by their
place (`.en.2.srt`, `.en.3.srt`). Whether Plex reads a language from a name
like `.en.2.srt` is untested. A film still gets one SRT per language, named
after the film (`.en.srt`), so only one track of each language can go into
one film as text.

---

## 7. Configuration

```
built-in values
  → <data-dir>/config.toml         shared across nodes
    → config.local.toml            this machine: paths, node name
      → Plan override              this job only
```

`config.local.toml` lives in the platform's default data directory and is read
first for one setting, `paths.data`, since that decides where the shared file is.

Settings are grouped by what they belong to:

| Section | Belongs to | Typically in |
|---|---|---|
| `[node]`, `[paths]`, `[server]` | this computer | local |
| `[machine]` `max_conversions` | this computer's processor | local |
| `[drive]` `read_cache_mb` | this computer's drives (all of them, for now) | local |
| `[makemkv]` `min_title_seconds` | how discs are read, everywhere | shared |
| `[defaults]` | the defaults (§8) | shared |
| `[blueprint.<Name>]` | blueprints (§8) | shared |

`config.example.toml` in the repository is the annotated reference.

Last one wins. The UI shows provenance next to every setting
("5.1 E-AC-3 · inherited from shared config") so it is never a mystery which
file to edit.

No environment variables of our own. They exist to serve Docker and systemd; we use
neither. (The platform's own `APPDATA` / `XDG_CONFIG_HOME` are honoured when finding
the default data directory.)

---

## 8. Defaults and blueprints

Every Plan is filled in at PLAN from one of two things:

- **The defaults**, `[defaults]` in the settings file. They always exist, since
  ARFABIT has built-in values for every one.
- **A blueprint**: a starter template for planning a job to rip or transcode. It
  has a name and overrides some of the defaults. A blueprint that differs only in
  quality says only that.

Blueprints are optional. With none at all, every Plan starts from the defaults
and ARFABIT works the same. When there are several, one of them may be chosen on
the page to be **used by default**, and every new Plan then starts from it without
asking. Choosing none, or removing the one chosen, returns new Plans to the
defaults.

Either way, the values are **copied** into the Plan: quality per disc type,
preset, whether to copy UHD video, audio choices, subtitle choices, whether to
convert after ripping. From then on they are the Plan's own settings. The user
can change any of them for this job, and changing, renaming or removing a
blueprint afterwards has no effect on a job that already has a Plan. The job
record holds everything needed to run or re-run it and never looks anything up
again. A blueprint is what you draw from, not the thing built.

### Edition

Every Plan has an **edition**, the one setting that is ARFABIT's own rather than a
parameter for `makemkvcon` or `ffmpeg`. It names the Delivery
(`{edition-<edition>}`, §6). It is blank by default, which means no edition.

Every blueprint has an edition too, which starts as the blueprint's name and can
be changed or cleared, in the settings file (`edition = "..."`, or `edition = ""`
for none) or on the page. A Plan filled in from a blueprint takes its edition, and
the user can then change it or clear it on the Plan like any other setting. The
Plan also records which blueprint it came from, as `blueprint`, but only as a
record. Nothing reads it to decide anything; the name of the file comes from the
edition alone.

### Where blueprints come from

- **The settings file.** A section like `[blueprint.Small]` starts from the
  defaults, as they stand once every settings file is read, and changes only what
  it names.
- **The page.** Blueprints made or changed on the page are kept in
  `blueprints.json` in the data directory, as is which one is used by default.
  ARFABIT does not rewrite a settings file, since it is somebody's own file with
  their comments and their layout. Changing a file-defined blueprint on the page
  saves a version in `blueprints.json` that takes precedence. Removing one hides
  it there, and the line in the file stays and does nothing.
- **By hand.** A Project's line items changed directly, for that Project only,
  and not kept. Trying something once should not mean naming it and remembering
  it forever.

```toml
[defaults]
crf_uhd = 20
crf_bluray = 20
crf_dvd = 18
preset = "slow"
allow_uhd_copy = true
copy_native_audio = true
audio_bitrate = "256k"
include_forced_subs = true
include_full_subs = true
sub_languages = ["eng"]
keep_subtitle_pictures = false  # read picture subtitles into text where possible (§10)
convert_after_rip = true

[blueprint.Small]
crf_bluray = 24
preset = "medium"
edition = "Small"        # the name unless given; "" for none
```

The defaults and blueprints hold only what goes into a Plan. Settings that belong
to the machine, the drive or MakeMKV live in their own sections (§7).

### Projects

A **Project** is what to make from an Original. It is planned against what the Original
holds, track by track, in three sections — picture, sound, subtitles — as **line
items**. Each line item is one of the Original's tracks and what to do with it: **copy
as is**, or **convert** (the picture to HEVC at a quality and speed; sound to FLAC,
AAC or E-AC-3 — E-AC-3 only for surround of up to six channels, since for stereo
AAC does the same job everywhere). Lossy sound is never converted to a higher
bitrate than it has, which would only make a bigger file of the same sound: the
bitrate comes from MakeMKV (the scan's `SINFO` attribute 13, and the Original's
`BPS` tags), is shown beside each lossy track, and caps what is offered and
accepted. Converting lossy sound is noted as losing a little more. Every line starts copied as is; a **Convert** pill on the line turns converting on
and shows what to convert it to. "Add from original", under each section, lists
everything the Original holds, one line each, and adds a track as it is. Keeping a
track and a converted one beside it is two lines. Two lines exactly the same
are pointed out, since the file would hold the same track twice. A Blu-ray's
picture subtitles can be converted to text (SRT) where the computer can read them
(§10), and a blueprint does so unless it keeps them as pictures; elsewhere, and for
other subtitles, they can only be copied, and the page says why. Within a section, line items are dragged into
order (or moved with the arrow keys): the order of the tracks in the file, the
first sound track being the one a player starts with.

Beside a line item copied as is, the page says what that track costs on the
television, from what was tested (§4) — never a decision, only a note, such as that
Plex converts TrueHD every time it plays on an Apple TV and FLAC of the same width
would not.

**A blueprint is a recipe.** Used on an Original, it makes line items — the same ones
the user would add by hand — and is then out of the picture: the line items can be
changed like any others, and nothing reads the blueprint again. A blueprint that
keeps one of English, French or Japanese, used on an Original holding only Japanese,
leaves Japanese in the project, exactly as adding it by hand would. A blueprint
asking for something the Original lacks adds nothing for it; one whose sound rules fit
nothing adds no sound, and the user adds what they want. The defaults are a recipe
too.

One Project makes one file per container, MKV or MP4, and the container decides
which line items a file can carry. What MP4 can carry was found with ffmpeg
9.0.2 and nothing else: every sound format a disc has goes in as it is except
Dolby TrueHD, which ffmpeg calls experimental in MP4 and refuses, so a project
copying TrueHD into MP4 is refused and says so; picture subtitles have no place
in MP4 and are refused likewise; text subtitles copied go in as MP4's own text
format (`mov_text`), since ffmpeg refused SRT as it is; subtitles read into
text go beside it as SRT, as for MKV. ffmpeg labels HEVC in MP4 `hev1` unless
asked, and ARFABIT asks for `hvc1`, the label under which the picture's
parameter sets travel outside the picture. How any of it plays is untested
(§4). A film in MP4 counts as the same edition as one in MKV when ARFABIT looks
for a file already there, so making both of one film needs two editions. A **whole** Original
becomes a film in the library, beside it (stage PACKAGE), named with the Project's
edition. A
**stretch** becomes a clip in the clips folder (stage LAB). A stretch is cut first,
with every stream it needs copied together into a temporary piece of ARFABIT's own,
removed after, and the file is made from the piece without seeking: seeking while
copying some streams and converting others moved them against each other by up to
0.7 seconds. Copied streams can only start on a keyframe, so a clip starts at the
last one before the time asked for, usually under a second before.

A disc's Plan uses the same editor, fed with the disc's tracks from the scan. Its
project is copied into its own job at Start (§2) and pointed at the Original's tracks
once the Original exists, matching each by kind, language, format and width, since
MakeMKV numbers the Original's tracks its own way and turns uncompressed disc sound
into FLAC.

Not built: several containers from one Project sharing one encode of the picture;
making a Project's files while its Original is still being copied.

### Sound rules

A blueprint made on the page can choose sound tracks by rule rather than
leaving the stereo-first choice of §9 to decide. Rules are part of the recipe:
they decide which sound line items a blueprint makes. Rules describe tracks by what
they are, since one blueprint meets many discs:

- **Languages**: the first of a list the disc has, or each of them, or every
  language on the disc.
- **For each language, choices in turn**: *pick one of* or *keep all of*, the
  layouts wanted (7.1, 5.1, stereo; widest first is the order of preference),
  and lossless, lossy or either.

A choice the disc cannot meet is passed over. If nothing at all fits, nothing is
guessed: no sound line items are made, and the Plan says so. A stereo track ARFABIT
would make itself counts as lossy, and comes after the disc's own. Rules apply to
projects only; the original always keeps every track.

A blueprint also says whether to keep the picture exactly as it is
(`keep_picture`), and what to do with Dolby TrueHD (`truehd = "keep" | "flac" |
"both"`; keep by default). Changing TrueHD is the user's choice, made here once or
per project, never ARFABIT's.

Rules live in `blueprints.json`. The settings file cannot hold them, because its
reader is the small stand-in of §16 and is not to be extended.

### Not built: matching

Choosing a blueprint automatically per disc is roadmap (§17). When it comes, a
matcher chooses which blueprint fills the Plan, falling back to the defaults, evaluated most-specific-first, on:

- `source`: `uhd` / `bluray` / `dvd`
- `height`: 2160 / 1080 / 720 / 576 / 480
- `aspect`: from `ffmpeg cropdetect` on sampled frames

Aspect matters mostly for DVDs, where 4:3 and letterboxed 16:9 want different
treatment. Cropping is **never** automatic, because cropdetect misfires on dark
scenes. The Plan will offer it with the savings shown.

### Space check

The Plan will not start a job it cannot finish. Before offering a Plan, compare the
estimated total (Original + Delivery, from §12) against free space in the library.

ARFABIT does not monitor disk space in the background and shows no space meter during
normal use. It speaks up at exactly one moment: when the disc in the drive will not
fit. That message states four numbers and nothing else:

- what this rip is estimated to need
- how much room is left on the drive
- how much the originals take
- how much the films in the library take

Those last two are there because they are almost always the answer — the user has
originals they no longer need, and this is the moment they would want to know it.
It does not offer to remove anything (§0.6). Not built: a button to open the
folder.

Within about 10% of the threshold, the Plan shows the same four numbers as a note
rather than a stop, since an estimate can run low.

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

MakeMKV does not cause this problem — it remuxes, so the Original's HEVC bitstream still
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

If the source declares HDR but the metadata cannot be read, the job **stops** and
says why, rather than producing a grey file.

**The Matroska label.** The colour description is also written into the MKV's own
header, since a player may read that rather than the picture. Given only the encoder
options, ffmpeg 9.0.2 wrote the matrix there and nothing else, and a reader that
trusts the header then takes an HDR film's primaries and transfer to be unknown. The
frames are therefore stamped with the colour too (`setparams`), which puts all three
in the header. Mastering display and light levels travel in the picture only; the
MKV header does not carry them. `TestHDRSurvivesIntoMatroska` encodes a real file
and checks both. HDR has not yet been tried on a television (§4).

On the **UHD direct-copy** path none of this applies — the bitstream is never decoded,
so the metadata travels untouched. It should still be verified in the finished file
after muxing, since a copy that silently drops it fails the same way. Not built: that
check.

**Dolby Vision and HDR10+ are deferred.** UHD discs carry DV Profile 7 (dual layer);
Apple TV wants Profile 5 or 8.1, and converting needs `dovi_tool` plus a class of
failure modes we do not want on day 1. Every DV disc has an HDR10 base layer that
looks excellent. The Plan says so plainly when it applies.

### Audio

**Copy by default; changing a track is the user's choice.** Matroska carries every
sound format a disc has, so each track is kept bit for bit at no cost unless a
project or blueprint says otherwise. ARFABIT does not decide to convert anything
because of a device. It says what a choice costs, from what was tested (§4): beside
a TrueHD track, that Plex converts it every time it plays on an Apple TV, and that
converting it to FLAC of the same width keeps it lossless and plays directly.

Converting TrueHD to FLAC was checked: decoded, it is the same sound sample for
sample, every channel kept, at about the same size. It drops Dolby Atmos height
information, which only TrueHD can carry; untested on an Atmos disc.

FLAC is given frames of an even size (`asetnsamples`): TrueHD decodes into uneven
ones, and whether a cut starts on one too small for the encoder depends on where it
starts — 20:05.000 worked and 20:05.121 did not.

**An AAC stereo track is always added as a fallback**, whether or not a copy was
available.

A lossy track is converted only when a blueprint turns copying off. The target then
depends on how wide it is, because the encoders differ in ways that were measured
rather than assumed:

| Source width | Target | Bitrate | Why |
| --- | --- | --- | --- |
| 7.1 (8 channels) | AAC | 640k | the only one of the three that writes eight channels |
| 3.0 – 5.1 | E-AC-3 | 768k | a receiver can take the bitstream whole |
| Stereo or mono | AAC | 256k | |

**ffmpeg's E-AC-3 encoder stops at six channels** and downmixes anything wider
without a word — the format allows 7.1, the encoder does not. Verified:

```
7.1 source → -c:a eac3 → eac3, 6 channels, 5.1(side)
7.1 source → -c:a aac  → aac,  8 channels, 7.1
```

**Stereo is the default selection.** It plays on everything, needs no receiver,
and is the least surprising thing to find on the television. Every surround
track is listed beside it with what it would become, so turning one on is a
single click — the Plan shows the choice rather than making it. Where a disc
carries two stereo tracks with nothing to tell them apart, and one is often a
commentary, both are kept rather than one chosen wrongly.

A stereo track is always delivered: a real one from the disc where there is one,
otherwise a downmix made from the widest track in the wanted language.

**On Atmos.** Apple TV+ ships Dolby Atmos as E-AC-3 with Joint Object Coding — a
5.1 core plus an object substream the receiver renders to whatever speakers are
present. Discrete 7.1 is effectively a Blu-ray-only format; streaming went 5.1
then Atmos, which is why ffmpeg's E-AC-3 encoder never implemented 7.1. ARFABIT
cannot produce Atmos: JOC encoding needs Dolby's licensed tools.

Track order matters: Apple TV selects the first compatible track, so multichannel
is listed first when present, with the stereo fallback after it.

---

## 10. Subtitles

**SRT, not ASS.** ASS requires a rendering engine, so Plex burns it into the picture,
which forces a full video transcode at playback — the exact thing this project exists
to avoid. SRT is handed to the client as text and direct-plays. There is also no
fidelity to gain: the source is bitmaps, so any styling would be invented.

Output: **SRT sidecars**, beside the file, named as Plex matches them (§6). Subtitles
read into text are not put inside the MKV. A sidecar SRT is not yet in §4's table
of what was tested to play directly; SRT inside MKV is.

### Reading subtitles (OCR)

A disc's subtitles are pictures of text. ARFABIT reads them with a reader the
computer already has, rather than shipping one: nothing to download, nothing to
train, and nothing in the binary.

| System | Reader | How |
|---|---|---|
| macOS | Vision (`VNRecognizeTextRequest`, accurate, language correction on) | a JavaScript for Automation script run by `osascript` |
| Windows | `Windows.Media.Ocr` | a Windows PowerShell 5.1 script, awaiting WinRT calls with `AsTask` |
| Linux and others | Tesseract, when installed | the `tesseract` command, one run per 100 pictures, TSV out |
| — without one | none | the page says subtitles cannot be read into text on this computer, and that Tesseract is used when installed |

ARFABIT never installs a reader. On macOS and Windows one is always there; on
Linux, one that somebody installed is used.

Both scripts live in `internal/ocr`, are embedded in the binary, and are written
beside the pictures when used. Each reads a list of PNG files and writes its
answer as JSON to a file, so text never passes through a console's character set.
Tesseract is given the list itself and reads each picture as a page.

**Measured so far.** On macOS, an earlier version of the Vision script read a real
film's English subtitles with about 99.98% of characters right. The misses were
`f`→`t` three times, `I`→`|` once, `!`→`.` once, and one short line, "I...",
that came back empty. On *The Sheep Detectives* (1,864 subtitles, read in 57
seconds) the misses that could be seen were `I`→`|` three times, all listed for
checking, and one empty line, which the picture shows is a lone "I..." again.
Vision's own confidence was 1.00 on every line read, the wrong ones included, so
ARFABIT does not use it. Read again in Vision's fast mode, 205 lines differed;
compared by words alone (below), 34 did, and about 22 of those were real
misreadings in the accurate reading that nothing else had caught: 11 lines
starting "I" with the "I" dropped, "II" for "ll", "tor", "ot", "tell" and "ort"
for "for", "of", "fell" and "orf". On *In the Grey* (1,996 subtitles, 79 seconds
for both readings), 50 were listed at first; with fast mode's own habits left out
of the comparison ("!" read as "l", accents dropped), 26 are, and 20 of those are
real misreadings in the accurate reading: "II" for "ll" eight times, "t" for "f"
six times ("wondertul", "torce", "shitt"), a dropped "I" three times, "out" for
"but", "ifourteen" and a `|`. Four are fast mode's slips ("Mclntyre",
"papenNork") and two are unclear. The Windows and Tesseract readers have not
been run on real subtitles yet.

**When.** Reading is a task of its own, stage OCR, one per subtitle track, read
from the original into the SRT beside it (§2, §6). A disc's Plan says which of
its picture subtitle tracks to read: at first those its film converts to text,
and any can be ticked or unticked. Once the disc is copied, each chosen track,
and each its film converts, is read. A film made later from an original, in
Projects, starts an OCR task for any track it converts that has no SRT yet. A
track is never read over an SRT already there.

A film takes a copy of the original's SRT for each subtitle line item it
converts to text, at DELIVER (§2). If a track cannot be read — a language the
reader does not have, or anything else — the film is delivered all the same,
without it, and the note says why.

**How a track is read.**

1. The track is copied out of the file being read (`ffmpeg -f sup`) and its
   pictures decoded (`subs.ParseSUP`). Only a Blu-ray's picture subtitles (PGS)
   are read; DVD subtitles are not yet.
2. The pictures are written as they are to the system's temporary folder, 100 at
   a time, and read in chunks so progress can be shown; they are removed as each
   chunk is done.
3. The reader is asked for the track's language: the ISO 639-2 code is turned into
   the reader's own name for it ("eng" → "en" for macOS and Windows, "eng" for
   Tesseract) and matched against the languages the reader has. A language it
   cannot read is said in plain words: on Windows, that the language pack is not
   installed; with Tesseract, that its language data is not.
4. On macOS each picture is read twice, in Vision's accurate mode and then its
   fast mode, in the same run. The accurate reading is what goes into the SRT.
   The fast one is a second opinion only.
5. A subtitle is of **low confidence** when nothing was read from it, when the
   reading holds a mark known to be a misreading, or when the fast reading says
   other words. The marks are a `|` (for `I`, on both films) and, in English, a
   letter English does not use ("killeriại"); the accents English borrows, as in
   "café", do not count. Other languages get their own marks when a film shows
   what their misreadings look like. Words are compared with case, punctuation and
   spacing left out, and a lone `l`, `1` or `|` counted as `I`, since fast mode
   gets those wrong all the time. Windows and Tesseract have no second reading,
   so there only the first two apply.
6. Every line is kept as read, never corrected (§15). Each subtitle of low
   confidence is listed on the task with both readings and its picture, kept in
   the node's own folder (`nodes/<id>/ocr/<task>/`), and in the log with its
   time; the task's note says how many there are. A subtitle with nothing read
   is left out of the SRT.
7. The SRT is written beside the original, named after it and the language
   (§6), or beside a clip, named after the clip. A file already there with that
   name is not replaced: that is checked before anything is read.

**Clips.** A clip reads its own subtitles, in its own task, from the piece cut
for it, trimmed to its length; it does not take lines from the original's SRT.
A piece copied from the Original starts at the keyframe before the time asked
for, so shifting the Original's times by that time would put every line early
by up to the gap between keyframes; the piece is on the same clock as the clip,
which `TestClipSubtitlesKeepTimeWithThePicture` checks (3.2 s asked for, the
piece starting at 3.0 s). Taking lines from the original's SRT instead would
need the piece's true start found for every clip, and a clip is made to judge
settings, not to keep; reading its few lines again costs seconds.

**Blueprints.** A Blu-ray's picture subtitles are converted to text wherever the
computer can read them, unless the blueprint says `keep_subtitle_pictures`. Where
it cannot, they are copied, and the line says why.

**Checking them.** Every OCR task has a tag in Recent tasks: **green** when
none of its subtitles has low confidence, or somebody has looked at them;
**yellow** while some have low confidence and nobody has; **red** when reading
did not finish. A yellow task stays in Recent tasks however long ago it ran,
so a fix can always be found. Its subtitles of low confidence are listed, each
with its time and picture. Where there were two readings, the choice is
Accurate, Fast or Custom; otherwise it is the line as read, or Custom. Picking a
reading writes it into the SRT beside the original at once; Custom opens a box
filled with whichever was picked last, to change and save. Only that subtitle
changes, and an empty Custom takes it out. Nothing is changed until somebody
chooses. **These are fine** turns the task green without going line by line,
and changes nothing. A clip's subtitles are checked the same way, in its SRT.

**Copies fall behind, and are brought up to date.** When a film takes a copy
of an SRT beside its original, ARFABIT records the copy's SHA-256 on the film's
task. A fix to the original's SRT leaves every copy out of date. The OCR task
says how many films have one from before the change, and each film's task says
its own; both offer **Bring up to date**. To do it ARFABIT hashes the copy
again: if it is still exactly what ARFABIT wrote, nobody has changed it, and
it is replaced whole with the original's SRT as it is now, written aside and
renamed into place, and the new SHA-256 recorded. If not, it is left alone and
the page says it was changed by someone else. That copy is the only file
ARFABIT ever replaces, and only on that proof (§6). Whether Plex notices a
changed SRT without the film's metadata being refreshed is untested.

Not built: a context pass over what was read; telling SDH and forced tracks apart;
DVD subtitles.

**Forced subtitles** are identified separately and default to **on**. On most Blu-rays
the alien-language and signage translations are a distinct PGS track flagged `forced`,
not burned into the picture. Not built yet (Appendix A).

---

## 11. Metadata — offline

No network at rip time.

**Source:** IMDb `title.basics.tsv.gz`, filtered to `movie` + `tvMovie`, keeping
`tconst / primaryTitle / originalTitle / startYear / runtimeMinutes`.
About 700k rows, **~60 MB** on disk.

Stored as `titles.json` in the data directory: title, year and runtime per film.

**Matching** uses two signals the disc hands us:

1. **Volume label**, cleaned and compared by significant words. A candidate must
   resemble the label at all (similarity ≥ 0.5) regardless of runtime.
2. **Runtime** from the MakeMKV scan. A match within tolerance lifts the score and
   a mismatch halves it.

The Plan shows the top three candidates with years and runtimes, best pre-selected.
You confirm or type your own. Never a silent wrong rename.

**Updates:** IMDb publishes no deltas, only full files. So:

- Default is **never auto-update**. Titles and runtimes of existing films do not change.
- The page has a **Refresh** button showing the current index date. Built.
- Not built: an optional monthly check, a **TV checkbox** (the parser supports it; the
  page always builds films only), and an optional online lookup for discs the offline
  index cannot pin down.

Licensing: IMDb datasets are free for personal, non-commercial use. The index is
therefore **downloaded by the user from the page**, not bundled in the released
binary. Without it, the disc's own name is used.

---

## 12. The estimator

After a few jobs, ARFABIT knows how long _your_ hardware takes and how big _your_
files get. No other ripper does this.

Rip speed is a property of the drive and encode speed is a property of the CPU, so
they calibrate independently. Two drives of different speeds on one machine
converge separately. Rip speed is keyed on **(drive, source type)**; encode on
**(preset, output height, CRF)**. Both live in the node's own `calibration.json`.

```jsonc
// calibration.json
{
  "drives": {
    "BD-RE HL-DT-ST BD-RE BU40N": {
      "mb_per_second": { "bluray": 22.4, "dvd": 8.1 },
      "samples": { "bluray": 7, "dvd": 2 },
    },
  },
  "encode": {
    "x265/slow/1080p/crf20": {
      "preset": "slow", "height": 1080, "crf": 20,
      "fps": 4.7, "bits_per_pixel": 0.061, "samples": 5,
    },
  },
}
```

- **Time** = source duration x frames x observed fps.
- **Size** = picture bits-per-pixel observed x resolution x duration, plus the
  Plan's audio at its bitrates. An observation subtracts the audio before
  recording bits-per-pixel, so audio is not counted twice.

A quality not yet observed borrows the nearest CRF observed at the same preset and
height (or the seed, which stands for CRF 20), scaled by x265's rule of thumb that
every 6 steps of CRF roughly halves the bitrate. That keeps blueprints that differ
only in quality apart from the first job. Borrowed figures always read as a range.
The frame rate is assumed to be 24 (§17).

Seeded with conservative priors so estimates exist from job one, then weighted toward
recent observations. Each estimate knows whether it has enough samples to be a
single figure (_"about 6 hours"_) or should be a range (_"roughly 4–9 hours"_).
Not built: the page uses that. The Plan shows a single figure either way.

---

## 13. Running as a service

Autostart is a toggle in the web UI. The binary writes the right thing per platform.

| Platform | Mechanism                                        | Note                                                                       |
| -------- | ------------------------------------------------ | -------------------------------------------------------------------------- |
| macOS    | LaunchAgent in `~/Library/LaunchAgents`          | **Agent, not Daemon** — needs the user session for drive and folder access |
| Windows  | Task Scheduler, at-logon                         | More reliable than a Run key, and visible where a person can find it       |
| Linux    | systemd **user** unit + `loginctl enable-linger` | Lingering is what survives logout                                          |

None of them restarts ARFABIT when it exits, deliberately: otherwise Stop would
appear not to work. The toggle says which mechanism it set up. Restart and Stop buttons sit beside it
in Settings. Not built: live status such as "Running, started 3 days ago".

---

## 14. The web UI

### Sections

Still one HTML page. A bar fixed to the bottom of the window switches between
four sections, and the address remembers which (`#tasks`, `#projects`, …). Every
section keeps running whichever is showing, so switching loses nothing.

| Section | Holds |
|---|---|
| **Tasks** | Watching: Doctor, each drive, the queue, logs, clips compared, recent tasks |
| **Projects** | Creating: what to start from, and what to make. Nothing finished is shown here; that is Tasks |
| **Blueprints** | Recipes, applied from the start by a drive or by hand |
| **Settings** | Autostart, restart and stop, the film list |

Two workflows matter, and the page exists to serve them: **automatic**, where
a disc goes in and, with nothing clicked, the film comes out with its
subtitles read into text (a drive set to *Blueprint*); and **manual**, clips to
find the settings and making films from an original again later. OCR is never
perfect, so both leave a way to fix subtitles after the fact, however long ago
(§10).

The drive's box is headed with the drive's own name, which never changes; what
the drive is doing is written underneath.

Each drive has **When a disc goes in: Nothing / Copy / Blueprint: [name]**,
kept by the drive's name in the node's own `drives.json`. *Nothing* waits for
Plan to be pressed. *Copy* reads the disc as soon as it goes in and starts
copying it, from the blueprint used by default, with no film; its subtitles
are read as the Plan chose. *Blueprint* does the same and makes a film too,
from that blueprint or, as *Blueprint: Defaults*, from the defaults. Either
way the Plan stays open while the disc copies (§2), to change anything or add
a film. What stops a disc starting on its own — too little room, a file
already there, a blueprint since removed — leaves its Plan waiting, and the
log says why. A disc already in the drive when ARFABIT starts did not just go
in, and is left alone.

### The Plan

A disc's Plan is on the Projects page, with the disc chosen to start from. It
is two parts. **The original** says what is on the disc and goes
into the original — picture, and each sound track with whether it is lossless —
and nothing else: nothing in it is converted, so it never talks about
converting. **Make a film** turns on the second part, **the film**: the
same line-item editor as the Projects page, fed with the disc's tracks, started
from the default blueprint or the defaults (§8, Projects).

Once started, the Plan stays up while any of it can still change (§2), saying
which parts are still open; the rest is greyed and cannot be changed.

The Plan will not start a job that would replace a file already there — an
original, or a film in the library under the same edition — and says which file
is in the way (§0.6). ffmpeg is run with `-y`, so this is looked for again just
before each file is written, since a job can wait in the line for hours.

### Recent tasks

Subtitles still to be checked are named at the top, each opening its task.
Below them, the last twenty tasks of every kind, labelled Copy, OCR, Package or Clip, with
what each became, when, and the names of the files it made: a copy's original,
an OCR task's SRT, a package's film or clip. The label's colour says how it
ended, and an OCR task's says whether its subtitles are still to be checked
(§10); one that is stays in the list, beyond the twenty. Anything that can be
started again says so. The folders are the record of what exists; this is the
record of what was done.

### The queue

- A one-line summary counts what is in it ("1 disc being read · 2 waiting their
  turn"). With more than one kind of thing, each part narrows the list to that
  kind and a leading total widens it again.
- Clocks are kept by the page, started from the stage's start time and
  corrected for any difference between the two computers' clocks. The figures
  under a bar sit in fixed-width cells so the line does not shift as they change.
- Each job is headed with the file it is making, named exactly: the copy's
  `.mkv` during RIP (MakeMKV's own name, until it is renamed), the Delivery's or clip's `.mkv` with its
  edition after.
- Anything that could throw away ten minutes or more with one stray click asks
  first, saying how much work would be lost: stopping a job that is working, and
  restarting or stopping ARFABIT while anything is. Something still waiting its
  turn has done nothing yet, and simply stops.
- Jobs waiting for the processor are dragged by a handle to a new place in the
  line (or moved with the arrow keys), and one drag is one change. The line is
  kept in memory, not in a file.
- A disc's read speed is kept through RIP as averages over each 30 seconds —
  everything read in the stretch divided by how long it took — as `read_speed`
  in the job record, and drawn under the rip in recent tasks once it is over.

### Logging is a feature, not an afterthought

- **Append-only over SSE.** The page never reloads. New lines are appended, so
  scroll position, text selection, and filtering are never disturbed.
- **Follow toggle**, on by default, disengages when you scroll up and re-engages
  via "Jump to live".
- **Filter box**, plus clicking a job in the queue to show only its lines.
- **Stable line IDs** (job and line number) so filtering never scrambles anything.
- **Technical details** under any line that has raw output, always complete.
- **Per-job plain-text log on disk**, in `nodes/<id>/log/`. Copy-paste always works.

Not built: a virtualized list for very long logs, level and stage filter chips, a
filter that is remembered between visits, and a Download button for a job's log.

### Doctor

First screen on a fresh install. Checks `makemkvcon`, `ffmpeg`, the MakeMKV key,
drive presence, free disk space, and the metadata index. Each failure gets a
copy-pasteable fix.

MakeMKV beta keys expire about every 60 days. Doctor looks for a key in MakeMKV's
settings and warns within a week of expiry. Not built: fetching a fresh beta key
automatically, and the Settings links to the purchase page, the forum and r/makemkv.

### Projects page

Setting something up to be made. **Start from** lists the disc in the drive,
every original, every film in the library, and every clip. The user is trusted to
know what they are making.

- **The disc** shows its Plan (§14, The Plan) once it has been read, with a
  button to read it until then. The drive's Plan button on Tasks reads the disc
  and comes here. Tasks says, under the drive, when a disc's Plan is here
  waiting or still open to change.
- **A file** makes one of three things. **Subtitles only**: its picture
  subtitle tracks, ticked, each read into text as an OCR task of its own into
  the SRT beside the file (§10); no picture or sound is made. **A film**: all of
  it, converted or remuxed as its line items say, as MKV or MP4 (§8), into the
  library in the film's folder. **A clip**: a stretch — a start (`01:23:45`) and a
  length, 5 s to 10 min — into `clips/<film>/`, named as an edition (§6). A film
  or a clip starts from the defaults or a blueprint, and its line items can
  then be changed (§8, Projects).

Starting a project adds its tasks to the queue, and the page says they are on
Tasks. Clips are **files you watch on your own TV** — ARFABIT does not pick a
winner and does not change your settings. Judging is yours. Comparing two
settings is two projects from the same Original.

Alongside each clip, what the whole film would come to, extrapolated from clip
length to the whole Original. Size and time are normalised so the best entry reads 100%:

| Column | Basis for 100% |
|---|---|
| Size per hour | the smallest result |
| Whole-film size | the smallest result |
| Whole-film encode time | the fastest result |

Not built: an average-bitrate column, and VMAF. VMAF, when it comes, is scored against
a theoretical bit-perfect 100 rather than the best entry, so a row reading 94% means
"94% of perfect", not "best of a bad set".

A single clip lies — a dark, static scene flatters every bitrate; falling snow
destroys them all. Auto-sampling three clips by scene complexity is **phase 2**.

---

## 15. Error policy

> **Never replace. Always augment. Never guess.**

The common failure in this kind of tool is substituting a guessed explanation for the
real error, and users burn hours chasing the guess. So:

- A plain-language line appears **only** on a tested signature match — exact exit code
  plus a verified stderr pattern.
- The verbatim output is always one click away under "Technical details", always complete.
- Anything unmatched gets an honest generic: _"ARFABIT did not finish this disc.
  Here is exactly what the ripper reported:"_ followed by raw output. No invented cause.
- Every message says what ARFABIT **did**: _"The original is still in your library.
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

## 16. What is built

**Working today.** SCAN, PLAN, RIP, EJECT, OCR, QUEUED, PACKAGE, DELIVER, running end to end
from the web page on one machine, with a queue for the processor, optional
blueprints as recipes, and Projects (lab clips and whole-Original Deliveries).

| Piece | State |
|---|---|
| `makemkvcon` robot-mode parsing | built, tested against four real discs |
| Main-feature selection and obfuscation detection | built, calibrated on a real obfuscated disc |
| Rip with progress and cancellation | built |
| HDR10 metadata extraction and propagation | built, tested (into MKV with a real encode; not yet on a television) |
| Encoder argument construction, audio copy rule (copy unless the user chooses otherwise) | built, tested |
| Layered TOML settings with provenance | built, tested |
| File-backed job store, atomic writes | built, tested |
| Plex naming | built, tested |
| Estimator keyed on drive and processor | built, tested |
| Space check | built, tested |
| Web page, log view, server-sent events | built, tested |
| Defaults and blueprints: settings file, made on the page, one-off, optional default blueprint | built, tested |
| Processor slots and QUEUED | built, tested |
| A disc's Plan open to change until each step starts; what a drive does when a disc goes in | built, tested (starting on its own not yet tried with a real drive) |
| Projects from any file ARFABIT made: subtitles only, a film as MKV or MP4, a clip | built, tested (MP4 made and read back; not played anywhere) |
| Projects: line items copied or converted, blueprints as recipes, lab clips and whole-Original Deliveries | built, tested (no VMAF) |
| Offline IMDb index: download, lookup, suggestions on the Plan | built, tested |
| Jobs left behind by a restart; running a stopped job again from its own Plans | built, tested |
| Doctor | built |
| Autostart on all three platforms | built |
| Eject on all three platforms | built |
| Subtitles read into text by the computer's own reader, as OCR tasks into the SRT beside the original, with low confidence listed and tagged (§10) | built; macOS reader measured on two films, Windows and Tesseract not yet run on real subtitles |

**Subtitles.** A Blu-ray's picture subtitles are read into SRT on macOS, on
Windows, and wherever Tesseract is installed (§10), as tasks of their own once
a disc is copied, and films take a copy.
A disc's Plan made before projects still logs that its subtitles are not read.

**Known shortcuts.**

- The TOML reader is a deliberately small hand-written subset, because the
  machine this was built on could not reach the Go module proxy. Replacing it
  with a full implementation is a one-file change.
- Calibration is saved when ARFABIT stops, not after each job, so a crash loses
  what was learned since the last clean stop.
- Not every write is atomic yet (§6): `calibration.json` and `titles.json` are
  written in place, and `blueprints.json` uses a fixed temporary name, so two
  nodes saving at the same instant could collide.
- A job's fields are changed from more than one goroutine without a lock, when a
  job is stopped while it is working. The race detector reports it.
- `read_cache_mb` applies to every drive on a machine. Per-drive values need a
  stable way to name a drive in the settings file.

## 17. Roadmap

**Next**
Measuring OCR
on more discs, on Windows and with Tesseract · the encoder's own observed frame
rate rather than an assumed 24 · VMAF in the lab.

**Later**
Blueprint matchers (§8) · multichannel E-AC-3 · OS-level disc detection ·
"fix my subtitle file" as a standalone tool · TV series naming ·
**lining up a subtitle file the user downloads** (from opensubtitles.com, say) with
the film: shifting and stretching its times to match the disc's own subtitles or
sound. A computer with no reader, such as Linux without Tesseract, gets subtitles
this way, and it helps everywhere a disc's own subtitles are missing or poor.

**Eventually**
Music CDs (a separate backend — MakeMKV cannot read CDDA; needs libcdio and
MusicBrainz) · Dolby Vision (P7 to P8.1 via `dovi_tool`) · HDR10+ ·
`arfabit-control` for NAS targets · a context pass over what OCR read.

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
| 1009 | "default blueprint missing" | **suppress** — appears every run, harmless |
| 1011 | "Using LibreDrive mode" | good: raw access engaged |
| 2010 | "opened in OS access mode" | degraded access; usually another process holds the drive |
| 5010 | "Failed to open disc" | **expected and ignorable after a `disc:9999` probe**; a real error otherwise |

`disc:9999` is a pseudo-index that enumerates drives, then fails to open disc 9999.
Treating its trailing 5010 as an error is a bug.

### Read speed

`--cache=<megabytes>` is the read buffer, and it decides how fast a disc is
copied more than anything else does. ARFABIT once passed `--cache=1` on the
assumption that a small cache saved memory; reading a 40 GB disc a megabyte at
a time is what that means, and it held a USB 3.0 drive to about 3 MB/s.

Nothing is passed now unless `blueprint.read_cache_mb` asks for it, so MakeMKV
chooses.

### DRV

Sixteen slots always print. The second field is the drive's state:

| Value | Meaning |
| --- | --- |
| 0 | drive present, tray closed, empty |
| 1 | drive present, tray open |
| 2 | disc inserted |
| 3 | loading |
| 256 | no drive in this slot |
| 257 | unmounting |

A drive's measured read speed (§12) is kept under its name, not its device
path: the system numbers a disc as it appears, and an empty drive reports no
path at all.

**256 means an empty slot, not a drive.** Filter on it or the UI shows sixteen
phantom drives.

**A drive with nothing in it reports no device path and no label**, so neither
can be required to recognise one. Requiring the device path made the drive
disappear from the page the moment a disc was ejected.

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
