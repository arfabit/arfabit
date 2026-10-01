import AppKit

/// Updater installs a new version that ARFABIT has downloaded, checked and
/// staged (docs/RELEASE.md §6). ARFABIT never replaces itself; when it exits
/// with 75 the app looks here, and if a version is staged, installs it:
///
/// 1. unpacks the new ARFABIT.app beside this one;
/// 2. renames this one "ARFABIT (previous version).app" and the new one into
///    its place, so the path, the login item and folder permissions stay;
/// 3. opens the new one as a new copy, and quits.
///
/// The new app finishes the job: once its ARFABIT prints the ready line, the
/// previous version goes to the Trash. If it does not, the new app goes to the
/// Trash, the previous version is put back and opened, and failed.json says
/// what happened, so ARFABIT does not try that version again by itself.
enum Updater {
    /// ARFABIT's staging folder, the same one it writes to
    /// (internal/update, config.DefaultDir).
    static let folder = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("Library/Application Support/arfabit/update")

    private static var stagedURL: URL { folder.appendingPathComponent("staged.json") }
    private static var installingURL: URL { folder.appendingPathComponent("installing.json") }
    private static var failedURL: URL { folder.appendingPathComponent("failed.json") }

    /// What ARFABIT staged.
    private struct Staged: Codable {
        var version: String
        var file: String
    }

    /// An install under way, written by the old app for the new one.
    struct Installing: Codable {
        var version: String
        var previous: String
        var pid: Int32
    }

    /// A version that did not start, read by ARFABIT.
    private struct Failed: Codable {
        var version: String
        var detail: String
    }

    private struct Problem: LocalizedError {
        var errorDescription: String?
        init(_ text: String) { errorDescription = text }
    }

    // MARK: the old app

    /// Installs a staged version, if there is one. Calls back with true once
    /// the new app is opened and this one should quit, or false to start
    /// ARFABIT again as it is.
    static func installStaged(_ done: @escaping (Bool) -> Void) {
        guard let staged: Staged = read(stagedURL) else {
            done(false)
            return
        }
        // Whatever happens next, this staging is used once.
        try? FileManager.default.removeItem(at: stagedURL)

        let app = Bundle.main.bundleURL
        let previous = app.deletingLastPathComponent().appendingPathComponent("ARFABIT (previous version).app")
        do {
            try swap(staged, app: app, previous: previous)
        } catch {
            fail(staged.version, error.localizedDescription)
            done(false)
            return
        }

        write(Installing(version: staged.version, previous: previous.path, pid: getpid()), to: installingURL)
        open(app) { error in
            guard let error else {
                done(true)
                return
            }
            // macOS would not open it: put this version back.
            let fm = FileManager.default
            try? fm.trashItem(at: app, resultingItemURL: nil)
            try? fm.moveItem(at: previous, to: app)
            try? fm.removeItem(at: installingURL)
            fail(staged.version, error.localizedDescription)
            done(false)
        }
    }

    /// Unpacks the staged app beside this one and swaps them, with renames.
    private static func swap(_ staged: Staged, app: URL, previous: URL) throws {
        let fm = FileManager.default
        let zip = folder.appendingPathComponent(staged.file)
        let work = app.deletingLastPathComponent().appendingPathComponent(".ARFABIT-update")

        try? fm.removeItem(at: work)
        defer { try? fm.removeItem(at: work) }
        try run("/usr/bin/ditto", ["-x", "-k", zip.path, work.path])

        let fresh = work.appendingPathComponent("ARFABIT.app")
        let found = Bundle(url: fresh)?.object(forInfoDictionaryKey: "ARFABITVersion") as? String
        guard found == staged.version else {
            throw Problem("The download holds ARFABIT \(found ?? "of no version"), not \(staged.version).")
        }

        if fm.fileExists(atPath: previous.path) {
            try fm.trashItem(at: previous, resultingItemURL: nil)
        }
        try fm.moveItem(at: app, to: previous)
        do {
            try fm.moveItem(at: fresh, to: app)
        } catch {
            try? fm.moveItem(at: previous, to: app)
            throw error
        }
        try? fm.removeItem(at: zip)
    }

    // MARK: the new app

    /// The install this app is finishing, if it was just installed. The old
    /// app is still quitting: this waits for it, so the two never both run
    /// ARFABIT.
    static func finishing() -> Installing? {
        guard let installing: Installing = read(installingURL) else { return nil }
        guard installing.pid != getpid() else { return nil }

        let deadline = Date().addingTimeInterval(30)
        while kill(installing.pid, 0) == 0 && Date() < deadline {
            usleep(200_000)
        }
        return installing
    }

    /// The new version printed its ready line: the update is done, and the
    /// previous version goes to the Trash.
    static func succeeded(_ installing: Installing) {
        let fm = FileManager.default
        try? fm.trashItem(at: URL(fileURLWithPath: installing.previous), resultingItemURL: nil)
        try? fm.removeItem(at: installingURL)
        try? fm.removeItem(at: failedURL)
    }

    /// The new version did not start. It goes to the Trash, the previous
    /// version is put back in its place and opened, and failed.json says what
    /// happened. Returns once the previous version is opened, or could not be.
    static func rollBack(_ installing: Installing, detail: String, _ done: @escaping () -> Void) {
        let fm = FileManager.default
        let app = Bundle.main.bundleURL
        let previous = URL(fileURLWithPath: installing.previous)

        fail(installing.version, detail)
        try? fm.removeItem(at: installingURL)

        guard fm.fileExists(atPath: previous.path) else {
            done()
            return
        }
        do {
            try fm.trashItem(at: app, resultingItemURL: nil)
            try fm.moveItem(at: previous, to: app)
        } catch {
            done()
            return
        }
        open(app) { _ in done() }
    }

    // MARK: helpers

    private static func open(_ app: URL, _ done: @escaping (Error?) -> Void) {
        // A new copy: opened the ordinary way, macOS would hand the open to
        // the app already running, which is about to quit.
        let config = NSWorkspace.OpenConfiguration()
        config.createsNewApplicationInstance = true
        NSWorkspace.shared.openApplication(at: app, configuration: config) { _, error in
            DispatchQueue.main.async { done(error) }
        }
    }

    private static func fail(_ version: String, _ detail: String) {
        write(Failed(version: version, detail: detail), to: failedURL)
    }

    private static func run(_ tool: String, _ args: [String]) throws {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: tool)
        p.arguments = args
        let pipe = Pipe()
        p.standardError = pipe
        p.standardOutput = pipe
        try p.run()
        p.waitUntilExit()
        guard p.terminationStatus == 0 else {
            let said = String(decoding: pipe.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
            throw Problem("\(tool) exited with \(p.terminationStatus): \(said)")
        }
    }

    private static func read<T: Decodable>(_ url: URL) -> T? {
        guard let data = try? Data(contentsOf: url) else { return nil }
        return try? JSONDecoder().decode(T.self, from: data)
    }

    private static func write<T: Encodable>(_ value: T, to url: URL) {
        try? FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        guard let data = try? JSONEncoder().encode(value) else { return }
        try? data.write(to: url, options: .atomic)
    }
}
