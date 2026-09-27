// ARFABIT's page.
//
// Everything arrives over a server-sent event stream, so the page never
// reloads. That is deliberate: a page that refreshes underneath you throws
// away your scroll position, your text selection and whatever you were
// searching for.
//
// Order matters in this file. Everything is declared before it is used, and
// nothing runs until start() at the bottom, because a script that throws part
// way through leaves half the page dead with no sign of why.

"use strict";

const $ = (id) => document.getElementById(id);

const seenLogIds = new Set();
let followLog = true;
let events = null;
let drives = [];
let driveBusy = "";
let activeJobs = [];
let lastActive = []; // every active job, whatever the queue is showing
let queueFilter = "all";
let focusedJob = "";
let resumable = {};
let driveSettings = {}; // what each drive does when a disc goes in, by name
let blueprintNames = []; // for "When a disc goes in"
let editing = null;  // what on the Plan shown can still be changed
let copies = {};   // how each film's SRTs stand, by the task that made them
let copiesOf = {}; // the same, by the OCR task whose SRT they copy
let line = [];
let clockTimer = null;

// How far this browser's clock is ahead of ARFABIT's, which may be running on
// another computer. Clocks are started from ARFABIT's times, so without this
// every one of them would be out by the difference.
let clockOffset = 0;

// When each clock on the page started, by browser time. Fixed the first time
// a stage is seen, so later updates never nudge a clock that is running.
const clockStarts = new Map();

// --- problems the page cannot hide --------------------------------------

// showProblem puts an error where it can be seen.
//
// A silent failure in here used to leave buttons that did nothing and a page
// that looked merely slow. Anything that goes wrong now says so.
function showProblem(text, kind) {
  let banner = $("page-problem");
  if (!banner) {
    banner = document.createElement("div");
    banner.id = "page-problem";
    banner.className = "page-problem";
    document.body.prepend(banner);
  }

  banner.hidden = false;
  banner.textContent = kind === "script"
    ? `Something in this page stopped working: ${text}. Reloading may help.`
    : text;
}

window.addEventListener("error", (e) => showProblem(e.message, "script"));
window.addEventListener("unhandledrejection", (e) => showProblem(String(e.reason), "script"));

// --- small helpers --------------------------------------------------------

function show(id, visible) {
  const el = $(id);
  if (el) el.hidden = !visible;
}

function bytes(n) {
  if (!n) return "0 bytes";
  const units = ["bytes", "kB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1000 && i < units.length - 1) { n /= 1000; i++; }
  return `${i === 0 ? n : n.toFixed(1)} ${units[i]}`;
}

const LANGUAGES = {
  eng: "English", fra: "French", fre: "French", spa: "Spanish", deu: "German",
  ger: "German", ita: "Italian", jpn: "Japanese", nld: "Dutch", dut: "Dutch",
  por: "Portuguese", rus: "Russian", kor: "Korean", zho: "Chinese", chi: "Chinese",
};

function languageName(code) {
  return LANGUAGES[(code || "").toLowerCase()] || (code ? code.toUpperCase() : "Unknown");
}

// filmName is what to call a job on the page: its title and, once known, its
// year, which is what tells two films of the same name apart.
function filmName(job, fallback = "A disc") {
  const name = job.title || job.disc_name || job.disc_label || fallback;
  return job.year ? `${name} (${job.year})` : name;
}

function stageWords(stage) {
  return {
    LAB: "Making part of it",
    QUEUED: "Waiting its turn",
    SCAN: "Reading the disc",
    PLAN: "Working out what to do",
    RIP: "Copying the disc",
    OCR: "Reading the subtitles",
    PACKAGE: "Making the movie file",
    DELIVER: "Putting it in your library",
    EJECT: "Ejecting the disc",
  }[stage] || "Working";
}

// busy marks a button while its request is in flight, so a click is never
// swallowed silently.
async function busy(button, working, done, action) {
  const original = button.textContent;
  button.disabled = true;
  button.classList.add("busy");
  button.textContent = working;

  const restore = () => {
    button.classList.remove("busy", "done");
    button.textContent = original;
    button.disabled = false;
  };

  try {
    const result = await action();
    button.classList.remove("busy");

    if (result !== null && done) {
      button.classList.add("done");
      button.textContent = done;
      setTimeout(restore, 1600);
      return result;
    }
    restore();
    return result;
  } catch (err) {
    restore();
    showProblem(String(err));
    return null;
  }
}

// --- talking to the server ------------------------------------------------

async function post(path, body) {
  const res = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : null,
  });
  if (!res.ok) {
    const problem = await res.json().catch(() => ({ message: "Something did not work." }));
    showNotice(problem);
    return null;
  }
  return res.json();
}

// showNotice puts a problem where it will be seen.
//
// It used to write into the Plan card, which is hidden whenever there is no
// disc waiting — so every failure from the settings buttons went nowhere at
// all, and a click that did not work was indistinguishable from one that had
// not registered.
function showNotice(problem) {
  const text = problem.detail
    ? `${problem.message}\n\n${problem.detail}`
    : problem.message;

  const plan = $("plan");
  const notice = $("plan-notice");

  if (plan && !plan.hidden && notice) {
    notice.hidden = false;
    notice.textContent = text;
    return;
  }

  showProblem(text);
}

// --- the log --------------------------------------------------------------

function logLine(entry) {
  const line = document.createElement("div");
  line.className = "line";
  line.dataset.text = `${entry.disc || ""} ${entry.stage} ${entry.text}`.toLowerCase();
  line.dataset.job = entry.job || "";

  const when = document.createElement("span");
  when.className = "when";
  when.textContent = new Date(entry.time).toLocaleTimeString();

  const stage = document.createElement("span");
  stage.className = "stage";
  stage.textContent = entry.stage;

  // Which disc this line is about, shown only when several are in flight.
  const disc = document.createElement("span");
  disc.className = "disc";
  disc.textContent = entry.disc || "";

  const text = document.createElement("span");
  text.className = "text";
  text.textContent = entry.text;

  line.append(when, stage, disc, text);

  if (!entry.detail) return line;

  // The raw account is always one click away and never summarised.
  const wrap = document.createElement("div");
  wrap.append(line);

  const details = document.createElement("details");
  const summary = document.createElement("summary");
  summary.textContent = "Technical details";
  const pre = document.createElement("pre");
  pre.textContent = entry.detail;
  details.append(summary, pre);
  wrap.append(details);

  return wrap;
}

function applyFilter() {
  const term = $("log-filter").value.trim().toLowerCase();

  for (const line of $("log").querySelectorAll(".line")) {
    const matchesText = !term || line.dataset.text.includes(term);
    const matchesJob = !focusedJob || line.dataset.job === focusedJob;
    line.style.display = matchesText && matchesJob ? "" : "none";
  }

  // The disc column is noise when there is only one disc to speak of.
  const several = new Set(
    [...$("log").querySelectorAll(".line")].map((l) => l.dataset.job)
  ).size > 1;
  $("log").classList.toggle("several-discs", several && !focusedJob);

  $("log-scope").textContent = focusedJob
    ? `Showing one disc only. Click it again in the queue to see everything.`
    : "";
}

// focusJob narrows the log to one disc, or widens it again.
//
// Clicking the disc you are watching is how you say "just this one"; clicking
// it again is how you take that back.
function focusJob(id) {
  focusedJob = focusedJob === id ? "" : id;

  for (const card of $("working-list").children) {
    card.classList.toggle("focused", card.dataset.job === focusedJob);
  }
  applyFilter();
}

function appendLog(entries) {
  if (!entries || entries.length === 0) return;

  const box = $("log");
  const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40;

  for (const entry of entries) {
    // Line numbers restart with each disc, so the disc is part of the
    // identity. Without it, three discs at once would hide each other's lines.
    const key = `${entry.job}:${entry.id}`;
    if (seenLogIds.has(key)) continue;
    seenLogIds.add(key);
    box.append(logLine(entry));
  }

  applyFilter();
  show("log-card", box.children.length > 0);
  if (followLog && atBottom) box.scrollTop = box.scrollHeight;
}

// note makes a line of text for prepending to a section.
function note(text) {
  const el = document.createElement("div");
  el.className = "drive-fast";
  el.textContent = text;
  return el;
}

// --- the drive ------------------------------------------------------------

// renderDrives says what is in the drive, so nobody has to ask.
//
// The heading is always the drive's own name. Somebody with two drives needs
// to know which box is which, and a heading that changed with every disc would
// make them read it again each time to find out.
function renderDrives(list) {
  drives = list || [];

  const scan = $("scan");
  const drive = drives[0];
  const loaded = drives.find((d) => d.Loaded);

  $("drive-name").textContent = (drive && drive.Name) || "Disc drive";
  scan.textContent = "Plan";
  renderDriveWhen(drive);
  renderDiscSource();

  const status = (text, canRead, canEject) => {
    $("drive-status").textContent = text;
    scan.disabled = !canRead;
    $("eject").disabled = !canEject;
    $("drive-check").disabled = !drive || !!driveBusy;
  };

  // The drive may be busy with a disc even while other films convert.
  if (driveBusy) {
    status(`Busy with ${driveBusy}. It will be free once that disc comes out.`, false, false);
    return;
  }

  if (!drive) {
    status("No disc drive found. Plug one in and ARFABIT will notice. Some drives need their own power supply.", false, false);
    return;
  }

  if (loaded) {
    status(`${loaded.Label || "A disc"} is in the drive.`, true, true);
    return;
  }

  // The drive is there, just not holding anything readable. Saying which is
  // the difference between "put a disc in" and "wait a moment". An open tray
  // and a closed empty one are the same thing to the person standing there,
  // and slot-loading drives have no tray, so the wording never mentions one.
  if (drive.State === "loading") {
    status("Reading the disc. The drive is spinning up, which takes a few seconds.", false, true);
    return;
  }
  status("Empty. Insert a disc and ARFABIT will notice.", false, true);
}

// renderDriveWhen shows what the drive does when a disc goes in, and lets it
// be changed. Left alone while somebody has it open.
function renderDriveWhen(drive) {
  const select = $("drive-when");
  if (document.activeElement === select) return;
  select.disabled = !drive;
  const options = [["nothing", "Nothing"], ["copy", "Copy"], ["blueprint:", "Blueprint: Defaults"],
    ...blueprintNames.map((name) => [`blueprint:${name}`, `Blueprint: ${name}`])];
  const set = (drive && driveSettings[drive.Name]) || { when: "nothing" };
  const value = set.when === "blueprint" ? `blueprint:${set.blueprint || ""}` : set.when;
  if (!options.some(([v]) => v === value)) options.push([value, `Blueprint: ${set.blueprint}, which is no longer there`]);
  select.replaceChildren(...options.map(([v, label]) => new Option(label, v, false, v === value)));

  $("drive-when-note").textContent = {
    nothing: "Press Plan to read a disc and decide what to do with it.",
    copy: "A disc is copied as soon as it goes in, and its subtitles read into text. Its Plan stays open while it copies, to add a file or change anything.",
    blueprint: `A disc is copied as soon as it goes in, its subtitles read, and a file made from ${set.blueprint ? `the ${set.blueprint} blueprint` : "the defaults"}. Its Plan stays open while it copies.`,
  }[set.when] || "";
}

async function saveDriveWhen() {
  const drive = drives[0];
  if (!drive) return;
  const [when, blueprint = ""] = $("drive-when").value.split(/:(.*)/s);
  const result = await post("/api/drive-setting", { drive: drive.Name, when, blueprint });
  if (result) {
    driveSettings[drive.Name] = result;
    renderDriveWhen(drive);
  }
}

// readDisc reads the disc in the drive, for its Plan. The scan outlives the
// request that starts it, so the button stays put until a job appears and the
// drive says it is busy.
async function readDisc(button) {
  const label = button.textContent;
  button.disabled = true;
  button.textContent = "Waking the drive\u2026";

  const result = await post("/api/scan");
  if (result) {
    button.textContent = "Reading the disc\u2026";
  } else {
    button.disabled = false;
    button.textContent = label;
  }
}

// --- the plan -------------------------------------------------------------



function spaceMessage(space) {
  if (space.Unknown) {
    return "ARFABIT could not check how much room is left, so it will go ahead.";
  }
  const lead = space.Fits
    ? "There is just enough room, and the estimate could be low."
    : "There is not enough room for this disc.";
  return `${lead}\n\nThis rip needs about ${bytes(space.Needed)}.\n` +
    `The drive has ${bytes(space.Free)} free.\n` +
    `Your originals take ${bytes(space.Originals)}.\n` +
    `Your films take ${bytes(space.Library)}.`;
}

// sendPlanChange sends what can be changed on the Plan outside its film.
function sendPlanChange() {
  post("/api/plan", {
    convert: $("plan-convert").checked,
    edition: $("plan-edition").value,
  }).then(() => refresh());
}

// The film on the Plan, as last drawn and as being edited. It is drawn
// again only when it really changes, so an update arriving from elsewhere does
// not pull a list out from under somebody changing it.
let planProject = null;
let planProjectDrawn = "";

function renderPlanProject(plan) {
  const incoming = JSON.stringify(plan.project || null);
  if (incoming === planProjectDrawn && planProject) return;
  planProjectDrawn = incoming;
  planProject = plan.project ? JSON.parse(incoming) : { containers: ["mkv"], items: [] };
  planProject.items = planProject.items || [];
  drawPlanProject(plan);
}

function drawPlanProject(plan) {
  renderProjectEditor($("plan-project"), plan.tracks || [], planProject, () => {
    drawPlanProject(plan);
    planProjectDrawn = JSON.stringify(planProject);
    post("/api/plan", { project: planProject }).then(() => refresh());
  });
}

async function saveTitle() {
  await busy($("title-save"), "Saving", "Saved", () =>
    post("/api/title", {
      title: $("title-name").value,
      year: Number($("title-year").value) || 0,
    }));
  refresh();
}

