# ARFABIT — Release

How ARFABIT reaches a person's computer, starts, stops, and stays up to date.
`ARCHITECTURE.md` is what ARFABIT does; this is how it is built, packaged,
installed, and updated.

**Status: a draft. Nothing here is built yet.** There are no releases today, and
the README says to build with Go. What is still open is gathered in §10.

---

## 0. Goals, in order

When two of these pull against each other, the one higher up wins.

1. **No terminal, ever, for someone using a release on macOS or Windows.** Not
   when they open ARFABIT themselves, and not when the computer starts it at
   login. A terminal is for people working on ARFABIT. On Linux, where ARFABIT
   is a server, a few setup steps are commands, as they are for any server
   (§3); installing and running it are not.
2. **Updates without effort.** A new version arrives without a trip to a
   website, never interrupts a disc, and never asks the person to know where
   anything was installed.
3. **Installing the way each platform expects.** One obvious way per platform,
   with instructions that work for an eight-year-old. Other package managers
   come after that, and only if they cost little.

Everything in `ARCHITECTURE.md` §0 still holds. ARFABIT itself stays one static
Go binary per platform, built without cgo. What surrounds it on each platform —
an app bundle, an icon, an installer — is packaging, and may be written in
whatever that platform expects. Nothing in the person's library is ever removed,
whether by installing, updating, or uninstalling.

---

## 1. Two parts: ARFABIT and its shell

| | What it is | Written in |
|---|---|---|
| **ARFABIT** | the program: the drive, the page, everything in `ARCHITECTURE.md` | Go, one static binary, the same on every platform |
| **The shell** | what makes it feel like an app on that platform: the icon, starting ARFABIT, starting at login, opening the page, installing updates | Swift on macOS, C# on Windows (§3) |

Linux has no shell. It runs ARFABIT as a system service, the way it runs Plex
Media Server: systemd starts it, and the page is the way in (§3).

The shell holds no knowledge of discs, jobs, or files. It talks to ARFABIT
through ARFABIT's own web API and live events, exactly as the page does, plus
one line and an exit code (below). So:

- the shell can be replaced, or missing, and ARFABIT still works through the page;
- both shells do the same few things, listed in §4;
- ARFABIT needs a few small additions for the shell to use (below), and holds
  no icon or login-item code for any platform.

### What ARFABIT needs

| Addition | Why | § |
|---|---|---|
| A version number | there is none today | 7 |
| **Show the icon** and **Start at login** settings, in its own state, sent in its live events | the shell reads them and applies them | 3, 4 |
| A way for the shell to report what the system did with Start at login | the page never disagrees with the system | 3 |
| The handshake and exit codes below | the shell knows where the page is, and why ARFABIT exited | 1 |
| Checking for, fetching, verifying, and staging updates; never installing them | goal 2 | 6 |
| Nothing said only to the terminal | nobody sees one | 2 |
| Telling systemd its address and what it is doing | `systemctl status arfabit` shows them | 3 |
| Restarting into a new version when systemd says one is installed | the package never interrupts a disc | 6 |
| `install-service`, for the Linux `.tar.gz` only | does what the package does | 5 |

And one removal: `internal/autostart`. Starting at login moves to the shells,
and on Linux to the package.

### ARFABIT and its shell

The whole contract between them:

- **The shell says it is there.** It starts ARFABIT with `-no-open -shell`:
  flags, since ARFABIT has no environment variables of its own
  (`ARCHITECTURE.md` §7). Under a shell, ARFABIT never restarts itself and
  never asks another copy to stand down (§3); it exits with a code and leaves
  the rest to the shell.
- **ARFABIT says where it is.** Once it is serving, it prints one line,
  `ARFABIT ready http://localhost:7847`, with its real address. The shell
  reads that line from ARFABIT's output, which it is already sending to the
  log, and opens the page with it. Until the line arrives, the page is not
  opened.
- **The exit code says what to do next:**

  | Exit | Means | The shell |
  |---|---|---|
  | 0 | Stop | quits |
  | 75 | Start me again | installs the staged update, if there is one (§6), then starts ARFABIT again |
  | anything else | ARFABIT stopped by itself | quits, after saying "ARFABIT stopped unexpectedly" with a link to the log |

  A stop the person did not ask for is never silent, and never restarted
  either (§3, Everywhere).
