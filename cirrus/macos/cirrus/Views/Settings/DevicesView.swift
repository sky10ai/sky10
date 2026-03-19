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
    @State private var isLoading = true

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

            if isLoading {
                VStack(spacing: 8) {
                    ProgressView()
                    Text("Loading devices...")
                        .foregroundStyle(.secondary)
                        .font(.caption)
                }
                .frame(maxWidth: .infinity, minHeight: 60)
            } else if devices.isEmpty {
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
                        deviceRow(device)
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
            // If no devices found, retry after 3s (daemon may still be registering)
            if devices.isEmpty {
                try? await Task.sleep(for: .seconds(3))
                await loadDevices()
            }
            isLoading = false
        }
    }

    @ViewBuilder
    private func deviceRow(_ device: DeviceInfo) -> some View {
        let isSelf = device.pubkey == thisDevice
        HStack(spacing: 10) {
            Image(systemName: iconName(for: device))
                .font(.system(size: 20))
                .foregroundStyle(isSelf ? .blue : .secondary)
                .frame(width: 28)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(device.name)
                        .fontWeight(.medium)
                    if isSelf {
                        Text("(this device)")
                            .font(.caption2)
                            .foregroundStyle(.blue)
                    }
                }
                HStack(spacing: 8) {
                    if let ip = device.ip, !ip.isEmpty {
                        Text(ip)
                            .monospaced()
                    }
                    if let location = device.location, !location.isEmpty {
                        Text(location)
                    }
                }
                .font(.caption2)
                .foregroundStyle(.secondary)
                Text("Joined " + formatDate(device.joined))
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }
            Spacer()
            if !isSelf {
                Button {
                    Task { await removeDevice(device) }
                } label: {
                    Image(systemName: "xmark.circle")
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
                .help("Remove device")
            }
        }
        .padding(.vertical, 8)
        .padding(.horizontal, 4)
    }

    private func iconName(for device: DeviceInfo) -> String {
        switch device.platform {
        case "macOS": return "laptopcomputer"
        case "Linux": return "server.rack"
        default: return "desktopcomputer"
        }
    }

    private func formatDate(_ dateStr: String) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        if let date = formatter.date(from: dateStr) {
            let display = DateFormatter()
            display.dateStyle = .medium
            return display.string(from: date)
        }
        return String(dateStr.prefix(10))
    }

    private func loadDevices() async {
        do {
            let response = try await appState.client.listDevices()
            let oldCount = devices.count
            devices = response.devices
            thisDevice = response.thisDevice

            // New device appeared — dismiss the invite code
            if devices.count > oldCount && oldCount > 0 && inviteCode != nil {
                inviteCode = nil
            }
        } catch {
            // silently fail — backend might not be ready
        }
    }

    private func removeDevice(_ device: DeviceInfo) async {
        do {
            try await appState.client.removeDevice(pubkey: device.pubkey)
            devices.removeAll { $0.pubkey == device.pubkey }
        } catch {
            // silently fail
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
