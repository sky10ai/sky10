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

    private var provider: StorageProvider {
        StorageProvider.all.first { $0.id == providerID } ?? .backblaze
    }

    var body: some View {
        Form {
            // Provider picker
            Picker("Provider", selection: $providerID) {
                ForEach(StorageProvider.all) { p in
                    Label(p.name, systemImage: p.icon).tag(p.id)
                }
            }
            .onChange(of: providerID) { _, newValue in
                applyProviderDefaults(newValue)
            }

            // Bucket name (always shown)
            TextField("Bucket", text: $bucket)
                .textFieldStyle(.roundedBorder)

            // Region picker (provider-specific)
            if provider.regions.count > 1 {
                Picker("Region", selection: $region) {
                    ForEach(provider.regions) { r in
                        Text(r.label).tag(r.id)
                    }
                }
            }

            // Account ID (Cloudflare R2)
            if provider.needsAccountID {
                TextField("Account ID", text: $accountID)
                    .textFieldStyle(.roundedBorder)
            }

            // Custom endpoint (MinIO)
            if provider.id == "minio" {
                TextField("Endpoint URL", text: $endpoint)
                    .textFieldStyle(.roundedBorder)
                    .help("e.g. http://localhost:9000")
            }

            // Computed endpoint display (read-only, for non-MinIO)
            if provider.id != "minio" && !region.isEmpty {
                LabeledContent("Endpoint") {
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
            // Set default region if empty
            if region.isEmpty, let first = provider.regions.first {
                region = first.id
            }
            forcePathStyle = provider.forcePathStyle
        }
    }

    private func applyProviderDefaults(_ newProviderID: String) {
        guard let newProvider = StorageProvider.all.first(where: { $0.id == newProviderID }) else { return }

        // Set first region as default
        if let firstRegion = newProvider.regions.first {
            region = firstRegion.id
        }

        // Auto-compute endpoint for non-MinIO providers
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