function renderTitleChoice(job) {
  $("title-choice").hidden = false;

  // Left alone while somebody is typing in them, or the next update would
  // snatch the text out from under them.
  const typing = [$("title-name"), $("title-year")].includes(document.activeElement);
  if (!typing) {
    $("title-name").value = job.title || job.disc_name || "";
    $("title-year").value = job.year ? String(job.year) : "";
  }

  const matches = job.matches || [];
  $("title-suggestions").replaceChildren(...matches.map((match) => {
    const button = document.createElement("button");
    button.textContent = `${match.title.Name}${match.title.Year ? ` (${match.title.Year})` : ""}`;
    button.title = match.Why || "";
    button.addEventListener("click", () => {
      $("title-name").value = match.title.Name;
      $("title-year").value = match.title.Year || "";
      saveTitle();
    });
    return button;
  }));
}

// existingMessage says which files are already there. ARFABIT does not replace
// files, so the Plan cannot start until they are moved or renamed.
function existingMessage(paths, plan) {
  return paths.map((path) => {
    const name = path.split(/[\\/]/).pop();
    const folder = path.slice(0, path.length - name.length - 1);
    return name.includes("{edition-Original}") || name === plan.rip_name
      ? `You already have an original of this disc: ${name}, in ${folder}. ARFABIT does not replace files, so move that one somewhere else to copy this disc again.`
      : `${name} is already in ${folder}. ARFABIT does not replace files, so give this one a different edition, or move that file somewhere else.`;
  }).join("\n\n");
}

// renderOriginal says what goes into the original: everything on the disc, as it
// is. Nothing is chosen here and nothing is converted, so there is nothing to
// tick and no talk of converting.
function renderOriginal(plan) {
  const tracks = plan.tracks || [];
  const video = tracks.find((t) => t.kind === "video");
  $("original-video").textContent = video
    ? video.label
    : `${plan.source_codec} · ${plan.resolution}${plan.hdr ? " · HDR" : ""}`;

  const byLanguage = (list, describe) => {
    const groups = new Map();
    for (const track of list) {
      const lang = languageName(track.lang);
      if (!groups.has(lang)) groups.set(lang, []);
      groups.get(lang).push(track);
    }
    return [...groups].map(([lang, items]) => {
      const box = document.createElement("div");
      box.className = "lang-group";
      const heading = document.createElement("h4");
      heading.textContent = lang;
      box.append(heading, ...items.map((t) => {
        const line = document.createElement("div");
        line.className = "small";
        line.dataset.track = String(t.index);
        line.append(...titleOf(describe(t), t));
        return line;
      }));
      return box;
    });
  };

  // The language is the heading, so each line says the rest.
  const rest = (t) => t.label.split(" · ").slice(1).join(" · ");
  const sound = tracks.filter((t) => t.kind === "audio");
  const subs = tracks.filter((t) => t.kind === "subtitle");

  if (sound.length) $("original-audio").replaceChildren(...byLanguage(sound, rest));
  else $("original-audio").textContent = "None on this disc.";
  if (subs.length) $("original-subs").replaceChildren(...byLanguage(subs, rest), ...readingNote(subs));
  else $("original-subs").textContent = "None on this disc.";

  // A Blu-ray's picture subtitles can be read into text once the disc is
  // copied, each track a task of its own. Ticking one is all it takes.
  const read = new Set(plan.read || []);
  for (const line of $("original-subs").querySelectorAll("[data-track]")) {
    const track = subs.find((t) => String(t.index) === line.dataset.track);
    if (!track || track.codec !== "hdmv_pgs_subtitle" || !subtitleReading.ocr) continue;
    const box = document.createElement("input");
    box.type = "checkbox";
    box.checked = read.has(track.index);
    box.disabled = Boolean(editing && !editing.read);
    box.title = "Read into text once the disc is copied";
    box.addEventListener("change", () =>
      post("/api/plan", { read: { [track.index]: box.checked } }).then(() => refresh()));
    const label = document.createElement("label");
    label.className = "inline small";
    label.append(box, document.createTextNode(" Read into text"));
    line.append(" ", label);
  }
}

// readingNote says what ticking a subtitle track does, or why it cannot be
// ticked here.
function readingNote(subs) {
  if (!subs.some((t) => t.codec === "hdmv_pgs_subtitle")) return [];
  const p = document.createElement("p");
  p.className = "muted small";
  p.textContent = subtitleReading.ocr
    ? "Those ticked are read into text from the original once the disc is copied, and kept beside it."
    : subtitleReading.note;
  return [p];
}

function renderPlan(job, existing = []) {
  const plan = job.plan;
  if (!plan) return;

  // The edition names the film, so it is only part of the name when
  // there is going to be one.
  const transcoding = plan.convert !== false;
  $("plan-title").textContent = plan.edition && transcoding
    ? `${filmName(job, "Found a movie")} {edition-${plan.edition}}`
    : filmName(job, "Found a movie");
  $("plan-summary").textContent = `${plan.duration} · ${bytes(plan.source_size)} on the disc`;

  const notices = [];
  if (plan.obfuscated) notices.push(plan.reason);
  if (job.space && (!job.space.Fits || job.space.Tight || job.space.Unknown)) {
    notices.push(spaceMessage(job.space));
  }
  if (existing.length) notices.push(existingMessage(existing, plan));

  // Nothing the blueprint asks for is on this disc. Rather than guess, the
  // choice is handed over.
  const items = (plan.project && plan.project.items) || [];
  const soundChosen = items.some((it) => it.kind === "audio");
  if (transcoding && plan.sound_not_found && !soundChosen) {
    notices.push("None of the audio this blueprint asks for is on this disc. Add the audio you want below, or turn off Also make a file from it to copy the disc and decide later.");
  }

  $("plan-notice").hidden = notices.length === 0;
  $("plan-notice").textContent = notices.join("\n\n");

  renderOriginal(plan);

  renderPlanProject(plan);

  renderTitleChoice(job);

  // Left alone while somebody is typing in it, or the next update would
  // snatch the text out from under them.
  if (document.activeElement !== $("plan-edition")) {
    $("plan-edition").value = plan.edition || "";
  }
  $("edition-note").textContent = plan.edition
    ? `The file will be called ${job.title || "the movie"}${job.year ? ` (${job.year})` : ""} {edition-${plan.edition}}. Plex shows the edition as the name of this version.`
    : "No edition. Give one to keep this version apart from others of the same movie, such as \"Director's Cut\".";

  // Stopping at the copy is often the right choice: the copy is the only part
  // that needs the disc, and a film can be made any time afterwards.
  $("plan-convert").checked = transcoding;
  $("plan-film").hidden = !transcoding;
  $("convert-note").textContent = transcoding
    ? "It joins the queue as a task of its own, and starts once the original is copied."
    : "ARFABIT will copy the disc and stop. You can make anything from the original later, in Projects.";
  $("film-target").textContent =
    `Started from ${plan.blueprint ? `the ${plan.blueprint} blueprint` : "the defaults"}. Everything the original will hold is listed; change anything.`;

  const est = plan.estimated_time ? Math.round(plan.estimated_time / 60000000000) : 0;
  $("plan-estimate").textContent = est
    ? `About ${est} minutes, finishing around ${bytes(plan.estimated_size)}.`
    : "";

  // Once started, each part stays open until its step begins (§2).
  const open = editing || { started: false, name: true, read: true, film: true };
  $("title-choice").inert = !open.name;
  $("plan-convert").disabled = !open.film;
  $("plan-film").inert = !open.film;
  $("plan-actions").hidden = open.started;
  $("plan-started").hidden = !open.started;
  if (open.started) {
    const parts = [];
    if (open.name) parts.push("its name and the subtitles to read, until the copy is finished");
    if (open.film) parts.push(transcoding ? "the file made from it, until that starts" : "whether to make a file from it, until the copy is finished");
    $("plan-started").textContent = (job.state === "running" ? "Being copied." : "Copied.")
      + (parts.length ? ` You can still change ${parts.join("; and ")}.` : "");
  }

  const fits = !job.space || job.space.Fits;
  const videos = items.filter((it) => it.kind === "video").length;
  const nothing = transcoding && (items.length === 0 || videos > 1);
  $("start").disabled = !fits || existing.length > 0 || nothing;
  $("plan-blocked").textContent = !fits
    ? "Free up some room and look for the disc again."
    : existing.length ? "A file with this name is already there."
      : nothing ? (items.length ? "A file can hold one video." : "It has nothing to make.") : "";
}

// --- the queue ------------------------------------------------------------

// The clocks belong to the page.
//
// Copying a disc reports its first percentage a couple of minutes in, and the
// log goes quiet while it works. A number that moves is the only thing that
// distinguishes working from hung, so the page keeps its own time rather than
// waiting to be told.
//
// Each clock starts from when ARFABIT says its stage began, fixed once, and
// is redrawn only when the second it shows changes. Redrawing it whenever an
// update happened to arrive made it step unevenly, sometimes twice in quick
// succession and sometimes not for two seconds.
function startClock() {
  if (clockTimer) return;
  clockTimer = setInterval(tickClock, 200);
}

function stopClock() {
  if (!clockTimer) return;
  clearInterval(clockTimer);
  clockTimer = null;
}

function tickClock() {
  if (activeJobs.length === 0) {
    stopClock();
    return;
  }
  for (const card of $("working-list").children) {
    const cell = card.querySelector(".t-elapsed");
    const text = elapsedText(card.dataset.clock);
    if (cell.textContent !== text) cell.textContent = text;
  }
}

// clockStart is when a job's current stage began, in this browser's time.
function clockStart(job) {
  const since = job.progress && job.progress.since;
  if (!since) return 0;

  const began = new Date(since).getTime();
  if (Number.isNaN(began) || began <= 0) return 0;

  const key = `${job.id} ${job.stage} ${since}`;
  if (!clockStarts.has(key)) clockStarts.set(key, began + clockOffset);
  return clockStarts.get(key);
}

