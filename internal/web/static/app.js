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
let queueFilter = "all";
let focusedJob = "";
let masterTracks = [];
let clockTimer = null;

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

function stageWords(stage) {
  return {
    LAB: "Making test clips",
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
function renderDrives(list) {
  drives = list || [];

  const scan = $("scan");
  const loaded = drives.find((d) => d.Loaded);

  // The drive may be busy with a disc even while other films convert.
  if (driveBusy) {
    $("idle-title").textContent = `The drive is busy with ${driveBusy}`;
    $("idle-detail").textContent = "It will be free once that disc comes out.";
    scan.disabled = true;
    $("eject").disabled = true;
    return;
  }

  if (drives.length === 0) {
    $("idle-title").textContent = "No disc drive found";
    $("idle-detail").textContent =
      "Plug one in and ARFABIT will notice. Some drives need their own power supply.";
    scan.disabled = true;
    $("eject").disabled = true;
    return;
  }

  $("eject").disabled = false;

  if (loaded) {
    $("idle-title").textContent = `${loaded.Label || "A disc"} is in the drive`;
    $("idle-detail").textContent = loaded.Name || "";
    scan.disabled = false;
    scan.textContent = "Read this disc";
    return;
  }

  // The drive is there, just not holding anything readable. Saying which is
  // the difference between "put a disc in" and "wait a moment".
  const drive = drives[0];
  scan.disabled = true;
  scan.textContent = "Read this disc";

  // An open tray and a closed empty one are the same thing to the person
  // standing there: nothing to watch. Slot-loading drives have no tray to
  // close, so the wording never mentions one.
  const states = {
    loading: ["Reading the disc", "The drive is spinning up. This takes a few seconds."],
    empty: ["The drive is empty", "Insert a disc."],
  };
  const [title, detail] = states[drive.State] || states.empty;

  $("idle-title").textContent = title;
  $("idle-detail").textContent = `${detail}\n${drive.Name || ""}`;
}

// --- the plan -------------------------------------------------------------

function trackRow(id, sourceIndex, selected, label, note) {
  const wrap = document.createElement("label");
  wrap.className = "track";

  const box = document.createElement("input");
  box.type = "checkbox";
  box.checked = !!selected;
  box.dataset.source = sourceIndex;
  box.dataset.kind = id.startsWith("audio") ? "audio" : "subtitles";
  box.addEventListener("change", sendPlanChange);

  const text = document.createElement("span");
  text.textContent = note ? `${label} — ${note}` : label;

  wrap.append(box, text);
  return wrap;
}

// groupByLanguage lays the tracks out the way a disc's own menu reads.
function groupByLanguage(tracks, kind) {
  const groups = [];
  const byLang = new Map();

  tracks.forEach((track, i) => {
    const lang = languageName(track.lang);
    if (!byLang.has(lang)) {
      const group = { lang, rows: [] };
      byLang.set(lang, group);
      groups.push(group);
    }
    byLang.get(lang).rows.push({ track, i });
  });

  return groups.map((group) => {
    const box = document.createElement("div");
    box.className = "lang-group";

    const heading = document.createElement("h4");
    heading.textContent = group.lang;
    box.append(heading);

    for (const { track, i } of group.rows) {
      box.append(trackRow(`${kind}-${i}`, track.source_index, track.selected,
        track.label || track.codec, ""));
    }
    return box;
  });
}

function spaceMessage(space) {
  if (space.Unknown) {
    return "ARFABIT could not check how much room is left, so it will go ahead.";
  }
  const lead = space.Fits
    ? "There is just enough room, and the estimate could be low."
    : "There is not enough room for this disc.";
  return `${lead}\n\nThis rip needs about ${bytes(space.Needed)}.\n` +
    `The drive has ${bytes(space.Free)} free.\n` +
    `Your masters folder holds ${bytes(space.Masters)}.\n` +
    `Your library folder holds ${bytes(space.Library)}.`;
}

function sendPlanChange() {
  const change = { audio: {}, subtitles: {}, convert: $("plan-convert").checked };

  for (const box of document.querySelectorAll("#plan input[type=checkbox]")) {
    if (!box.dataset.kind) continue;
    change[box.dataset.kind][Number(box.dataset.source)] = box.checked;
  }

  post("/api/plan", change).then(() => refresh());
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
  $("title-name").value = job.title || job.disc_name || "";
  $("title-year").value = job.year ? String(job.year) : "";

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

function renderPlan(job) {
  const plan = job.plan;
  if (!plan) return;

  $("plan-title").textContent = job.title || job.disc_name || "Found a movie";
  $("plan-summary").textContent = `${plan.duration} · ${bytes(plan.source_size)} on the disc`;

  const notices = [];
  if (plan.obfuscated) notices.push(plan.reason);
  if (job.space && (!job.space.Fits || job.space.Tight || job.space.Unknown)) {
    notices.push(spaceMessage(job.space));
  }

  $("plan-notice").hidden = notices.length === 0;
  $("plan-notice").textContent = notices.join("\n\n");

  $("plan-video").textContent = plan.video_copy
    ? `Kept exactly as it is on the disc — ${plan.resolution}.`
    : `${plan.source_codec} · ${plan.resolution} · converted to HEVC, quality ${plan.crf}, ${plan.preset}.`;

  $("plan-audio").replaceChildren(...groupByLanguage(plan.audio || [], "audio"));

  const subs = plan.subtitles || [];
  if (subs.length === 0) {
    $("plan-subs").textContent = "This disc has no subtitles.";
  } else {
    $("plan-subs").replaceChildren(...groupByLanguage(subs.map((t) => ({
      ...t,
      label: `${t.forced ? "Only for foreign speech" : "Full subtitles"}${t.label ? ` · ${t.label}` : ""}`,
    })), "sub"));
  }

  renderTitleChoice(job);

  // Stopping at the copy is often the right choice: the copy is the only part
  // that needs the disc, and converting can be done any time afterwards.
  $("plan-convert").checked = plan.convert !== false;
  $("convert-note").textContent = plan.convert !== false
    ? "Turn this off to stop after copying the disc. The copy is the slow part that needs the drive; you can make the Apple TV file later from Your copies."
    : "ARFABIT will copy the disc and stop. Make the Apple TV file whenever you like, from Your copies below.";

  const est = plan.estimated_time ? Math.round(plan.estimated_time / 60000000000) : 0;
  $("plan-estimate").textContent = est
    ? `About ${est} minutes, finishing around ${bytes(plan.estimated_size)}.`
    : "";

  const fits = !job.space || job.space.Fits;
  $("start").disabled = !fits;
  $("plan-blocked").textContent = fits ? "" : "Free up some room and look for the disc again.";
}

// The clock ticks once a second whether or not anything else changes.
//
// Copying a disc reports its first percentage a couple of minutes in, and the
// log goes quiet while it works. A number that moves is the only thing that
// distinguishes working from hung, so the page keeps its own time rather than
// waiting to be told.
function startClock() {
  if (clockTimer) return;
  clockTimer = setInterval(tickClock, 1000);
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
  for (const job of activeJobs) {
    updateJobCard(job);
  }
}

// elapsedSince renders how long ago something started, counting in seconds
// early on so the number visibly moves.
function elapsedSince(when) {
  if (!when) return "";

  const started = new Date(when);
  if (Number.isNaN(started.getTime()) || started.getTime() === 0) return "";

  const seconds = Math.max(0, Math.floor((Date.now() - started.getTime()) / 1000));
  if (seconds < 60) return `${seconds}s`;

  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;

  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

// renderActive draws one card per disc being worked on.
//
// More than one is ordinary: a film being converted has finished with the
// drive, so the next disc can be going in while it runs.
// kindOf groups a job for the queue filter.
//
// Test clips are their own kind. A disc is reading while it needs the drive
// and converting once it does not, which is the distinction somebody filtering
// the queue actually cares about.
function kindOf(job) {
  if (job.kind === "lab") return "lab";

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

function renderActive(jobs) {
  activeJobs = (jobs || []).filter((job) =>
    queueFilter === "all" || kindOf(job) === queueFilter);

  const all = jobs || [];
  show("queue", all.length > 0);
  $("queue-summary").textContent = queueSummary(all);

  const box = $("working-list");
  const wanted = new Set(activeJobs.map((j) => j.id));

  // Remove cards for jobs that have finished.
  for (const card of [...box.children]) {
    if (!wanted.has(card.dataset.job)) card.remove();
  }

  for (const job of activeJobs) {
    let card = box.querySelector(`[data-job="${CSS.escape(job.id)}"]`);
    if (!card) {
      card = jobCard(job);
      box.append(card);
    }
    updateJobCard(job);
  }

  if (activeJobs.length > 0) startClock();
  else stopClock();
}

// queueSummary says what the queue amounts to in one line.
function queueSummary(jobs) {
  const reading = jobs.filter((j) => kindOf(j) === "rip").length;
  const converting = jobs.filter((j) => j.stage === "PACKAGE").length;
  const clips = jobs.filter((j) => j.stage === "LAB").length;
  const waiting = jobs.filter((j) => j.stage === "QUEUED").length;

  const parts = [];
  if (reading) parts.push(`${reading} disc${reading === 1 ? "" : "s"} being read`);
  if (converting) parts.push(`${converting} converting`);
  if (clips) parts.push(`${clips} making test clips`);
  if (waiting) parts.push(`${waiting} waiting their turn`);

  return parts.length ? parts.join(" · ") : "";
}

function jobCard(job) {
  const card = document.createElement("section");
  card.className = "card job-card";
  card.dataset.job = job.id;

  const title = document.createElement("h2");
  title.className = "job-title";
  card.append(title);

  // Clicking the card narrows the log to this disc, and clicking it again
  // widens it back out.
  card.addEventListener("click", (e) => {
    if (e.target.tagName === "BUTTON") return;
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

  const line = document.createElement("div");
  line.className = "progress-line";
  const detail = document.createElement("span");
  detail.className = "muted job-detail";
  const estimate = document.createElement("span");
  estimate.className = "muted estimate-right job-estimate";
  line.append(detail, estimate);
  card.append(line);

  const actions = document.createElement("div");
  actions.className = "actions";
  const stop = document.createElement("button");
  stop.textContent = "Stop";
  stop.addEventListener("click", (e) =>
    busy(e.target, "Stopping\u2026", null, () => post("/api/stop", { id: job.id })));
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

  card.querySelector(".job-title").textContent = job.title || job.disc_name || "A disc";
  card.querySelector(".stage").textContent = stageWords(job.stage);

  const progress = job.progress || {};
  const pct = Math.round(progress.percent || 0);
  card.querySelector(".bar-fill").style.width = `${pct}%`;

  const now = [];
  const elapsed = elapsedSince(progress.since);
  if (elapsed) now.push(elapsed);
  if (pct > 0) now.push(`${pct}%`);
  // A speed is the only figure here that can be compared to anything.
  if (progress.rate) now.push(progress.rate);
  if (progress.operation) now.push(progress.operation);
  card.querySelector(".job-detail").textContent = now.join(" · ");

  card.querySelector(".job-estimate").textContent = estimateText(progress, pct);
}

// estimateText says how much longer, as honestly as it can.
function estimateText(progress, pct) {
  if (progress.remaining && progress.remaining !== "unknown") {
    return `about ${progress.remaining} left`;
  }

  if (pct > 0 && pct < 100 && progress.since) {
    const elapsedMs = Date.now() - new Date(progress.since).getTime();
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

function renderJob(job) {
  if (!job) {
    show("plan", false);
    show("done", false);
    return;
  }

  const waiting = job.state === "waiting";
  show("plan", waiting);
  show("done", job.state === "done" || job.state === "stopped");

  if (waiting) renderPlan(job);

  if (job.state === "done" || job.state === "stopped") {
    $("done-title").textContent = job.state === "done" ? "Ready" : "Stopped";
    $("done-detail").textContent = job.note || "";
  }
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

function renderRecent(jobs) {
  const box = $("recent");
  if (!jobs || jobs.length === 0) {
    box.textContent = "Nothing yet.";
    return;
  }

  box.replaceChildren(...jobs.map((job) => {
    const row = document.createElement("div");
    row.className = "row";

    const name = document.createElement("span");
    name.textContent = job.title || job.disc_name || job.disc_label || "A disc";

    const right = document.createElement("span");
    right.className = "muted small";
    right.textContent = `${jobOutcome(job)} · ${whenText(job.started)}`;

    row.append(name, right);
    return row;
  }));
}

async function refresh() {
  const state = await fetch("/api/state").then((r) => r.json());
  driveBusy = state.drive_busy || "";
  renderDrives(state.drives);
  renderActive(state.active || []);
  renderJob(state.job);
  renderRecent(state.recent || []);

  // The idle card is only for when nothing is asking for a decision.
  show("idle", !state.job);

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
async function loadDriveHealth() {
  const box = $("drive-health");
  box.textContent = "Checking how ARFABIT is reaching the drive\u2026";

  let report;
  try {
    report = await fetch("/api/drive-health").then((r) => r.json());
  } catch (err) {
    box.textContent = String(err);
    return;
  }

  if (report.message) {
    box.textContent = report.message;
    return;
  }

  const drives = report.drives || [];
  if (drives.length === 0) {
    box.textContent = "No disc drive found.";
    return;
  }

  // The system holding the disc is the usual reason a drive reads slowly, and
  // unlike the access mode it is something a person can fix in one click.
  $("drive-free").hidden = !drives.some((d) => d.mounted);

  box.replaceChildren(...drives.map((drive) => {
    const block = document.createElement("div");

    const name = document.createElement("div");
    name.textContent = drive.name;
    block.append(name);

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

    if (drive.observed > 0) {
      const observed = document.createElement("div");
      observed.className = "small";
      const hours = (40700 / drive.observed / 3600).toFixed(1);
      observed.textContent =
        `Measured at ${drive.observed.toFixed(1)} MB per second over ${drive.samples} disc${drive.samples === 1 ? "" : "s"}` +
        ` — a 40 GB film would take about ${hours} hours at that rate.`;
      block.append(observed);
    } else {
      const observed = document.createElement("div");
      observed.className = "small";
      observed.textContent = "No disc has been copied yet, so there is nothing measured to compare.";
      block.append(observed);
    }

    return block;
  }));
}

// --- the lab --------------------------------------------------------------

async function loadMasters() {
  const { masters } = await fetch("/api/masters").then((r) => r.json());
  const select = $("lab-master");

  if (!masters || masters.length === 0) {
    select.replaceChildren(new Option("No masters yet — read a disc first", ""));
    $("lab-run").disabled = true;
    return;
  }

  select.replaceChildren(...masters.map((m) => {
    const option = new Option(`${m.title} (${bytes(m.size)})`, m.path);
    option.dataset.film = m.title;
    return option;
  }));
  $("lab-run").disabled = false;
}

// Remembering the Transcode settings between visits.
//
// Kept in the browser rather than saved as defaults: where somebody last took
// a clip from is a convenience, not a decision about how ARFABIT should work.
const REMEMBERED = "arfabit.transcode";

function rememberTranscode() {
  try {
    localStorage.setItem(REMEMBERED, JSON.stringify({
      at: $("lab-at").value,
      length: $("lab-length").value,
    }));
  } catch {
    // Private windows and cleared storage are ordinary; there is simply
    // nothing to remember with.
  }
}

function recallTranscode() {
  try {
    const saved = JSON.parse(localStorage.getItem(REMEMBERED) || "null");
    if (!saved) return;

    if (saved.at) $("lab-at").value = saved.at;
    if (saved.length !== undefined) $("lab-length").value = String(saved.length);
  } catch {
    // Anything unreadable simply leaves the defaults in place.
  }
}

// loadMasterTracks shows what is inside the chosen master.
//
// A master holds everything the disc had, which is the point of keeping it. A
// file for a television usually wants a few of those and not the rest.
async function loadMasterTracks() {
  const master = $("lab-master").value;
  masterTracks = [];

  if (!master) {
    $("tracks-heading").hidden = true;
    $("track-list").replaceChildren();
    return;
  }

  let reply;
  try {
    reply = await fetch(`/api/master-tracks?master=${encodeURIComponent(master)}`)
      .then((r) => r.json());
  } catch {
    $("tracks-heading").hidden = true;
    return;
  }

  masterTracks = reply.tracks || [];
  $("tracks-heading").hidden = masterTracks.length === 0;

  $("track-list").replaceChildren(...masterTracks.map((track, i) => {
    const row = document.createElement("label");
    row.className = "track";

    const box = document.createElement("input");
    box.type = "checkbox";
    box.className = "track-choice";
    box.value = String(track.index);
    box.dataset.kind = track.kind;
    box.checked = track.carriable && track.selected;
    box.disabled = !track.carriable;

    const text = document.createElement("span");
    text.textContent = track.note ? `${track.label} — ${track.note}` : track.label;
    if (!track.carriable) text.className = "muted";

    row.append(box, text);
    return row;
  }));
}

// chosenAudio is which sound tracks to keep.
function chosenAudio() {
  return [...document.querySelectorAll(".track-choice")]
    .filter((box) => box.checked && box.dataset.kind === "audio")
    .map((box) => Number(box.value));
}

// loadProfiles lists the named settings to choose between.
//
// Several at once is the point: comparing means having both to watch, and a
// whole film under two profiles is two editions to pick between in Plex.
async function loadProfiles() {
  const { profiles } = await fetch("/api/profiles").then((r) => r.json());
  const box = $("profile-list");

  if (!profiles || profiles.length === 0) {
    box.textContent = "No profiles are defined.";
    return;
  }

  box.replaceChildren(...profiles.map((profile, i) => {
    const row = document.createElement("label");
    row.className = "track";

    const box = document.createElement("input");
    box.type = "checkbox";
    box.value = profile.name;
    box.className = "profile-choice";
    // The default is ticked, so pressing Start without thinking does the
    // ordinary thing.
    box.checked = profile.default;
    box.addEventListener("change", describeDestination);

    const text = document.createElement("span");
    text.textContent = `${profile.name} — ${profile.description}`;

    row.append(box, text);
    return row;
  }));

  renderProfileManager(profiles);
  describeDestination();
}

// profileForm builds the form for filling in a profile.
//
// The same form serves a saved profile and a one-off, because they are the
// same thing: one is remembered and one is not.
function profileForm(values, options) {
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
  field("Stereo sound", bitrate);

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
  tick("Keep Dolby sound exactly as it is", "copy_native_audio", values.copy_native_audio !== false);

  const note = document.createElement("p");
  note.className = "muted small";
  note.textContent = "Lower numbers mean better pictures and bigger files. 20 is close to indistinguishable from the disc.";
  box.append(note);

  return box;
}

// readProfileForm gathers what was filled in.
function readProfileForm(box) {
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

  return values;
}

// chosenProfiles is what is ticked, in the order shown.
function chosenProfiles() {
  return [...document.querySelectorAll(".profile-choice")]
    .filter((box) => box.checked)
    .map((box) => box.value);
}

// customProfile is the one-off form's contents, or nothing if it is not in use.
function customProfile() {
  if (!$("use-custom").checked) return null;

  const values = readProfileForm($("custom-form"));
  if (!values.name) values.name = "Custom";
  return values;
}

// renderProfileManager lists the profiles with a way to change them.
function renderProfileManager(profiles) {
  const box = $("profile-manager");

  box.replaceChildren(...profiles.map((profile) => {
    const row = document.createElement("div");
    row.className = "row";

    const left = document.createElement("div");
    const name = document.createElement("div");
    name.textContent = profile.default ? `${profile.name} (used by default)` : profile.name;

    const detail = document.createElement("div");
    detail.className = "muted small";
    detail.textContent = profile.description;

    left.append(name, detail);



    const buttons = document.createElement("div");
    buttons.className = "button-row";

    if (!profile.default) {
      const makeDefault = document.createElement("button");
      makeDefault.textContent = "Use by default";
      makeDefault.addEventListener("click", async (e) => {
        const result = await busy(e.target, "Setting\u2026", null, () =>
          post("/api/profiles/default", { name: profile.name }));
        if (result) loadProfiles();
      });
      buttons.append(makeDefault);
    }

    const edit = document.createElement("button");
    edit.textContent = "Change";
    edit.addEventListener("click", () => openProfileEditor(profile));
    buttons.append(edit);

    const copy = document.createElement("button");
    copy.textContent = "Copy";
    copy.addEventListener("click", () =>
      openProfileEditor({ ...profile, name: `${profile.name} copy` }));
    buttons.append(copy);

    // The default is always needed, so it is never offered for removal.
    if (!profile.default) {
      const remove = document.createElement("button");
      remove.textContent = "Remove";
      remove.addEventListener("click", async (e) => {
        const result = await busy(e.target, "Removing\u2026", null, () =>
          fetch(`/api/profiles/${encodeURIComponent(profile.name)}`, { method: "DELETE" })
            .then((res) => (res.ok ? res.json() : null)));
        if (result) loadProfiles();
      });
      buttons.append(remove);
    }

    row.append(left, buttons);
    return row;
  }));
}

// openProfileEditor shows the form for making or changing a profile.
function openProfileEditor(values) {
  const editor = $("profile-editor");
  editor.hidden = false;

  const form = profileForm(values || {}, { named: true });

  const actions = document.createElement("div");
  actions.className = "actions";

  const save = document.createElement("button");
  save.className = "primary";
  save.textContent = "Save";
  save.addEventListener("click", async (e) => {
    const result = await busy(e.target, "Saving\u2026", "Saved", () =>
      post("/api/profiles", readProfileForm(form)));
    if (result) {
      editor.hidden = true;
      loadProfiles();
    }
  });

  const cancel = document.createElement("button");
  cancel.textContent = "Cancel";
  cancel.addEventListener("click", () => { editor.hidden = true; });

  actions.append(save, cancel);
  editor.replaceChildren(form, actions);
}

// describeDestination says what pressing Start will produce and where it goes.
//
// A whole film is a film and belongs in the library; a stretch of one is a
// clip to watch and compare. Saying which avoids the surprise.
function describeDestination() {
  const chosen = chosenProfiles();
  const custom = customProfile() ? 1 : 0;
  const whole = Number($("lab-length").value) === 0;

  if (chosen.length + custom === 0) {
    $("lab-destination").textContent = "Tick at least one profile.";
    $("lab-run").disabled = true;
    return;
  }

  const count = chosen.length + custom;
  $("lab-run").disabled = false;
  $("lab-destination").textContent = whole
    ? `${count} full-length file${count === 1 ? "" : "s"}, saved to your library as separate editions. This takes hours.`
    : `${count} clip${count === 1 ? "" : "s"}, saved to the lab folder to watch and compare.`;
}

// parseTimestamp reads "1:15:20", "15:20" or plain seconds.
function parseTimestamp(text) {
  const parts = String(text).trim().split(":").map(Number);
  if (parts.some(Number.isNaN)) return 0;

  return parts.reduce((total, part) => total * 60 + part, 0);
}

// loadLabClips shows what is already in the lab folder.
//
// The folder is the record: clips outlive the program, and somebody coming
// back tomorrow should find yesterday's work rather than an empty table.
async function loadLabClips() {
  const { folder, clips } = await fetch("/api/lab").then((r) => r.json());

  $("lab-folder").textContent = clips && clips.length
    ? `The clips are in ${folder}, a folder per master. Point Plex at it and each setting appears as an edition of the same title, so they play one after another.`
    : `Clips will be saved in ${folder}, a folder per master.`;

  $("lab-clips-heading").hidden = !clips || clips.length === 0;

  if (!clips || clips.length === 0) {
    $("lab-clips").replaceChildren();
    return;
  }

  $("lab-clips").replaceChildren(...clips.map((clip) => {
    const row = document.createElement("div");
    row.className = "row";

    const name = document.createElement("span");
    name.textContent = clip.film ? `${clip.film} — ${clip.name}` : clip.name;

    const right = document.createElement("span");
    right.className = "muted small";
    right.textContent = `${bytes(clip.size)} · ${whenText(clip.made)}`;

    row.append(name, right);
    return row;
  }));
}

function renderLabResults(comparison) {
  const rows = (comparison && comparison.clips) || [];
  if (rows.length === 0) {
    $("lab-results").replaceChildren();
    return;
  }

  const table = document.createElement("table");
  table.className = "results";

  const head = document.createElement("tr");
  for (const heading of ["Setting", "Whole film", "Encode time", "Size", "Speed"]) {
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
      "Change profile.max_conversions in your settings file to allow more."
    : `Up to ${limit} at once. Change profile.max_conversions in your settings file to alter this.`;
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

// --- wiring ---------------------------------------------------------------

function connect() {
  events = new EventSource("/events");

  // Any job change may add or remove a card, so the whole picture is
  // refreshed rather than patched.
  events.addEventListener("job", () => refresh());
  events.addEventListener("log", (e) => appendLog([JSON.parse(e.data)]));
  events.addEventListener("index", (e) => renderIndexStatus(JSON.parse(e.data)));

  events.addEventListener("drives", (e) => {
    const list = JSON.parse(e.data);
    // Only redraw the idle card when nothing is in progress.
    drives = list || [];
    if (!$("idle").hidden) renderDrives(drives);
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
  // The scan outlives the request that starts it, so the button stays put
  // until a job appears and the idle card gives way to the working one.
  on("scan", "click", async (e) => {
    e.target.disabled = true;
    e.target.textContent = "Waking the drive…";

    const result = await post("/api/scan");
    if (result) {
      e.target.textContent = "Reading the disc…";
    } else {
      e.target.disabled = false;
      e.target.textContent = "Look for a disc";
    }
  });

  on("start", "click", (e) =>
    busy(e.target, "Starting…", null, () => post("/api/start")));


  on("eject", "click", async (e) => {
    const result = await busy(e.target, "Ejecting…", null, () => post("/api/eject"));
    if (result) $("eject-detail").textContent = result.message;
  });

  on("title-save", "click", saveTitle);
  on("plan-convert", "change", sendPlanChange);

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
    const result = await busy(e.target, "Restarting…", null, () => post("/api/restart"));
    if (result) waitForRestart();
  });

  on("quit", "click", async (e) => {
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
    box.textContent = "Asking your computer to let go of the disc\u2026";

    const result = await busy(e.target, "Letting go\u2026", null, () => post("/api/drive-free"));

    // Either way the drive is re-read, so the section says what is true now
    // rather than leaving the old answer on screen.
    await loadDriveHealth();

    if (result) {
      $("drive-health").prepend(note("Your computer has let go of the disc. It is still in the drive."));
    }
  });



  on("queue-filter", "change", (e) => {
    queueFilter = e.target.value;
    refresh();
  });

  on("lab-length", "change", () => {
    rememberTranscode();
    describeDestination();
  });

  on("lab-at", "input", rememberTranscode);
  on("lab-master", "change", loadMasterTracks);

  on("use-custom", "change", (e) => {
    const box = $("custom-form");
    box.hidden = !e.target.checked;

    if (e.target.checked && box.children.length === 0) {
      // Named, so it can be told apart in a folder listing weeks later.
      box.replaceChildren(profileForm({ name: "Custom" }, { named: true }));
    }
    describeDestination();
  });

  on("profile-new", "click", () => openProfileEditor({ name: "" }));

  on("lab-run", "click", async (e) => {
    const chosen = $("lab-master").selectedOptions[0];

    const result = await busy(e.target, "Adding\u2026", "Added to the queue", () =>
      post("/api/transcode", {
        master: $("lab-master").value,
        film: chosen ? chosen.dataset.film : "",
        at: parseTimestamp($("lab-at").value),
        length: Number($("lab-length").value),
        profiles: chosenProfiles(),
        audio: chosenAudio(),
        custom: customProfile(),
      }));

    if (result) {
      $("lab-status").textContent =
        "Added to the queue. Its progress and log are there with everything else.";
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
  wireButtons();
  connect();
  refresh();
  runDoctor();
  loadAutostart();
  loadIndexStatus();
  loadConversionLimit();
  recallTranscode();
  loadMasters().then(loadMasterTracks);
  loadProfiles();
  loadLabClips();
  loadDriveHealth();
}

start();
