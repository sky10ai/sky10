import AppKit
import SwiftUI

/// Menu bar dropdown content.
struct MenuBarView: View {
    @EnvironmentObject var appState: AppState
    @Environment(\.openWindow) var openWindow

    var body: some View {
        // Status line
        Label(statusText, systemImage: appState.syncState.icon)
            .disabled(true)

        Divider()

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
                .disabled(true)

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

    private var statusText: String {
        if let err = appState.error {
            return err
        }
        return appState.syncState.label
    }
}