- **Everything else goes through the web API and live events**, as for the
  page: the status line, Show the icon, Start at login, and the shell's report
  of what the system did with it.

---

## 2. No terminal

| | Opened by hand | Started by the computer |
|---|---|---|
| **macOS** | `ARFABIT.app` | at login, the same app |
| **Windows** | Start menu → ARFABIT | at login, the same program |
| **Linux** | not opened; it is already running. The app menu entry opens the page | systemd, at boot |

None of these shows a terminal:

- **macOS.** A bare Unix program opened from Finder runs in Terminal; an app
  bundle does not. So a release is always `ARFABIT.app`. Its `Info.plist` sets
  `LSUIElement`, so there is no Dock icon; the menu bar icon, when shown, is
  the only sign it is running. The shell sends ARFABIT's output to
  `~/Library/Logs/arfabit.log`.
- **Windows.** What the person opens, and what starts at login, is the shell,
  a windowed program. It starts `arfabit.exe` with no console window
  (`CreateNoWindow`) and sends its output to
  `%LOCALAPPDATA%\arfabit\arfabit.log`. `arfabit.exe` stays an ordinary console
  program, so it still prints when run by hand in a terminal. It sits in a
  `core` folder inside the install folder, where nobody is directed to open it.
- **Linux.** A systemd service never has a terminal; its output goes to the
  journal (`journalctl -u arfabit`). The app menu entry says `Terminal=false`.

**A development build keeps its terminal.** `macos-dev.sh`, `go run`, and
`arfabit.exe` run by hand all print as they do today, without a shell.

**Anything worth saying goes on the page.** Today some things are only printed:
the address it is running at, "N jobs were interrupted when ARFABIT last
stopped", and warnings that the film list or saved blueprints could not be read.
Each of these needs a place on the page, or in the log, before a release has no
terminal to print them to.

---

## 3. The shell on each platform

| | Shell | Starts ARFABIT? | Runs |
|---|---|---|---|
| **macOS** | a Swift menu bar app, `ARFABIT.app` | yes, as its child | from login until logout |
| **Windows** | a C# notification area app, `ARFABIT.exe` | yes, as its child | from login until logout |
| **Linux** | none; systemd starts ARFABIT | — | from boot |

### macOS: a Swift app around the Go binary

```
ARFABIT.app/Contents/
  Info.plist                 LSUIElement: no Dock icon
  MacOS/ARFABIT              the shell, Swift
  Resources/arfabit          ARFABIT, the Go binary
```

- **Opening the app** starts the shell, which starts `arfabit -no-open -shell`
  as its child and opens the page once ARFABIT says it is ready (§1).
- **Opening it again** while it runs does not start a second copy: macOS hands
  the reopen to the running app, and the shell opens the page. It does this
  whether the icon is shown or hidden.
- **Hiding the icon** is a setting on ARFABIT's page. The shell follows it from
  ARFABIT's live events. With the icon hidden, ARFABIT runs with nothing on
  screen, and reopening the app is how to get to the page.
- **Stop** from the page or the menu: ARFABIT exits with 0, and the shell quits
  with it (§1).
- **Restart** from the page or the menu: ARFABIT exits with 75, and the shell
  starts it again (§1).
- **Start at login** is the shell registering itself with `SMAppService`,
  macOS's own login item API (macOS 13 and later). It replaces the LaunchAgent
  ARFABIT writes today:
  - macOS then starts the app as a double-click would, so opening it again
    reaches the running copy. To be tested once, not assumed (§10);
  - it appears in System Settings → General → Login Items as ARFABIT, with a
    switch the person can find without ARFABIT;
  - it starts the app once at login and never relaunches it, so Stop stays
    stopped.

  The toggle on the page and the tick in the menu change a setting in
  ARFABIT's state; the shell follows it, as it follows Show the icon, and
  reports back whether macOS accepted it. If the person turns it off in System
  Settings instead, the shell reports that too, so the page never disagrees
  with macOS.
