import AppKit

// ARFABIT.app: the menu bar app around ARFABIT, the Go program
// (docs/RELEASE.md §3). It starts ARFABIT, opens the page once ARFABIT says
// where it is, shows the icon, keeps the login item, and starts ARFABIT again
// when it exits asking to be. It knows nothing about discs.

final class AppDelegate: NSObject, NSApplicationDelegate {
    private let core = Core()
    private let menu = StatusMenu()
    private var api: API?

    /// The page is opened once per run of the app, unless Hide on startup is
    /// on, and not again after a restart: a page already open reconnects by
    /// itself. Opening the app again while it runs always opens it.
    private var startedOnce = false

    /// The app is quitting, and waiting for ARFABIT to finish first.
    private var quitting = false

    private var lastReport: LoginItem.Report?
    private var loginTimer: Timer?

    /// Start at login as this app last acted on it, and whether macOS has
    /// had it on since. The app acts only when the setting changes: a change
    /// made in System Settings is the person's, and is followed, never undone.
    private var loginSetting: Bool?
    private var sawLoginOn = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        if MoveToApplications.offerIfNeeded() {
            NSApp.terminate(nil)
            return
        }

        core.onReady = { [weak self] url in self?.ready(url) }
        core.onExit = { [weak self] status, reason in self?.exited(status, reason) }

        menu.onOpen = { [weak self] in self?.openPage() }
        menu.onStartAtLogin = { [weak self] on in
            self?.api?.post("api/shell/settings", ["start_at_login": on]) { self?.say($0) }
        }
        menu.onRestart = { [weak self] in
            self?.api?.post("api/restart") { self?.say($0) }
        }
        menu.onStop = { [weak self] in
            self?.api?.post("api/quit") { self?.say($0) }
        }
        menu.onMenuOpening = { [weak self] in self?.api?.refresh() }

        start()
    }

    private func start() {
        do {
            try core.start()
        } catch {
            NSApp.activate(ignoringOtherApps: true)
            let alert = NSAlert()
            alert.messageText = "ARFABIT did not start."
            alert.informativeText = error.localizedDescription
            alert.runModal()
            NSApp.terminate(nil)
        }
    }

    /// ARFABIT is serving at url.
    private func ready(_ url: URL) {
        let api = API(base: url)
        api.onShell = { [weak self] state in self?.follow(state) }
        api.onStatus = { [weak self] line in self?.menu.setStatus(line) }
        api.connect()
        self.api = api

        if !startedOnce {
            startedOnce = true
            // Without an answer, the page opens as it always has.
            api.fetch { [weak self] state in
                if state?.hideOnStart != true {
                    self?.openPage()
                }
            }
        }

        // Turned off in System Settings rather than on the page: noticed here
        // and reported, so the page never disagrees with macOS.
        loginTimer?.invalidate()
        loginTimer = Timer.scheduledTimer(withTimeInterval: 30, repeats: true) { [weak self] _ in
            self?.observe(LoginItem.current())
        }
    }

    /// Applies the settings ARFABIT keeps for the app.
    private func follow(_ state: ShellState) {
        menu.setShown(state.showIcon)
        menu.setStatus(state.status)

        switch loginSetting {
        case nil:
            // The first word since the app opened. A login item that is on
            // in the setting but not in macOS was taken off in System
            // Settings meanwhile, and the setting follows.
            loginSetting = state.startAtLogin
            let now = LoginItem.current()
            if state.startAtLogin && !now.enabled {
                followSystem()
                report(now)
            } else {
                report(LoginItem.apply(state.startAtLogin))
            }
        case state.startAtLogin:
            // Not a change: this is an echo of a report, a refresh, or the
            // other setting changing.
            observe(LoginItem.current())
        default:
            // Changed on the page or in the menu: the person asked.
            loginSetting = state.startAtLogin
            report(LoginItem.apply(state.startAtLogin))
        }
    }

    /// Notices the login item being removed or switched off in System
    /// Settings, and turns the setting off to match.
    private func observe(_ now: LoginItem.Report) {
        if loginSetting == true && sawLoginOn && !now.enabled {
            followSystem()
        }
        report(now)
    }

    private func followSystem() {
        loginSetting = false
        api?.post("api/shell/settings", ["start_at_login": false])
    }

    /// Tells ARFABIT what macOS did with Start at login, when that has changed.
    private func report(_ report: LoginItem.Report) {
        menu.setStartsAtLogin(report.enabled)
        sawLoginOn = report.enabled
        guard report != lastReport else { return }
        lastReport = report
        api?.post("api/shell/login", report.body)
    }

    private func openPage() {
        if let url = core.url {
            NSWorkspace.shared.open(url)
        }
    }

    /// ARFABIT has exited. Its exit code says what to do next
    /// (docs/RELEASE.md §1).
    private func exited(_ status: Int32, _ reason: Process.TerminationReason) {
        api?.close()
        api = nil
        loginTimer?.invalidate()
        lastReport = nil

        if quitting {
            NSApp.reply(toApplicationShouldTerminate: true)
            return
        }

        switch (reason, status) {
        case (.exit, 0):
            menu.setShown(false)
            NSApp.terminate(nil)
        case (.exit, 75):
            // Installing a staged update comes here, before starting again
            // (docs/RELEASE.md §6). Not built yet.
            menu.setStatus("Starting")
            start()
        default:
            stoppedUnexpectedly()
        }
    }

    /// A stop nobody asked for is never silent, and never restarted.
    private func stoppedUnexpectedly() {
        menu.setShown(false)
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = "ARFABIT stopped unexpectedly"
        alert.informativeText = "What it said last is in its log. Open ARFABIT again to start it."
        alert.addButton(withTitle: "Show the log")
        alert.addButton(withTitle: "Close")
        if alert.runModal() == .alertFirstButtonReturn {
            NSWorkspace.shared.open(Core.logURL)
        }
        NSApp.terminate(nil)
    }

    /// Says why ARFABIT did not do what the menu asked, in its own words.
    private func say(_ refusal: Refusal?) {
        guard let refusal else { return }
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = refusal.message
        alert.informativeText = refusal.detail ?? ""
        alert.runModal()
    }

    /// Opened again while running, from Finder, Spotlight or the Dock:
    /// macOS hands that to this app, which opens the page. This works the same
    /// whether the icon is shown or hidden.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        openPage()
        return false
    }

    /// Logging out, or quit some other way: ARFABIT is asked to stop as
    /// Ctrl+C would, and the app waits for it.
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard core.isRunning else { return .terminateNow }
        quitting = true
        core.stop()
        return .terminateLater
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
