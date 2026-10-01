import AppKit

/// MoveToApplications offers to move the app into Applications.
///
/// An app opened straight from Downloads is run by macOS from a hidden,
/// read-only copy in a new place each time. From there it could not replace
/// itself to update, and its login item would point at a path that goes away
/// (docs/RELEASE.md §5). Dragging it from the disk image avoids this; this is
/// for when that did not happen.
enum MoveToApplications {
    /// Offers the move when the app is not in Applications. Returns true when
    /// the moved copy has been opened and this one should quit.
    static func offerIfNeeded() -> Bool {
        let here = Bundle.main.bundleURL.resolvingSymlinksInPath()
        let home = FileManager.default.homeDirectoryForCurrentUser
        let folders = ["/Applications", home.appendingPathComponent("Applications").path]
        if folders.contains(where: { here.path.hasPrefix($0 + "/") }) {
            return false
        }

        NSApp.activate(ignoringOtherApps: true)
        let ask = NSAlert()
        ask.messageText = "Move ARFABIT to your Applications folder?"
        ask.informativeText = "ARFABIT needs to be in Applications to start when you log in and to update itself."
        ask.addButton(withTitle: "Move to Applications")
        ask.addButton(withTitle: "Not now")
        guard ask.runModal() == .alertFirstButtonReturn else { return false }

        do {
            let moved = try move(original(of: here))
            let config = NSWorkspace.OpenConfiguration()
            config.createsNewApplicationInstance = true
            NSWorkspace.shared.openApplication(at: moved, configuration: config) { _, _ in }
            return true
        } catch {
            let said = NSAlert()
            said.messageText = "ARFABIT could not be moved."
            said.informativeText = "Drag ARFABIT into your Applications folder in Finder, then open it from there.\n\n\(error.localizedDescription)"
            said.runModal()
            return false
        }
    }

    /// Moves the app into Applications, replacing any ARFABIT already there.
    /// The one replaced goes to the Trash.
    private static func move(_ app: URL) throws -> URL {
        let fm = FileManager.default
        var folder = URL(fileURLWithPath: "/Applications")
        if !fm.isWritableFile(atPath: folder.path) {
            folder = fm.homeDirectoryForCurrentUser.appendingPathComponent("Applications")
            try fm.createDirectory(at: folder, withIntermediateDirectories: true)
        }

        let target = folder.appendingPathComponent("ARFABIT.app")
        if fm.fileExists(atPath: target.path) {
            try fm.trashItem(at: target, resultingItemURL: nil)
        }
        try fm.moveItem(at: app, to: target)

        // The person has already opened this copy once, past macOS's first-open
        // question. Without this, macOS would go on running it from a hidden
        // copy, even from Applications.
        clearQuarantine(target)
        return target
    }

    private static func clearQuarantine(_ url: URL) {
        let name = "com.apple.quarantine"
        removexattr(url.path, name, XATTR_NOFOLLOW)
        guard let walk = FileManager.default.enumerator(at: url, includingPropertiesForKeys: nil) else { return }
        for case let item as URL in walk {
            removexattr(item.path, name, XATTR_NOFOLLOW)
        }
    }

    /// Where the app really is, when macOS is running it from a hidden copy.
    ///
    /// macOS has no public call for this. Security.framework's
    /// SecTranslocateCreateOriginalPathForURL is the one others use; if it is
    /// ever missing, the app's own path is returned and the move fails with
    /// the system's own message.
    private static func original(of url: URL) -> URL {
        typealias Create = @convention(c) (CFURL, UnsafeMutablePointer<Unmanaged<CFError>?>?) -> Unmanaged<CFURL>?

        guard url.path.contains("/AppTranslocation/"),
              let security = dlopen("/System/Library/Frameworks/Security.framework/Security", RTLD_LAZY),
              let symbol = dlsym(security, "SecTranslocateCreateOriginalPathForURL")
        else { return url }

        let create = unsafeBitCast(symbol, to: Create.self)
        guard let found = create(url as CFURL, nil) else { return url }
        return found.takeRetainedValue() as URL
    }
}
