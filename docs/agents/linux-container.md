# Hand-over: ARFABIT on Linux, in a container

Written 2026-11-05 at the end of a long discussion with the maintainer. It is
the brief for the agent that does `docs/RELEASE.md` §12 step 5 (Linux). It
records what was decided, the facts found on the way, and what is still the
maintainer's to decide. Once the docs say all of this, remove this file (see `CLAUDE.md`).

## Read first

1. `CLAUDE.md`, all of it. Two rules matter most here: **never name** other
   rippers or container projects in anything git tracks (`temp/` holds clones
   for reading only), and **never invent an error explanation** (§15).
2. `docs/ARCHITECTURE.md` §0 (invariants), §3 (stack table), §13 (running as a
   service), §14 (the MakeMKV key step and fetching the beta key).
3. `docs/RELEASE.md` §0 (goals), §1 (the shell contract: `-shell`, the ready
   line, exit codes 0 and 75), §3, §5, §6, §7, §12 and its status table.

The Linux parts of `RELEASE.md` §1, §3, §5 and §6 describe the **old plan**:
a `.deb`/`.rpm` system service, an apt/dnf repository on GitHub Pages, the
AUR, `install-service`, `systemctl reload`. That plan is replaced (below).
Do not build any of it.

## What was decided

**Linux runs ARFABIT in a container, under Podman or Docker.** Same image
for both. Reasons:

- On Linux a container shares the host's kernel, so the real drive can be
  passed in (`/dev/sr0` and its `/dev/sgN`).
- The image brings MakeMKV, `ffmpeg`, `ffprobe`, `tesseract` and `eject`.
  MakeMKV is the hard part of Linux today, since no distribution packages it.
  Native packages would not have solved that.
- One image covers every distribution, plus Unraid, Synology and TrueNAS.
- One Containerfile is far less to maintain than `.deb`, `.rpm`, two signed
  repositories and the AUR. The maintainer plans only a few releases.

**macOS and Windows stay native**, as now. There, Docker and Podman run a
hidden Linux virtual machine. On macOS it has no USB passthrough at all. On
Windows, a USB drive needs usbipd-win, and an internal SATA drive has no route
at all. Native also gives Vision and `Windows.Media.Ocr`, real folders and
permissions, and no hidden virtual machine. The §3 Docker row's reason stays
true for those two.

**One repository.** No split into core and shell repos. The shells and the
core share one contract and one tag; `release.sh` and `build.ps1 -Upload`
check `HEAD` is the tag. The container goes in `packaging/container/`.

**Users build the image from our Git repository first.** We do not publish an
image, at least for now, so we never ship MakeMKV ourselves. Their machine
downloads MakeMKV from makemkv.com while building, as `make install` would.

- **Docker Compose:** `build:` with a Git URL. Keep the build context at the
  repo root and name the Containerfile with `dockerfile:`. A
  `#<ref>:packaging/container` context would leave out the Go source the image
  needs to build ARFABIT.
- **Podman:** `podman build` and `podman-compose` take Git URLs. A Quadlet
  `.build` unit may too (`SetWorkingDirectory=`), but that needs Podman 5 and
  is untested. Ubuntu 24.04 is believed to ship 4.9: check.
- **The licence:** the build stops unless the user sets a build argument
  (proposed: `MAKEMKV_EULA=accept`), and its error points to the licence text.
  The user accepts, not us.
- **Publishing later:** a published image on ghcr.io can be added without
  changing anything else. It would be the same Containerfile. Do not publish
  one without the maintainer's say.

**ARFABIT updates itself inside the container.** The image holds the tools
and a small start program. ARFABIT's own binary lives in the `/config`
volume, and is updated the way the shells do it:

- the signed `latest.json`;
- the SHA-256 check;
- waiting until nothing is running;
- exit 75.

The start program runs the copy in `/config` if there is one, else the copy
built into the image. So no image rebuild is needed for a new ARFABIT, and it
works the same under Docker and Podman. Docker's restart policy restarts the
same container on the same image, so swapping images would not update
anything there. Podman's `AutoUpdate=` timer and image-watching tools restart
whenever they like, even mid-rip. The image needs rebuilding only when MakeMKV
or ffmpeg change.

**The start program is Linux's shell.** It follows `RELEASE.md` §1:
- start ARFABIT with `-no-open -shell`;
- on 75, install the staged update and start ARFABIT again;
- on 0, stop;
- on anything else, stop and leave the log saying why.

ARFABIT has no environment variables of its own (ARCHITECTURE §7). So
anything it needs to know about running in a container comes by flag, from
the start program.

**The beta key** is already decided in ARCHITECTURE §14: fetch the current
one from the MakeMKV forum post. It is not built yet. In the container,
MakeMKV's settings live in `/config` (set `HOME` for `makemkvcon`
accordingly), so the key survives rebuilds.

## Facts found (2026-11-04/05)

**MakeMKV's licence**, `src/eula_en_linux.txt` in makemkv-bin 2.0.0,
"Copyright (C) 2007-2026 GuinpinSoft inc":

> You are allowed to redistribute the Software in it original unmodified form.
> In addition, as a special exception, you may extract and repackage
> Linux-specific part of the Software for the purpose of creating a
> distribution-specific package, as long as program files and this license
> remain unchanged.

> You may not sell, rent, lease or sublicense the Software, without the
> explicit written consent of GuinpinSoft inc.

> You shall not modify, translate, reverse engineer, decompile or disassemble
> the Software or any part thereof or otherwise attempt to derive source code,
> create or use derivative works therefrom.