// elapsedText renders how long a clock has run: seconds while it is new, so
// the number visibly moves, and hours and minutes once seconds stop mattering.
function elapsedText(start) {
  start = Number(start);
  if (!start) return "";

  const seconds = Math.max(0, Math.floor((Date.now() - start) / 1000));
  if (seconds < 60) return `${seconds}s`;

  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;

  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

// kindOf groups a job for the queue summary.
//
// A disc is being read while it needs the drive and converting once it does
// not. Anything waiting for the processor is waiting, whatever it is.
function kindOf(job) {
  if (job.stage === "QUEUED") return "waiting";
  if (job.stage === "LAB") return "lab";
  if (job.kind === "ocr") return "reading";

  switch (job.stage) {
    case "SCAN":
    case "PLAN":
    case "RIP":
    case "EJECT":
      return "rip";
    default:
      return "convert";
  }
}

const KINDS = [
  ["rip", (n) => `${n} disc${n === 1 ? "" : "s"} being read`],
  ["convert", (n) => `${n} converting`],
  ["lab", (n) => `${n} making test clips`],
  ["reading", (n) => `${n} reading subtitles`],
  ["waiting", (n) => `${n} waiting their turn`],
];

// inLineOrder puts whatever is working first, then whatever is waiting in the
// order it will start, which is the order the line can be rearranged in.
function inLineOrder(jobs) {
  const place = (job) => {
    const at = line.indexOf(job.id);
    return at < 0 ? -1 : at;
  };
  return [...jobs].sort((a, b) => place(a) - place(b));
}

function renderActive(jobs) {
  const all = jobs || [];

  lastActive = all;
  const counts = new Map();
  for (const job of all) counts.set(kindOf(job), (counts.get(kindOf(job)) || 0) + 1);

  // A filter for something no longer in the queue would show nothing at all.
  if (queueFilter !== "all" && !counts.has(queueFilter)) queueFilter = "all";

  activeJobs = inLineOrder(all.filter((job) =>
    queueFilter === "all" || kindOf(job) === queueFilter));

  show("queue", all.length > 0);
  renderQueueSummary(all.length, counts);

  const box = $("working-list");
  const wanted = new Set(activeJobs.map((j) => j.id));

  // Remove cards for jobs that have finished.
  for (const card of [...box.children]) {
    if (!wanted.has(card.dataset.job)) card.remove();
  }

  // Cards are put in order every time, since moving a job in the line moves
  // its card.
  // Not while one is being dragged, though: the card stays where the pointer
  // put it until it is let go.
  for (const job of activeJobs) {
    let card = box.querySelector(`[data-job="${CSS.escape(job.id)}"]`);
    if (!card) card = jobCard(job);
    if (!dragging || !card.parentNode) box.append(card);
    updateJobCard(job);
  }

  if (activeJobs.length > 0) startClock();
  else stopClock();
}

// renderQueueSummary says what the queue amounts to in one line.
//
// Each part narrows the queue to just that when clicked, and the total widens
// it back out. With only one kind of thing in the queue there is nothing to
// narrow, so the line is plain text.
function renderQueueSummary(total, counts) {
  const box = $("queue-summary");
  const kinds = KINDS.filter(([kind]) => counts.has(kind));
  const linked = kinds.length > 1;

  const part = (text, filter) => {
    if (!linked) return document.createTextNode(text);

    const link = document.createElement("button");
    link.className = "link";
    link.textContent = text;
    link.setAttribute("aria-pressed", String(queueFilter === filter));
    link.addEventListener("click", () => {
      queueFilter = filter;
      refresh();
    });
    return link;
  };

  const parts = [];
  if (linked) parts.push(part(`${total} total`, "all"));
  for (const [kind, words] of kinds) parts.push(part(words(counts.get(kind)), kind));

  box.replaceChildren(...parts.flatMap((p, i) =>
    i === 0 ? [p] : [document.createTextNode(" · "), p]));
}

function jobCard(job) {
  const card = document.createElement("section");
  card.className = "card job-card";
  card.dataset.job = job.id;

  const head = document.createElement("div");
  head.className = "job-head";

  // Only what is waiting has a handle, and dragging it moves the job in the
  // line. It is a button, so the arrow keys move it too.
  const grip = document.createElement("button");
  grip.className = "grip";
  grip.setAttribute("aria-label", "Drag to change the order");
  grip.title = "Drag to change the order";
  grip.innerHTML = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3 5h10M3 8h10M3 11h10"/></svg>';
  grip.addEventListener("pointerdown", (e) => startDrag(e, card));
  grip.addEventListener("keydown", (e) => {
    if (e.key === "ArrowUp" || e.key === "ArrowDown") {
      e.preventDefault();
      moveInLine(job.id, e.key === "ArrowUp" ? -1 : 1).then(() => grip.focus());
    }
  });

  const title = document.createElement("h2");
  title.className = "job-title";
  head.append(grip, title);
  card.append(head);

  // Clicking the card narrows the log to this disc, and clicking it again
  // widens it back out.
  card.addEventListener("click", (e) => {
    if (e.target.closest("button")) return;
    focusJob(job.id);
  });

  const stage = document.createElement("div");
  stage.className = "stage";
  card.append(stage);

  const bar = document.createElement("div");
  bar.className = "bar";
  const fill = document.createElement("div");
  fill.className = "bar-fill";
  bar.append(fill);
  card.append(bar);

  // Every figure has a cell of its own, the same width whatever it holds, so
  // a clock going from 9s to 10s does not shove everything after it along.
  const progress = document.createElement("div");
  progress.className = "progress-line";
  const figures = document.createElement("span");
  figures.className = "figures muted";
  for (const name of ["t-elapsed", "t-pct", "t-rate", "t-op"]) {
    const cell = document.createElement("span");
    cell.className = name;
    figures.append(cell);
  }
  const estimate = document.createElement("span");
  estimate.className = "muted estimate-right job-estimate";
  progress.append(figures, estimate);
  card.append(progress);

  const actions = document.createElement("div");
  actions.className = "actions";

  const stop = document.createElement("button");
  stop.className = "stop";
  stop.textContent = "Stop";
  stop.addEventListener("click", (e) => askToStop(e.target, job.id));

  actions.append(stop);
  card.append(actions);

  return card;
}

function updateJobCard(job) {
  const card = $("working-list").querySelector(`[data-job="${CSS.escape(job.id)}"]`);
  if (!card) return;

  // A finished lab run has a comparison worth showing.
  if (job.comparison && job.comparison.clips) {
    renderLabResults(job.comparison);
  }

  const progress = job.progress || {};
  const queued = job.stage === "QUEUED";

  // The file being made, exactly as it will be named, once there is one: a
  // copy is MakeMKV's own .mkv until it is renamed, and a film or clip is an .mkv carrying its
  // edition.
  card.querySelector(".job-title").textContent = job.file || filmName(job);
  // Waiting has more than one flavour, and the job says which.
  card.querySelector(".stage").textContent = queued && progress.operation
    ? progress.operation
    : stageWords(job.stage);

  const pct = Math.round(progress.percent || 0);
  card.querySelector(".bar-fill").style.width = `${pct}%`;

  card.dataset.clock = String(clockStart(job));
  card.querySelector(".t-elapsed").textContent = elapsedText(card.dataset.clock);
  card.querySelector(".t-pct").textContent = pct > 0 ? `${pct}%` : "";
  // A speed is the only figure here that can be compared to anything.
  card.querySelector(".t-rate").textContent = progress.rate || "";
  card.querySelector(".t-op").textContent = queued ? "" : progress.operation || "";

  card.querySelector(".job-estimate").textContent =
    queued ? "" : estimateText(progress, pct, Number(card.dataset.clock));

  // Only what is waiting can be moved, and only when there is something
  // else waiting to move it past.
  card.querySelector(".grip").hidden = line.indexOf(job.id) < 0 || line.length < 2;
}

// moveInLine moves a waiting job some places towards the front (negative) or
// the back.
async function moveInLine(id, by) {
  const place = line.indexOf(id);
  const to = Math.max(0, Math.min(line.length - 1, place + by));
  if (place < 0 || to === place) {
    // Put a card dragged back to where it started exactly where it was.
    await refresh();
    return;
  }

  const result = await post("/api/line", { id, to });
  if (result) line = result.line;
  await refresh();
}

// Dragging a waiting job to a new place in the line.
//
// The card follows the pointer among the other waiting cards, and nothing is
// sent until it is let go, so one drag is one change however far it goes.
// Pointer events cover a mouse, a finger and a pen alike.
let dragging = null;

function startDrag(e, card) {
  dragging = card;
  const waiting = () => [...$("working-list").children]
    .filter((c) => c !== card && line.includes(c.dataset.job));

  dragAmong(e, card, waiting, () => {
    dragging = null;

    // Where it landed, as a place in the whole line. The list may be showing
    // only some of what is waiting, so the place comes from whichever waiting
    // job it now sits in front of.
    const id = card.dataset.job;
    const rest = line.filter((other) => other !== id);
    let next = card.nextElementSibling;
    while (next && !line.includes(next.dataset.job)) next = next.nextElementSibling;
    const to = next ? rest.indexOf(next.dataset.job) : rest.length;

    moveInLine(id, to - line.indexOf(id));
  });
}

// dragAmong moves an element among others as the pointer moves, and calls
// dropped when it is let go.
//
// The whole window is listened to, not the handle. Moving the element in the
// page makes some browsers let go of a handle's hold on the pointer, and then
// nothing more arrives: dragging downwards happened to work, and dragging
// upwards stopped after the first step. Near the top or bottom of the window
// the page scrolls, so a long list can be crossed in one drag.
function dragAmong(e, el, others, dropped) {
  if (e.button !== 0) return;
  e.preventDefault();
  el.classList.add("dragging");
  document.body.classList.add("dragging-something");

  let y = e.clientY;
  const place = () => {
    const list = others();
    const before = list.find((o) => {
      const box = o.getBoundingClientRect();
      return y < box.top + box.height / 2;
    });
    if (before) {
      if (el.nextElementSibling !== before) before.before(el);
    } else if (list.length && list[list.length - 1].nextElementSibling !== el) {
      list[list.length - 1].after(el);
    }
  };

  const edge = 60;
  const scroller = setInterval(() => {
    const step = y < edge ? -14 : y > window.innerHeight - edge ? 14 : 0;
    if (step) {
      window.scrollBy(0, step);
      place();
    }
  }, 30);

  const move = (ev) => {
    y = ev.clientY;
    place();
  };
  const drop = () => {
    window.removeEventListener("pointermove", move);
    window.removeEventListener("pointerup", drop);
    window.removeEventListener("pointercancel", drop);
    clearInterval(scroller);
    el.classList.remove("dragging");
    document.body.classList.remove("dragging-something");
    dropped();
  };
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", drop);
  window.addEventListener("pointercancel", drop);
}

// --- stopping -------------------------------------------------------------

// confirmFirst asks before doing something that would throw work away, and
// resolves true only if the answer was yes.
//
// Kept for the few things that could cost somebody ten minutes or more with a
// stray click. Everything else just happens.
function confirmFirst({ title, detail, confirm, cancel }) {
  const dialog = $("confirm-dialog");
  $("confirm-title").textContent = title;
  $("confirm-detail").textContent = detail;
  $("confirm-yes").textContent = confirm;
  $("confirm-no").textContent = cancel;

  return new Promise((resolve) => {
    dialog.onclose = () => resolve(dialog.returnValue === "yes");
    dialog.returnValue = "";
    dialog.showModal();
    $("confirm-yes").focus();
  });
}

// working is what would lose progress if it stopped now. Anything waiting its
// turn has done nothing yet.
function working() {
  return lastActive.filter((job) => job.stage !== "QUEUED");
}

// lostWork says how much work stopping a job would throw away.
function lostWork(job) {
  const ran = Date.now() - (clockStart(job) || Date.now());
  return ran >= 60000
    ? `All progress from the last ${spokenDuration(ran)} will be lost.`
    : "It has only just started.";
}

// askToStop stops a job, asking first if stopping would throw work away.
//
// A job still waiting its turn has done nothing yet, so it simply stops. One
// that is working loses what it has done so far, which after a couple of hours
// of copying is worth a second look before it goes.
async function askToStop(button, id) {
  const job = working().find((j) => j.id === id);

  if (job) {
    const kept = ["PACKAGE", "LAB", "OCR", "DELIVER"].includes(job.stage)
      ? " The original is kept, so this can be started again later."
      : "";
    // A film waiting on this copy has nothing to work from without it.
    const follower = lastActive.find((j) => j.from === job.id);
    const also = follower ? " What was to be made from it will be removed from the queue too." : "";
    const yes = await confirmFirst({
      title: `Stop ${filmName(job)}?`,
      detail: lostWork(job) + kept + also,
      confirm: "Confirm stop",
      cancel: "Cancel stop",
    });
    if (!yes) return;
  }

  busy(button, "Stopping\u2026", null, () => post("/api/stop", { id }));
}

// askToEnd asks before restarting or stopping ARFABIT while something is
// working, since either ends every job in progress.
async function askToEnd(verb) {
  const busyJobs = working();
  if (busyJobs.length === 0) return true;

  const longest = busyJobs.reduce((a, b) => (clockStart(a) <= clockStart(b) ? a : b));
  const what = busyJobs.length === 1
    ? `${filmName(longest)} is still working.`
    : `${busyJobs.length} things are still working, the longest ${filmName(longest)}.`;
  const waiting = lastActive.length > busyJobs.length
    ? " Anything waiting its turn can be started again from Recent tasks."
    : "";

  return confirmFirst({
    title: `${verb} ARFABIT?`,
    detail: `${what} ${lostWork(longest)}${waiting}`,
    confirm: `Confirm ${verb.toLowerCase()}`,
    cancel: `Cancel ${verb.toLowerCase()}`,
  });
}

// spokenDuration says a length of time the way a person would.
function spokenDuration(ms) {
  const minutes = Math.floor(ms / 60000);
  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;

  const unit = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;
  if (hours === 0) return unit(minutes, "minute");
  if (rest === 0) return unit(hours, "hour");
  return `${unit(hours, "hour")} ${unit(rest, "minute")}`;
}

// estimateText says how much longer, as honestly as it can.
function estimateText(progress, pct, started) {
  if (progress.remaining && progress.remaining !== "unknown") {
    return `about ${progress.remaining} left`;
  }

  if (pct > 0 && pct < 100 && started) {
    const elapsedMs = Date.now() - started;
    const totalMs = (elapsedMs / pct) * 100;
    const leftMs = Math.max(0, totalMs - elapsedMs);
    if (elapsedMs > 20000) return `about ${humanMs(leftMs)} left`;
  }

  if (progress.expected) return `usually takes ${progress.expected}`;
  return "working out how long this will take";
}

function humanMs(ms) {
  const minutes = Math.round(ms / 60000);
  if (minutes < 1) return "under a minute";
  if (minutes < 60) return `${minutes} minutes`;

  const hours = Math.floor(minutes / 60);
  const rest = minutes % 60;
  return rest === 0 ? `${hours} hours` : `${hours}h ${rest}m`;
}

// The disc's Plan as last sent, so it can be drawn again when the disc is
// chosen in Projects.
let lastJob = null;
let lastExisting = [];

function renderJob(job, existing) {
  lastJob = job;
  lastExisting = existing || [];
  renderDiscSource();
  renderDrivePlan(job);
  if (!job) {
    show("plan", false);
    show("done", false);
    return;
  }

  // A disc's Plan stays up after Start while any of it can still change. It
  // is in Projects, with the disc chosen to start from.
  const waiting = job.state === "waiting" || Boolean(editing && editing.started);
  show("plan", waiting && $("project-source").value === "disc");
  show("done", !waiting && (job.state === "done" || job.state === "stopped"));

  if (waiting) renderPlan(job, existing);

  if (job.state === "done" || job.state === "stopped") {
    $("done-title").textContent = job.state === "done" ? "Ready" : "Stopped";
    $("done-detail").textContent = job.note || "";
  }
}

// renderDrivePlan says, under the drive, where the disc's Plan is: in
// Projects, one click away.
function renderDrivePlan(job) {
  const line = $("drive-plan");
  const open = job && (job.state === "waiting" || (editing && editing.started));
  line.hidden = !open;
  if (!open) return;
  const link = document.createElement("a");
  link.href = "#projects";
  link.textContent = "in Projects";
  link.addEventListener("click", chooseDisc);
  const where = job.state === "waiting" ? "waiting to be started" : "still open to change";
  line.replaceChildren(document.createTextNode(`The Plan for ${filmName(job)} is ${where}, `), link, document.createTextNode("."));
}

// jobOutcome says what became of a disc, in words rather than state names.
function jobOutcome(job) {
  switch (job.state) {
    case "done":
      return "Finished";
    case "running":
      return stageWords(job.stage);
    case "waiting":
      return "Waiting for you to start it";
    default:
      // A stopped job carries its own explanation, which is more useful than
      // the word "stopped".
      return job.note || "Stopped";
  }
}

// whenText says when something happened, in the terms a person would use.
function whenText(when) {
  if (!when) return "";

  const then = new Date(when);
  if (Number.isNaN(then.getTime())) return "";

  const clock = { hour: "numeric", minute: "2-digit" };
  const today = new Date();

  if (then.toDateString() === today.toDateString()) {
    return then.toLocaleTimeString([], clock);
  }

  const yesterday = new Date(today);
  yesterday.setDate(today.getDate() - 1);
  if (then.toDateString() === yesterday.toDateString()) {
    return `yesterday, ${then.toLocaleTimeString([], clock)}`;
  }

  return then.toLocaleDateString();
}

// Which recent tasks are open, so redrawing the list keeps them open.
const openRecent = new Set();

// speedGraph draws how fast a disc was read over the course of its copy.
//
// One line, one axis. The slowest and fastest speeds label the axis, the start
// and end of the copy label the bottom, and pointing anywhere along it says
// exactly what the speed was then.
function speedGraph(samples) {
  const NS = "http://www.w3.org/2000/svg";
  const W = 600, H = 120, left = 64, right = 8, top = 8, bottom = 22;

  const el = (tag, attrs, text) => {
    const node = document.createElementNS(NS, tag);
    for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, v);
    if (text !== undefined) node.textContent = text;
    return node;
  };

  const last = samples[samples.length - 1].seconds || 1;
  const top_ = Math.max(...samples.map((p) => p.mb_per_second));
  const ceiling = Math.max(1, Math.ceil(top_ / 5) * 5);

  const x = (sec) => left + (sec / last) * (W - left - right);
  const y = (mb) => top + (1 - mb / ceiling) * (H - top - bottom);

  const svg = el("svg", { viewBox: `0 0 ${W} ${H}`, class: "speed-graph", role: "img",
    "aria-label": `Reading speed over ${spokenDuration(last * 1000)}, up to ${top_.toFixed(1)} MB/s` });

  // A recessive frame: a baseline, a line at the ceiling, and their values.
  for (const mb of [0, ceiling]) {
    svg.append(el("line", { x1: left, x2: W - right, y1: y(mb), y2: y(mb), class: "grid" }));
    svg.append(el("text", { x: left - 8, y: y(mb) + 4, "text-anchor": "end", class: "axis" }, `${mb} MB/s`));
  }
  svg.append(el("text", { x: left, y: H - 4, class: "axis" }, "0"));
  svg.append(el("text", { x: W - right, y: H - 4, "text-anchor": "end", class: "axis" }, elapsedText(Date.now() - last * 1000)));

  svg.append(el("polyline", {
    class: "speed-line",
    points: samples.map((p) => `${x(p.seconds).toFixed(1)},${y(p.mb_per_second).toFixed(1)}`).join(" "),
  }));

  // Pointing at the graph shows the nearest sample.
  const cross = el("line", { y1: top, y2: H - bottom, class: "cross", visibility: "hidden" });
  const dot = el("circle", { r: 4, class: "speed-dot", visibility: "hidden" });
  const hit = el("rect", { x: left, y: 0, width: W - left - right, height: H, fill: "transparent" });
  svg.append(cross, dot, hit);

  const wrap = document.createElement("div");
  wrap.className = "speed-wrap";
  const tip = document.createElement("div");
  tip.className = "speed-tip";
  tip.hidden = true;
  wrap.append(svg, tip);

  hit.addEventListener("pointermove", (e) => {
    const box = svg.getBoundingClientRect();
    const sec = ((e.clientX - box.left) / box.width * W - left) / (W - left - right) * last;
    const nearest = samples.reduce((a, b) =>
      Math.abs(b.seconds - sec) < Math.abs(a.seconds - sec) ? b : a);

    const px = x(nearest.seconds), py = y(nearest.mb_per_second);
    cross.setAttribute("x1", px);
    cross.setAttribute("x2", px);
    cross.setAttribute("visibility", "visible");
    dot.setAttribute("cx", px);
    dot.setAttribute("cy", py);
    dot.setAttribute("visibility", "visible");

    tip.hidden = false;
    tip.textContent = `${nearest.mb_per_second.toFixed(1)} MB/s · ${elapsedText(Date.now() - nearest.seconds * 1000) || "0s"} in`;
    const share = px / W;
    tip.style.left = `${share * 100}%`;
    tip.style.transform = `translateX(${share > 0.7 ? "-100%" : "0"})`;
  });
  hit.addEventListener("pointerleave", () => {
    cross.setAttribute("visibility", "hidden");
    dot.setAttribute("visibility", "hidden");
    tip.hidden = true;
  });

  return wrap;
}

