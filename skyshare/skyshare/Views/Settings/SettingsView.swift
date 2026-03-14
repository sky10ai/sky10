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
        .frame(width: 500, height: 380)
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
    @AppStorage("s3Provider") private var providerID = "backblaze"
    @AppStorage("s3Bucket") private var bucket = ""
    @AppStorage("s3Region") private var region = ""
    @AppStorage("s3Endpoint") private var endpoint = ""
    @AppStorage("s3AccountID") private var accountID = ""
    @AppStorage("s3ForcePathStyle") private var forcePathStyle = false

    private let labelWidth: CGFloat = 90

    private var provider: StorageProvider {
        StorageProvider.all.first { $0.id == providerID } ?? .backblaze
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            // Provider
            row("Provider") {
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

            // Bucket
            row("Bucket") {
                TextField("my-bucket", text: $bucket)
                    .textFieldStyle(.roundedBorder)
            }

            // Region
            row("Region") {
                if provider.regions.count > 1 {
                    Picker("", selection: $region) {
                        ForEach(provider.regions) { r in
                            Text(r.label).tag(r.id)
                        }
                    }
                    .labelsHidden()
                } else {
                    Text(provider.regions.first?.label ?? "—")
                        .foregroundStyle(.secondary)
                }
            }

            // Account ID (Cloudflare) / Endpoint (MinIO) / computed endpoint (others)
            row("Endpoint") {
                if provider.needsAccountID {
                    TextField("Account ID", text: $accountID)
                        .textFieldStyle(.roundedBorder)
                } else if provider.id == "minio" {
                    TextField("http://localhost:9000", text: $endpoint)
                        .textFieldStyle(.roundedBorder)
                } else {
                    Text(provider.endpoint(region: region, accountID: accountID))
                        .font(.system(.caption, design: .monospaced))
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
            }

            Divider()

            HStack {
                Button("Test Connection") {
                    // TODO: call skyfs.info via RPC to verify
                }

                Spacer()

                Link("Setup Guide", destination: URL(string: provider.helpURL)!)
                    .font(.caption)
            }
        }
        .padding()
        .onAppear {
            if region.isEmpty, let first = provider.regions.first {
                region = first.id
            }
            forcePathStyle = provider.forcePathStyle
        }
    }

    private func row<Content: View>(_ label: String, @ViewBuilder content: () -> Content) -> some View {
        HStack(alignment: .firstTextBaseline) {
            Text(label)
                .frame(width: labelWidth, alignment: .trailing)
                .foregroundStyle(.secondary)
            content()
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
