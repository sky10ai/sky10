import AppKit
import SwiftUI

/// Drives management view — create, list, start/stop sync drives.
struct DrivesView: View {
    @EnvironmentObject var appState: AppState
    @State private var showCreateSheet = false

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Sync Drives")
                    .font(.headline)
                Spacer()
                Button {
                    showCreateSheet = true
                } label: {
                    Image(systemName: "plus")
                }
                .help("Create a new drive")
            }

            Text("Each drive syncs a local folder to encrypted cloud storage.")
                .font(.caption)
                .foregroundStyle(.secondary)

            if appState.syncState == .error || appState.syncState == .offline {
                HStack(spacing: 6) {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                    Text(appState.error ?? "Backend not connected. Run: sky10 serve")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .padding(8)
                .background(Color.orange.opacity(0.1))
                .cornerRadius(6)
            }

            if appState.drives.isEmpty {
                VStack(spacing: 8) {
                    Image(systemName: "externaldrive.badge.plus")
                        .font(.system(size: 32))
                        .foregroundStyle(.secondary)
                    Text("No drives yet")
                        .foregroundStyle(.secondary)
                    Button("Create Drive") {
                        showCreateSheet = true
                    }
                    .buttonStyle(.borderedProminent)
                }
                .frame(maxWidth: .infinity, minHeight: 120)
            } else {
                List {
                    ForEach(appState.drives, id: \.id) { drive in
                        DriveRow(drive: drive)
                            .environmentObject(appState)
                    }
                }
                .listStyle(.inset)
            }
        }
        .padding()
        .task {
            await appState.loadDrives()
        }
        .sheet(isPresented: $showCreateSheet) {
            CreateDriveSheet()
                .environmentObject(appState)
        }
    }
}

/// A single drive row showing name, path, status, and controls.
struct DriveRow: View {
    let drive: SkyClient.DriveInfoResult
    @EnvironmentObject var appState: AppState

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: drive.running ? "externaldrive.fill.badge.checkmark" : "externaldrive")
                .foregroundStyle(drive.running ? .green : .secondary)
                .frame(width: 24)

            VStack(alignment: .leading, spacing: 2) {
                Text(drive.name)
                    .fontWeight(.medium)
                Text(drive.localPath)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer()

            Button {
                Task { await appState.toggleDrive(id: drive.id, running: drive.running) }
            } label: {
                Image(systemName: drive.running ? "pause.circle" : "play.circle")
            }
            .buttonStyle(.borderless)
            .help(drive.running ? "Pause sync" : "Resume sync")

            Button {
                Task { await appState.removeDrive(id: drive.id) }
            } label: {
                Image(systemName: "trash")
                    .foregroundStyle(.red)
            }
            .buttonStyle(.borderless)
            .help("Remove drive")
        }
        .padding(.vertical, 4)
    }
}

/// Sheet for creating a new drive.
struct CreateDriveSheet: View {
    @EnvironmentObject var appState: AppState
    @Environment(\.dismiss) var dismiss
    @State private var name = ""
    @State private var path = ""

    private var defaultPath: String {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        return "\(home)/Cirrus/\(name)"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Create Drive")
                .font(.headline)

            TextField("Drive name", text: $name)
                .textFieldStyle(.roundedBorder)

            HStack {
                TextField("Folder path", text: $path)
                    .textFieldStyle(.roundedBorder)
                Button("Choose...") {
                    let panel = NSOpenPanel()
                    panel.canChooseFiles = false
                    panel.canChooseDirectories = true
                    panel.canCreateDirectories = true
                    if panel.runModal() == .OK, let url = panel.url {
                        path = url.path
                    }
                }
            }

            if !name.isEmpty && path.isEmpty {
                Text("Default: \(defaultPath)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            HStack {
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Spacer()
                Button("Create") {
                    let finalPath = path.isEmpty ? defaultPath : path
                    Task {
                        await appState.createDrive(name: name, path: finalPath)
                        dismiss()
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(name.isEmpty)
                .keyboardShortcut(.defaultAction)
            }
        }
        .padding()
        .frame(width: 400)
    }
}