// What the recent list was last drawn from. A copy in progress updates the
// page several times a second, and redrawing an unchanged list each time
// would close whatever somebody was pointing at in it.
let recentDrawn = "";

// taskKind names what a task was.
function taskKind(job) {
  if (job.kind === "lab") return "Package";
  if (job.kind === "ocr") return "OCR";
  if (job.kind === "convert") return "Package";
  return "Copy";
}

// taskOutcome sorts how a task ended, for its label's colour, with the same
// thing in words for anyone who cannot tell the colours apart.
//
// "Stopped" covers three things that call for different reactions: one that
// can be started again, one that did not finish and has the output to show
// why, and one stopped on purpose or by a restart with nothing to resume.
function taskOutcome(job) {
  // Reading subtitles: green once read and nothing is left to look at,
  // yellow while some have low confidence, red if reading did not finish.
  if (job.kind === "ocr") {
    if (job.state !== "done") return ["failed", "Did not finish"];
    if (toCheck(job)) return ["check", "Some subtitles have low confidence"];
    return ["done", "Read"];
  }
  if (job.state === "done") return ["done", "Finished"];
  if (resumable[job.id]) return ["resumable", "Did not finish, and can be started again"];
  if (job.detail) return ["failed", "Did not finish"];
  return ["stopped", "Stopped"];
}

// toCheck reports whether an OCR task has subtitles of low confidence nobody
// has looked at yet.
function toCheck(job) {
  return job.kind === "ocr" && job.state === "done" && !(job.reading && job.reading.fine)
    && (job.low_confidence || []).some((l) => !l.changed);
}

// madeBy is the files a task made: a copy's original, or a package's files.
function madeBy(job) {
  if (job.made && job.made.length) return job.made;
  if (job.kind === "disc") return [job.original, job.delivery].filter(Boolean);
  return job.delivery ? [job.delivery] : [];
}

function renderRecent(jobs) {
  const box = $("recent");
  const drawn = JSON.stringify([jobs, resumable, copies, copiesOf, new Date().toDateString()]);
  if (drawn === recentDrawn) return;
  recentDrawn = drawn;
  renderToCheck(jobs || []);

  // A task that has finished may have made a file to start a project from.
  loadSources();

  if (!jobs || jobs.length === 0) {
    box.textContent = "Nothing yet.";
    return;
  }

  // One line each: what it was, what it was of, and when. Everything else is
  // a click away, underneath it.
  box.replaceChildren(...jobs.map((job) => {
    const item = document.createElement("details");
    item.className = "recent-item";
    item.dataset.job = job.id;
    item.open = openRecent.has(job.id);
    item.addEventListener("toggle", () => {
      if (item.open) openRecent.add(job.id);
      else openRecent.delete(job.id);
    });

    const line = document.createElement("summary");
    const kind = document.createElement("span");
    const [outcome, meaning] = taskOutcome(job);
    kind.className = `tag task-${outcome}`;
    kind.textContent = taskKind(job);
    kind.title = meaning;

    const name = document.createElement("span");
    name.className = "recent-name";
    name.textContent = filmName(job);

    const when = document.createElement("span");
    when.className = "muted small recent-when";
    when.textContent = whenText(job.started);

    line.append(kind, name, when);
    item.append(line, recentDetail(job));
    return item;
  }));
}

// renderToCheck names, above Recent tasks, every OCR task whose subtitles are
// still to be checked, however far down the list it is. Each opens its task.
function renderToCheck(jobs) {
  const waiting = jobs.filter(toCheck);
  const box = $("to-check");
  box.hidden = waiting.length === 0;
  if (!waiting.length) return;
  const links = waiting.map((job) => {
    const link = document.createElement("button");
    link.className = "link";
    link.textContent = `${filmName(job)}, ${languageName(job.reading && job.reading.lang)}`;
    link.addEventListener("click", () => {
      openRecent.add(job.id);
      openLowConfidence.add(job.id);
      recentDrawn = "";
      renderRecent(jobs);
      const item = $("recent").querySelector(`[data-job="${CSS.escape(job.id)}"]`);
      if (item) item.scrollIntoView({ block: "start", behavior: "smooth" });
    });
    return link;
  });
  box.replaceChildren(document.createTextNode("Subtitles to check: "),
    ...links.flatMap((l, i) => (i === 0 ? [l] : [document.createTextNode(" · "), l])));
}

// recentDetail is everything about a task beyond its line: how it ended, what
// it made, how the copy went, and what can be done about it.
function recentDetail(job) {
  const body = document.createElement("div");
  body.className = "recent-detail";

  const said = document.createElement("p");
  said.textContent = jobOutcome(job);
  body.append(said);

  // What it made, by name, so it can be found in the folder.
  const made = madeBy(job);
  if (made.length) {
    const files = document.createElement("div");
    files.className = "recent-files muted small";
    for (const path of made) {
      const file = document.createElement("div");
      file.textContent = path.split(/[\\/]/).pop();
      file.title = path;
      files.append(file);
    }
    body.append(files);
  }

  // Subtitles OCR may have read wrong, to check against their pictures.
  if ((job.low_confidence || []).length) body.append(lowConfidenceList(job));

  // A film's subtitles are copies of those beside its original, and fall
  // behind when those are fixed.
  const copied = job.kind === "ocr" ? copiesOf[job.id] : copies[job.id];
  if (copied && copied.length) body.append(copiesNote(job, copied));

  // Looking at every one is not the only way to be done with them.
  if (toCheck(job)) {
    const fine = document.createElement("button");
    fine.textContent = "These are fine";
    fine.title = "Leave the subtitles as they were read";
    fine.addEventListener("click", async (e) => {
      const result = await busy(e.target, "Saving\u2026", "Saved", () =>
        post(`/api/jobs/${encodeURIComponent(job.id)}/fine`));
      if (result) refresh();
    });
    body.append(fine);
  }

  // How the copy went. Worth seeing because a drive that slows down half way
  // through looks just like a slow drive from the average alone.
  if ((job.read_speed || []).length > 1) {
    const heading = document.createElement("div");
    heading.className = "muted small";
    heading.textContent = "Reading speed";
    body.append(heading, speedGraph(job.read_speed));
  }

  // The raw account, always complete, for anything that did not finish (§15).
  if (job.detail) {
    const details = document.createElement("details");
    const summary = document.createElement("summary");
    summary.textContent = "Technical details";
    const pre = document.createElement("pre");
    pre.textContent = job.detail;
    details.append(summary, pre);
    body.append(details);
  }

  // Anything working from a copy can simply be started again, because the
  // copy is still there.
  if (resumable[job.id]) {
    const again = document.createElement("button");
    again.textContent = "Start again";
    again.addEventListener("click", async (e) => {
      const result = await busy(e.target, "Starting\u2026", "Added", () =>
        post("/api/resume", { id: job.id }));
      if (result) refresh();
    });
    body.append(again);
  }

  return body;
}

// copiesNote says how the copies of some subtitles stand, and offers to bring
// those that are out of date up to date. A copy somebody else has changed is
// only mentioned: ARFABIT leaves it as it is.
function copiesNote(job, copied) {
  const box = document.createElement("div");
  box.className = "copies small";
  const name = (path) => path.split(/[\\/]/).pop();
  const line = (text) => {
    const p = document.createElement("p");
    p.textContent = text;
    box.append(p);
  };

  const behind = copied.filter((c) => c.state === "behind");
  if (job.kind === "ocr") {
    line(behind.length === 0
      ? `${copied.length === 1 ? "One film has" : `${copied.length} films have`} a copy of these subtitles.`
      : behind.length === 1
        ? `${name(behind[0].to)} has these subtitles as they were before they were changed.`
        : `${behind.length} films have these subtitles as they were before they were changed.`);
  } else {
    for (const c of behind) line(`${name(c.to)} is older than the subtitles beside the original, which have been changed since.`);
  }
  for (const c of copied) {
    if (c.state === "changed") line(`${name(c.to)} was changed by someone else since ARFABIT put it there, so ARFABIT leaves it as it is.`);
    if (c.state === "gone") line(`${name(c.to)} is no longer where ARFABIT put it.`);
    if (c.state === "no_origin") line(`The subtitles beside the original are no longer there, so ${name(c.to)} stays as it is.`);
  }

  if (behind.length) {
    const update = document.createElement("button");
    update.textContent = behind.length === 1 ? "Bring it up to date" : "Bring them up to date";
    update.addEventListener("click", async (e) => {
      const result = await busy(e.target, "Bringing up to date\u2026", "Up to date", () =>
        post(`/api/jobs/${encodeURIComponent(job.id)}/bring-up-to-date`));
      if (result) refresh();
    });
    box.append(update);
  }
  return box;
}

// Subtitles of low confidence (§10), each beside its picture. The sidecar
// holds the accurate reading until somebody chooses otherwise. Picking a
// reading saves it; Custom starts from whichever was picked last.
const openLowConfidence = new Set();

function lowConfidenceList(job) {
  const entries = job.low_confidence;
  const box = document.createElement("details");
  box.className = "low-confidence";
  box.open = openLowConfidence.has(job.id);
  box.addEventListener("toggle", () => {
    if (box.open) openLowConfidence.add(job.id);
    else openLowConfidence.delete(job.id);
  });

  const summary = document.createElement("summary");
  const chosen = entries.filter((e) => e.changed).length;
  summary.textContent = `Low confidence: ${entries.length} subtitle${entries.length === 1 ? "" : "s"}`
    + (chosen ? `, ${chosen} chosen` : "");
  box.append(summary, ...entries.map((entry, n) => lowConfidenceRow(job, entry, n)));
  return box;
}

function lowConfidenceRow(job, entry, n) {
  const row = document.createElement("div");
  row.className = "low-confidence-row";

  const when = document.createElement("div");
  when.className = "muted small";
  when.textContent = clockText(entry.start);

  const picture = document.createElement("img");
  picture.loading = "lazy";
  picture.alt = "The subtitle as it is on the disc";
  picture.src = `/api/jobs/${encodeURIComponent(job.id)}/low-confidence/${n}/picture`;

  // The readings to choose from: both where there were two, otherwise the
  // one there was.
  const readings = entry.fast
    ? [["Accurate", entry.text || ""], ["Fast", entry.fast]]
    : [["As read", entry.text || ""]];
  const current = entry.changed ? entry.chosen || "" : entry.text || "";
  const picked = readings.findIndex(([, text]) => text === current);

  const choose = async (text) => {
    const reply = await post(`/api/jobs/${encodeURIComponent(job.id)}/low-confidence/${n}`, { text });
    if (reply) refresh();
    return reply;
  };

  const custom = document.createElement("div");
  custom.className = "low-confidence-custom";
  custom.hidden = picked !== -1;
  const box = document.createElement("textarea");
  box.rows = 2;
  box.value = current;
  const save = document.createElement("button");
  save.type = "button";
  save.textContent = "Save";
  save.addEventListener("click", (e) => busy(e.target, "Saving\u2026", "Saved", () => choose(box.value)));
  custom.append(box, save);

  const group = document.createElement("div");
  group.className = "low-confidence-choices";
  const option = (label, text, checked, chosen) => {
    const wrap = document.createElement("label");
    wrap.className = "inline";
    const radio = document.createElement("input");
    radio.type = "radio";
    radio.name = `low-confidence-${job.id}-${n}`;
    radio.checked = checked;
    radio.addEventListener("change", chosen);
    const name = document.createElement("span");
    name.textContent = label;
    wrap.append(radio, name);
    if (text !== null) {
      const said = document.createElement("span");
      said.className = "low-confidence-reading";
      said.textContent = text || "Nothing was read";
      wrap.append(said);
    }
    group.append(wrap);
  };
  readings.forEach(([label, text], i) => option(label, text, i === picked, () => {
    box.value = text;
    custom.hidden = true;
    choose(text);
  }));
  option("Custom", null, picked === -1, () => {
    custom.hidden = false;
    box.focus();
  });

  row.append(when, picture, group, custom);
  return row;
}

