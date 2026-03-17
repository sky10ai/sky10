import AppKit
import SwiftUI

/// Menu bar dropdown content.
struct MenuBarView: View {
    @EnvironmentObject var appState: AppState
    @Environment(\.openWindow) var openWindow

    var body: some View {
        Label(statusText, systemImage: appState.syncState.icon)
            .disabled(true)

        let activeDrives = appState.drives.filter { $0.running }
        if !activeDrives.isEmpty {
            ForEach(activeDrives, id: \.id) { drive in
                Label(drive.name, systemImage: "folder.fill")
                    .disabled(true)
            }
        }

        Divider()

        Button("Open Cirrus") {
            openWindow(id: "browser")
            NSApplication.shared.activate(ignoringOtherApps: true)
        }
        .keyboardShortcut("b")

        Button("Refresh") {
            Task { await appState.refresh() }
        }
        .keyboardShortcut("r")

        Divider()

        if let info = appState.storeInfo {
            Text("\(info.fileCount) files")
                .disabled(true)
            Divider()
        }

        SettingsLink {
            Text("Preferences...")
        }
        .keyboardShortcut(",")

        Divider()

        Text("Cirrus v\(appVersion)")
            .disabled(true)

        Button("Quit Cirrus") {
            appState.daemonManager.stop()
            NSApplication.shared.terminate(nil)
        }
        .keyboardShortcut("q")
    }

    private var statusText: String {
        if let err = appState.error {
            return err
        }
        let running = appState.drives.filter { $0.running }.count
        if running > 0 {
            return "Syncing \(running) drive\(running == 1 ? "" : "s")"
        }
        return appState.syncState.label
    }

    private var appVersion: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "?"
    }
}
