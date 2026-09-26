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
    setAttribute() {},
    removeAttribute() {},
    closest: () => null,
    get options() { return this.children; },
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

// An original is chosen, so the project editor is drawn rather than skipped.
byId.get("project-original").value = "/library/Crime 101 (2025)/Crime 101 (2025) {edition-Original}.mkv";

global.document = {
  getElementById: (id) => byId.get(id) || null,
  createElement: (tag) => element(tag),
  createElementNS: (ns, tag) => element(tag),
  createTextNode: (text) => ({ textContent: text }),
  querySelectorAll: () => [],
  body: element("body"),
};

global.window = {
  addEventListener() {},
  scrollTo() {},
  location: { reload() {} },
};
global.location = { hash: "#tasks", reload() {} };

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
  blueprint: "Archive",
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
    { source_index: 1, lang: "eng", channels: 8, label: "English · 7.1", source: "English · 7.1 · DTS-HD Master Audio · lossless", lossless: true, selected: false },
    { source_index: 3, lang: "eng", channels: 2, label: "English · Stereo", selected: true },
    { source_index: 4, lang: "fra", channels: 6, label: "French · 5.1", selected: false },
  ],
  convert: true,
  read: [7],
  tracks: [
    { index: 0, kind: "video", codec: "h264", height: 1080, label: "H.264 · 1080p" },
    { index: 1, kind: "audio", codec: "dts", lang: "eng", channels: 8, lossless: true, label: "English · 7.1 · DTS-HD Master Audio · lossless" },
    { index: 3, kind: "audio", codec: "ac3", lang: "eng", channels: 2, label: "English · Stereo · Dolby Digital · lossy" },
    { index: 7, kind: "subtitle", codec: "hdmv_pgs_subtitle", lang: "eng", label: "English · pictures", note: "Plex has to convert the whole picture to show these on Apple TV." },
  ],
  project: {
    containers: ["mkv"], edition: "Archive", blueprint: "Archive",
    items: [
      { kind: "video", action: "convert", source: 0, codec: "h264", to: "hevc", crf: 20, preset: "slow", label: "H.264 · 1080p" },
      { kind: "audio", action: "copy", source: 1, codec: "dts", lang: "eng", channels: 8, lossless: true, label: "English · 7.1 · DTS-HD Master Audio · lossless" },
      { kind: "subtitle", action: "copy", source: 7, codec: "hdmv_pgs_subtitle", lang: "eng", label: "English · pictures" },
    ],
  },
  sound: [
    { language: "English", choice: "pick one of 7.1 or 5.1, lossless", matched: ["7.1 · DTS-HD Master Audio · lossless"] },
    { language: "English", choice: "pick one of stereo" },
  ],
  subtitles: [
    { source_index: 7, lang: "eng", forced: false, label: "PGS English", selected: true },
    { source_index: 8, lang: "eng", forced: true, label: "PGS English forced", selected: true },
  ],
};

