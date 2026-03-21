import SwiftUI

/// Bottom bar showing sync activity in the browser window.
struct SyncStatusBar: View {
    @EnvironmentObject var appState: AppState

    var body: some View {
        HStack(spacing: 8) {
            // Connection indicator
            Circle()
                .fill(appState.daemonConnected ? .green : .red)
                .frame(width: 6, height: 6)

            switch appState.syncState {
            case .syncing:
                ProgressView()
                    .scaleEffect(0.6)
                    .frame(width: 16, height: 16)
                if appState.outboxPending > 0 {
                    Text("Uploading \(appState.outboxPending) files…")
                        .font(.caption)
                } else {
                    Text("Syncing…")
                        .font(.caption)
                }

            case .synced:
                Text("Up to date")
                    .font(.caption)
                    .foregroundStyle(.secondary)

            case .error:
                Image(systemName: "exclamationmark.circle.fill")
                    .foregroundStyle(.red)
                    .font(.caption)
                Text(appState.daemonConnected ? (appState.error ?? "Error") : "Daemon not responding")
                    .font(.caption)
                    .lineLimit(1)

            case .offline:
                Text("Offline")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Spacer()

            Text("\(appState.files.count) files")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(.bar)
    }
}
