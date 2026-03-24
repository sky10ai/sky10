import SwiftUI

/// Live activity feed showing real-time sync operations.
struct ActivityView: View {
    @EnvironmentObject var appState: AppState

    var body: some View {
        List {
            // Current status banner
            if appState.syncState == .syncing {
                Section {
                    HStack(spacing: 8) {
                        ProgressView()
                            .scaleEffect(0.7)
                            .frame(width: 16, height: 16)
                        if !appState.syncDetail.isEmpty {
                            Text(appState.syncDetail)
                                .font(.body)
                                .foregroundStyle(.primary)
                        } else {
                            Text("Syncing…")
                                .font(.body)
                                .foregroundStyle(.primary)
                        }
                        Spacer()
                    }
                    .padding(.vertical, 4)
                }
            }

            // Activity log
            if appState.activityLog.entries.isEmpty {
                Section {
                    ContentUnavailableView {
                        Label("No Activity", systemImage: "clock")
                    } description: {
                        Text("Uploads, downloads, and sync events will appear here")
                    }
                }
            } else {
                Section("Recent Activity") {
                    ForEach(appState.activityLog.entries) { entry in
                        ActivityRow(entry: entry)
                    }
                }
            }
        }
        .listStyle(.inset(alternatesRowBackgrounds: true))
    }
}

private struct ActivityRow: View {
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
