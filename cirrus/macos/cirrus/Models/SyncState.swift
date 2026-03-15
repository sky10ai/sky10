import Foundation

/// Represents the current sync state for UI display.
enum SyncState {
    case synced
    case syncing
    case error
    case offline

    var icon: String {
        switch self {
        case .synced:  return "cloud.fill"
        case .syncing: return "arrow.triangle.2.circlepath.cloud"
        case .error:   return "exclamationmark.icloud"
        case .offline: return "icloud.slash"
        }
    }

    var label: String {
        switch self {
        case .synced:  return "Synced"
        case .syncing: return "Syncing..."
        case .error:   return "Sync Error"
        case .offline: return "Offline"
        }
    }

    var color: String {
        switch self {
        case .synced:  return "green"
        case .syncing: return "blue"
        case .error:   return "red"
        case .offline: return "gray"
        }
    }
}
