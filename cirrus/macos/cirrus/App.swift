import SwiftUI

@main
struct CirrusApp: App {
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

        WindowGroup("Cirrus", id: "browser") {
            if appState.onboardingComplete {
                BrowserView()
                    .environmentObject(appState)
                    .frame(minWidth: 800, minHeight: 500)
            } else {
                OnboardingView()
                    .environmentObject(appState)
            }
        }
        .defaultSize(width: appState.onboardingComplete ? 1000 : 480,
                     height: appState.onboardingComplete ? 600 : 400)

        Settings {
            SettingsView()
                .environmentObject(appState)
        }
    }
}
