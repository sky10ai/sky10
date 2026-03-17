import SwiftUI

private let allFilesTag = "__all__"

/// Sidebar showing synced drives and storage info.
struct SidebarView: View {
    @EnvironmentObject var appState: AppState
    @Binding var selectedDrive: String?
    @State private var selection: String = allFilesTag

    var body: some View {
        List(selection: $selection) {
            Section("Drives") {
                Label("All Files", systemImage: "externaldrive.fill")
                    .tag(allFilesTag)

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
                    .tag(drive.namespace)
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
        .onChange(of: selection) { _, newValue in
            selectedDrive = (newValue == allFilesTag) ? nil : newValue
        }
    }
}
