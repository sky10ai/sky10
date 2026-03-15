import SwiftUI

@main
struct SkyshareApp: App {
    @StateObject private var appState = AppState()

    init() {
        FileProviderManager.register()
    }

    var body: some Scene {
        MenuBarExtra {
            MenuBarView()
                .environmentObject(appState)
        } label: {
            Image(systemName: appState.syncState.icon)
        }

        WindowGroup("Sky Browser", id: "browser") {
            BrowserView()
                .environmentObject(appState)
                .frame(minWidth: 800, minHeight: 500)
                .task {
                    await appState.refresh()
                }
        }
        .defaultSize(width: 1000, height: 600)

        Settings {
            SettingsView()
                .environmentObject(appState)
        }
    }
}
