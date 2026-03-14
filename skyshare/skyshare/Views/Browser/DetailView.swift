import SwiftUI

/// Detail panel showing file information.
struct DetailView: View {
    let file: FileNode
    @EnvironmentObject var appState: AppState

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                // File icon + name
                HStack {
                    Image(systemName: file.icon)
                        .font(.system(size: 48))
                        .foregroundStyle(.blue)
                    VStack(alignment: .leading) {
                        Text(file.name)
                            .font(.title2)
                            .fontWeight(.semibold)
                        Text(file.path)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }

                Divider()

                // Metadata
                Group {
                    detailRow("Size", file.formattedSize)
                    detailRow("Modified", file.formattedDate)
                    detailRow("Namespace", file.namespace)
                    detailRow("Chunks", "\(file.chunks)")
                    detailRow("Checksum", String(file.checksum.prefix(16)) + "...")
                }

                Divider()

                // Actions
                HStack(spacing: 12) {
                    Button("Download") {
                        downloadFile()
                    }
                    .buttonStyle(.borderedProminent)

                    Button("Delete", role: .destructive) {
                        Task { await appState.removeFile(path: file.path) }
                    }
                    .buttonStyle(.bordered)
                }
            }
            .padding()
        }
        .frame(minWidth: 250)
    }

    private func detailRow(_ label: String, _ value: String) -> some View {
        HStack {
            Text(label)
                .foregroundStyle(.secondary)
                .frame(width: 80, alignment: .leading)
            Text(value)
                .textSelection(.enabled)
            Spacer()
        }
    }

    private func downloadFile() {
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
