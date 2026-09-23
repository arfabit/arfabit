// ARFABIT's page.
//
// Everything arrives over a server-sent event stream, so the page never
// reloads. That is deliberate: a page that refreshes underneath you throws
// away your scroll position, your text selection and whatever you were
// searching for.

const $ = (id) => document.getElementById(id);

const seenLogIds = new Set();
let followLog = true;

// --- rendering ------------------------------------------------------------

function show(id, visible) {
  $(id).hidden = !visible;
}

function bytes(n) {
  if (!n) return "0 bytes";
  const units = ["bytes", "kB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1000 && i < units.length - 1) { n /= 1000; i++; }
  return `${i === 0 ? n : n.toFixed(1)} ${units[i]}`;
}

function renderPlan(job) {
  const plan = job.plan;
  if (!plan) return;

  $("plan-title").textContent = job.title || job.disc_name || "Found a movie";
  $("plan-summary").textContent = `${plan.duration} · ${bytes(plan.source_size)} on the disc`;

  const notices = [];
  if (plan.obfuscated) notices.push(plan.reason);
  if (job.space && !job.space.Fits) notices.push(spaceMessage(job.space));
  else if (job.space && job.space.Tight) notices.push(spaceMessage(job.space));

  $("plan-notice").hidden = notices.length === 0;
  $("plan-notice").textContent = notices.join("\n\n");

  $("plan-video").textContent = plan.video_copy
    ? `Kept exactly as it is on the disc — ${plan.resolution}.`
    : `${plan.source_codec} · ${plan.resolution} · converted to HEVC, quality ${plan.crf}, ${plan.preset}.`;

  $("plan-audio").replaceChildren(...(plan.audio || []).map((track, i) =>
    trackRow(`audio-${i}`, track.source_index, track.selected,
      track.label || track.codec, track.copy ? "kept as-is" : `converted to ${track.codec}`)));

  const subs = plan.subtitles || [];
  if (subs.length === 0) {
    $("plan-subs").textContent = "This disc has no subtitles.";
  } else {
    $("plan-subs").replaceChildren(...subs.map((track, i) =>
      trackRow(`sub-${i}`, track.source_index, track.selected, track.label, track.forced ? "forced" : "")));
  }

  const est = plan.estimated_time ? Math.round(plan.estimated_time / 60000000000) : 0;
  $("plan-estimate").textContent = est
    ? `About ${est} minutes, finishing around ${bytes(plan.estimated_size)}.`
    : "";

  const fits = !job.space || job.space.Fits;
  $("start").disabled = !fits;
  $("plan-blocked").textContent = fits ? "" : "Free up some room and look for the disc again.";
}

function spaceMessage(space) {
  const lead = space.Fits
    ? "There is just enough room, and the estimate could be low."
    : "There is not enough room for this disc.";
  return `${lead}\n\nThis rip needs about ${bytes(space.Needed)}.\n` +
    `The drive has ${bytes(space.Free)} free.\n` +
    `Your masters folder holds ${bytes(space.Masters)}.\n` +
    `Your library folder holds ${bytes(space.Library)}.`;
}

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

function renderJob(job) {
  if (!job) {
    show("idle", true);
    show("plan", false); show("working", false); show("done", false);
    return;
  }

  const waiting = job.state === "waiting";
  const running = job.state === "running";
  const done = job.state === "done";
  const stopped = job.state === "stopped";

  show("idle", false);
  show("plan", waiting);
  show("working", running && job.stage !== "SCAN" && job.stage !== "PLAN");
  show("done", done || stopped);

  if (waiting) renderPlan(job);

  if (running) {
    const pct = Math.round((job.progress && job.progress.percent) || 0);
    $("working-title").textContent = stageWords(job.stage);
    $("bar-fill").style.width = `${pct}%`;
    const remaining = job.progress && job.progress.remaining;
    $("working-detail").textContent = remaining && remaining !== "unknown"
      ? `${pct}% · about ${remaining} left`
      : `${pct}%`;
  }

  if (done || stopped) {
    $("done-title").textContent = done ? "Ready" : "Stopped";
    $("done-detail").textContent = job.note || "";
  }
}

function stageWords(stage) {
  return {
    SCAN: "Reading the disc",
    PLAN: "Working out what to do",
    RIP: "Copying the disc",
    OCR: "Reading the subtitles",
    PACKAGE: "Making the movie file",
    DELIVER: "Putting it in your library",
    EJECT: "Ejecting the disc",
  }[stage] || "Working";
}

// --- the log --------------------------------------------------------------

function appendLog(entries) {
  const box = $("log");
  const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40;

  for (const entry of entries) {
    if (seenLogIds.has(entry.id)) continue;
    seenLogIds.add(entry.id);
    box.append(logLine(entry));
  }

  applyFilter();
  if (followLog && atBottom) box.scrollTop = box.scrollHeight;
}

function logLine(entry) {
  const line = document.createElement("div");
  line.className = "line";
  line.dataset.text = `${entry.stage} ${entry.text}`.toLowerCase();

  const when = document.createElement("span");
  when.className = "when";
  when.textContent = new Date(entry.time).toLocaleTimeString();

  const stage = document.createElement("span");
  stage.className = "stage";
  stage.textContent = entry.stage;

  const text = document.createElement("span");
  text.className = "text";
  text.textContent = entry.text;

  line.append(when, stage, text);

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
    line.style.display = !term || line.dataset.text.includes(term) ? "" : "none";
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
    alertQuietly(problem);
    return null;
  }
  return res.json();
}