const answers = {
  "/api/state": {
    node_name: "test",
    ocr: true,
    now: new Date().toISOString(),
    line: ["queued-job", "queued-disc"],
    drive_busy: "",
    job: {
      id: "waiting-job",
      state: "waiting",
      stage: "PLAN",
      title: "Crime 101",
      year: 2025,
      disc_name: "CRIME_101",
      plan,
      space: { Needed: 45000000000, Free: 400000000000, Originals: 40000000000, Library: 11000000000, Fits: true },
      matches: [{ title: { Name: "Crime 101", Year: 2025 }, Why: "the name is close and the length matches" }],
    },
    active: [{
      id: "running-job",
      state: "running",
      stage: "RIP",
      title: "In the Grey",
      file: "In the Grey_t00.mkv",
      progress: { percent: 12, operation: "Saving to MKV file", since: new Date().toISOString(), expected: "40 minutes", rate: "6.4 MB/s" },
    }, {
      id: "queued-job",
      kind: "lab",
      state: "running",
      stage: "QUEUED",
      title: "Crime 101",
      year: 2025,
      progress: { operation: "Getting things ready", since: new Date().toISOString() },
    }, {
      id: "queued-disc",
      from: "running-job",
      kind: "convert",
      state: "running",
      stage: "QUEUED",
      title: "Alien",
      file: "Alien {edition-Archive}.mp4",
      progress: { operation: "Waiting for a turn", since: new Date().toISOString() },
    }],
    recent: [
      { id: "o", kind: "ocr", state: "done", stage: "OCR", title: "Blade Runner", year: 1982, started: new Date().toISOString(),
        original: "/lib/Blade Runner (1982)/Blade Runner (1982) {edition-Original}.mkv",
        reading: { stream: 3, lang: "eng", srt: "/lib/Blade Runner (1982)/Blade Runner (1982) {edition-Original}.en.srt" },
        sidecars: ["/lib/Blade Runner (1982)/Blade Runner (1982) {edition-Original}.en.srt"],
        low_confidence: [{ sidecar: "x.srt", start: 61000000000, end: 62000000000, text: "|t was", fast: "It was", picture: "/p.png" }] },
      { id: "p", kind: "convert", state: "done", stage: "DELIVER", title: "Blade Runner", year: 1982,
        made: ["/lib/Blade Runner (1982)/Blade Runner (1982) {edition-Archive}.mkv"], started: new Date().toISOString() },
      { id: "a", state: "done", stage: "DELIVER", title: "Blade Runner", year: 1982, original: "/lib/Blade Runner (1982)/Blade Runner (1982) {edition-Original}.mkv", started: new Date().toISOString(),
        read_speed: [{ seconds: 30, mb_per_second: 6.1 }, { seconds: 60, mb_per_second: 22.4 }, { seconds: 90, mb_per_second: 18 }] },
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
  "/api/originals": { originals: [{ title: "Crime 101 (2025)", path: "/library/Crime 101 (2025)/Crime 101 (2025) {edition-Original}.mkv", size: 40700000000 }] },
  "/api/original": {
    duration: 5825,
    tracks: [
      { index: 0, kind: "video", codec: "h264", height: 1080, label: "H.264 · 1080p" },
      { index: 1, kind: "audio", codec: "truehd", lang: "eng", channels: 8, lossless: true, label: "English · 7.1 · Dolby TrueHD · lossless",
        note: "Plex has to convert this every time it plays on Apple TV. Consider adding/substituting a converted FLAC 7.1 that remains lossless and plays directly." },
      { index: 2, kind: "audio", codec: "ac3", lang: "eng", channels: 2, label: "English · Stereo · Dolby Digital · lossy" },
      { index: 3, kind: "audio", codec: "ac3", lang: "fra", channels: 6, label: "French · 5.1 · Dolby Digital · lossy" },
      { index: 4, kind: "subtitle", codec: "hdmv_pgs_subtitle", lang: "eng", label: "English · pictures",
        note: "Plex has to convert the whole picture to show these on Apple TV." },
    ],
  },
  "/api/project/fill": {
    project: {
      containers: ["mkv"], edition: "Archive", blueprint: "Archive",
      items: [
        { kind: "video", action: "convert", source: 0, codec: "h264", to: "hevc", crf: 20, preset: "slow", label: "H.264 · 1080p" },
        { kind: "audio", action: "copy", source: 1, codec: "truehd", lang: "eng", channels: 8, lossless: true, label: "English · 7.1 · Dolby TrueHD · lossless" },
        { kind: "audio", action: "convert", source: 1, codec: "truehd", lang: "eng", channels: 8, lossless: true, to: "flac", label: "English · 7.1 · Dolby TrueHD · lossless" },
        { kind: "audio", action: "convert", source: 2, codec: "ac3", lang: "eng", channels: 2, to: "aac", bitrate: "256k", label: "English · Stereo · Dolby Digital · lossy" },
        { kind: "subtitle", action: "copy", source: 4, codec: "hdmv_pgs_subtitle", lang: "eng", label: "English · pictures" },
      ],
    },
  },
  "/api/blueprints": {
    blueprints: [
      { name: "Archive", description: "HEVC quality 20, slow", default: true, editable: true, source: "your settings file", preset: "slow", crf_uhd: 20, crf_bluray: 20, crf_dvd: 18, audio_bitrate: "256k", allow_uhd_copy: true, copy_native_audio: true },
      { name: "Small", description: "HEVC quality 24, medium", default: false, editable: true, source: "made here", preset: "medium", crf_uhd: 24, crf_bluray: 24, crf_dvd: 22, audio_bitrate: "192k", allow_uhd_copy: false, copy_native_audio: true,
        sound: { languages: ["eng", "fra"], language_mode: "all", choices: [{ mode: "one", layouts: ["7.1", "5.1"], quality: "lossless" }, { mode: "one", layouts: ["stereo"], quality: "" }] } },
    ],
    defaults: { name: "", description: "HEVC quality 20, slow", default: false, editable: false, source: "your settings file", preset: "slow", crf_uhd: 20, crf_bluray: 20, crf_dvd: 18, audio_bitrate: "256k", allow_uhd_copy: true, copy_native_audio: true },
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
  json: async () => answers[path.split("?")[0]] ?? {},
});

global.setInterval = () => 0;
global.clearInterval = () => {};
global.setTimeout = () => 0;
global.localStorage = {
  store: {},
  getItem(k) { return this.store[k] ?? null; },
  setItem(k, v) { this.store[k] = String(v); },
};
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
