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
        Grid(alignment: .leading, verticalSpacing: 10) {
            // Provider
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

            // Bucket
            GridRow {
                label("Bucket")
                TextField("my-bucket", text: $bucket)
                    .textFieldStyle(.roundedBorder)
            }

            // Region — always a Picker, single-region providers get one disabled option
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

            // Endpoint — always a TextField. Pre-filled and disabled for auto-computed providers.
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
                Color.clear.gridCellUnsizedAxes([.horizontal, .vertical])
                Divider()
            }

            GridRow {
                Color.clear.gridCellUnsizedAxes([.horizontal, .vertical])
                HStack {
                    Button("Test Connection") {
                        // TODO: call skyfs.info via RPC
                    }
                    Spacer()
                    Link("Setup Guide", destination: URL(string: provider.helpURL)!)
                        .font(.caption)
                }
            }
        }
        .padding()
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
        // Read-only: show computed endpoint
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
