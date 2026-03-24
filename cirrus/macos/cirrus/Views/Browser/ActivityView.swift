import SwiftUI

/// Main activity view showing pending sync operations and recent history.
struct ActivityView: View {
    @EnvironmentObject var appState: AppState

    var body: some View {
        List {
            if !appState.pendingActivity.isEmpty {
                Section("Pending") {
                    ForEach(appState.pendingActivity) { entry in
                        PendingRow(entry: entry)
                    }
                }
            }

            Section("Recent") {
                if appState.activityLog.entries.isEmpty && appState.pendingActivity.isEmpty {
                    ContentUnavailableView {
                        Label("No Activity", systemImage: "clock")
                    } description: {
                        Text("Sync operations will appear here")
                    }
                } else if appState.activityLog.entries.isEmpty {
                    Text("No completed operations yet")
                        .foregroundStyle(.secondary)
                        .font(.caption)
                } else {
                    ForEach(appState.activityLog.entries) { entry in
                        CompletedRow(entry: entry)
                    }
                }
            }
        }
        .listStyle(.inset(alternatesRowBackgrounds: true))
    }
}

/// Row for a pending outbox/inbox entry.
private struct PendingRow: View {
    let entry: SkyClient.SyncActivityEntry

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: icon)
                .foregroundStyle(iconColor)
                .frame(width: 20)

            VStack(alignment: .leading, spacing: 2) {
                Text(entry.path)
                    .font(.body)
                    .lineLimit(1)
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Spacer()

            Text(formattedTime)
                .font(.caption2)
                .foregroundStyle(.tertiary)
        }
        .padding(.vertical, 2)
    }

    private var icon: String {
        if entry.direction == "up" {
            return entry.op == "delete" ? "trash" : "arrow.up.circle"
        } else {
            return entry.op == "delete" ? "trash" : "arrow.down.circle"
        }
    }

    private var iconColor: Color {
        entry.direction == "up" ? .blue : .green
    }

    private var subtitle: String {
        let dir = entry.direction == "up" ? "Uploading" : "Downloading"
        let op = entry.op == "delete" ? "Deleting" : dir
        return "\(op) — \(entry.driveName)"
    }

    private var formattedTime: String {
        let date = Date(timeIntervalSince1970: TimeInterval(entry.ts))
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .abbreviated
        return formatter.localizedString(for: date, relativeTo: Date())
    }
}

/// Row for a completed activity log entry.
private struct CompletedRow: View {
    let entry: ActivityEntry

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: entry.icon)
                .foregroundStyle(colorForType(entry.type))
                .frame(width: 20)

            VStack(alignment: .leading, spacing: 2) {
                Text(entry.path)
                    .font(.body)
                    .lineLimit(1)
                Text(entry.detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Spacer()

            Text(entry.formattedTime)
                .font(.caption2)
                .foregroundStyle(.tertiary)
        }
        .padding(.vertical, 2)
    }

    private func colorForType(_ type: ActivityEntry.ActivityType) -> Color {
        switch type {
        case .uploaded:   return .blue
        case .downloaded: return .green
        case .deleted:    return .orange
        case .conflict:   return .yellow
        case .error:      return .red
        case .synced:     return .green
        case .symlink:    return .purple
        }
    }
}
