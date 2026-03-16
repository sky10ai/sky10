import AppKit
import SwiftUI

/// Application settings.
struct SettingsView: View {
    @EnvironmentObject var appState: AppState

    var body: some View {
        TabView {
            DrivesView()
                .environmentObject(appState)
                .tabItem { Label("Drives", systemImage: "folder.badge.gearshape") }

            DevicesView()
                .environmentObject(appState)
                .tabItem { Label("Devices", systemImage: "desktopcomputer") }

            StorageSettingsView()
                .environmentObject(appState)
                .tabItem { Label("Storage", systemImage: "externaldrive") }

            GeneralSettingsView()
                .environmentObject(appState)
                .tabItem { Label("General", systemImage: "gear") }
        }
        .frame(width: 500, height: 420)
    }
}

struct GeneralSettingsView: View {
    @EnvironmentObject var appState: AppState
    @AppStorage("launchAtLogin") private var launchAtLogin = false
    @AppStorage("pollInterval") private var pollInterval = 30

    var body: some View {
        Form {
            Toggle("Launch at Login", isOn: $launchAtLogin)

            Picker("Poll Interval", selection: $pollInterval) {
                Text("15 seconds").tag(15)
                Text("30 seconds").tag(30)
                Text("60 seconds").tag(60)
                Text("5 minutes").tag(300)
            }

            Divider()

            if let info = appState.storeInfo {
                LabeledContent("Identity") {
                    Text(info.id)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                }
                LabeledContent("Files") {
                    Text("\(info.fileCount)")
                }
                LabeledContent("Total Size") {
                    Text(ByteCountFormatter.string(fromByteCount: info.totalSize, countStyle: .file))
                }
            } else {
                HStack {
                    Image(systemName: "exclamationmark.triangle")
                        .foregroundStyle(.orange)
                    Text("Not connected to backend")
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding()
    }
}

struct StorageSettingsView: View {
    @EnvironmentObject var appState: AppState
    @AppStorage("s3Endpoint") private var endpoint = ""
    @AppStorage("s3Bucket") private var bucket = ""
    @AppStorage("s3AccessKeyID") private var accessKeyID = ""
    @AppStorage("s3SecretAccessKey") private var secretAccessKey = ""
    @AppStorage("s3ForcePathStyle") private var forcePathStyle = false
    @State private var testing = false
    @State private var testResult: String?
    @State private var testOK = false

    private let labelWidth: CGFloat = 90

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 4) {
                Text(StorageProviderDocs.headline)
                    .font(.headline)
                HStack(spacing: 0) {
                    Text(StorageProviderDocs.description)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(3)
                    Text(" ")
                        .font(.caption)
                    Link("Learn More...", destination: StorageProviderDocs.learnMoreURL)
                        .font(.caption)
                }
            }
            .padding(.bottom, 12)

            Grid(alignment: .leading, verticalSpacing: 10) {
                GridRow {
                    label("Endpoint")
                    TextField("https://atl1.digitaloceanspaces.com", text: $endpoint)
                        .textFieldStyle(.roundedBorder)
                }

                GridRow {
                    label("Bucket")
                    TextField("my-bucket", text: $bucket)
                        .textFieldStyle(.roundedBorder)
                }

                GridRow {
                    label("Access Key")
                    SecureField("access key", text: $accessKeyID)
                        .textFieldStyle(.roundedBorder)
                }

                GridRow {
                    label("Secret Key")
                    SecureField("secret key", text: $secretAccessKey)
                        .textFieldStyle(.roundedBorder)
                }

                GridRow {
                    label("")
                    Toggle("Path-style addressing (MinIO)", isOn: $forcePathStyle)
                        .font(.caption)
                }

                GridRow {
                    Color.clear.gridCellUnsizedAxes([.horizontal, .vertical])
                    Divider()
                }

                GridRow {
                    Color.clear.gridCellUnsizedAxes([.horizontal, .vertical])
                    HStack(spacing: 8) {
                        Button("Save & Test") {
                            saveAndTest()
                        }
                        .disabled(endpoint.isEmpty || bucket.isEmpty || accessKeyID.isEmpty || secretAccessKey.isEmpty)

                        if testing {
                            ProgressView()
                                .scaleEffect(0.6)
                                .frame(width: 16, height: 16)
                            Text("Connecting...")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        } else if let result = testResult {
                            Image(systemName: testOK ? "checkmark.circle.fill" : "xmark.circle.fill")
                                .foregroundStyle(testOK ? .green : .red)
                            Text(result)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }

                        Spacer()
                    }
                }
            }
            Spacer()
        }
        .padding(.horizontal)
        .padding(.top, 8)
    }

    private func label(_ text: String) -> some View {
        Text(text)
            .frame(width: labelWidth, alignment: .trailing)
            .foregroundStyle(.secondary)
    }

    private func saveAndTest() {
        testing = true
        testResult = nil
        testOK = false

        // Restart daemon with new credentials
        appState.daemonManager.restart()

        Task {
            // Wait for daemon to start
            try? await Task.sleep(for: .seconds(8))

            do {
                let info = try await appState.client.getInfo()
                testResult = "Connected — \(info.fileCount) files"
                testOK = true
                await appState.refresh()
            } catch {
                testResult = error.localizedDescription
                testOK = false
            }
            testing = false
        }
    }
}
