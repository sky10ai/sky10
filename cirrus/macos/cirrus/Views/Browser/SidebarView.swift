import SwiftUI

/// Sidebar showing synced drives and storage info.
struct SidebarView: View {
    @EnvironmentObject var appState: AppState
    @Binding var selectedDrive: String?

    var body: some View {
        List(selection: $selectedDrive) {
            Section("Drives") {
                Label("All Files", systemImage: "externaldrive.fill")
                    .tag(nil as String?)

                ForEach(appState.drives, id: \.id) { drive in
                    HStack(spacing: 8) {
                        Image(systemName: drive.running ? "folder.fill" : "folder")
                            .foregroundStyle(drive.running ? .blue : .secondary)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(drive.name)
                            Text(drive.localPath)
                                .font(.caption2)
                                .foregroundStyle(.tertiary)
                                .lineLimit(1)
                        }
                    }
                    .tag(drive.namespace as String?)
                }
            }

            if let info = appState.storeInfo {
                Section("Storage") {
                    HStack {
                        Text("Files")
                        Spacer()
                        Text("\(info.fileCount)")
                            .foregroundStyle(.secondary)
                    }
                    HStack {
                        Text("Total")
                        Spacer()
                        Text(ByteCountFormatter.string(fromByteCount: info.totalSize, countStyle: .file))
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
        .listStyle(.sidebar)
        .frame(minWidth: 180)
    }
}
