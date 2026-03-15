import SwiftUI

/// Shows recent sync activity.
struct ActivityLogView: View {
    @ObservedObject var log: ActivityLog

    var body: some View {
        VStack(alignment: .leading) {
            Text("Activity")
                .font(.headline)
                .padding(.bottom, 4)

            if log.entries.isEmpty {
                Text("No recent activity")
                    .foregroundStyle(.secondary)
                    .font(.caption)
            } else {
                List(log.entries) { entry in
                    HStack(spacing: 8) {
                        Image(systemName: entry.icon)
                            .foregroundStyle(colorForType(entry.type))
                            .frame(width: 16)

                        VStack(alignment: .leading, spacing: 2) {
                            Text(entry.path)
                                .font(.caption)
                                .lineLimit(1)
                            Text(entry.detail)
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }

                        Spacer()

                        Text(entry.formattedTime)
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }
                }
                .listStyle(.plain)
            }
        }
        .padding()
        .frame(minWidth: 300, minHeight: 200)
    }

    private func colorForType(_ type: ActivityEntry.ActivityType) -> Color {
        switch type {
        case .uploaded:   return .blue
        case .downloaded: return .green
        case .deleted:    return .orange
        case .conflict:   return .yellow
        case .error:      return .red
        }
    }
}
