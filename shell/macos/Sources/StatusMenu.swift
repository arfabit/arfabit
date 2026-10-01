import AppKit

/// StatusMenu is ARFABIT's icon in the menu bar: a shortcut to the page that
/// never does anything the page cannot (docs/RELEASE.md §4).
final class StatusMenu: NSObject, NSMenuDelegate {
    var onOpen: (() -> Void)?
    var onStartAtLogin: ((Bool) -> Void)?
    var onRestart: (() -> Void)?
    var onStop: (() -> Void)?
    var onUpdate: (() -> Void)?
    var onMenuOpening: (() -> Void)?

    private var item: NSStatusItem?
    private let menu = NSMenu()
    private let status = NSMenuItem(title: "Starting", action: nil, keyEquivalent: "")
    private let login = NSMenuItem(title: "Start at login", action: #selector(toggleLogin), keyEquivalent: "")
    private let update = NSMenuItem(title: "Update and restart", action: #selector(installUpdate), keyEquivalent: "")

    override init() {
        super.init()
        menu.autoenablesItems = false
        menu.delegate = self

        // One line saying what is happening. Not pressable.
        status.isEnabled = false
        menu.addItem(status)
        menu.addItem(.separator())

        menu.addItem(entry("Open ARFABIT", #selector(open)))
        login.target = self
        menu.addItem(login)
        menu.addItem(.separator())
        menu.addItem(entry("Restart ARFABIT", #selector(restart)))
        menu.addItem(entry("Stop ARFABIT", #selector(stop)))

        // Only there when a new version is ready (docs/RELEASE.md §4).
        update.target = self
        update.isHidden = true
        menu.addItem(update)
    }

    private func entry(_ title: String, _ action: Selector) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: action, keyEquivalent: "")
        item.target = self
        return item
    }

    /// Shows or hides the icon, following Show the icon on the page.
    func setShown(_ shown: Bool) {
        if shown, item == nil {
            let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
            let image = NSImage(systemSymbolName: "opticaldisc", accessibilityDescription: "ARFABIT")
            image?.isTemplate = true
            item.button?.image = image
            item.button?.toolTip = "ARFABIT"
            item.menu = menu
            self.item = item
        } else if !shown, let item {
            NSStatusBar.system.removeStatusItem(item)
            self.item = nil
        }
    }

    func setStatus(_ line: String) {
        status.title = line
    }

    /// Ticks Start at login as the computer has it, not as it was asked for.
    func setStartsAtLogin(_ on: Bool) {
        login.state = on ? .on : .off
    }

    /// Shows Update and restart when a version is ready, naming it.
    func setUpdateReady(_ version: String?) {
        update.isHidden = version == nil
        if let version {
            update.title = "Update to \(version) and restart"
        }
    }

    func menuWillOpen(_ menu: NSMenu) {
        onMenuOpening?()
    }

    @objc private func open() { onOpen?() }
    @objc private func toggleLogin() { onStartAtLogin?(login.state != .on) }
    @objc private func restart() { onRestart?() }
    @objc private func stop() { onStop?() }
    @objc private func installUpdate() { onUpdate?() }
}
