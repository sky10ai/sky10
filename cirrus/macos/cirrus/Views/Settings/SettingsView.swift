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

            StorageSettingsView()
                .environmentObject(appState)
                .tabItem { Label("Storage", systemImage: "externaldrive") }

            GeneralSettingsView()
                .environmentObject(appState)
                .tabItem { Label("General", systemImage: "gear") }
        }
        .frame(width: 500, height: 400)
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
    @AppStorage("s3Provider") private var providerID = "backblaze"
    @AppStorage("s3Bucket") private var bucket = ""
    @AppStorage("s3Region") private var region = ""
    @AppStorage("s3Endpoint") private var endpoint = ""
    @AppStorage("s3AccountID") private var accountID = ""
    @AppStorage("s3ForcePathStyle") private var forcePathStyle = false
    @AppStorage("s3AccessKeyID") private var accessKeyID = ""
    @AppStorage("s3SecretAccessKey") private var secretAccessKey = ""
    @State private var connectionStatus: String?
    @State private var connectionOK = false

    private let labelWidth: CGFloat = 90

    private var provider: StorageProvider {
        StorageProvider.all.first { $0.id == providerID } ?? .backblaze
    }

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
                    label("Provider")
                    Picker("", selection: $providerID) {
                        ForEach(StorageProvider.all) { p in
                            Label(p.name, systemImage: p.icon).tag(p.id)
                        }
                    }
                    .labelsHidden()
                    .onChange(of: providerID) { _, newValue in
                        applyProviderDefaults(newValue)
                    }
                }

                GridRow {
                    label("Bucket")
                    TextField("my-bucket", text: $bucket)
                        .textFieldStyle(.roundedBorder)
                }

                GridRow {
                    label("Region")
                    Picker("", selection: $region) {
                        ForEach(provider.regions) { r in
                            Text(r.label).tag(r.id)
                        }
                    }
                    .labelsHidden()
                    .disabled(provider.regions.count <= 1)
                }

                GridRow {
                    label(provider.needsAccountID ? "Account ID" : "Endpoint")
                    TextField(
                        provider.id == "minio" ? "http://localhost:9000" : "auto",
                        text: endpointBinding
                    )
                    .textFieldStyle(.roundedBorder)
                    .disabled(!isEndpointEditable)
                }

                GridRow {
                    label("Access Key")
                    SecureField("S3_ACCESS_KEY_ID", text: $accessKeyID)
                        .textFieldStyle(.roundedBorder)
                }

                GridRow {
                    label("Secret Key")
                    SecureField("S3_SECRET_ACCESS_KEY", text: $secretAccessKey)
                        .textFieldStyle(.roundedBorder)
                }

                GridRow {
                    Color.clear.gridCellUnsizedAxes([.horizontal, .vertical])
                    Divider()
                }

                GridRow {
                    Color.clear.gridCellUnsizedAxes([.horizontal, .vertical])
                    HStack {
                        Button("Save & Test") {
                            // Restart daemon with new credentials
                            appState.daemonManager.restart()
                            connectionStatus = nil
                            connectionOK = false

                            Task {
                                try? await Task.sleep(for: .seconds(8))
                                do {
                                    let info = try await appState.client.getInfo()
                                    connectionStatus = "Connected — \(info.fileCount) files"
                                    connectionOK = true
                                    await appState.refresh()
                                } catch {
                                    connectionStatus = error.localizedDescription
                                    connectionOK = false
                                }
                            }
                        }
                        .disabled(bucket.isEmpty || accessKeyID.isEmpty || secretAccessKey.isEmpty)

                        if let status = connectionStatus {
                            Image(systemName: connectionOK ? "checkmark.circle.fill" : "xmark.circle.fill")
                                .foregroundStyle(connectionOK ? .green : .red)
                            Text(status)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }

                        Spacer()
                        Link("Setup Guide", destination: URL(string: provider.helpURL)!)
                            .font(.caption)
                    }
                }
            }
            Spacer()
        }
        .padding(.horizontal)
        .padding(.top, 8)
        .onAppear {
            if region.isEmpty, let first = provider.regions.first {
                region = first.id
            }
            forcePathStyle = provider.forcePathStyle
            updateComputedEndpoint()
        }
    }

    private func label(_ text: String) -> some View {
        Text(text)
            .frame(width: labelWidth, alignment: .trailing)
            .foregroundStyle(.secondary)
    }

    private var isEndpointEditable: Bool {
        provider.id == "minio" || provider.needsAccountID
    }

    private var endpointBinding: Binding<String> {
        if provider.needsAccountID {
            return $accountID
        }
        if provider.id == "minio" {
            return $endpoint
        }
        return .constant(provider.endpoint(region: region, accountID: accountID))
    }

    private func updateComputedEndpoint() {
        if !isEndpointEditable {
            endpoint = provider.endpoint(region: region, accountID: accountID)
        }
    }

    private func applyProviderDefaults(_ newProviderID: String) {
        guard let newProvider = StorageProvider.all.first(where: { $0.id == newProviderID }) else { return }
        if let firstRegion = newProvider.regions.first {
            region = firstRegion.id
        }
        if newProvider.id != "minio" {
            endpoint = newProvider.endpoint(region: region, accountID: accountID)
        }
        forcePathStyle = newProvider.forcePathStyle
        accountID = ""
    }
}
