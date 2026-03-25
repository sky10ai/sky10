import AppKit
import SwiftUI

/// Developer tools window for daemon control and debugging.
struct DevToolsView: View {
    @EnvironmentObject var appState: AppState
    @State private var selectedTab = 0

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                Image(systemName: "wrench.and.screwdriver")
                    .font(.title2)
                Text("Developer Tools")
                    .font(.title2)
                    .fontWeight(.semibold)
                Spacer()
            }
            .padding()
            .background(Color(nsColor: .windowBackgroundColor))

            Divider()

            // Tabs
            Picker("", selection: $selectedTab) {
                Text("Controls").tag(0)
                Text("Sync Activity").tag(1)
            }
            .pickerStyle(.segmented)
            .padding(.horizontal)
            .padding(.vertical, 8)

            // Content
            switch selectedTab {
            case 0:
                ControlsTab()
            case 1:
                SyncActivityTab()
            default:
                EmptyView()
            }
        }
        .frame(width: 600, height: 650)
    }
}

// MARK: - Controls Tab

struct ControlsTab: View {
    @EnvironmentObject var appState: AppState
    @State private var healthInfo: SkyClient.HealthResult?
    @State private var healthError: String?
    @State private var opsCount = "—"
    @State private var dumpStatus = ""
    @State private var isDumping = false

    private let statusTimer = Timer.publish(every: 2, on: .main, in: .common).autoconnect()

    private var daemonStatus: String {
        if let error = healthError { return error }
        guard let h = healthInfo else { return "Checking…" }
        return "Running"
    }

    private var statusColor: Color {
        if healthError != nil { return .red }
        if healthInfo == nil { return .yellow }
        return .green
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                // Daemon Health
                GroupBox("Daemon") {
                    VStack(alignment: .leading, spacing: 8) {
                        HStack {
                            Circle()
                                .fill(statusColor)
                                .frame(width: 8, height: 8)
                            Text(daemonStatus)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            Spacer()
                        }

                        if let h = healthInfo {
                            Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 4) {
                                GridRow {
                                    Text("Version").font(.caption).foregroundStyle(.secondary)
                                    Text(h.version).font(.system(.caption, design: .monospaced))
                                }
                                GridRow {
                                    Text("Uptime").font(.caption).foregroundStyle(.secondary)
                                    Text(h.uptime).font(.system(.caption, design: .monospaced))
                                }
                                GridRow {
                                    Text("Drives").font(.caption).foregroundStyle(.secondary)
                                    Text("\(h.drivesRunning)/\(h.drives) running").font(.system(.caption, design: .monospaced))
                                }
                                GridRow {
                                    Text("Outbox").font(.caption).foregroundStyle(.secondary)
                                    Text("\(h.outboxPending) pending").font(.system(.caption, design: .monospaced))
                                        .foregroundStyle(h.outboxPending > 0 ? .orange : .secondary)
                                }
                                GridRow {
                                    Text("Last I/O").font(.caption).foregroundStyle(.secondary)
                                    Text(h.lastActivityAgo + " ago").font(.system(.caption, design: .monospaced))
                                }
                            }
                        }

                        HStack(spacing: 8) {
                            Button("Restart Daemon") {
                                appState.daemonManager.restart()
                                checkHealth()
                            }
                            .buttonStyle(.borderedProminent)

                            Button("Stop Daemon") {
                                appState.daemonManager.stop()
                                healthInfo = nil
                                healthError = "Stopped"
                            }
                            .buttonStyle(.bordered)

                            Button("Start Daemon") {
                                appState.daemonManager.start()
                                DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
                                    checkHealth()
                                }
                            }
                            .buttonStyle(.bordered)
                        }
                    }
                    .padding(.vertical, 4)
                }

                // S3 State
                GroupBox("S3 / Ops") {
                    VStack(alignment: .leading, spacing: 8) {
                        HStack {
                            Text("Ops count:")
                                .font(.caption)
                            Text(opsCount)
                                .font(.system(.caption, design: .monospaced))
                            Spacer()
                            Button("Compact") {
                                Task { await compact() }
                            }
                            .controlSize(.small)

                            Button("Reset All") {
                                Task { await resetAll() }
                            }
                            .controlSize(.small)
                            .foregroundStyle(.red)
                        }
                    }
                    .padding(.vertical, 4)
                }

                // Debug Dump
                GroupBox("Debug Dump") {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Upload device state (drives, ops log, outbox, local files) to S3 under debug/")
                            .font(.caption)
                            .foregroundStyle(.secondary)

                        HStack {
                            Button {
                                Task { await uploadDebugDump() }
                            } label: {
                                HStack(spacing: 4) {
                                    if isDumping {
                                        ProgressView().scaleEffect(0.5)
                                    } else {
                                        Image(systemName: "arrow.up.doc")
                                    }
                                    Text("Upload Debug Dump")
                                }
                            }
                            .buttonStyle(.borderedProminent)
                            .disabled(isDumping)

                            if !dumpStatus.isEmpty {
                                Text(dumpStatus)
                                    .font(.caption)
                                    .foregroundStyle(dumpStatus.hasPrefix("Error") ? .red : .green)
                            }
                        }
                    }
                    .padding(.vertical, 4)
                }

                // Quick Actions
                GroupBox("Quick Actions") {
                    VStack(alignment: .leading, spacing: 8) {
                        Button("Open ~/.sky10/ in Finder") {
                            let home = FileManager.default.homeDirectoryForCurrentUser.path
                            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: "\(home)/.sky10")
                        }
                        .controlSize(.small)

                        Button("Force Refresh UI") {
                            Task { await appState.refresh() }
                        }
                        .controlSize(.small)

                        Button("Clear Drive Folder") {
                            clearDriveFolder()
                        }
                        .controlSize(.small)
                        .foregroundStyle(.red)
                    }
                    .padding(.vertical, 4)
                }
            }
            .padding()
        }
        .task { checkHealth() }
        .onReceive(statusTimer) { _ in checkHealth() }
    }

    private func checkHealth() {
        Task {
            do {
                healthInfo = try await appState.client.health()
                healthError = nil
            } catch {
                healthInfo = nil
                healthError = "Not responding"
            }
        }
    }

    private func compact() async {
        opsCount = "Compacting..."
        do {
            let result = try await appState.client.compact(keep: 3)
            opsCount = "Removed \(result.opsRemoved) ops, kept \(result.opsKept)"
        } catch {
            opsCount = "Error: \(error.localizedDescription)"
        }
    }

    private func resetAll() async {
        do {
            let result = try await appState.client.reset()
            opsCount = "Reset: \(result.s3Deleted) S3 + \(result.localDeleted) local deleted"
            appState.daemonManager.restart()
        } catch {
            opsCount = "Error: \(error.localizedDescription)"
        }
    }

    private func uploadDebugDump() async {
        isDumping = true
        dumpStatus = ""
        do {
            let result = try await appState.client.debugDump()
            dumpStatus = "Uploaded: \(result.key) (\(result.size) bytes)"
        } catch {
            dumpStatus = "Error: \(error.localizedDescription)"
        }
        isDumping = false
    }

    private func clearDriveFolder() {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let path = "\(home)/Cirrus/Test"
        if let items = try? FileManager.default.contentsOfDirectory(atPath: path) {
            for item in items where !item.hasPrefix(".") {
                try? FileManager.default.removeItem(atPath: "\(path)/\(item)")
            }
        }
    }
}

