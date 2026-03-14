import AppKit
import SwiftUI

/// Menu bar dropdown content.
struct MenuBarView: View {
    @EnvironmentObject var appState: AppState
    @Environment(\.openWindow) var openWindow

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            // Status
            HStack {
                Image(systemName: appState.syncState.icon)
                    .foregroundStyle(statusColor)
                Text(statusText)
                    .font(.headline)
            }
            .padding(.horizontal, 8)
            .padding(.top, 4)

            Divider()

            // Actions
            Button("Open Sky Browser") {
                openWindow(id: "browser")
            }
            .keyboardShortcut("b")

            Button("Sync Now") {
                Task { await appState.refresh() }
            }
            .keyboardShortcut("r")

            Divider()

            if let info = appState.storeInfo {
                Text("\(info.fileCount) files · \(ByteCountFormatter.string(fromByteCount: info.totalSize, countStyle: .file))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 8)

                Divider()
            }

            SettingsLink {
                Text("Preferences...")
            }
            .keyboardShortcut(",")

            Divider()

            Button("Quit skyshare") {
                appState.daemonManager.stop()
                NSApplication.shared.terminate(nil)
            }
            .keyboardShortcut("q")
        }
        .padding(4)
        .frame(width: 220)
    }

    private var statusColor: Color {
        switch appState.syncState {
        case .synced:  return .green
        case .syncing: return .blue
        case .error:   return .red
        case .offline: return .gray
        }
    }

    private var statusText: String {
        if let err = appState.error {
            return err
        }
        return appState.syncState.label
    }
}
