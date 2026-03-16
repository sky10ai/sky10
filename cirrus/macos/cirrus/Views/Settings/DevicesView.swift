import AppKit
import SwiftUI

/// Shows registered devices and allows generating invite codes.
struct DevicesView: View {
    @EnvironmentObject var appState: AppState
    @State private var devices: [DeviceInfo] = []
    @State private var thisDevice: String = ""
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
                    Task { await generateInvite() }
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
                    Text("No devices registered yet")
                        .foregroundStyle(.secondary)
                        .font(.caption)
                }
                .frame(maxWidth: .infinity, minHeight: 60)
            } else {
                VStack(spacing: 0) {
                    ForEach(devices, id: \.pubkey) { device in
                        HStack(spacing: 10) {
                            Image(systemName: iconName(for: device))
                                .foregroundStyle(device.pubkey == thisDevice ? .blue : .secondary)
                                .frame(width: 24)
                            VStack(alignment: .leading, spacing: 2) {
                                HStack(spacing: 6) {
                                    Text(device.name)
                                        .fontWeight(.medium)
                                    if device.pubkey == thisDevice {
                                        Text("(this device)")
                                            .font(.caption2)
                                            .foregroundStyle(.blue)
                                    }
                                }
                                Text(String(device.pubkey.prefix(24)) + "...")
                                    .font(.caption2)
                                    .foregroundStyle(.tertiary)
                                    .monospaced()
                            }
                            Spacer()
                            Text("Joined " + String(device.joined.prefix(10)))
                                .font(.caption2)
                                .foregroundStyle(.tertiary)
                        }
                        .padding(.vertical, 8)
                        .padding(.horizontal, 4)
                        if device.pubkey != devices.last?.pubkey {
                            Divider()
                        }
                    }
                }
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
                        Button(copied ? "Copied!" : "Copy") {
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
                    Text("On the new device, run:")
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                    Text("sky10 fs join <code>")
                        .font(.system(.caption2, design: .monospaced))
                        .foregroundStyle(.tertiary)
                }
            }
        }
        .padding()
        .task {
            await loadDevices()
        }
    }

    private func iconName(for device: DeviceInfo) -> String {
        switch device.platform {
        case "macOS": return "laptopcomputer"
        case "Linux": return "server.rack"
        default: return "desktopcomputer"
        }
    }

    private func loadDevices() async {
        do {
            let response = try await appState.client.listDevices()
            devices = response.devices
            thisDevice = response.thisDevice
        } catch {
            // silently fail — backend might not be ready
        }
    }

    private func generateInvite() async {
        generating = true
        inviteCode = nil
        do {
            inviteCode = try await appState.client.generateInvite()
        } catch {
            inviteCode = "Error: \(error.localizedDescription)"
        }
        generating = false
    }
}