// clockText is a moment in a film, given in nanoseconds, as a player shows it.
function clockText(ns) {
  const total = Math.round((ns || 0) / 1e9);
  const h = Math.floor(total / 3600);
  const m = String(Math.floor(total / 60) % 60).padStart(2, "0");
  const sec = String(total % 60).padStart(2, "0");
  return `${h}:${m}:${sec}`;
}

async function refresh() {
  const state = await fetch("/api/state").then((r) => r.json());
  if (state.now) clockOffset = Date.now() - new Date(state.now).getTime();

  const wasBusy = driveBusy;
  driveBusy = state.drive_busy || "";
  resumable = state.resumable || {};
  driveSettings = state.drive_settings || {};
  editing = state.editing || null;
  copies = state.copies || {};
  copiesOf = state.copies_of || {};
  line = state.line || [];
  subtitleReading = { ocr: Boolean(state.ocr), note: state.ocr_note || "" };
  renderDrives(state.drives);
  renderActive(state.active || []);
  renderJob(state.job, state.existing || []);
  renderRecent(state.recent || []);

  // The drive is free to be asked again, and a disc has just been through
  // it, so the measured speed has probably changed.
  if (wasBusy && !driveBusy) loadDriveHealth();

  const log = await fetch("/api/log").then((r) => r.json());
  appendLog(log);
}

// --- doctor ---------------------------------------------------------------

async function runDoctor() {
  show("doctor", true);
  $("doctor-heading").textContent = "Checking your computer";
  $("doctor-status").textContent = "Looking for MakeMKV and FFmpeg.";
  $("doctor-list").replaceChildren();

  let report;
  try {
    report = await fetch("/api/doctor").then((r) => r.json());
  } catch (err) {
    $("doctor-heading").textContent = "Could not finish checking";
    $("doctor-status").textContent = String(err);
    return;
  }

  const problems = (report.checks || []).filter((c) => c.status !== "ok");

  if (problems.length === 0) {
    // Deliberately says nothing about the drive: that is live state, shown
    // on its own card, and a claim made here would go stale immediately.
    $("doctor-heading").textContent = "Everything is installed";
    $("doctor-status").textContent = "MakeMKV and FFmpeg are both set up.";
    setTimeout(() => show("doctor", false), 2500);
    return;
  }

  $("doctor-heading").textContent = "Before you start";
  $("doctor-status").textContent = "";

  $("doctor-list").replaceChildren(...problems.map((check) => {
    const row = document.createElement("div");
    row.className = "check";

    const dot = document.createElement("span");
    dot.className = `dot ${check.status}`;

    const body = document.createElement("div");
    const msg = document.createElement("div");
    msg.textContent = check.message;
    body.append(msg);

    if (check.fix) {
      const fix = document.createElement("div");
      fix.className = "muted small";
      fix.textContent = check.fix;
      body.append(fix);
    }
    if (check.command) {
      const code = document.createElement("code");
      code.textContent = check.command;
      body.append(code);
    }
    if (check.detail) {
      const details = document.createElement("details");
      const summary = document.createElement("summary");
      summary.textContent = "Technical details";
      const pre = document.createElement("pre");
      pre.textContent = check.detail;
      details.append(summary, pre);
      body.append(details);
    }

    row.append(dot, body);
    return row;
  }));
}

// note makes a line of text for prepending to a section.
function note(text) {
  const el = document.createElement("div");
  el.className = "drive-fast";
  el.textContent = text;
  return el;
}

// --- the drive ------------------------------------------------------------

// loadDriveHealth reports how ARFABIT is reaching the drive.
//
// This is the single thing that most decides how long a disc takes: reached
// directly, a film is read in tens of minutes; reached through the operating
// system, an encrypted disc crawls at roughly the speed it would play at.
//
// Asking needs the drive, so it is not asked while a disc is being read. The
// last answer stays on screen until it can be asked again.
async function loadDriveHealth() {
  if (driveBusy) return;

  const box = $("drive-health");

  let report;
  try {
    report = await fetch("/api/drive-health").then((r) => r.json());
  } catch (err) {
    box.textContent = String(err);
    return;
  }

  // Something started using the drive while it was being asked.
  if (driveBusy) return;

  if (report.message) {
    box.textContent = report.message;
    return;
  }

  const found = report.drives || [];
  if (found.length === 0) {
    box.textContent = "";
    return;
  }

  // The system holding the disc is the usual reason a drive reads slowly, and
  // unlike the access mode it is something a person can fix in one click.
  $("drive-free").hidden = !found.some((d) => d.mounted);

  box.replaceChildren(...found.map((drive) => {
    const block = document.createElement("div");

    // The heading already names a single drive.
    if (found.length > 1) {
      const name = document.createElement("div");
      name.textContent = drive.name;
      block.append(name);
    }

    const mode = document.createElement("div");
    mode.className = drive.fast ? "drive-fast" : "drive-slow";
    mode.textContent = drive.explanation;
    block.append(mode);

    if (drive.mount_note) {
      const mount = document.createElement("div");
      mount.className = "drive-slow";
      mount.textContent = drive.mount_note;
      block.append(mount);
    }

    const observed = document.createElement("div");
    if (drive.observed > 0) {
      const hours = (40700 / drive.observed / 3600).toFixed(1);
      observed.textContent =
        `Measured at ${drive.observed.toFixed(1)} MB per second over ${drive.samples} disc${drive.samples === 1 ? "" : "s"}` +
        ` — a 40 GB film would take about ${hours} hours at that rate.`;
    } else {
      observed.textContent = "ARFABIT has not timed this drive yet. Once it has copied a disc, this will say how fast the drive reads.";
    }
    block.append(observed);

    return block;
  }));
}

// --- what a project starts from -----------------------------------------

// Every file a project can start from: originals, films and clips.
let sources = [];

// loadSources lists what a project can start from: the disc in the drive,
// and every file ARFABIT made, grouped by what it is.
async function loadSources() {
  const reply = await fetch("/api/sources").then((r) => r.json());
  sources = reply.sources || [];
  renderSources();
}

function renderSources() {
  const select = $("project-source");
  const was = select.value;
  const drive = drives[0];

  const disc = document.createElement("optgroup");
  disc.label = "Disc";
  disc.append(new Option(drive && drive.Name ? `The disc in ${drive.Name}` : "The disc in the drive", "disc"));
  const library = document.createElement("optgroup");
  library.label = "Library";
  for (const source of sources) {
    const option = new Option(`${source.path.split(/[\\/]/).pop()} (${bytes(source.size)})`, source.path);
    option.dataset.film = source.title;
    library.append(option);
  }
  select.replaceChildren(disc, ...(sources.length ? [library] : []));

  const values = [...select.querySelectorAll("option")].map((o) => o.value);
  select.value = values.includes(was) ? was : sources.length ? sources[0].path : "disc";
  if (select.value !== was) sourceChanged();
}

// chooseDisc makes the disc what the project starts from, as the drive's Plan
// button does.
function chooseDisc() {
  $("project-source").value = "disc";
  sourceChanged();
}

// sourceChanged shows the part of the page for what was chosen: the disc's
// Plan, or what to make from a file.
function sourceChanged() {
  const disc = $("project-source").value === "disc";
  show("project-disc", disc);
  show("project-file", !disc);
  updateBar();
  renderJob(lastJob, lastExisting);
  if (!disc) loadSource();
}

// renderDiscSource says where the disc has got to, with a button to read it
// when there is no Plan yet.
function renderDiscSource() {
  const drive = drives[0];
  const loaded = drives.find((d) => d.Loaded);
  const planned = Boolean(lastJob);
  let text = "";
  if (planned) text = "";
  else if (driveBusy) text = `The drive is busy with ${driveBusy}.`;
  else if (!drive) text = "No disc drive found. Plug one in and ARFABIT will notice.";
  else if (!loaded) text = "There is no disc in the drive. Put one in to plan what to make from it.";
  else text = `${loaded.Label || "A disc"} is in the drive. Read it to see what is on it and plan what to make.`;
  $("project-disc-status").textContent = text;
  show("project-disc-status", Boolean(text));
  show("project-read-disc", !planned && Boolean(loaded) && !driveBusy);
}

// Remembering the stretch a clip was last taken from, between visits.
//
// Kept in the browser rather than saved as defaults: where somebody last took
// a clip from is a convenience, not a decision about how ARFABIT should work.
const REMEMBERED = "arfabit.stretch";

function rememberStretch() {
  try {
    localStorage.setItem(REMEMBERED, JSON.stringify({
      at: $("lab-at").value,
    }));
  } catch {
    // Private windows and cleared storage are ordinary; there is simply
    // nothing to remember with.
  }
}

function recallStretch() {
  try {
    const saved = JSON.parse(localStorage.getItem(REMEMBERED) || "null");
    if (!saved) return;

    if (saved.at) $("lab-at").value = saved.at;
  } catch {
    // Anything unreadable simply leaves the defaults in place.
  }
}

// --- projects -------------------------------------------------------------

// What the chosen file holds, and the project being planned from it.
let sourceInfo = { tracks: [], duration: 0 };

// Whether this computer can read picture subtitles into text (§10).
let subtitleReading = { ocr: false, note: "" };
let pkg = null;


// The defaults' values, for a line that starts being converted by hand.
let defaultValues = { crf_uhd: 20, crf_bluray: 20, crf_dvd: 18, preset: "slow", audio_bitrate: "256k" };

const PRESETS = ["superfast", "medium", "slow", "slower", "veryslow"];
const BITRATES = ["128k", "192k", "256k", "320k", "448k", "640k", "768k"];
const SECTIONS = [["video", "Video"], ["audio", "Audio"], ["subtitle", "Subtitles"]];

// part reports whether the project makes something of part of the file
// rather than all of it: any length but Full.
function part() {
  return Number($("lab-length").value) !== 0;
}

// projectName is what the project's files will be called, before the
// language and extension: the film's folder name and the edition, as the
// server names them (braces cannot be in an edition).
function projectName() {
  const chosen = $("project-source").selectedOptions[0];
  const film = chosen ? chosen.dataset.film || "" : "";
  const edition = $("project-edition").value.replace(/[{}]/g, "").trim();
  return edition ? `${film} {edition-${edition}}` : film;
}

// updateBar shows the parts of the bar at the top of Projects that belong to
// a file: its edition, container, stretch, name and Start.
function updateBar() {
  const file = $("project-source").value !== "disc" && Boolean($("project-source").value);
  $("project-file-fields").hidden = !file;
  $("project-summary").hidden = !file;
}

function containerChoice() {
  const chosen = document.querySelector('input[name="project-container"]:checked');
  return chosen ? chosen.value : "mkv";
}

// hasAV reports whether a project makes a video or audio file, rather than
// subtitle files alone.
function hasAV(p) {
  return Boolean(p) && p.items.some((it) => it.kind === "video" || it.kind === "audio");
}

// loadSource reads what the chosen file holds, then fills the project in
// from whatever "Start from" shows, as a Plan starts from the defaults.
async function loadSource() {
  const path = $("project-source").value;
  const asked = ++sourceAsked;
  sourceInfo = { tracks: [], duration: 0 };
  pkg = null;
  renderProject();
  if (!path || path === "disc") return;
  editorSays("Reading what this file holds\u2026");

  let reply;
  try {
    reply = await askWithin(`/api/source?path=${encodeURIComponent(path)}`);
  } catch (err) {
    if (asked === sourceAsked) editorSays(String(err.message || err), null, true);
    return;
  }
  // Another file was chosen while this one was being read.
  if (asked !== sourceAsked) return;
  if (!reply.tracks) {
    editorSays(reply.message || "That file could not be read.", reply.detail, true);
    return;
  }
  sourceInfo = reply;
  await fillProject(asked);
}

// sourceAsked counts the files asked about, so an answer about one no longer
// chosen is ignored rather than drawn over the one that is.
let sourceAsked = 0;

// askWithin fetches a page of ARFABIT's answers, and gives up after 15
// seconds with a sentence saying so, rather than leaving the page waiting
// with nothing to show for it.
async function askWithin(path, options) {
  const stop = new AbortController();
  const timer = setTimeout(() => stop.abort(), 15000);
  try {
    const res = await fetch(path, { ...options, signal: stop.signal });
    return await res.json();
  } catch (err) {
    if (stop.signal.aborted) throw new Error("ARFABIT did not answer within 15 seconds.");
    throw err;
  } finally {
    clearTimeout(timer);
  }
}

// editorSays puts a line where the line items go: what is happening, or what
// went wrong with its raw account one click away (§15), and a way to try
// again.
function editorSays(text, detail, again) {
  const box = $("project-editor");
  const line = document.createElement("p");
  line.className = "muted";
  line.textContent = text;
  box.replaceChildren(line);
  if (detail) {
    const details = document.createElement("details");
    const summary = document.createElement("summary");
    summary.textContent = "Technical details";
    const pre = document.createElement("pre");
    pre.textContent = detail;
    details.append(summary, pre);
    box.append(details);
  }
  if (again) {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = "Try again";
    button.addEventListener("click", loadSource);
    box.append(button);
  }
}

// fillProject starts the project again from a blueprint, or from the defaults.
async function fillProject(asked = sourceAsked) {
  const path = $("project-source").value;
  if (!path || path === "disc") return;
  let reply;
  try {
    reply = await askWithin("/api/project/fill", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ source: path, blueprint: $("project-blueprint").value }),
    });
  } catch (err) {
    if (asked === sourceAsked) editorSays(String(err.message || err), null, true);
    return;
  }
  if (asked !== sourceAsked) return;
  if (!reply.project) {
    editorSays(reply.message || "ARFABIT could not fill this in.", reply.detail, true);
    return;
  }
  pkg = reply.project;
  pkg.items = pkg.items || [];
  renderProject();
}