- **Folder access prompts** (Downloads, §6 of `ARCHITECTURE.md`) are asked on
  behalf of the app, so they name ARFABIT.

Swift is used because a menu bar icon means calling Apple's own frameworks, and
from Go that needs cgo. Kept in the shell, ARFABIT stays cgo-free and builds
anywhere; only the shell and the `.app` need a Mac, and so do the `.dmg` and
signing regardless.

### Windows: a C# app around the Go binary

```
%LOCALAPPDATA%\Programs\ARFABIT\
  ARFABIT.exe                the shell, C#
  core\arfabit.exe           ARFABIT, the Go binary
```

The Go binary is in its own folder because Windows does not tell upper case
from lower in file names: `ARFABIT.exe` and `arfabit.exe` side by side would be
one file.

The same pattern as macOS:

- **Opening ARFABIT** from the Start menu, or at login, starts the shell, which
  starts `core\arfabit.exe -no-open -shell` as its child and opens the page once
  ARFABIT says it is ready.
- **Opening it again** while it runs does not start a second copy: the shell
  finds the running one (a named mutex) and opens the page.
- **Show the icon**, **Stop**, and **Restart** behave as on macOS, through the
  same exit codes. ARFABIT's own way of restarting on Windows, starting a fresh
  copy and exiting, is kept for development builds only.
- **Start at login** is a Task Scheduler entry at logon, as today, but created
  and removed by the shell, pointing at the shell. It follows the same setting
  in ARFABIT's state as on macOS, and has no restart on exit.

Written in C# against **.NET Framework 4.8, with WinForms' `NotifyIcon`**:

- .NET Framework 4.8 is part of Windows 10 and 11, so there is no runtime to
  install or bundle, and the shell is a small `.exe`.
- `NotifyIcon` and its menu are built into WinForms. It is the Windows
  counterpart of the Swift shell's menu bar item, at about the same size.
- It builds with the .NET SDK on GitHub's Windows runners, as a plain `.exe`
  that needs neither signing nor MSIX.

Set aside:

- **WinUI 3.** It has no notification area icon of its own and would still
  reach the Win32 API underneath. It needs the Windows App SDK runtime, and its
  usual packaging, MSIX, will not install unless signed (§5).
- **Newer .NET (8 and on).** It needs a runtime installed, or a copy of it
  bundled at tens of MB, and WinForms cannot be compiled to a small native
  program.
- **Go inside ARFABIT.** Go reaches `Shell_NotifyIcon` without cgo, and Go
  compiles Windows-only files into the Windows build alone. But it would put the
  shell's work into ARFABIT on one platform and not the others.

### Linux: a system service, as Plex Media Server does it

Installing the package is the whole setup, as it is for Plex:

- It creates a system account, `arfabit`, and adds it to the group that may read
  disc drives on that distribution (`cdrom` on Debian and Ubuntu, `optical` on
  Arch). That group is the account's own, not a person's.
- It installs `arfabit.service`, enables it, and starts it. From then on
  ARFABIT runs as `arfabit` from boot, whether anyone is logged in or not.
- Its state lives in `/var/lib/arfabit`, the account's home, which systemd
  creates for it (`StateDirectory=arfabit`).
- There is no `Restart=` line, so Stop on the page stays stopped until the next
  boot, or `sudo systemctl start arfabit`.

**Finding it.** Like Plex's `http://<computer>:32400/web`, ARFABIT is at
`http://<computer>:7847`, and the install instructions say so. The address can
also be read from the computer itself:

```
systemctl status arfabit
```

The service is `Type=notify`: ARFABIT tells systemd its address and what it is
doing, and `systemctl status` shows that on its `Status:` line, for example
`Status: "Open http://nas.local:7847 — nothing running"`. It stays there
however much has been logged since, which the log's own first line would not.
When ARFABIT listens on every network, as it does by default, that address
uses the computer's own name. Today ARFABIT writes `localhost` there, which
works only from the computer itself.
The package also adds an app menu entry, ARFABIT, which opens
`http://localhost:7847` for anyone at a desktop.

