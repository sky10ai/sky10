import AppKit
import SwiftUI

/// Menu bar dropdown content.
struct MenuBarView: View {
    @EnvironmentObject var appState: AppState
    @Environment(\.openWindow) var openWindow
    @Environment(\.openSettings) var openSettings

    var body: some View {
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
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
                NSApplication.shared.activate(ignoringOtherApps: true)
            }
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

        Button("Preferences...") {
            openSettings()
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
                NSApplication.shared.activate(ignoringOtherApps: true)
            }
        }
        .keyboardShortcut(",")

        Divider()

        Button("Developer Tools") {
            openWindow(id: "devtools")
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) {
                NSApplication.shared.activate(ignoringOtherApps: true)
            }
        }
        .keyboardShortcut("d", modifiers: [.command, .option])

        Divider()

        Text("Cirrus v\(appVersion)")
            .disabled(true)

        Button("Quit Cirrus") {
            appState.daemonManager.stop()
            NSApplication.shared.terminate(nil)
        }
        .keyboardShortcut("q")
    }

    private var appVersion: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "?"
    }
}
