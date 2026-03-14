import AppKit
import SwiftUI

/// Application settings.
struct SettingsView: View {
    @EnvironmentObject var appState: AppState

    var body: some View {
        TabView {
            GeneralSettingsView()
                .tabItem { Label("General", systemImage: "gear") }

            StorageSettingsView()
                .tabItem { Label("Storage", systemImage: "externaldrive") }

            AccountSettingsView()
                .environmentObject(appState)
                .tabItem { Label("Account", systemImage: "person") }
        }
        .frame(width: 450, height: 300)
    }
}

struct GeneralSettingsView: View {
    @AppStorage("syncDirectory") private var syncDirectory = ""
    @AppStorage("launchAtLogin") private var launchAtLogin = false
    @AppStorage("pollInterval") private var pollInterval = 30

    var body: some View {
        Form {
            HStack {
                TextField("Sync Directory", text: $syncDirectory)
                Button("Choose...") {
                    let panel = NSOpenPanel()
                    panel.canChooseFiles = false
                    panel.canChooseDirectories = true
                    if panel.runModal() == .OK, let url = panel.url {
                        syncDirectory = url.path
                    }
                }
            }

            Toggle("Launch at Login", isOn: $launchAtLogin)

            Picker("Poll Interval", selection: $pollInterval) {
                Text("15 seconds").tag(15)
                Text("30 seconds").tag(30)
                Text("60 seconds").tag(60)
                Text("5 minutes").tag(300)
            }
        }
        .padding()
    }
}

struct StorageSettingsView: View {
    @AppStorage("s3Bucket") private var bucket = ""
    @AppStorage("s3Region") private var region = "us-east-1"
    @AppStorage("s3Endpoint") private var endpoint = ""

    var body: some View {
        Form {
            TextField("Bucket", text: $bucket)
            TextField("Region", text: $region)
            TextField("Endpoint (optional)", text: $endpoint)

            Button("Test Connection") {
                // TODO: call skyfs.info via RPC to verify
            }
        }
        .padding()
    }
}

struct AccountSettingsView: View {
    @EnvironmentObject var appState: AppState

    var body: some View {
        Form {
            if let info = appState.storeInfo {
                LabeledContent("Identity") {
                    Text(info.id)
                        .font(.system(.body, design: .monospaced))
                        .textSelection(.enabled)
                }

                LabeledContent("Files") {
                    Text("\(info.fileCount)")
                }

                LabeledContent("Total Size") {
                    Text(ByteCountFormatter.string(fromByteCount: info.totalSize, countStyle: .file))
                }
            } else {
                Text("Not connected to backend")
                    .foregroundStyle(.secondary)
            }
        }
        .padding()
    }
}
