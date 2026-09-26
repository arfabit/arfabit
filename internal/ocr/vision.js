// Reads the text in subtitle pictures with macOS Vision.
//
//   osascript -l JavaScript vision.js <list> <answer> [language]
//
// <list> holds one PNG path per line. The answer is JSON: a line per picture,
// in the same order, read in accurate mode ("text") and again in fast mode
// ("fast") as a second opinion, or {"missing": true} when this Mac cannot read
// the language asked for. language is a tag such as "en", matched against the
// start of the tags Vision names its languages by ("en-US").
ObjC.import('Vision');
ObjC.import('Foundation');

function run(argv) {
  const list = argv[0], out = argv[1], tag = (argv[2] || '').toLowerCase();
  const text = ObjC.unwrap($.NSString.stringWithContentsOfFileEncodingError(list, $.NSUTF8StringEncoding, null));
  const pictures = text.split('\n').filter((p) => p !== '');

  let languages = null;
  if (tag) {
    const probe = $.VNRecognizeTextRequest.alloc.init;
    probe.setRecognitionLevel(0);
    const supported = ObjC.deepUnwrap(probe.supportedRecognitionLanguagesAndReturnError(null)) || [];
    languages = supported.filter((l) => l.toLowerCase() === tag || l.toLowerCase().startsWith(tag + '-'));
    if (languages.length === 0) {
      write(out, { missing: true });
      return;
    }
  }

  const lines = pictures.map((path) => read(path, languages));
  write(out, { lines: lines });
}

// read reads one picture both ways.
function read(path, languages) {
  return { text: recognise(path, languages, 0), fast: recognise(path, languages, 1) };
}

// recognise reads every line of text in a picture, top to bottom. level is
// Vision's: 0 is accurate, 1 fast.
function recognise(path, languages, level) {
  const url = $.NSURL.fileURLWithPath(path);
  const handler = $.VNImageRequestHandler.alloc.initWithURLOptions(url, $({}));
  const request = $.VNRecognizeTextRequest.alloc.init;
  request.setRecognitionLevel(level);
  request.setUsesLanguageCorrection(true);
  if (languages) request.setRecognitionLanguages($(languages));

  if (!handler.performRequestsError($([request]), null)) return '';

  const found = [];
  const results = request.results;
  for (let i = 0; results && i < results.count; i++) {
    const observation = results.objectAtIndex(i);
    const candidates = observation.topCandidates(1);
    if (candidates.count === 0) continue;
    // Vision measures from the bottom left, as a share of the picture. If
    // the box cannot be read, the order Vision gave is kept.
    let top = -i, left = 0;
    try {
      const box = observation.boundingBox;
      top = box.origin.y + box.size.height;
      left = box.origin.x;
    } catch (e) {}
    found.push({ text: ObjC.unwrap(candidates.objectAtIndex(0).string), top: top, left: left });
  }
  found.sort((a, b) => (Math.abs(a.top - b.top) > 0.05 ? b.top - a.top : a.left - b.left));
  return found.map((f) => f.text).join('\n');
}

function write(path, value) {
  $(JSON.stringify(value)).writeToFileAtomicallyEncodingError(path, true, $.NSUTF8StringEncoding, null);
}