function renderProject() {
  $("lab-at").disabled = !part();
  const box = $("project-editor");
  if (pkg) {
    pkg.length = part() ? Number($("lab-length").value) * 1e9 : 0;
    pkg.containers = [containerChoice()];
  }
  show("project-containers", hasAV(pkg));
  $("project-container-note").hidden = !hasAV(pkg) || containerChoice() !== "mp4";
  $("project-container-note").textContent =
    "How an MP4 plays on the Apple TV has not been tried yet. An MP4 cannot hold picture subtitles, or Dolby TrueHD as it is.";
  if (!pkg) {
    box.replaceChildren();
    describeProject();
    return;
  }
  renderProjectEditor(box, sourceInfo.tracks, pkg, () => {
    renderProject();
  }, "Add from this file");
  if (document.activeElement !== $("project-edition")) {
    $("project-edition").value = pkg.edition || "";
  }
  describeProject();
}

// projectBody is the project as Start would send it.
function projectBody() {
  const chosen = $("project-source").selectedOptions[0];
  const seconds = (n) => Math.round(n * 1e9);
  return {
    source: $("project-source").value,
    film: chosen ? chosen.dataset.film : "",
    project: {
      ...pkg,
      edition: $("project-edition").value.trim(),
      containers: [containerChoice()],
      at: part() ? seconds(parseTimestamp($("lab-at").value)) : 0,
      length: part() ? seconds(Number($("lab-length").value)) : 0,
    },
  };
}

// The files the project would make that are already there. Start waits until
// there are none, since ARFABIT does not replace them (§0.6).
let conflicts = [];
let checkTimer = null;

// renderName asks what the project's files will be called, and whether any is
// already there, a moment after the last change, and shows the answer.
function renderName() {
  const name = projectName();
  $("project-name").textContent = name;
  clearTimeout(checkTimer);
  if (!pkg || !pkg.items.length) {
    conflicts = [];
    $("project-conflict").hidden = true;
    return;
  }
  checkTimer = setTimeout(async () => {
    const res = await fetch("/api/project/check", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(projectBody()),
    }).catch(() => null);
    if (!res || !res.ok) return;
    const reply = await res.json();
    const base = (p) => p.split(/[\\/]/).pop();
    const files = (reply.files || []).map(base);
    $("project-name").textContent = files.length ? files.join(" · ") : name;
    conflicts = reply.existing || [];
    $("project-conflict").hidden = conflicts.length === 0;
    $("project-conflict").textContent = conflicts.length
      ? `${conflicts.map(base).join(", ")} ${conflicts.length === 1 ? "is" : "are"} already in this film's folder, and ARFABIT does not replace files. Give this another edition, or move that file somewhere else.`
      : "";
    describeProject(true);
  }, 250);
}

// describeProject says what pressing Start will make, and where it goes.
function describeProject(checked) {
  if (!checked) renderName();
  updateBar();
  const say = (text, ready) => {
    $("project-destination").textContent = text;
    $("project-run").disabled = !ready || conflicts.length > 0;
  };
  if (!pkg) {
    say("", false);
    return;
  }
  if (pkg.items.length === 0) {
    say("Add video, audio or subtitles from the file, above.", false);
    return;
  }
  if (pkg.items.filter((it) => it.kind === "video").length > 1) {
    say("A file can hold one video.", false);
    return;
  }

  const much = part() ? "part of it" : "all of it";
  const subs = pkg.items.filter((it) => it.kind === "subtitle");
  const read = subs.filter((it) => it.action === "convert").length;
  const kept = subs.length - read;
  const plural = (n, one) => `${n} ${one}${n === 1 ? "" : "s"}`;
  let text;
  if (hasAV(pkg)) {
    text = `One ${containerChoice().toUpperCase()} of ${much}`;
    if (read) text += `, with ${plural(read, "subtitle file")} read into text beside it`;
  } else {
    const files = [];
    if (kept) files.push(`${plural(kept, "subtitle file")} as ${kept === 1 ? "it is" : "they are"}`);
    if (read) files.push(`${plural(read, "subtitle file")} read into text`);
    text = `${files.join(" and ")}, of ${much}`;
  }
  say(`${text}.`, true);
}

// renderProjectEditor lays out a project's line items, section by section,
// with what else the source holds underneath each, one click to add.
//
// Written once for both places a project is planned: from an original here, and
// from a disc on its Plan. changed is called after anything changes.
function renderProjectEditor(box, tracks, pkg, changed, addLabel = "Add from original") {
  const sections = SECTIONS.map(([kind, title]) => {
    const section = document.createElement("div");
    section.className = "project-section";

    const heading = document.createElement("h3");
    heading.textContent = title;
    if (kind === "audio") {
      const hint = document.createElement("span");
      hint.className = "section-hint";
      hint.textContent = "The first in the list plays first";
      heading.append(hint);
    }

    const rows = document.createElement("div");
    rows.className = "project-rows";
    const items = pkg.items.filter((it) => it.kind === kind);
    rows.append(...items.map((item, i) => itemRow(item, i, tracks, pkg, changed, rows, kind)));
    if (items.length === 0) {
      const none = document.createElement("p");
      none.className = "muted small";
      none.textContent = {
        video: "No video.",
        audio: "No audio.",
        subtitle: "No subtitles.",
      }[kind];
      rows.append(none);
    }

    section.append(heading, rows);

    // Everything the source holds of this kind, one line each, to add as
    // it is or converted. A film holds one picture, so the picture is
    // only offered while there is none.
    const offered = tracks.filter((t) => t.kind === kind);
    if (offered.length && (kind !== "video" || items.length === 0)) {
      const more = document.createElement("div");
      more.className = "project-spare";
      const label = document.createElement("div");
      label.className = "muted small";
      label.textContent = addLabel;
      // Each line adds its track as it is; converting it is a choice made
      // on the line once it is in the project.
      more.append(label, ...offered.map((track) => {
        // How many lines already use this track, so what is in the
        // project, and what is in it twice, shows at a glance.
        const uses = pkg.items.filter((it) => it.kind === kind && it.source === track.index).length;
        const count = document.createElement("span");
        count.className = uses ? "spare-count used" : "spare-count";
        count.textContent = `${uses}\u00d7`;

        const b = document.createElement("button");
        b.type = "button";
        b.className = "spare";
        b.append(count, ...titleOf(track.label, track));
        b.title = uses
          ? `In the project ${uses === 1 ? "once" : `${uses} times`}. Click to add it again, as it is.`
          : "Add to the project as it is";
        b.addEventListener("click", () => {
          pkg.items.push(copyItem(track));
          changed();
        });
        return b;
      }));
      section.append(more);
    }
    return section;
  });

  box.replaceChildren(...sections);
}

// lossless is the badge a lossless track carries in its title. Lossy tracks
// carry none: their title says their bitrate instead.
function lossless(track) {
  const badge = document.createElement("span");
  badge.className = "badge-lossless";
  badge.textContent = "Lossless";
  return badge;
}

// titleOf is a track's title, with its Lossless badge if it has one.
function titleOf(text, track) {
  return track && track.lossless ? [document.createTextNode(text), lossless(track)] : [document.createTextNode(text)];
}

// lineKey is everything that makes a line what it is, so two the same can be
// told apart from two that only share a track.
function lineKey(it) {
  return [it.kind, it.source, it.action, it.to, it.crf, it.preset, it.bitrate, it.out_channels || 0].join("|");
}

function copyItem(track) {
  return {
    kind: track.kind, action: "copy", source: track.index, codec: track.codec,
    lang: track.lang, channels: track.channels, lossless: track.lossless, label: track.label,
    source_bitrate: track.bitrate || 0,
  };
}

// bitratesFor are the bitrates worth offering for a track: for lossy sound,
// none above what it has, since more is only a bigger file of the same sound.
function bitratesFor(track) {
  if (track.lossless || !track.bitrate) return BITRATES;
  const fit = BITRATES.filter((b) => parseInt(b, 10) * 1000 <= track.bitrate);
  return fit.length ? fit : [BITRATES[0]];
}

// bitrateFor is the one to start from: what is wanted, or the most the track
// has if that is less.
function bitrateFor(want, track) {
  const offered = bitratesFor(track);
  return offered.includes(want) ? want : offered[offered.length - 1];
}

// convertItem is where a line starts when it is first converted: the
// defaults' picture quality, FLAC for lossless sound, AAC otherwise.
function convertItem(track) {
  const item = { ...copyItem(track), action: "convert" };
  if (track.kind === "video") {
    const crf = track.height >= 2000 ? defaultValues.crf_uhd
      : track.height && track.height < 700 ? defaultValues.crf_dvd : defaultValues.crf_bluray;
    return { ...item, to: "hevc", crf, preset: defaultValues.preset };
  }
  if (track.kind === "subtitle") return { ...item, to: "srt" };
  if (track.lossless) return { ...item, to: "flac" };
  return { ...item, to: "aac", bitrate: bitrateFor(track.channels > 2 ? "640k" : defaultValues.audio_bitrate, track) };
}

function select(options, value, onChange) {
  const el = document.createElement("select");
  for (const [v, label] of options) el.append(new Option(label, v, false, String(v) === String(value)));
  el.addEventListener("change", () => onChange(el.value));
  return el;
}

// itemRow is one line item: its track, what to do with it, and what that
// costs on the television.
function itemRow(item, position, tracks, pkg, changed, rows, kind) {
  const track = tracks.find((t) => t.kind === item.kind && t.index === item.source)
    || { ...item, index: item.source, note: "" };

  const row = document.createElement("div");
  row.className = "project-item";
  row.item = item;

  const line = document.createElement("div");
  line.className = "project-line";

  const grip = document.createElement("button");
  grip.type = "button";
  grip.className = "grip";
  grip.setAttribute("aria-label", "Drag to change the order");
  grip.title = "Drag to change the order";
  grip.innerHTML = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3 5h10M3 8h10M3 11h10"/></svg>';
  grip.hidden = kind === "video";
  const reorder = (ordered) => {
    let i = 0;
    pkg.items = pkg.items.map((it) => (it.kind === kind ? ordered[i++] : it));
    changed();
  };
  grip.addEventListener("pointerdown", (e) => dragRow(e, row, rows, () =>
    reorder([...rows.children].map((r) => r.item).filter(Boolean))));
  // The arrow keys move it too, one place at a time.
  grip.addEventListener("keydown", (e) => {
    if (e.key !== "ArrowUp" && e.key !== "ArrowDown") return;
    e.preventDefault();
    const ordered = pkg.items.filter((it) => it.kind === kind);
    const at = ordered.indexOf(item);
    const to = at + (e.key === "ArrowUp" ? -1 : 1);
    if (to < 0 || to >= ordered.length) return;
    [ordered[at], ordered[to]] = [ordered[to], ordered[at]];
    reorder(ordered);
  });

  const text = document.createElement("span");
  text.className = "project-label";
  text.append(...titleOf(track.label || item.label, track));

  // A line is copied as it is unless Convert is on, which shows what to
  // convert it to. Keeping a track and a converted one beside it is two
  // lines, the second added from the original below. Subtitles are converted
  // only where this computer can read them into text, and only a Blu-ray's
  // picture subtitles are read (§10).
  const convert = document.createElement("label");
  convert.className = "chip convert-pill";
  const toggle = document.createElement("input");
  toggle.type = "checkbox";
  toggle.checked = item.action === "convert";
  toggle.addEventListener("change", () => {
    const at = pkg.items.indexOf(item);
    pkg.items[at] = toggle.checked ? convertItem(track) : copyItem(track);
    changed();
  });
  convert.append(toggle, document.createTextNode("Convert"));
  const readable = track.codec === "hdmv_pgs_subtitle";
  convert.hidden = kind === "subtitle" && !(readable && subtitleReading.ocr);

  const remove = document.createElement("button");
  remove.type = "button";
  remove.textContent = "Remove";
  remove.addEventListener("click", () => {
    pkg.items.splice(pkg.items.indexOf(item), 1);
    changed();
  });

  line.append(grip, text, convert);
  if (item.action === "convert") line.append(...convertFields(item, track, changed));
  line.append(remove);

  row.append(line);

  // A copy is exactly what the track is on the television; a conversion is
  // into something that plays directly (§4). A line the same as another
  // would put the same track in the file twice.
  const notes = [];
  if (item.action === "copy" && track.note) notes.push(track.note);
  if (item.action === "copy" && kind === "subtitle" && readable && !subtitleReading.ocr && subtitleReading.note) {
    notes.push(subtitleReading.note);
  }
  if (item.action === "convert" && kind === "subtitle") {
    notes.push(pkg.length
      ? "Read into text from the part cut for this, and kept beside what is made as an SRT file."
      : "Read into text from the file, as a task of its own, and kept beside it. What is made here gets a copy when it is finished, fixes and all.");
  }
  if (item.action === "convert" && kind === "audio" && !track.lossless) {
    notes.push(track.note
      ? "This audio is not lossless, so converting it loses a little more."
      : "This audio is not lossless, so converting it loses a little more, and it plays directly as it is.");
  }
  const same = pkg.items.filter((other) => lineKey(other) === lineKey(item)).length;
  if (same > 1) notes.push(`This line is the same as ${same === 2 ? "another" : `${same - 1} others`}, so the file would hold the same track ${same} times.`);
  for (const text of notes) {
    const n = document.createElement("p");
    n.className = "project-note small";
    n.textContent = text;
    row.append(n);
  }
  return row;
}

// convertFields are the settings for a converted line.
function convertFields(item, track, changed) {
  const set = (key, cast = (v) => v) => (value) => { item[key] = cast(value); changed(); };

  if (item.kind === "subtitle") {
    const label = document.createElement("span");
    label.className = "muted small";
    label.textContent = "Text (SRT)";
    return [label];
  }

  if (item.kind === "video") {
    const crf = document.createElement("input");
    crf.type = "number";
    crf.min = "0";
    crf.max = "51";
    crf.value = item.crf;
    crf.title = "Quality: lower is better and bigger. 20 is close to the disc.";
    crf.addEventListener("change", () => set("crf", Number)(crf.value));
    const label = document.createElement("span");
    label.className = "muted small";
    label.textContent = "HEVC, quality";
    return [label, crf, select(PRESETS.map((p) => [p, p]), item.preset, set("preset"))];
  }

  // E-AC-3 is for surround a receiver can take whole. Its encoder stops at
  // six channels (§9), and for stereo AAC does the same job everywhere.
  const surround = track.channels > 2 && track.channels <= 6 && (item.out_channels || 0) !== 2;
  const formats = [["aac", "AAC"]];
  if (surround || item.to === "eac3") formats.push(["eac3", "E-AC-3"]);
  if (track.lossless || item.to === "flac") formats.unshift(["flac", "FLAC, lossless"]);
  const fields = [select(formats, item.to, (value) => {
    item.to = value;
    if (value === "flac") delete item.bitrate;
    else if (!item.bitrate) item.bitrate = bitrateFor(track.channels > 2 ? "640k" : defaultValues.audio_bitrate, track);
    changed();
  })];

  if ((track.channels || 0) > 2) {
    fields.push(select([["0", `All ${track.channels} channels`], ["2", "2.0"]],
      String(item.out_channels || 0), (value) => {
        item.out_channels = Number(value);
        // Stereo E-AC-3 is offered nowhere else, so it is not kept here.
        if (item.out_channels === 2 && item.to === "eac3") item.to = "aac";
        changed();
      }));
  }
  if (item.to !== "flac") {
    const offered = bitratesFor(track);
    fields.push(select(offered.map((b) => [b, b]),
      item.bitrate || bitrateFor(defaultValues.audio_bitrate, track), set("bitrate")));
  }
  return fields;
}

