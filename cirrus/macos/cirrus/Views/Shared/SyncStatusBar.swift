import SwiftUI

/// Bottom bar showing sync activity in the browser window.
struct SyncStatusBar: View {
    @EnvironmentObject var appState: AppState

    var body: some View {
        HStack(spacing: 8) {
            switch appState.syncState {
            case .syncing:
                ProgressView()
                    .scaleEffect(0.6)
                    .frame(width: 16, height: 16)
                Text("Syncing...")
                    .font(.caption)

            case .synced:
                Image(systemName: "checkmark.circle.fill")
                    .foregroundStyle(.green)
                    .font(.caption)
                Text("Up to date")
                    .font(.caption)

            case .error:
                Image(systemName: "exclamationmark.circle.fill")
                    .foregroundStyle(.red)
                    .font(.caption)
                Text(appState.error ?? "Error")
                    .font(.caption)
                    .lineLimit(1)

            case .offline:
                Image(systemName: "icloud.slash")
                    .foregroundStyle(.gray)
                    .font(.caption)
                Text("Offline")
                    .font(.caption)
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
