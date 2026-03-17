import AppKit
import SwiftUI

/// Slim inspector panel for selected file info.
struct InspectorView: View {
    let file: FileNode
    @EnvironmentObject var appState: AppState

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            // Icon + name
            HStack(spacing: 10) {
                Image(systemName: file.icon)
                    .font(.system(size: 32))
                    .foregroundStyle(.blue)
                VStack(alignment: .leading, spacing: 2) {
                    Text(file.name)
                        .font(.headline)
                        .lineLimit(2)
                    Text(file.path)
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                        .lineLimit(1)
                }
            }

            Divider()

            Grid(alignment: .leading, horizontalSpacing: 8, verticalSpacing: 6) {
                GridRow {
                    Text("Size").foregroundStyle(.secondary)
                    Text(file.formattedSize)
                }
                GridRow {
                    Text("Modified").foregroundStyle(.secondary)
                    Text(file.formattedDate)
                }
                GridRow {
                    Text("Chunks").foregroundStyle(.secondary)
                    Text("\(file.chunks)")
                }
                GridRow {
                    Text("Checksum").foregroundStyle(.secondary)
                    Text(String(file.checksum.prefix(16)) + "...")
                        .font(.caption2)
                        .monospaced()
                }
            }
            .font(.caption)

            Spacer()

            HStack(spacing: 8) {
                Button("Download") { downloadFile() }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                Button("Delete", role: .destructive) {
                    Task { await appState.removeFile(path: file.path) }
                }
                .buttonStyle(.bordered)
                .controlSize(.small)
            }
        }
        .padding()
    }

    private func downloadFile() {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = file.name
        if panel.runModal() == .OK, let url = panel.url {
            Task {
                await appState.downloadFile(remotePath: file.path, localPath: url.path)
            }
        }
    }
}
