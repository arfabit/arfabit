// Runs the page's script against a stub browser and reports anything it
// throws.
//
// The page died at load three times — a missing element, a temporal dead zone,
// and two deleted functions — and each time the only sign was a banner after
// the fact. This catches all three kinds before anybody sees them.

const fs = require("fs");

const problems = [];

// A DOM stub: enough for the script to run, not enough to render anything.
function element(id) {
  const el = {
    id,
    hidden: false,
    disabled: false,
    textContent: "",
    value: "",
    checked: false,
    className: "",
    style: {},
    dataset: {},
    children: [],
    selectedOptions: [],
    classList: { add() {}, remove() {}, toggle() {}, contains: () => false },
    tagName: "DIV",
    addEventListener() {},
    append(...kids) { this.children.push(...kids); },
    prepend() {},
    replaceChildren(...kids) { this.children = kids; },
    remove() {},
    querySelector: () => element("stub"),
    querySelectorAll: () => [],
    scrollTop: 0,
    scrollHeight: 0,
    clientHeight: 0,
  };
  return el;
}

const byId = new Map();

// Every id the page actually defines, read from the template.
for (const match of fs.readFileSync(process.argv[3], "utf8").matchAll(/id="([a-z0-9-]+)"/g)) {
  byId.set(match[1], element(match[1]));
}

global.document = {
  getElementById: (id) => byId.get(id) || null,
  createElement: (tag) => element(tag),
  querySelectorAll: () => [],
  body: element("body"),
};

global.window = {
  addEventListener() {},
  location: { reload() {} },
};

global.CSS = { escape: (s) => s };
global.Option = function (text, value) { return element("option"); };

global.EventSource = function () {
  return { addEventListener() {}, readyState: 0 };
};
global.EventSource.CLOSED = 2;

// Everything the page fetches, answered with something shaped right.
//
// The answers are deliberately full rather than empty: a stub that returns
// nothing leaves most of the page unexercised, which is how a missing function
// slipped through the first version of this test.
const plan = {
  profile: "Archive",
  duration: "2h 12m",
  source_size: 40700000000,
  resolution: "1920x1080",
  source_codec: "Mpeg4 AVC High@L4.1",
  crf: 20,
  preset: "slow",
  estimated_size: 2500000000,
  estimated_time: 2400000000000,
  obfuscated: true,
  reason: "This disc lists 3 titles of exactly the same length.",
  audio: [
    { source_index: 1, lang: "eng", channels: 8, label: "English · 7.1", selected: false },
    { source_index: 3, lang: "eng", channels: 2, label: "English · Stereo", selected: true },
    { source_index: 4, lang: "fra", channels: 6, label: "French · 5.1", selected: false },
  ],
  subtitles: [
    { source_index: 7, lang: "eng", forced: false, label: "PGS English", selected: true },
    { source_index: 8, lang: "eng", forced: true, label: "PGS English forced", selected: true },
  ],
};

const answers = {
  "/api/state": {
    node_name: "test",
    drive_busy: "",
    job: {
      id: "waiting-job",
      state: "waiting",
      stage: "PLAN",
      title: "Crime 101",
      year: 2025,
      disc_name: "CRIME_101",
      plan,
      space: { Needed: 45000000000, Free: 400000000000, Masters: 40000000000, Library: 11000000000, Fits: true },
      matches: [{ title: { Name: "Crime 101", Year: 2025 }, Why: "the name is close and the length matches" }],
    },
    active: [{
      id: "running-job",
      state: "running",
      stage: "RIP",
      title: "In the Grey",
      progress: { percent: 12, operation: "Saving to MKV file", since: new Date().toISOString(), expected: "40 minutes" },
    }],
    recent: [
      { id: "a", state: "done", stage: "DELIVER", title: "Blade Runner", started: new Date().toISOString() },
      { id: "b", state: "stopped", stage: "RIP", title: "Alien", note: "Not started.", started: "2026-09-20T10:00:00Z" },
      { id: "c", state: "waiting", stage: "PLAN", disc_label: "SOME_DISC", started: "2026-09-23T10:00:00Z" },
    ],
    drives: [{ Index: 0, Name: "BD-RE BU40N", Device: "/dev/rdisk8", Label: "CRIME_101", State: "loaded", Loaded: true }],
  },
  "/api/log": [
    { id: 1, time: new Date().toISOString(), stage: "SCAN", text: "Reading the disc." },
    { id: 2, time: new Date().toISOString(), stage: "RIP", text: "Copying.", detail: "raw output" },
  ],
  "/api/doctor": {
    checks: [
      { name: "MakeMKV", status: "ok", message: "MakeMKV is ready." },
      { name: "Folders", status: "warn", message: "Something to look at.", fix: "Try this.", command: "brew install x", detail: "raw" },
    ],
  },
  "/api/autostart": { enabled: true, mechanism: "launchd user agent", path: "/tmp/x.plist" },
  "/api/index": { state: "ready", count: 272565, path: "/tmp/titles.json", built: new Date().toISOString() },
  "/api/masters": { masters: [{ title: "Crime 101 (2025)", path: "/masters/Crime 101 (2025)/x.mkv", size: 40700000000 }] },
  "/api/lab": {
    folder: "/tmp/lab",
    clips: [{ film: "Crime 101 (2025)", name: "Lab 001 - crf20-slow - 0h10m00s", path: "/tmp/lab/x.mp4", size: 60000000, made: new Date().toISOString() }],
  },
  "/api/drive-health": {
    drives: [{
      name: "BD-RE BU40N", access: "os", fast: false, mounted: true,
      explanation: "Reached through macOS.", mount_note: "Your computer has this disc open.",
      observed_mb_per_second: 2.6, samples: 1,
    }],
  },
};

global.fetch = async (path) => ({
  ok: true,
  json: async () => answers[path] ?? {},
});

global.setInterval = () => 0;
global.clearInterval = () => {};
global.setTimeout = () => 0;
global.console = { ...console, warn() {} };

process.on("uncaughtException", (err) => problems.push(String(err)));
process.on("unhandledRejection", (err) => problems.push(String(err)));

try {
  new Function(fs.readFileSync(process.argv[2], "utf8"))();
} catch (err) {
  problems.push(String(err));
}

// Give the promises a turn before reporting.
setImmediate(() => {
  if (problems.length > 0) {
    console.log(problems.join("\n"));
    process.exit(1);
  }
  process.exit(0);
});
