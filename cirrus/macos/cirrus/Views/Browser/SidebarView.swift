import AppKit
import SwiftUI

private let allFilesTag = "__all__"
private let s3Tag = "__s3__"

/// Sidebar showing S3 browser, synced drives, and storage info.
struct SidebarView: View {
    @EnvironmentObject var appState: AppState
    @Binding var selectedDrive: String?
    @State private var selection: String = allFilesTag

    var body: some View {
        List(selection: $selection) {
            Section("S3") {
                Label("Bucket", systemImage: "externaldrive.connected.to.line.below")
                    .tag(s3Tag)
            }

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
                    .contextMenu {
                        Button("Reveal in Finder") {
                            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: drive.localPath)
                        }
                    }
                }
            }

            if let info = appState.storeInfo {
                Section("Storage") {
                    HStack {
                        Text("Files")
                        Spacer()
                        Text("\(appState.files.count)")
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
            switch newValue {
            case s3Tag:
                selectedDrive = s3Tag
            case allFilesTag:
                selectedDrive = nil
            default:
                selectedDrive = newValue
            }
        }
    }
}