It also says: "You are entitled to use the Software on all personal computers
(laptops/desktops)." Read literally, a headless server is not one. The
maintainer knows about this wording.

**The makemkv-bin 2.0.0 tarball:**
- It has a ready-built `makemkvcon` for `amd64`, `arm64`, `armhf` and `i386`
  under `bin/<arch>/`, so ARM is possible.
- Its `Makefile` shows the licence with `less` through `src/ask_eula.sh` and
  writes `tmp/eula_accepted` when the person types "yes". An unattended build
  has to create that file itself, which is what the build argument above
  gates.
- `makemkv-oss` is the open-source part and is compiled. Build it without its
  desktop app.
- Check what the ready-built `makemkvcon` links against (`readelf -d`) before
  choosing a base image. Debian is the safe guess, because of glibc, but
  verify it.

**The current version** is listed in the forum's Linux thread,
<https://forum.makemkv.com/forum/viewtopic.php?f=3&t=224>. On 2026-11-05 it
was 2.0.0. Old versions stop with "This application version is too old".

**Drive access:**
- `makemkvcon` needs the drive's `/dev/sgN`. `/dev/sr0` is optional but may be
  slower without it.
- `lsscsi -g` on the host pairs them.
- The `sg` number can change between boots.
- `--device`, or `AddDevice=` in Quadlet, is enough; `--privileged` is not
  needed.
- A drive plugged in after the container starts is not seen until it
  restarts.
- Images like this usually take a user and group number (default 1000) and
  supplementary groups, so files in the output folder belong to the right
  person on the host. Docker users expect that.

**NAS app screens** (Unraid templates, TrueNAS apps, probably Synology
Container Manager) pull ready-made images and cannot build from Git. Those
users need SSH and `docker compose`, or Unraid's Compose Manager plugin. That
is the cost of building first.

**Releases have no Linux binary yet:**
- `packaging/release/release.sh` uploads the `.dmg`, the macOS `.zip` and the
  Windows `setup.exe`.
- `packaging/release/latest` signs `-darwin` and `-windows` only.
- `internal/update` picks a download by `runtime.GOOS` alone
  (`update.go:205`).

For Linux that needs a binary per architecture (amd64, arm64 at least), so
the key must include `GOARCH`. Keep the existing `darwin` and `windows` keys
working for copies already installed.

## Still the maintainer's to decide

Ask; do not choose these yourself.

1. **Which MakeMKV version a build gets:**
   - **Pin it in the Containerfile:** predictable, but a commit for every
     MakeMKV release.
   - **Fetch the newest at build time** from the forum thread: a rebuild alone
     stays current, but it relies on reading a web page.
2. **Stop versus starting at boot.** `RELEASE.md` §3 says Stop on the page
   stays stopped, and an unexpected stop is reported but not restarted.
   - **Podman:** systemd can restart only on 75 (`RestartForceExitStatus=75`).
   - **Docker:** its restart policies cannot tell exit codes apart.
     `unless-stopped` starts at boot but restarts after Stop. `no` keeps Stop
     but doesn't start at boot.
   The start program handling 75 inside the container covers updates either
   way. What should happen on Docker after Stop and after a crash is open.
3. **The Quadlet form:**
   - a `.container` file using an image the user built;
   - or a `.build` unit, which needs Podman 5.
4. **Leftovers of the old plan:**
   - `packaging/linux/arfabit.asc` (the repository's GPG public key) and
     `arfabit.desktop`;
   - the `ARFABIT_REPO_KEY` and `ARFABIT_PACKAGES_DEPLOY_KEY` secrets;
   - the `install-service` and `systemctl reload` designs;
   - the welcome's `setfacl` and `usermod` steps.

   They are probably all dropped. Confirm before removing anything.

## Order of work

1. **Docs first, shown to the maintainer before any code:**
   - ARCHITECTURE §0 invariant 1, for example: "One static binary. Native on
     macOS and Windows; on Linux, run inside a container built from our
     Containerfile";
   - the §3 rows for Shell, Autostart, Release and Docker;
   - CLAUDE.md's short-version table, which says "No ... Docker";
   - RELEASE.md §1, §3, §5 (and its "Why not Flatpak" part), §6, §8, §9, §10,
     and the §12 step 5 row and secrets table;
   - a written-down shell contract that all three platforms follow;
   - delete this file once all of it is in the docs.
2. **`packaging/container/`:** the Containerfile, the start program, a
   `compose.yaml`, and a Quadlet file.
3. **ARFABIT's side:**
   - a flag saying it runs in a container;
   - hide what means nothing there, such as Start at login;
   - update into `/config`;
   - a Doctor check that names the `/dev/sgN` to pass in;
   - the beta key fetch (§14), if the maintainer wants it now;
   - "too old" gets a plain message only once that exact output has been seen
     and tested (§15).
4. **Releases:** Linux binaries in `release.sh`, `latest.json` keyed by
   platform and architecture, and the `-check` read-back covering them.
5. **Run on a real Linux machine with a drive.** Ask the maintainer what they
   have: distribution, Podman or Docker version, drive and how it's connected,
   CPU architecture.

## Working notes

- **The sandbox:** a sandboxed session cannot listen on a port or run
  containers. Give the maintainer exact commands to run, with `!` in front,
  and say what to look for.
- **Testing on other machines:** the maintainer tests on other machines with
  `git pull`, so push finished changes before giving test steps.
- **Commits:** commit only when asked, and branch first if asked to work off
  `main`.
- **The status table:** update `RELEASE.md` §12 "Where this stands" as things
  finish. It is the hand-over between sessions.