**No icon.** GNOME shows none without an extension, most Linux computers doing
this job have no screen, and the page does everything a menu would.

**The settings page** has no start-at-login toggle on Linux. It says ARFABIT is
started by the system at boot, and that `sudo systemctl disable arfabit` stops
that.

### Linux: permissions are the person's to give

ARFABIT runs as `arfabit`, never as the person. As with Plex, giving that
account access to anything outside its own home is the person's job, done with
the administrator password, which ARFABIT does not have and never asks for.
ARFABIT's part is to need as little as possible, to check what it needs, and to
say exactly which command gives it.

What it needs, and what the person does:

| Needed | Default | If not |
|---|---|---|
| **Write the library** | `/var/lib/arfabit/library`, made by the package: nothing to do | a library elsewhere, such as the folder Plex reads, needs the `arfabit` account given write access there, for example `sudo setfacl -R -m u:arfabit:rwX -m d:u:arfabit:rwX /media/films` |
| **Plex reads the library** | films are written readable by everyone: nothing to do | — |
| **The person changes files in the library** | — | the person joins the `arfabit` group: `sudo usermod -aG arfabit <them>`. The service writes files its group can change (`UMask=0002`), so the originals can be moved to the trash, as the README tells people to do |
| **MakeMKV's key** | — | MakeMKV reads it from the account's home, `/var/lib/arfabit/.MakeMKV/settings.conf`, not from the person's. That home is ARFABIT's own, so the key step on the page (`ARCHITECTURE.md` §14) writes it there: the current beta key, or one pasted in. No command needed |
| **Read the disc drive** | the package adds `arfabit` to the drive group: nothing to do | — |

**Where the person learns this:**

- **The first screen.** On a fresh install, Doctor is already the first thing
  the page shows (`ARCHITECTURE.md` §14). On Linux it becomes a short welcome:
  one step for each row above that is not yet true, each with its command
  written out for this computer (the real library path, the real account
  names) and a **Check again** button. Steps already true are shown as done.
  The welcome ends when everything ARFABIT needs is in place, and Doctor keeps
  checking afterwards.
- **The Linux install instructions** carry the same table, for anyone setting
  up before opening the page.

A command is offered only for a situation Doctor has checked, such as "the
`arfabit` account cannot write to this folder". Whatever the system reported is
shown with it, verbatim, as `ARCHITECTURE.md` §15 requires; the command is what
to do next, not an explanation of why.

This replaces the user unit and `loginctl enable-linger` in
`internal/autostart/autostart_linux.go`. That approach needed no password, but
it quietly did nothing on systems that refuse lingering, and a system service is
what anyone running a Linux server expects.

### Everywhere

- Running while logged in is the rule on macOS and Windows. A computer that
  should rip with nobody at it uses automatic login, which the person turns on;
  ARFABIT says where the setting is, next to the start-at-login toggle, and
  never changes it itself.
- Nothing restarts ARFABIT unless it asks to be, with exit code 75 or, on
  Linux, by restarting in place. Not the shells and not systemd, as now
  (`ARCHITECTURE.md` §13), so Stop stays stopped, and an unexpected stop is
  reported but not retried.
- The existing takeover, where a newer copy asks the running one to stand down,
  stays for development builds only. A person who opens ARFABIT twice wants the
  page, not a restart.

---

## 4. The icon

macOS and Windows only. The icon is a shortcut to the page. It never does
anything the page cannot.

| Item | Does |
|---|---|
| *What is happening* | one line, not pressable: "Nothing running", "Copying Blade Runner — 42%" |
| **Open ARFABIT** | opens the page in the browser |
| **Start at login** | a tick, the same setting as in Settings |
| **Restart ARFABIT** | the page's restart |
| **Stop ARFABIT** | the page's stop. The icon goes with it |
| *Update and restart* | appears only when a new version is ready; see §6 |

The status line comes from the same live events the page uses.

**Show the icon** is a setting on the page, on by default, kept in ARFABIT's
own state. Both shells follow it. Hidden, ARFABIT keeps running, and opening
ARFABIT again (the app, or the Start menu entry) opens the page.

