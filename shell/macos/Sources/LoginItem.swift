import Foundation
import ServiceManagement

/// LoginItem starts the app when the person logs in, through macOS's own
/// login item API. It is listed in System Settings → General → Login Items as
/// ARFABIT, and macOS starts the app once at login and never relaunches it, so
/// Stop stays stopped (docs/RELEASE.md §3).
enum LoginItem {
    /// What macOS did, sent to ARFABIT as it is (internal/web/shell.go).
    struct Report: Equatable {
        var enabled: Bool
        var state: String
        var detail: String?

        var body: [String: Any] {
            var body: [String: Any] = ["enabled": enabled, "state": state]
            if let detail { body["detail"] = detail }
            return body
        }
    }

    /// Makes the login item match the setting, and says what macOS made of it.
    ///
    /// A login item the person has turned off in System Settings is waiting
    /// for their approval there. It is left as it is: turning it back on is
    /// theirs to do, and the page says where.
    static func apply(_ wanted: Bool) -> Report {
        let service = SMAppService.mainApp
        var detail: String?
        do {
            switch (wanted, service.status) {
            case (true, .notRegistered), (true, .notFound):
                try service.register()
            case (false, .enabled), (false, .requiresApproval):
                try service.unregister()
            default:
                break
            }
        } catch {
            detail = String(describing: error)
        }
        var report = current()
        report.detail = detail
        return report
    }

    /// What macOS says now.
    static func current() -> Report {
        let status = SMAppService.mainApp.status
        return Report(enabled: status == .enabled, state: name(status), detail: nil)
    }

    private static func name(_ status: SMAppService.Status) -> String {
        switch status {
        case .enabled: return "enabled"
        case .requiresApproval: return "requiresApproval"
        case .notRegistered: return "notRegistered"
        case .notFound: return "notFound"
        @unknown default: return "status \(status.rawValue)"
        }
    }
}
