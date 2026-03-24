import Foundation

/// A single entry in the activity log.
struct ActivityEntry: Identifiable {
    let id = UUID()
    let timestamp: Date
    let type: ActivityType
    let path: String
    let detail: String

    enum ActivityType {
        case uploaded
        case downloaded
        case deleted
        case conflict
        case error
        case synced      // poll/sync summary
        case symlink
    }

    var icon: String {
        switch type {
        case .uploaded:   return "arrow.up.circle.fill"
        case .downloaded: return "arrow.down.circle.fill"
        case .deleted:    return "trash.circle.fill"
        case .conflict:   return "exclamationmark.triangle.fill"
        case .error:      return "xmark.circle.fill"
        case .synced:     return "checkmark.circle.fill"
        case .symlink:    return "link.circle.fill"
        }
    }

    var color: String {
        switch type {
        case .uploaded:   return "blue"
        case .downloaded: return "green"
        case .deleted:    return "orange"
        case .conflict:   return "yellow"
        case .error:      return "red"
        case .synced:     return "green"
        case .symlink:    return "purple"
        }
    }

    var formattedTime: String {
        let formatter = DateFormatter()
        formatter.timeStyle = .medium
        formatter.dateStyle = .none
        return formatter.string(from: timestamp)
    }
}

/// Observable activity log that tracks recent sync events.
@MainActor
class ActivityLog: ObservableObject {
    @Published var entries: [ActivityEntry] = []

    private let maxEntries = 100

    func logUpload(path: String, size: Int64) {
        add(.uploaded, path: path, detail: "Uploaded (\(formatBytes(size)))")
    }

    func logDownload(path: String, size: Int64) {
        add(.downloaded, path: path, detail: "Downloaded (\(formatBytes(size)))")
    }

    func logDelete(path: String) {
        add(.deleted, path: path, detail: "Deleted")
    }

    func logConflict(path: String) {
        add(.conflict, path: path, detail: "Modified on multiple devices")
    }

    func logError(path: String, message: String) {
        add(.error, path: path, detail: message)
    }

    private func formatBytes(_ bytes: Int64) -> String {
        switch bytes {
        case 1_073_741_824...: return String(format: "%.1f GB", Double(bytes) / 1_073_741_824)
        case 1_048_576...:     return String(format: "%.1f MB", Double(bytes) / 1_048_576)
        case 1024...:          return String(format: "%.1f KB", Double(bytes) / 1024)
        default:               return "\(bytes) B"
        }
    }

    func add(_ type: ActivityEntry.ActivityType, path: String, detail: String) {
        let entry = ActivityEntry(
            timestamp: Date(),
            type: type,
            path: path,
            detail: detail
        )
        entries.insert(entry, at: 0)
        if entries.count > maxEntries {
            entries = Array(entries.prefix(maxEntries))
        }
    }
}
