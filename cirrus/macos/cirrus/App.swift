import SwiftUI

@main
struct CirrusApp: App {
    @StateObject private var appState = AppState()
    @State private var animationFrame = 0
    private let animationTimer = Timer.publish(every: 0.5, on: .main, in: .common).autoconnect()

    // 3-frame animation: cloud hump shifts left → center → right
    private let syncFrames = ["cloud_sync_1", "cloud_sync_2", "cloud_sync_3"]

    init() {
        FileProviderManager.register()
    }

    var body: some Scene {
        MenuBarExtra {
            MenuBarView()
                .environmentObject(appState)
        } label: {
            if appState.syncState == .syncing {
                Image(syncFrames[animationFrame % syncFrames.count])
                    .onReceive(animationTimer) { _ in
                        if appState.syncState == .syncing {
                            animationFrame += 1
                        }
                    }
            } else {
                Image(systemName: appState.syncState.icon)
            }
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

        WindowGroup("Developer Tools", id: "devtools") {
            DevToolsView()
                .environmentObject(appState)
        }
        .defaultSize(width: 450, height: 550)

        Settings {
            SettingsView()
                .environmentObject(appState)
        }
    }
}