"Stop", not "Quit", to match the page's own button. `ARCHITECTURE.md` §15's
word list already makes it *stop* for abort and kill.

---

## 5. Packages

Nothing is signed with a paid certificate for now. Each platform's section says
what that costs the person, once, and how the instructions deal with it. Paid
signing can be added later without changing anything else here.

| | Primary | Later, if cheap |
|---|---|---|
| **macOS** | `ARFABIT.dmg` holding `ARFABIT.app`, on GitHub Releases | Homebrew cask |
| **Windows** | `ARFABIT-setup.exe`, on GitHub Releases | winget |
| **Linux** | `.deb` and `.rpm` on GitHub Releases, which add ARFABIT's own package repository | — |
| **Linux, other** | `.tar.gz` with the binary, on GitHub Releases | — |

### macOS

- A `.dmg` with the app and an Applications shortcut, so installing is one drag.
- **Moving to Applications.** An app opened from Downloads straight after
  downloading is run by macOS from a hidden, read-only copy in a new place each
  time (App Translocation). It could not replace itself to update, and the
  login item would point at a path that no longer exists. So when the shell
  finds it is not in `/Applications` or `~/Applications`, it asks the person to
  move it there, and does the move when they agree. Dragging from the `.dmg`
  avoids this from the start.
- **Not notarized.** The first time it is opened, macOS refuses, and the person
  has to go to System Settings → Privacy & Security and press **Open Anyway**.
  On current macOS there is no shorter way; the install instructions show this
  step with pictures. It happens once: updates are downloaded by ARFABIT, not a
  browser, so macOS does not ask again.
- **Signed with a free, self-made certificate**, the same one every release.
  This does nothing for the first-open step above. It keeps the app's identity
  the same from one version to the next, and macOS remembers folder
  permissions by that identity. Without it, every update would ask again for
  access to Downloads. To be tested on a real update (§10).

### Windows

