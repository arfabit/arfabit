import Foundation

/// ShellState is what ARFABIT sends the app about itself: the settings the
/// app follows and the line at the top of its menu (internal/web/shell.go).
struct ShellState: Decodable {
    var showIcon: Bool
    var startAtLogin: Bool
    var hideOnStart: Bool
    var status: String

    /// A new version waiting to be installed, if there is one.
    var updateReady: String?

    enum CodingKeys: String, CodingKey {
        case showIcon = "show_icon"
        case startAtLogin = "start_at_login"
        case hideOnStart = "hide_on_start"
        case updateReady = "update_ready"
        case status
    }
}

/// A refusal from ARFABIT, in its own words (§15).
struct Refusal {
    var message: String
    var detail: String?
}

/// API talks to ARFABIT through its web API and live events, exactly as the
/// page does. The app knows nothing about discs or jobs.
final class API: NSObject, URLSessionDataDelegate {
    let base: URL

    var onShell: ((ShellState) -> Void)?
    var onStatus: ((String) -> Void)?

    private var session: URLSession!
    private var stream: URLSessionDataTask?
    private var unread = ""
    private var closed = false

    init(base: URL) {
        self.base = base
        super.init()

        // The live stream stays open for as long as ARFABIT runs.
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 24 * 60 * 60
        config.timeoutIntervalForResource = .infinity
        session = URLSession(configuration: config, delegate: self, delegateQueue: .main)
    }

    /// Fetches the settings, then listens for changes to them.
    func connect() {
        refresh()
        listen()
    }

    /// Stops listening, for an ARFABIT that has stopped.
    func close() {
        closed = true
        stream?.cancel()
        session.invalidateAndCancel()
    }

    /// Asks for the settings and status line again.
    func refresh() {
        fetch { [weak self] state in
            if let state { self?.onShell?(state) }
        }
    }

    /// Fetches the settings and status line, and calls back on the main queue
    /// with them, or nil if ARFABIT did not answer.
    func fetch(_ done: @escaping (ShellState?) -> Void) {
        var request = URLRequest(url: base.appendingPathComponent("api/shell"))
        request.timeoutInterval = 5
        session.dataTask(with: request) { data, _, _ in
            let state = data.flatMap { try? JSONDecoder().decode(ShellState.self, from: $0) }
            DispatchQueue.main.async { done(state) }
        }.resume()
    }

    /// Sends a POST, and calls back on the main queue with ARFABIT's refusal,
    /// if it gave one.
    func post(_ path: String, _ body: [String: Any]? = nil, done: ((Refusal?) -> Void)? = nil) {
        var request = URLRequest(url: base.appendingPathComponent(path))
        request.httpMethod = "POST"
        request.timeoutInterval = 10
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let body {
            request.httpBody = try? JSONSerialization.data(withJSONObject: body)
        }

        session.dataTask(with: request) { data, response, error in
            var refusal: Refusal?
            if let error {
                refusal = Refusal(message: "ARFABIT did not answer.", detail: error.localizedDescription)
            } else if let http = response as? HTTPURLResponse, http.statusCode != 200 {
                let reply = (data.flatMap { try? JSONSerialization.jsonObject(with: $0) }) as? [String: Any]
                refusal = Refusal(
                    message: reply?["message"] as? String ?? "ARFABIT did not do that.",
                    detail: reply?["detail"] as? String)
            }
            DispatchQueue.main.async { done?(refusal) }
        }.resume()
    }

    // MARK: live events

    private func listen() {
        guard !closed else { return }
        unread = ""
        var request = URLRequest(url: base.appendingPathComponent("events"))
        request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
        stream = session.dataTask(with: request)
        stream?.resume()
    }

    func urlSession(_ session: URLSession, dataTask: URLSessionDataTask, didReceive data: Data) {
        unread += String(decoding: data, as: UTF8.self)

        // Events are separated by a blank line.
        while let end = unread.range(of: "\n\n") {
            let block = String(unread[unread.startIndex..<end.lowerBound])
            unread.removeSubrange(unread.startIndex..<end.upperBound)
            handle(block)
        }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        guard task === stream, !closed else { return }

        // Dropped while ARFABIT still runs: listen again shortly, and catch up
        // on anything missed meanwhile.
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) { [weak self] in
            guard let self, !self.closed else { return }
            self.refresh()
            self.listen()
        }
    }

    private func handle(_ block: String) {
        var name = ""
        var data = ""
        for line in block.split(separator: "\n", omittingEmptySubsequences: false) {
            if line.hasPrefix("event: ") {
                name = String(line.dropFirst(7))
            } else if line.hasPrefix("data: ") {
                data += String(line.dropFirst(6))
            }
        }

        switch name {
        case "shell":
            if let state = try? JSONDecoder().decode(ShellState.self, from: Data(data.utf8)) {
                onShell?(state)
            }
        case "status":
            if let reply = try? JSONSerialization.jsonObject(with: Data(data.utf8)) as? [String: Any],
               let line = reply["line"] as? String {
                onStatus?(line)
            }
        default:
            break
        }
    }
}
