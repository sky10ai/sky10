import AppKit
import SwiftUI

/// Shows registered devices and allows generating invite codes.
struct DevicesView: View {
    @EnvironmentObject var appState: AppState
    @State private var devices: [DeviceResult] = []
    @State private var inviteCode: String?
    @State private var generating = false
    @State private var copied = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Synced Devices")
                    .font(.headline)
                Spacer()
                Button {
                    generateInvite()
                } label: {
                    HStack(spacing: 4) {
                        if generating {
                            ProgressView().scaleEffect(0.5)
                        } else {
                            Image(systemName: "plus")
                        }
                        Text("Add Device")
                    }
                }
                .disabled(generating)
            }

            if devices.isEmpty {
                VStack(spacing: 8) {
                    Image(systemName: "desktopcomputer")
                        .font(.system(size: 28))
                        .foregroundStyle(.secondary)
                    Text("Only this device")
                        .foregroundStyle(.secondary)
                        .font(.caption)
                }
                .frame(maxWidth: .infinity, minHeight: 60)
            } else {
                List(devices, id: \.pubkey) { device in
                    HStack(spacing: 10) {
                        Image(systemName: device.platform == "macOS" ? "laptopcomputer" : "desktopcomputer")
                            .foregroundStyle(.blue)
                            .frame(width: 24)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(device.name)
                                .fontWeight(.medium)
                            Text(String(device.pubkey.prefix(24)) + "...")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                                .monospaced()
                        }
                        Spacer()
                        Text(device.joined.prefix(10))
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }
                    .padding(.vertical, 2)
                }
                .listStyle(.inset)
            }

            if let code = inviteCode {
                Divider()
                VStack(alignment: .leading, spacing: 6) {
                    Text("Share this code with the other device:")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    HStack {
                        Text(code)
                            .font(.system(.caption2, design: .monospaced))
                            .lineLimit(2)
                            .textSelection(.enabled)
                        Spacer()
                        Button(copied ? "Copied" : "Copy") {
                            NSPasteboard.general.clearContents()
                            NSPasteboard.general.setString(code, forType: .string)
                            copied = true
                            DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
                                copied = false
                            }
                        }
                        .buttonStyle(.bordered)
                        .controlSize(.small)
                    }
                }
            }
        }
        .padding()
        .task {
            await loadDevices()
        }
    }

    private func loadDevices() async {
        // Uses RPC via a method we'll add to SkyClient
        // For now, just show empty until wired
    }

    private func generateInvite() {
        generating = true
        inviteCode = nil
        // Uses RPC via SkyClient — for now show placeholder
        DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
            self.inviteCode = "Run in terminal: sky10 fs invite"
            self.generating = false
        }
    }
}

private struct DeviceResult: Codable, Hashable {
    let pubkey: String
    let name: String
    let joined: String
    let platform: String?
}
