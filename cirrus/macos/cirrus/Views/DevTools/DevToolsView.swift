import AppKit
import SwiftUI

/// Developer tools window for daemon control and debugging.
struct DevToolsView: View {
    @EnvironmentObject var appState: AppState
    @State private var daemonStatus = "Unknown"
    @State private var manifestContent = ""
    @State private var opsCount = "—"
    @State private var autoRefresh = true
    @State private var dumpStatus = ""
    @State private var isDumping = false

    private let statusTimer = Timer.publish(every: 2, on: .main, in: .common).autoconnect()

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
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

            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    // Daemon Control
                    GroupBox("Daemon") {
                        VStack(alignment: .leading, spacing: 8) {
                            HStack {
                                Circle()
                                    .fill(daemonStatus == "Running" ? .green : .red)
                                    .frame(width: 8, height: 8)
                                Text(daemonStatus)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                Spacer()
                            }

                            HStack(spacing: 8) {
                                Button("Restart Daemon") {
                                    appState.daemonManager.restart()
                                    checkStatus()
                                }
                                .buttonStyle(.borderedProminent)

                                Button("Stop Daemon") {
                                    appState.daemonManager.stop()
                                    daemonStatus = "Stopped"
                                }
                                .buttonStyle(.bordered)

                                Button("Start Daemon") {
                                    appState.daemonManager.start()
                                    DispatchQueue.main.asyncAfter(deadline: .now() + 2) {
                                        checkStatus()
                                    }
                                }
                                .buttonStyle(.bordered)
                            }
                        }
                        .padding(.vertical, 4)
                    }

                    // Manifest
                    GroupBox("Manifest") {
                        VStack(alignment: .leading, spacing: 8) {
                            HStack {
                                Button("Reload") { loadManifest() }
                                    .controlSize(.small)
                                Button("Reset Manifest") {
                                    resetManifest()
                                }
                                .controlSize(.small)
                                .foregroundStyle(.red)
                                Spacer()
                            }

                            if manifestContent.isEmpty {
                                Text("No manifest loaded")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            } else {
                                Text(manifestContent)
                                    .font(.system(.caption2, design: .monospaced))
                                    .textSelection(.enabled)
                                    .frame(maxHeight: 200)
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
        }
        .frame(width: 450, height: 550)
        .task {
            checkStatus()
            loadManifest()
        }
        .onReceive(statusTimer) { _ in
            checkStatus()
            loadManifest()
        }
    }

    private func checkStatus() {
        Task {
            do {
                _ = try await appState.client.syncStatus()
                daemonStatus = "Running"
            } catch {
                daemonStatus = "Not responding"
            }
        }
    }

    private func loadManifest() {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let path = "\(home)/.sky10/fs/drives/drive_Test/manifest.json"
        guard let data = try? String(contentsOfFile: path, encoding: .utf8) else {
            manifestContent = "No manifest found"
            return
        }
        // Pretty print
        if let json = try? JSONSerialization.jsonObject(with: Data(data.utf8)),
           let pretty = try? JSONSerialization.data(withJSONObject: json, options: .prettyPrinted),
           let str = String(data: pretty, encoding: .utf8) {
            manifestContent = str
        } else {
            manifestContent = data
        }
    }

    private func resetManifest() {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let path = "\(home)/.sky10/fs/drives/drive_Test/manifest.json"
        try? FileManager.default.removeItem(atPath: path)
        manifestContent = "Manifest deleted. Restart daemon to rebuild."
        appState.daemonManager.restart()
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) {
            checkStatus()
            loadManifest()
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