// dragRow lets a line item be dragged to a new place in its own section.
// Nothing changes until it is let go, and then only that section's order.
function dragRow(e, row, rows, dropped) {
  dragAmong(e, row, () => [...rows.children].filter((r) => r !== row && r.item), dropped);
}

// loadBlueprints lists the blueprints to start a project from, and to manage.
async function loadBlueprints() {
  const { blueprints = [], defaults } = await fetch("/api/blueprints").then((r) => r.json());
  if (defaults) defaultValues = { ...defaultValues, ...defaults };

  blueprintNames = blueprints.map((b) => b.name);
  renderDriveWhen(drives[0]);

  const start = $("project-blueprint");
  const was = start.value;
  const chosen = blueprints.find((b) => b.default);
  start.replaceChildren(
    new Option("The defaults", ""),
    ...blueprints.map((b) => new Option(b.name, b.name)),
  );
  start.value = [...start.options].some((o) => o.value === was) && was !== ""
    ? was
    : chosen ? chosen.name : "";

  renderBlueprintManager(blueprints, defaults);
}

// --- sound rules ----------------------------------------------------------

// The languages a rule can name, in the order they are offered.
const RULE_LANGUAGES = ["eng", "fra", "spa", "deu", "ita", "jpn", "por", "nld", "rus", "kor", "zho"];
const LAYOUTS = ["7.1", "5.1", "stereo"];

// Starting points for the rules, since a blank set of rules is a puzzle.
const SOUND_PRESETS = [
  ["Everything", { languages: [], language_mode: "all", choices: [{ mode: "all", layouts: [], quality: "" }] }],
  ["Best English surround and English 2.0", {
    languages: ["eng"], language_mode: "one", choices: [
      { mode: "one", layouts: ["7.1", "5.1"], quality: "" },
      { mode: "one", layouts: ["stereo"], quality: "" },
    ],
  }],
  ["English 2.0 only", { languages: ["eng"], language_mode: "one", choices: [{ mode: "one", layouts: ["stereo"], quality: "" }] }],
];

// describeChoice reads one choice as a phrase, the same way ARFABIT reports
// what it found.
function describeChoice(c) {
  const layouts = c.layouts && c.layouts.length
    ? c.layouts.map((l) => (l === "stereo" ? "2.0" : l)).join(" or ")
    : "any layout";
  const quality = c.quality === "lossless" ? ", lossless" : c.quality === "lossy" ? ", not lossless" : "";
  return `${c.mode === "all" ? "keep all of" : "pick one of"} ${layouts}${quality}`;
}

// describeSound reads a whole set of rules as a sentence, so what they will do
// is never a matter of working it out from the controls.
function describeSound(rules) {
  if (!rules) return "Stereo first, with every surround track offered beside it.";

  const names = (rules.languages || []).map(languageName);
  const which = names.length === 0
    ? "For every language on the disc"
    : names.length === 1
      ? `For ${names[0]}`
      : rules.language_mode === "all"
        ? `For each of ${names.join(", ")}`
        : `For the first of ${names.join(", ")} the disc has`;

  const choices = (rules.choices || []).map(describeChoice);
  return `${which}: ${choices.join("; then ") || "nothing"}.`;
}

// soundEditor is the part of the blueprint form that sets its sound rules.
//
// It redraws itself from one object whenever anything changes, and the form
// reads that object back when it is saved.
function soundEditor(initial) {
  const box = document.createElement("div");
  box.className = "sound-editor";
  let rules = initial ? JSON.parse(JSON.stringify(initial)) : null;
  box.readSound = () => rules;

  const select = (options, value, onChange) => {
    const el = document.createElement("select");
    for (const [v, label] of options) el.append(new Option(label, v, false, v === value));
    el.addEventListener("change", () => onChange(el.value));
    return el;
  };

  const chip = (label, on, onChange) => {
    const wrap = document.createElement("label");
    wrap.className = "chip";
    const input = document.createElement("input");
    input.type = "checkbox";
    input.checked = on;
    input.addEventListener("change", () => onChange(input.checked));
    wrap.append(input, document.createTextNode(label));
    return wrap;
  };

  const draw = () => {
    const heading = document.createElement("h4");
    heading.textContent = "Audio";

    const mode = select([["usual", "The usual choice"], ["rules", "Choose by rule"]],
      rules ? "rules" : "usual", (v) => {
        rules = v === "rules" ? JSON.parse(JSON.stringify(SOUND_PRESETS[1][1])) : null;
        draw();
      });

    const reads = document.createElement("p");
    reads.className = "sound-reads";
    reads.textContent = describeSound(rules);

    const parts = [heading, mode, reads];

    if (rules) {
      // Which languages.
      const langs = document.createElement("div");
      langs.className = "rule-row";
      const langMode = select([["one", "The first of"], ["all", "Each of"]], rules.language_mode || "one",
        (v) => { rules.language_mode = v; draw(); });
      langs.append(langMode, ...RULE_LANGUAGES.map((code) =>
        chip(languageName(code), rules.languages.includes(code), (on) => {
          rules.languages = on
            ? [...rules.languages, code]
            : rules.languages.filter((c) => c !== code);
          draw();
        })));
      const langNote = document.createElement("p");
      langNote.className = "muted small";
      langNote.textContent = "Tick none for every language on the disc. The order ticked is the order preferred.";

      // What to choose in each language.
      const choices = rules.choices.map((c, i) => {
        const row = document.createElement("div");
        row.className = "rule-row";
        row.append(
          select([["one", "Pick one of"], ["all", "Keep all of"]], c.mode, (v) => { c.mode = v; draw(); }),
          ...LAYOUTS.map((layout) => chip(layout === "stereo" ? "2.0" : layout, c.layouts.includes(layout), (on) => {
            // Kept widest first, which is the order of preference.
            c.layouts = LAYOUTS.filter((l) => (l === layout ? on : c.layouts.includes(l)));
            draw();
          })),
          select([["", "Lossless or not"], ["lossless", "Lossless only"], ["lossy", "Not lossless"]], c.quality,
            (v) => { c.quality = v; draw(); }),
        );
        if (rules.choices.length > 1) {
          const remove = document.createElement("button");
          remove.type = "button";
          remove.textContent = "Remove";
          remove.addEventListener("click", () => { rules.choices.splice(i, 1); draw(); });
          row.append(remove);
        }
        return row;
      });

      const add = document.createElement("button");
      add.type = "button";
      add.textContent = "Add a choice";
      add.addEventListener("click", () => {
        rules.choices.push({ mode: "one", layouts: ["stereo"], quality: "" });
        draw();
      });

      const presets = document.createElement("div");
      presets.className = "suggestions";
      presets.append(document.createTextNode("Start from: "), ...SOUND_PRESETS.map(([label, preset]) => {
        const b = document.createElement("button");
        b.type = "button";
        b.textContent = label;
        b.addEventListener("click", () => { rules = JSON.parse(JSON.stringify(preset)); draw(); });
        return b;
      }));

      const each = document.createElement("div");
      each.className = "muted small";
      each.textContent = "In each language:";

      const note = document.createElement("p");
      note.className = "muted small";
      note.textContent = "Pick one of takes the widest layout ticked that the disc has. A choice the disc cannot meet is skipped. If nothing at all fits, the Plan asks you to choose the tracks yourself.";

      parts.push(langs, langNote, each, ...choices, add, presets, note);
    }

    box.replaceChildren(...parts);
  };

  draw();
  return box;
}

// blueprintForm builds the form for filling in a blueprint.
//
// The same form serves a saved blueprint and a one-off, because they are the
// same thing: one is remembered and one is not.
function blueprintForm(values, options) {
  const box = document.createElement("div");

  const fields = document.createElement("div");
  fields.className = "fields";

  const field = (label, input) => {
    const wrap = document.createElement("label");
    const text = document.createElement("span");
    text.textContent = label;
    wrap.append(text, input);
    fields.append(wrap);
    return input;
  };

  const number = (label, key, value) => {
    const input = document.createElement("input");
    input.type = "number";
    input.min = "0";
    input.max = "51";
    input.value = value;
    input.dataset.key = key;
    return field(label, input);
  };

  if (options.named) {
    const name = document.createElement("input");
    name.type = "text";
    name.value = values.name || "";
    name.dataset.key = "name";
    name.placeholder = "A name";
    field("Name", name);

    // The edition starts as the name, and follows it while the name is
    // typed, until somebody changes the edition itself. Clearing it is a
    // choice: that blueprint's films then have no edition.
    const edition = document.createElement("input");
    edition.type = "text";
    edition.value = values.edition ?? values.name ?? "";
    edition.dataset.key = "edition";
    edition.placeholder = "None";
    let following = edition.value === name.value;
    name.addEventListener("input", () => {
      if (following) edition.value = name.value;
    });
    edition.addEventListener("input", () => { following = false; });
    field("Edition", edition);
  }

  const preset = document.createElement("select");
  preset.dataset.key = "preset";
  for (const speed of ["superfast", "medium", "slow", "slower", "veryslow"]) {
    const option = new Option(speed, speed);
    option.selected = speed === (values.preset || "slow");
    preset.append(option);
  }
  field("Speed", preset);

  number("4K quality", "crf_uhd", values.crf_uhd ?? 20);
  number("Blu-ray quality", "crf_bluray", values.crf_bluray ?? 20);
  number("DVD quality", "crf_dvd", values.crf_dvd ?? 18);

  const bitrate = document.createElement("input");
  bitrate.type = "text";
  bitrate.value = values.audio_bitrate || "256k";
  bitrate.dataset.key = "audio_bitrate";
  field("Stereo audio", bitrate);

  box.append(fields);

  const tick = (label, key, checked) => {
    const wrap = document.createElement("label");
    wrap.className = "inline";

    const input = document.createElement("input");
    input.type = "checkbox";
    input.checked = checked;
    input.dataset.key = key;

    const text = document.createElement("span");
    text.textContent = label;

    wrap.append(input, text);
    box.append(wrap);
  };

  tick("Keep 4K pictures exactly as they are", "allow_uhd_copy", values.allow_uhd_copy !== false);
  tick("Keep the video exactly as it is", "keep_picture", values.keep_picture === true);
  tick("Keep audio exactly as it is", "copy_native_audio", values.copy_native_audio !== false);
  // Picture subtitles make Plex convert the whole picture to show them (§4),
  // so they are read into text, where this computer can (§10), unless kept.
  tick("Keep subtitles as pictures, rather than reading them into text", "keep_subtitle_pictures",
    values.keep_subtitle_pictures === true);

  // TrueHD plays on Apple TV only by Plex converting it every time (§4), so
  // what to do with it is a choice worth making once, here.
  const truehd = document.createElement("label");
  truehd.className = "inline";
  const truehdChoice = document.createElement("select");
  truehdChoice.dataset.key = "truehd";
  for (const [v, text] of [["keep", "Keep it as it is"], ["flac", "Convert it to FLAC"], ["both", "Both"]]) {
    truehdChoice.append(new Option(text, v, false, v === (values.truehd || "keep")));
  }
  truehd.append(document.createTextNode("Dolby TrueHD audio: "), truehdChoice);
  box.append(truehd);

  box.append(soundEditor(values.sound || null));

  const note = document.createElement("p");
  note.className = "muted small";
  note.textContent = "Lower numbers mean better pictures and bigger files. 20 is close to indistinguishable from the disc.";
  box.append(note);

  return box;
}

// readBlueprintForm gathers what was filled in.
function readBlueprintForm(box) {
  const values = {};

  for (const input of box.querySelectorAll("[data-key]")) {
    const key = input.dataset.key;
    if (input.type === "checkbox") {
      values[key] = input.checked;
    } else if (input.type === "number") {
      values[key] = Number(input.value);
    } else {
      values[key] = input.value;
    }
  }

  const sound = box.querySelector(".sound-editor");
  if (sound) values.sound = sound.readSound();

  return values;
}




// renderBlueprintManager lists the blueprints with a way to change them.
function renderBlueprintManager(blueprints, defaults) {
  const box = $("blueprint-manager");

  const summary = document.createElement("p");
  summary.className = "muted small";
  summary.textContent = defaults.default
    ? "No blueprint is used by default, so each new plan starts from the defaults in your settings file."
    : "New plans start from the blueprint marked below.";

  if (blueprints.length === 0) {
    box.replaceChildren(summary);
    return;
  }

  box.replaceChildren(summary, ...blueprints.map((blueprint) => {
    const row = document.createElement("div");
    row.className = "row";

    const left = document.createElement("div");
    const name = document.createElement("div");
    name.textContent = blueprint.default ? `${blueprint.name} (used by default)` : blueprint.name;

    const detail = document.createElement("div");
    detail.className = "muted small";
    detail.textContent = blueprint.description;

    const sound = document.createElement("div");
    sound.className = "muted small";
    sound.textContent = `Audio: ${describeSound(blueprint.sound)}`;

    left.append(name, detail, sound);



    const buttons = document.createElement("div");
    buttons.className = "button-row";

    const makeDefault = document.createElement("button");
    makeDefault.textContent = blueprint.default ? "Stop using by default" : "Use by default";
    makeDefault.addEventListener("click", async (e) => {
      const result = await busy(e.target, "Setting\u2026", null, () =>
        post("/api/blueprints/default", { name: blueprint.default ? "" : blueprint.name }));
      if (result) loadBlueprints();
    });
    buttons.append(makeDefault);

    const edit = document.createElement("button");
    edit.textContent = "Change";
    edit.addEventListener("click", () => openBlueprintEditor(blueprint));
    buttons.append(edit);

    const copy = document.createElement("button");
    copy.textContent = "Copy";
    copy.addEventListener("click", () =>
      openBlueprintEditor({ ...blueprint, name: `${blueprint.name} copy` }));
    buttons.append(copy);

    const remove = document.createElement("button");
    remove.textContent = "Remove";
    remove.addEventListener("click", async (e) => {
      const result = await busy(e.target, "Removing\u2026", null, () =>
        fetch(`/api/blueprints/${encodeURIComponent(blueprint.name)}`, { method: "DELETE" })
          .then((res) => (res.ok ? res.json() : null)));
      if (result) loadBlueprints();
    });
    buttons.append(remove);

    row.append(left, buttons);
    return row;
  }));
}