// MARK: - Sync Activity Tab

struct SyncActivityTab: View {
    @EnvironmentObject var appState: AppState

    var body: some View {
        VStack(spacing: 0) {
            // Live status banner
            if appState.syncState == .syncing {
                HStack(spacing: 8) {
                    ProgressView()
                        .scaleEffect(0.7)
                        .frame(width: 16, height: 16)
                    Text(appState.syncDetail.isEmpty ? "Syncing..." : appState.syncDetail)
                        .font(.system(.caption, design: .monospaced))
                    Spacer()
                }
                .padding(.horizontal)
                .padding(.vertical, 8)
                .background(.blue.opacity(0.1))

                Divider()
            }

            // Live event log
            if appState.activityLog.entries.isEmpty {
                VStack {
                    Spacer()
                    Image(systemName: "clock")
                        .font(.largeTitle)
                        .foregroundStyle(.secondary)
                    Text("No activity yet")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text("Uploads, downloads, deletes, and sync events will appear here in real time")
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 40)
                    Spacer()
                }
                .frame(maxWidth: .infinity)
            } else {
                List(appState.activityLog.entries) { entry in
                    HStack(spacing: 8) {
                        Image(systemName: entry.icon)
                            .foregroundStyle(colorForType(entry.type))
                            .frame(width: 16)

                        VStack(alignment: .leading, spacing: 2) {
                            Text(entry.path)
                                .font(.system(.caption, design: .monospaced))
                                .lineLimit(1)
                            Text(entry.detail)
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }

                        Spacer()

                        Text(entry.formattedTime)
                            .font(.system(.caption2, design: .monospaced))
                            .foregroundStyle(.tertiary)
                    }
                    .padding(.vertical, 1)
                }
                .listStyle(.plain)
            }
        }
    }

    private func colorForType(_ type: ActivityEntry.ActivityType) -> Color {
        switch type {
        case .uploaded:   return .blue
        case .downloaded: return .green
        case .deleted:    return .orange
        case .conflict:   return .yellow
        case .error:      return .red
        case .synced:     return .green
        case .symlink:    return .purple
        }
    }
}
