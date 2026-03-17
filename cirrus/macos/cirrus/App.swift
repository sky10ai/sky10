import SwiftUI

@main
struct CirrusApp: App {
    @StateObject private var appState = AppState()
    @State private var animationFrame = 0
    private let animationTimer = Timer.publish(every: 0.8, on: .main, in: .common).autoconnect()

    // Subtle 3-frame animation: cloud with arrow cycling up/down
    private let syncFrames = [
        "icloud.and.arrow.up.fill",
        "icloud.fill",
        "icloud.and.arrow.down.fill",
    ]

    init() {
        FileProviderManager.register()
    }

    var body: some Scene {
        MenuBarExtra {
            MenuBarView()
                .environmentObject(appState)
        } label: {
            if appState.syncState == .syncing {
                Image(systemName: syncFrames[animationFrame % syncFrames.count])
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

        Settings {
            SettingsView()
                .environmentObject(appState)
        }
    }
}