// openBlueprintEditor shows the form for making or changing a blueprint.
function openBlueprintEditor(values) {
  const editor = $("blueprint-editor");
  editor.hidden = false;

  const form = blueprintForm(values || {}, { named: true });

  const actions = document.createElement("div");
  actions.className = "actions";

  const save = document.createElement("button");
  save.className = "primary";
  save.textContent = "Save";
  save.addEventListener("click", async (e) => {
    const result = await busy(e.target, "Saving\u2026", "Saved", () =>
      post("/api/blueprints", readBlueprintForm(form)));
    if (result) {
      editor.hidden = true;
      loadBlueprints();
    }
  });

  const cancel = document.createElement("button");
  cancel.textContent = "Cancel";
  cancel.addEventListener("click", () => { editor.hidden = true; });

  actions.append(save, cancel);
  editor.replaceChildren(form, actions);
}


// parseTimestamp reads "1:15:20", "15:20" or plain seconds.
function parseTimestamp(text) {
  const parts = String(text).trim().split(":").map(Number);
  if (parts.some(Number.isNaN)) return 0;

  return parts.reduce((total, part) => total * 60 + part, 0);
}


function renderLabResults(comparison) {
  const rows = (comparison && comparison.clips) || [];
  show("lab-card", rows.length > 0);
  if (rows.length === 0) {
    $("lab-results").replaceChildren();
    return;
  }

  const table = document.createElement("table");
  table.className = "results";

  const head = document.createElement("tr");
  for (const heading of ["Setting", "All of it", "Encode time", "Size", "Speed"]) {
    const th = document.createElement("th");
    th.textContent = heading;
    head.append(th);
  }
  table.append(head);

  for (const row of rows) {
    const tr = document.createElement("tr");
    if (row.size_share === 100) tr.className = "best";

    const name = document.createElement("td");
    if (row.clip.path) {
      const link = document.createElement("a");
      link.textContent = row.clip.name;
      link.href = "#";
      link.title = row.clip.path;
      name.append(link);
    } else {
      name.textContent = row.clip.name;
    }
    tr.append(name);

    if (row.clip.problem) {
      const cell = document.createElement("td");
      cell.colSpan = 4;
      cell.className = "muted small";
      cell.textContent = "did not finish";
      tr.append(cell);
      table.append(tr);
      continue;
    }

    for (const value of [
      bytes(row.whole_film),
      humanMs(row.encode_time / 1000000),
      `${Math.round(row.size_share)}%`,
      `${Math.round(row.time_share)}%`,
    ]) {
      const td = document.createElement("td");
      td.className = "num";
      td.textContent = value;
      tr.append(td);
    }

    table.append(tr);
  }

  const note = document.createElement("p");
  note.className = "muted small";
  note.textContent =
    "Size and speed are shown against the best in each column, where the best is 100%. " +
    "Play them and pick.";

  $("lab-results").replaceChildren(table, note);
}

// --- settings -------------------------------------------------------------

// loadConversionLimit says how many things convert at once, and where to
// change it.
async function loadConversionLimit() {
  const state = await fetch("/api/state").then((r) => r.json());
  const limit = state.conversions_at_once || 1;

  $("conversions-detail").textContent = limit === 1
    ? "One film at a time, which is usually fastest: converting already uses every core, so a second one makes both later. " +
      "Change machine.max_conversions in your settings file to allow more."
    : `Up to ${limit} at once. Change machine.max_conversions in your settings file to alter this.`;
}

async function loadAutostart() {
  const status = await fetch("/api/autostart").then((r) => r.json());
  $("autostart").checked = !!status.enabled;
  $("autostart-detail").textContent = status.enabled
    ? `Set up through your computer's ${status.mechanism}.`
    : "";
}

async function loadIndexStatus() {
  const status = await fetch("/api/index").then((r) => r.json());
  renderIndexStatus(status);
}

// describeAge says how long ago something happened, in the words a person
// would use.
function describeAge(when) {
  if (!when) return "at some point";

  const then = new Date(when);
  if (Number.isNaN(then.getTime())) return "at some point";

  const days = Math.floor((Date.now() - then.getTime()) / 86400000);
  if (days <= 0) return "today";
  if (days === 1) return "yesterday";
  if (days < 30) return `${days} days ago`;
  if (days < 365) return `on ${then.toLocaleDateString()}`;
  return `on ${then.toLocaleDateString()}, over a year ago`;
}

// ageAdvice says whether downloading again is worth the trouble.
//
// A film's name and year do not change, so an old list is only a problem for
// films released since it was made.
function ageAdvice(when) {
  if (!when) return "";

  const days = Math.floor((Date.now() - new Date(when).getTime()) / 86400000);
  if (days < 180) {
    return "No need to download it again unless a very new film is not recognised.";
  }
  return "Worth downloading again if a recent film is not recognised.";
}

function renderIndexStatus(status) {
  const detail = $("index-detail");
  const button = $("index-build");

  if (status.state === "downloading") {
    button.disabled = true;
    button.textContent = "Downloading…";
    detail.textContent = status.read
      ? `Downloaded ${bytes(status.read)} of about 200 MB. Saving to ${status.path}.`
      : `Starting the download. It will be saved to ${status.path}.`;
    return;
  }

  button.disabled = false;

  if (status.state === "ready") {
    button.textContent = "Download it again";
    detail.textContent =
      `${(status.count || 0).toLocaleString()} films, downloaded ${describeAge(status.built)}. ` +
      `${ageAdvice(status.built)} Saved in ${status.path}.`;
    return;
  }

  if (status.state === "stopped") {
    button.textContent = "Try again";
    detail.replaceChildren(document.createTextNode("The film list did not download."));
    if (status.detail) {
      const details = document.createElement("details");
      const summary = document.createElement("summary");
      summary.textContent = "Technical details";
      const pre = document.createElement("pre");
      pre.textContent = status.detail;
      details.append(summary, pre);
      detail.append(details);
    }
    return;
  }

  button.textContent = "Download the film list";
  detail.textContent =
    `Not downloaded yet. About 200 MB, once. It would be saved in ${status.path}.`;
}

// waitForRestart holds the page until ARFABIT answers again, then reloads.
async function waitForRestart() {
  $("restart-detail").textContent = "Restarting. This page will come back on its own.";

  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 500));
    try {
      const res = await fetch("/api/state", { cache: "no-store" });
      if (res.ok) {
        location.reload();
        return;
      }
    } catch {
      // Expected while the program is coming back up.
    }
  }

  $("restart-detail").textContent =
    "ARFABIT has not come back yet. Reload this page, or start it again from where you launched it.";
}

// --- sections -------------------------------------------------------------

// showView shows one section of the page and hides the others.
//
// It is still one page: everything keeps running and listening whichever
// section is showing, so switching never loses a log line or a clock. The
// address remembers the section, so reloading comes back to it.
function showView() {
  const wanted = location.hash.slice(1);
  const views = [...document.querySelectorAll(".view")];
  const name = views.some((v) => v.dataset.view === wanted) ? wanted : "tasks";

  for (const view of views) view.hidden = view.dataset.view !== name;
  updateBar();
  for (const tab of document.querySelectorAll(".tabbar a")) {
    if (tab.dataset.tab === name) tab.setAttribute("aria-current", "page");
    else tab.removeAttribute("aria-current");
  }
  window.scrollTo(0, 0);
}

// --- wiring ---------------------------------------------------------------

function connect() {
  events = new EventSource("/events");

  // Any job change may add or remove a card, so the whole picture is
  // refreshed rather than patched.
  events.addEventListener("job", () => refresh());
  events.addEventListener("log", (e) => appendLog([JSON.parse(e.data)]));
  events.addEventListener("index", (e) => renderIndexStatus(JSON.parse(e.data)));

  events.addEventListener("drives", (e) => {
    renderDrives(JSON.parse(e.data));
  });

  events.addEventListener("error", () => {
    // EventSource reconnects on its own; a brief drop is not worth reporting.
    $("connection").hidden = events.readyState !== EventSource.CLOSED;
  });

  events.addEventListener("open", () => { $("connection").hidden = true; });
}

// on attaches a handler, and says so rather than throwing when the element is
// not there.
//
// One missing element used to stop every later line from running, so a button
// removed from the page took the rest of the page with it. A gap in the wiring
// is now a note in the console and nothing more.
function on(id, event, handler) {
  const el = $(id);
  if (!el) {
    console.warn(`ARFABIT: no element called ${id} to attach ${event} to`);
    return;
  }
  el.addEventListener(event, handler);
}

function wireButtons() {
  // Plan reads the disc and opens its Plan, which is in Projects.
  on("scan", "click", (e) => {
    location.hash = "#projects";
    chooseDisc();
    readDisc(e.target);
  });

  on("start", "click", (e) =>
    busy(e.target, "Starting…", null, () => post("/api/start")));


  on("eject", "click", async (e) => {
    const result = await busy(e.target, "Ejecting…", null, () => post("/api/eject"));
    if (result) $("eject-detail").textContent = result.message;
  });

  on("title-save", "click", saveTitle);
  on("drive-when", "change", saveDriveWhen);
  on("plan-convert", "change", sendPlanChange);
  on("plan-edition", "change", sendPlanChange);

  on("log-filter", "input", applyFilter);

  on("log-follow", "change", (e) => {
    followLog = e.target.checked;
    $("log-jump").hidden = followLog;
  });

  // Scrolling up steps out of follow mode, because you are reading something.
  on("log", "scroll", () => {
    const box = $("log");
    const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
    if (!atBottom && followLog) {
      followLog = false;
      $("log-follow").checked = false;
      $("log-jump").hidden = false;
    }
  });

  on("log-jump", "click", () => {
    followLog = true;
    $("log-follow").checked = true;
    $("log-jump").hidden = true;
    $("log").scrollTop = $("log").scrollHeight;
  });

  on("autostart", "change", async (e) => {
    const status = await post("/api/autostart", { enabled: e.target.checked });
    if (status) {
      $("autostart").checked = !!status.enabled;
      $("autostart-detail").textContent = status.enabled
        ? `Set up through your computer's ${status.mechanism}.`
        : "";
    }
  });

  on("restart", "click", async (e) => {
    if (!(await askToEnd("Restart"))) return;
    const result = await busy(e.target, "Restarting…", null, () => post("/api/restart"));
    if (result) waitForRestart();
  });

  on("quit", "click", async (e) => {
    if (!(await askToEnd("Stop"))) return;
    const result = await busy(e.target, "Stopping…", null, () => post("/api/quit"));
    if (result) {
      $("restart-detail").textContent = $("autostart").checked
        ? "ARFABIT has stopped. It will start again the next time you log in, or you can start it yourself."
        : "ARFABIT has stopped. Start it again from where you launched it.";
      $("restart").disabled = true;
      $("quit").disabled = true;
    }
  });

  // The download outlives the request that starts it, so the event stream
  // owns this button rather than busy().
  on("drive-check", "click", (e) =>
    busy(e.target, "Checking\u2026", null, async () => {
      await loadDriveHealth();
      return true;
    }));

  on("drive-free", "click", async (e) => {
    const box = $("drive-health");
    box.textContent = "Taking temporary ownership of the drive\u2026";

    const result = await busy(e.target, "Taking ownership\u2026", null, () => post("/api/drive-free"));

    // Either way the drive is re-read, so the section says what is true now
    // rather than leaving the old answer on screen.
    await loadDriveHealth();

    if (result) {
      $("drive-health").prepend(note("ARFABIT has temporary ownership of the drive. The disc is still in it."));
    }
  });



  on("confirm-yes", "click", () => $("confirm-dialog").close("yes"));
  on("confirm-no", "click", () => $("confirm-dialog").close(""));

  on("lab-length", "change", () => {
    rememberStretch();
    renderProject();
  });

  on("lab-at", "input", rememberStretch);
  on("project-source", "change", sourceChanged);
  for (const radio of document.querySelectorAll('input[name="project-container"]')) {
    radio.addEventListener("change", renderProject);
  }
  on("project-read-disc", "click", (e) => readDisc(e.target));
  on("project-fill", "click", () => fillProject());
  on("project-edition", "input", (e) => {
    if (pkg) pkg.edition = e.target.value.trim();
    describeProject();
  });

  on("blueprint-new", "click", () => openBlueprintEditor({ name: "" }));

  on("project-run", "click", async (e) => {
    if (!pkg) return;
    const body = projectBody();
    const result = await busy(e.target, "Adding\u2026", "Added to the queue", () => post("/api/project", body));

    if (result) {
      // The queue is on another page, so say which one and make it one click.
      const link = document.createElement("a");
      link.href = "#tasks";
      link.textContent = "Tasks page";
      $("project-status").replaceChildren(
        document.createTextNode("Added to the queue on the "), link,
        document.createTextNode(". Its progress and log are there with everything else."));
      refresh();
    }
  });

  on("index-build", "click", (e) => {
    e.target.disabled = true;
    e.target.textContent = "Starting…";
    post("/api/index");
  });
}

function start() {
  window.addEventListener("hashchange", showView);
  showView();
  wireButtons();
  connect();
  refresh();
  runDoctor();
  loadAutostart();
  loadIndexStatus();
  loadConversionLimit();
  recallStretch();
  // The blueprints first: "Start from" has to be there to fill a project in.
  loadBlueprints().then(loadSources);
  loadDriveHealth();
}

start();