function alertQuietly(problem) {
  const notice = $("plan-notice");
  notice.hidden = false;
  notice.textContent = problem.detail
    ? `${problem.message}\n\n${problem.detail}`
    : problem.message;
}

function sendPlanChange() {
  const change = { audio: {}, subtitles: {} };
  for (const box of document.querySelectorAll("#plan input[type=checkbox]")) {
    change[box.dataset.kind][Number(box.dataset.source)] = box.checked;
  }
  post("/api/plan", change);
}

async function refresh() {
  const res = await fetch("/api/state");
  const state = await res.json();
  renderJob(state.job);
  renderRecent(state.recent || []);

  const log = await fetch("/api/log").then((r) => r.json());
  appendLog(log);
}

function renderRecent(jobs) {
  $("recent").replaceChildren(...jobs.map((job) => {
    const row = document.createElement("div");
    row.className = "row";

    const name = document.createElement("span");
    name.textContent = job.title || job.disc_name || job.disc_label || "A disc";

    const when = document.createElement("span");
    when.className = "muted small";
    when.textContent = job.state === "done"
      ? new Date(job.started).toLocaleDateString()
      : job.note || job.state;

    row.append(name, when);
    return row;
  }));

  if (jobs.length === 0) $("recent").textContent = "Nothing yet.";
}

async function runDoctor() {
  const report = await fetch("/api/doctor").then((r) => r.json());
  const problems = (report.checks || []).filter((c) => c.status !== "ok");

  show("doctor", problems.length > 0);
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

    row.append(dot, body);
    return row;
  }));
}

// --- wiring ---------------------------------------------------------------

$("scan").addEventListener("click", () => post("/api/scan"));
$("start").addEventListener("click", () => post("/api/start"));
$("stop").addEventListener("click", () => post("/api/stop"));
$("log-filter").addEventListener("input", applyFilter);

$("log-follow").addEventListener("change", (e) => {
  followLog = e.target.checked;
  $("log-jump").hidden = followLog;
});

// Scrolling up steps out of follow mode, because you are reading something.
$("log").addEventListener("scroll", () => {
  const box = $("log");
  const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
  if (!atBottom && followLog) {
    followLog = false;
    $("log-follow").checked = false;
    $("log-jump").hidden = false;
  }
});

$("log-jump").addEventListener("click", () => {
  followLog = true;
  $("log-follow").checked = true;
  $("log-jump").hidden = true;
  $("log").scrollTop = $("log").scrollHeight;
});

const events = new EventSource("/events");
events.addEventListener("job", (e) => {
  const job = JSON.parse(e.data);
  renderJob(job);
  fetch("/api/log").then((r) => r.json()).then(appendLog);
});

refresh();
runDoctor();
