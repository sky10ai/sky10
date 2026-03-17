import AppKit
import SwiftUI

/// Flat sortable table view — like Finder's list view.
struct FileTableView: View {
    let files: [FileNode]
    @Binding var selectedFile: FileNode?
    @EnvironmentObject var appState: AppState
    @State private var sortOrder = [KeyPathComparator(\FileNode.path)]

    var body: some View {
        Table(sortedFiles, selection: Binding(
            get: { selectedFile?.id },
            set: { id in selectedFile = files.first { $0.id == id } }
        ), sortOrder: $sortOrder) {
            TableColumn("Name", value: \.path) { file in
                HStack(spacing: 8) {
                    Image(systemName: file.icon)
                        .foregroundStyle(.blue)
                        .frame(width: 18)
                    Text(file.path)
                        .lineLimit(1)
                }
            }
            .width(min: 250)

            TableColumn("Size") { file in
                Text(file.formattedSize)
                    .foregroundStyle(.secondary)
            }
            .width(70)

            TableColumn("Modified") { file in
                Text(file.formattedDate)
                    .foregroundStyle(.secondary)
            }
            .width(140)
        }
        .contextMenu(forSelectionType: String.self) { ids in
            if let id = ids.first, let file = files.first(where: { $0.id == id }) {
                Button("Download") { downloadFile(file) }
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

    private var sortedFiles: [FileNode] {
        files.sorted(using: sortOrder)
    }

    private func downloadFile(_ file: FileNode) {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = file.name
        if panel.runModal() == .OK, let url = panel.url {
            Task {
                await appState.downloadFile(remotePath: file.path, localPath: url.path)
            }
        }
    }
}