- **An installer**, because a bare `.exe` lives wherever it was downloaded.
  Moved or cleaned out of Downloads, it breaks autostart and updates. The
  installer:
  - puts ARFABIT in `%LOCALAPPDATA%\Programs\ARFABIT\`, per person, with no
    administrator password;
  - installs both programs, the shell and `core\arfabit.exe` (§3);
  - adds it to the Start menu, and to Installed apps so it can be uninstalled;
  - opens it when done.
- **The same installer is the update** (§6), run silently.
- **Built with Inno Setup**, not MSIX. An MSIX package cannot be installed at
  all unless it is signed by a certificate the computer already trusts, and
  asking people to trust a self-made one is worse than the warning below.
- **Not signed.** Running the downloaded installer the first time shows
  "Windows protected your PC"; the person presses **More info**, then **Run
  anyway**. The instructions show this with pictures. It happens once: updates
  are downloaded by ARFABIT, not a browser, so Windows does not ask again.
- Windows settings that refuse unsigned programs altogether, such as Smart App
  Control, are the person's choice, and are not worked around.

### Linux

- **A package repository** for apt (Debian, Ubuntu, Mint) and dnf (Fedora),
  served as static files from ARFABIT's own domain through Cloudflare, from a
  repository of its own. It is signed with ARFABIT's own key, which is free;
  apt and dnf refuse a repository that is not. Updates come with the rest of
  the system's updates, which is goal 2 done the Linux way.
- **Installing is opening one file.** The `.deb` and `.rpm` are also on GitHub
  Releases. Opened from the browser, the system's Software app installs it,
  and installing adds ARFABIT's repository and its key, so every later version
  arrives through the repository. Adding the repository by hand, then
  `sudo apt install arfabit`, does the same, for anyone who prefers it.
- The package holds the binary in `/usr/bin`, the unit
  `/usr/lib/systemd/system/arfabit.service`, and the app menu entry. It lists
  `ffmpeg` as a dependency. It cannot list MakeMKV, which no distribution
  carries.
- **Installing sets everything up and starts it** (§3): the `arfabit` account,
  its drive group, and the service, enabled and running. There is no second
  command.
- **`.tar.gz`** for everything else: the same binary, unit, and app menu entry.
  One command copies them into place and does what the package does:

  ```
  sudo ./arfabit install-service
  ```

  Updates are by hand: the page says a new version is out, and shows the two
  steps, unpacking the new `.tar.gz` and running that command again.

### Why not Flatpak, Snap, or AppImage

- **Flatpak** runs desktop apps in a sandbox inside someone's login. ARFABIT
  runs `makemkvcon` and `ffmpeg` from outside any sandbox, reads the drive
  directly, and runs as a service from boot. It would need every sandbox
  permission turned off, which Flathub does not accept, and it still could not
  be a service.
- **Snap** has the same problems unless given unconfined access, which the Snap
  Store grants case by case.
- **AppImage** makes a program one file that runs anywhere. ARFABIT's Linux
  build is already that, being static.

### Homebrew and winget

Third, and not needed for goals 1 or 2: ARFABIT updates itself (§6), and a
package manager replacing the same files would disagree with it about what is
installed. If added, ARFABIT notices it was installed that way and turns its own
updater off, saying `brew upgrade arfabit` or `winget upgrade arfabit` instead,
as a copy installed by apt or dnf already does (§6).

The Linux repository comes first because on Linux it *is* the update mechanism.
On macOS and Windows these would only be a second way in.

---

## 6. Updates

### The rule

**An update never interrupts a disc.** No copy, conversion, or reading of
subtitles is stopped to install a new version. The new version waits until
nothing is running, as the existing restart does.

### Finding out

Once a day, and when **Check for updates** is pressed in Settings, ARFABIT asks
ARFABIT's own domain what the newest version is: one small file,
`latest.json`, naming the version, what changed, and where each platform's
download is, with each download's checksum. The daily check is on by default
and can be turned off. It is the only contact ARFABIT makes that the person did
not ask for, and Settings says so beside it. Nothing is sent except the request
itself.

A copy installed by apt or dnf makes no such check: the system's own updates
already do it (Linux, below).

A file on ARFABIT's own domain, rather than GitHub's API, so that where releases
are kept can change without changing what older versions ask.

### macOS and Windows

1. A newer version is found. ARFABIT downloads it in the background.
2. ARFABIT verifies it (below), and stages it: puts it, verified, in a fixed
   place beside its own state. A download that fails the check is discarded,
   and the log says so with the raw reason.
3. The page and the icon say "A new version is ready", with what changed.
4. It is installed when nothing is running, on its own. **Install updates by
   themselves** is on by default; turned off, it waits for **Update and
   restart** on the page or in the icon's menu.
5. ARFABIT exits with 75 (§1). The shell finds the staged update and installs
   it, then the new version starts and the page reconnects.

ARFABIT fetches and verifies; the shell installs. So the Go binary holds no
installing code for any platform, and nothing replaces a program while that
program is running.

Replacing the program:

- **macOS:** the shell unpacks the new `ARFABIT.app` beside the old one, swaps
  it in with renames, and opens it as a new copy
  (`NSWorkspace.OpenConfiguration.createsNewApplicationInstance`), then quits.
  Opened the ordinary way, macOS would hand the open to the running app, the
  old shell, and nothing new would start. The new shell, finding the old one
  still quitting, waits for it rather than handing over to it.
- **Windows:** the staged update is the new installer. The shell runs it
  silently and quits. The installer waits until the shell's named mutex is
  released (§3), so it never finds the shell still running, then replaces both
  programs and starts the new shell. The update is the same installer as a
  first install, so there is one way files get onto the computer, not two.

The login item and the scheduled task point at a path that does not change, so
starting at login keeps working. Neither platform shows its first-open warning
again, because the download did not come through a browser.

### Linux

Updates arrive with the system's own updates (`apt upgrade`, the Software app,
or unattended upgrades). The package **does not restart the service** when it
is installed, because that would stop whatever is running. Instead it runs
`systemctl reload arfabit`, and the unit's `ExecReload` sends ARFABIT a signal
meaning "a new version is installed". ARFABIT says so on the page and in
`systemctl status`, and restarts into it when nothing is running, on the same
setting as macOS and Windows. A stopped service is not started by the reload.
This differs from Plex's package, which restarts its service on every upgrade.

For a `.tar.gz` install, the page says a new version is out and shows the two
steps to install it.

### Checking a download

With no paid signing, the check is ARFABIT's own:

- `latest.json` is signed as a whole with an Ed25519 key whose public half is
  built into the program, checked with Go's own `crypto/ed25519`. One file,
  one fetch, one check: the version, what changed, and each download's
  checksum are all covered, so nothing in it can be changed in transit. The
  download must match its checksum.
- The key is used for nothing else, and lives only in CI secrets.
- On Linux, apt and dnf check the repository's signature themselves.

Paid signing, if added, becomes a second check alongside this one.

### Things an update has to get right

- **State made by the last version opens in the new one:** jobs, projects,
  blueprints, drives, calibration, settings. A change to any of these formats
  reads the old form. Going back to an older version is not promised, and the
  release notes say when it would not work.
- **Restart starts the new file.** On Linux, ARFABIT restarts by replacing
  itself with whatever is at its path. Linux reports a replaced program's path
  with " (deleted)" on the end; Go's `os.Executable` removes that, so the
  restart finds the new file at the same path.
- **A new version that does not start.** The old one is kept until the new
  ARFABIT has printed its ready line (§1), so that a failed update can put it
  back: on macOS the old app, renamed beside the new; on Windows the previous
  installer. The new shell does this, since it is the one waiting for that
  line; a new shell that does not start at all cannot, and the old version is
  still beside it for the person to open.

---

## 7. Building a release

- **Version.** Semantic versions, starting at `v0.1.0`. Go's own tools expect
  them in tags, and a date scheme would read to Go as major version 2026, which
  it requires the module path to repeat. The number is built in with
  `-ldflags -X`, and shown in Settings and at the top of every log. A build
  without a tag says `dev`, and never checks for updates.
- **Built by GitHub Actions** when a tag is pushed:
  - ARFABIT for every platform, cgo off, from any runner: macOS arm64 and amd64,
    Windows amd64, Linux amd64 and arm64;
  - the macOS shell and `.app`, on a macOS runner, as one universal app;
  - the Windows shell and installer, on a Windows runner, which carries the
    .NET SDK. Whether it carries Inno Setup too, or it is installed in the
    job, is open (§10).
- **Then**, in the same run: signing with the self-made macOS certificate,
  making the `.dmg`, the installer, `.deb` and `.rpm`; publishing the release;
  writing and signing `latest.json`; and updating the package repository.
- **Signing keys** live only in CI secrets.

---

## 8. Uninstalling

Uninstalling removes ARFABIT's programs and its login item or service. It never
removes the library, the originals, or ARFABIT's own settings and state, so
installing again carries on where it left off.

| | How |
|---|---|
| macOS | Settings → **Remove ARFABIT from this computer**, which removes the login item and moves the app to the Trash |
| Windows | Installed apps, as for any other program. The installer's uninstaller removes the scheduled task |
| Linux | `sudo apt remove arfabit`, or `sudo dnf remove arfabit`, which stops and disables the service |

On Linux, `/var/lib/arfabit` and the `arfabit` account stay, even on
`apt purge`, because the library may be inside it. Debian's convention is that
purge removes a package's data; ARFABIT's package departs from it here, as
`ARCHITECTURE.md` §0 requires.

---

## 9. Where things live

```
cmd/arfabit/main.go            install-service, version, telling systemd its status,
                               the ready line and exit codes for the shell
