import AppKit
import SwiftUI

/// Main file browser — Finder-like with sidebar, file tree, and inspector.
struct BrowserView: View {
    @EnvironmentObject var appState: AppState
    @State private var selectedFile: FileNode?
    @State private var searchText = ""
    @State private var showActivityLog = false
    @State private var selectedDrive: String? = nil
    @State private var showInspector = false

    var body: some View {
        NavigationSplitView {
            SidebarView(selectedDrive: $selectedDrive)
                .environmentObject(appState)
        } content: {
            FileTreeView(
                root: buildTree(from: displayedFiles),
                selectedFile: $selectedFile
            )
            .environmentObject(appState)
            .searchable(text: $searchText, prompt: "Search files")
            .navigationTitle("Cirrus")
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
                        showInspector.toggle()
                    } label: {
                        Image(systemName: "sidebar.right")
                    }
                    .help("Inspector")

                    Button {
                        showActivityLog.toggle()
                    } label: {
                        Image(systemName: "list.bullet.rectangle")
                    }
                    .help("Activity log")
                }
            }
        } detail: {
            if showInspector, let file = selectedFile {
                InspectorView(file: file)
                    .environmentObject(appState)
                    .frame(minWidth: 220, maxWidth: 280)
            }
        }
        .frame(minWidth: 800, minHeight: 500)
        .overlay(alignment: .bottom) {
            SyncStatusBar()
                .environmentObject(appState)
        }
        .conflictAlert(path: $appState.conflictPath) { resolution in
            switch resolution {
            case .keepLatest: break
            case .keepBoth: break
            }
        }
        .popover(isPresented: $showActivityLog) {
            ActivityLogView(log: appState.activityLog)
                .frame(width: 400, height: 300)
        }
    }

    private var displayedFiles: [FileNode] {
        var result = appState.files
        // Filter by selected drive's namespace
        if let drive = selectedDrive {
            result = result.filter { $0.namespace == drive }
        }
        if !searchText.isEmpty {
            result = result.filter {
                $0.name.localizedCaseInsensitiveContains(searchText) ||
                $0.path.localizedCaseInsensitiveContains(searchText)
            }
        }
        return result
    }

    private func uploadFile() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = true
        panel.canChooseDirectories = false
        if panel.runModal() == .OK {
            for url in panel.urls {
                let remotePath = url.lastPathComponent
                Task {
                    await appState.uploadFile(localPath: url.path, remotePath: remotePath)
                }
            }
        }
    }
}
