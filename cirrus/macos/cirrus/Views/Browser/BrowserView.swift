import AppKit
import SwiftUI

/// Main file browser with three-column Finder-like layout.
struct BrowserView: View {
    @EnvironmentObject var appState: AppState
    @State private var selectedFile: FileNode?
    @State private var searchText = ""
    @State private var sortOrder = [KeyPathComparator(\FileNode.name)]
    @State private var showActivityLog = false

    var body: some View {
        VStack(spacing: 0) {
            NavigationSplitView {
                SidebarView(selectedNamespace: $appState.selectedNamespace)
                    .environmentObject(appState)
            } content: {
                FileListView(
                    files: displayedFiles,
                    selectedFile: $selectedFile,
                    sortOrder: $sortOrder
                )
                .environmentObject(appState)
                .searchable(text: $searchText, prompt: "Search files")
                .toolbar {
                    ToolbarItemGroup {
                        Button {
                            uploadFile()
                        } label: {
                            Image(systemName: "square.and.arrow.up")
                        }
                        .help("Upload file")

                        Button {
                            Task { await appState.refresh() }
                        } label: {
                            Image(systemName: "arrow.clockwise")
                        }
                        .help("Refresh")

                        Button {
                            showActivityLog.toggle()
                        } label: {
                            Image(systemName: "list.bullet.rectangle")
                        }
                        .help("Activity log")
                    }
                }
            } detail: {
                if let file = selectedFile {
                    DetailView(file: file)
                        .environmentObject(appState)
                } else {
                    Text("Select a file")
                        .foregroundStyle(.secondary)
                }
            }

            // Sync status bar at the bottom
            SyncStatusBar()
                .environmentObject(appState)
        }
        .navigationTitle("Sky")
        .conflictAlert(path: $appState.conflictPath) { resolution in
            // Handle conflict resolution
            switch resolution {
            case .keepLatest:
                break // LWW is default, nothing to do
            case .keepBoth:
                break // TODO: create conflict copy
            }
        }
        .popover(isPresented: $showActivityLog) {
            ActivityLogView(log: appState.activityLog)
                .frame(width: 400, height: 300)
        }
    }

    private var displayedFiles: [FileNode] {
        var result = appState.filteredFiles
        if !searchText.isEmpty {
            result = result.filter {
                $0.name.localizedCaseInsensitiveContains(searchText)
            }
        }
        return result.sorted(using: sortOrder)
    }

    private func uploadFile() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = true
        panel.canChooseDirectories = false

        if panel.runModal() == .OK {
            for url in panel.urls {
                let remotePath = url.lastPathComponent
                Task {
                    await appState.uploadFile(
                        localPath: url.path,
                        remotePath: remotePath
                    )
                }
            }
        }
    }
}
