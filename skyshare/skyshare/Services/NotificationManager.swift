import Foundation
import UserNotifications

/// Manages macOS notifications for sync events.
class NotificationManager {
    static let shared = NotificationManager()

    private init() {
        requestPermission()
    }

    private func requestPermission() {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }

    /// Notify that sync completed.
    func syncComplete(uploaded: Int, downloaded: Int) {
        guard uploaded > 0 || downloaded > 0 else { return }

        var parts: [String] = []
        if uploaded > 0 { parts.append("\(uploaded) uploaded") }
        if downloaded > 0 { parts.append("\(downloaded) downloaded") }

        send(
            title: "Sync Complete",
            body: parts.joined(separator: ", "),
            identifier: "sync-complete-\(Int(Date().timeIntervalSince1970))"
        )
    }

    /// Notify about a sync conflict.
    func syncConflict(path: String) {
        send(
            title: "Sync Conflict",
            body: "\(path) was modified on multiple devices",
            identifier: "sync-conflict-\(path.hashValue)"
        )
    }

    /// Notify about a sync error.
    func syncError(message: String) {
        send(
            title: "Sync Error",
            body: message,
            identifier: "sync-error-\(Int(Date().timeIntervalSince1970))"
        )
    }

    private func send(title: String, body: String, identifier: String) {
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.sound = .default

        let request = UNNotificationRequest(
            identifier: identifier,
            content: content,
            trigger: nil // deliver immediately
        )

        UNUserNotificationCenter.current().add(request)
    }
}
