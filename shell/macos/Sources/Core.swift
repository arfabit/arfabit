import Foundation

/// Core runs ARFABIT, the Go program inside the app, as a child process.
///
/// Everything ARFABIT prints goes to the log. One line of it matters to the
/// app: "ARFABIT ready <address>", which says where the page is
/// (docs/RELEASE.md §1).
final class Core {
    /// Where everything ARFABIT prints is kept.
    static let logURL = FileManager.default.homeDirectoryForCurrentUser
        .appendingPathComponent("Library/Logs/arfabit.log")

    /// What ARFABIT prints once it is serving, followed by its address.
    static let readyPrefix = "ARFABIT ready "

    /// The page's address, once ARFABIT has said it.
    private(set) var url: URL?

    var onReady: ((URL) -> Void)?
    var onExit: ((Int32, Process.TerminationReason) -> Void)?

    private var process: Process?
    // Kept on `queue`: the output not yet split into lines, and whether the
    // ready line has been seen.
    private var pending = Data()
    private var announced = false
    private var log: FileHandle?
    private let queue = DispatchQueue(label: "arfabit.core.output")

    var isRunning: Bool { process?.isRunning ?? false }

    func start() throws {
        url = nil
        queue.sync {
            pending = Data()
            announced = false
            openLog()
        }

        let program = Bundle.main.bundleURL.appendingPathComponent("Contents/Resources/arfabit")
        let p = Process()
        p.executableURL = program
        p.arguments = ["-no-open", "-shell"]
        p.standardInput = FileHandle.nullDevice

        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = pipe
        pipe.fileHandleForReading.readabilityHandler = { [weak self] handle in
            let data = handle.availableData
            if data.isEmpty {
                handle.readabilityHandler = nil
                return
            }
            self?.queue.async { self?.received(data) }
        }

        p.terminationHandler = { [weak self] p in
            // Whatever was still in the pipe goes to the log first.
            self?.queue.async {
                DispatchQueue.main.async {
                    self?.process = nil
                    self?.onExit?(p.terminationStatus, p.terminationReason)
                }
            }
        }

        try p.run()
        process = p
    }

    /// Asks ARFABIT to stop as Ctrl+C would: it puts everything away first.
    func stop() {
        process?.terminate()
    }

    /// Called on `queue`.
    private func openLog() {
        try? log?.close()
        let fm = FileManager.default
        try? fm.createDirectory(at: Core.logURL.deletingLastPathComponent(), withIntermediateDirectories: true)
        if !fm.fileExists(atPath: Core.logURL.path) {
            fm.createFile(atPath: Core.logURL.path, contents: nil)
        }
        log = try? FileHandle(forWritingTo: Core.logURL)
        _ = try? log?.seekToEnd()
    }

    /// Called on `queue`, in order.
    private func received(_ data: Data) {
        try? log?.write(contentsOf: data)

        guard !announced else { return }
        pending.append(data)
        while let newline = pending.firstIndex(of: UInt8(ascii: "\n")) {
            let line = String(decoding: pending[pending.startIndex..<newline], as: UTF8.self)
            pending.removeSubrange(pending.startIndex...newline)

            if line.hasPrefix(Core.readyPrefix),
               let found = URL(string: String(line.dropFirst(Core.readyPrefix.count)).trimmingCharacters(in: .whitespaces)) {
                pending = Data()
                announced = true
                DispatchQueue.main.async {
                    self.url = found
                    self.onReady?(found)
                }
                return
            }
        }
    }
}