internal/update/               check, download, verify, stage
internal/restart/              also restarts into a newly installed version
internal/autostart/            removed: the shells and the Linux package take this over
shell/
  macos/                       the Swift menu bar app: icon, login item, Info.plist, .dmg layout
  windows/                     the C# notification area app: icon, scheduled task
packaging/
  windows/                     Inno Setup script
  linux/                       arfabit.service, app menu entry, account and group setup, nfpm config
.github/workflows/release.yml
```

The package repository and `latest.json` live in a repository of their own,
served from ARFABIT's domain.

---

## 10. Decided, and still open

**Decided**

| | |
|---|---|
| ARFABIT itself | Go, cgo off, one static binary |
| macOS shell | Swift, around the Go binary |
| Windows shell | C#, .NET Framework 4.8, WinForms `NotifyIcon`, around the Go binary |
| Linux | no shell and no icon; a system service as its own `arfabit` account, set up and started by the package, as Plex does |
| Linux permissions | the person's to give; ARFABIT checks and shows the command, in a welcome on the first screen and in the install instructions |
| Linux library | `/var/lib/arfabit/library`, readable by everyone, changeable by the `arfabit` group |
| Starting at login | the shell: `SMAppService` on macOS (13 and later), a scheduled task on Windows; `internal/autostart` removed |
| Settings that refuse unsigned programs | the person's choice; not worked around |
| Signing | none paid for now; a free self-made certificate on macOS; `latest.json`, holding each download's checksum, signed with Ed25519 |
| ARFABIT and its shell | a `-shell` flag, a ready line, and exit codes 0, 75, and anything else (§1); the shell installs updates |
| Linux packages | on GitHub Releases as well as the repository, adding the repository when installed; no `latest.json` check; `systemctl reload` after an upgrade |
| Windows installer | Inno Setup |
| Linux repository | its own domain and repository, through Cloudflare |
| Updates | daily check on; installing by themselves when nothing is running, on |
| Version | semantic, from `v0.1.0` |

**Open**

To be tested on a real machine before it is relied on:

- **`SMAppService` at login** starts the app as a double-click would, so
  opening it again reaches the running copy (§3).
- **The self-made certificate** keeps macOS's folder permissions from one
  version to the next (§5).
- **Dragging the app to the Trash** removes its `SMAppService` login item. If
  it does, Settings → **Remove ARFABIT from this computer** (§8) is not needed.
- **Inno Setup** on GitHub's Windows runners: already there, or installed in
  the job (§7).

To decide:

- **Windows login item.** A scheduled task, as now, or the per-person Run key.
  The Run key is what Task Manager → Startup apps and Settings → Apps → Startup
  show and switch off, which is the Windows counterpart of the macOS Login
  Items switch (§3). It reverses `ARCHITECTURE.md` §13.
- **The page on the network.** ARFABIT listens on every network with no
  password. On a Linux server started at boot, anyone on the network can use
  the page. Either that stays, said plainly in the install instructions, or
  the default changes.

The MakeMKV key step and fetching the current beta key, which the Linux
welcome relies on, are app work, described in `ARCHITECTURE.md` §14.

---

## 11. How ARCHITECTURE.md reflects this

`ARCHITECTURE.md` now describes all of this, and marks what is not built:

- **§0, §3.** The shells, as packaging around the one Go binary; rows for the
  shell, starting at login, and releases.
- **§5.** `internal/update/`, `shell/`, `packaging/`, not built;
  `internal/autostart/` goes once the shells and the Linux package replace it.
- **§6.** For the Linux service, state in `/var/lib/arfabit` and the library
  in `/var/lib/arfabit/library`.
- **§7.** No environment variables of ARFABIT's own, so the shell uses the
  `-shell` flag.
- **§13.** Starting at login through the shells and the Linux package; the exit
  codes; what is built today and is to be replaced.
- **§14.** The Settings this adds, and the Linux welcome in Doctor.
- **§16, §17.** Releases, not built, and next in the roadmap.

A change to anything here that touches those sections changes both documents.

---

## 12. Order of work

1. A version number, everything printed also on the page or in the log, and
   ARFABIT's side of the shell contract: the `-shell` flag, the ready line, and
   the exit codes (§1). All are needed by everything after.
2. The macOS app: the Swift shell, its login item, the bundle, moving to
   Applications, the self-made certificate, the `.dmg`.
3. Windows: the C# shell, its scheduled task, the installer.
4. Updates on macOS and Windows, with the signed `latest.json`.
5. Linux: the `arfabit` account and service, `systemctl status`, packages, the
   repository, `install-service` for the `.tar.gz`, restarting on reload.
6. Removing `internal/autostart`, once both shells and the Linux service start
   ARFABIT by themselves.
7. Homebrew and winget, if still wanted.
