import AppKit
import SwiftUI

/// File list table with sortable columns.
struct FileListView: View {
    let files: [FileNode]
    @Binding var selectedFile: FileNode?
    @Binding var sortOrder: [KeyPathComparator<FileNode>]
    @EnvironmentObject var appState: AppState

    var body: some View {
        Table(files, selection: Binding(
            get: { selectedFile?.id },
            set: { id in selectedFile = files.first { $0.id == id } }
        ), sortOrder: $sortOrder) {
            TableColumn("Name", value: \.name) { file in
                FileRowView(file: file)
            }
            .width(min: 200)

            TableColumn("Size") { file in
                Text(file.formattedSize)
                    .foregroundStyle(.secondary)
            }
            .width(80)

            TableColumn("Modified") { file in
                Text(file.formattedDate)
                    .foregroundStyle(.secondary)
            }
            .width(140)

            TableColumn("Namespace", value: \.namespace) { file in
                Text(file.namespace)
                    .foregroundStyle(.secondary)
            }
            .width(100)
        }
        .contextMenu(forSelectionType: String.self) { ids in
            if let id = ids.first, let file = files.first(where: { $0.id == id }) {
                Button("Download") {
                    downloadFile(file)
                }
                Button("Copy Path") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(file.path, forType: .string)
                }
                Divider()
                Button("Delete", role: .destructive) {
                    Task { await appState.removeFile(path: file.path) }
                }
            }
        } primaryAction: { ids in
            if let id = ids.first, let file = files.first(where: { $0.id == id }) {
                downloadFile(file)
            }
        }
        .overlay {
            if files.isEmpty {
                ContentUnavailableView(
                    "No Files",
                    systemImage: "doc",
                    description: Text("Upload files or sync a directory to get started.")
                )
            }
        }
    }

    private func downloadFile(_ file: FileNode) {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = file.name
        if panel.runModal() == .OK, let url = panel.url {
            Task {
                await appState.downloadFile(
                    remotePath: file.path,
                    localPath: url.path
                )
            }
        }
    }
}
