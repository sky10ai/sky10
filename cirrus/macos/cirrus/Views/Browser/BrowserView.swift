import AppKit
import SwiftUI

enum ViewMode: String, CaseIterable {
    case activity
    case tree
    case list
}

/// Main file browser — Finder-like with sidebar, file tree/list, and inspector.
struct BrowserView: View {
    @EnvironmentObject var appState: AppState
    @State private var selectedFile: FileNode?
    @State private var searchText = ""
    @State private var showActivityLog = false
    // nil = "All Files", otherwise filter by drive namespace
    @State private var selectedDrive: String?
    @State private var showInspector = false
    @AppStorage("viewMode") private var viewMode: ViewMode = .activity

    var body: some View {
        NavigationSplitView {
            SidebarView(selectedDrive: $selectedDrive)
                .environmentObject(appState)
        } detail: {
            detailContent
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

                    // View mode toggle
                    Picker("View", selection: $viewMode) {
                        Image(systemName: "clock.arrow.circlepath")
                            .tag(ViewMode.activity)
                        Image(systemName: "list.bullet.indent")
                            .tag(ViewMode.tree)
                        Image(systemName: "rectangle.split.3x1")
                            .tag(ViewMode.list)
                    }
                    .pickerStyle(.segmented)
                    .help("Activity / Tree / Column view")

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
        }
        .frame(minWidth: 800, minHeight: 500)
        .overlay(alignment: .bottom) {
            SyncStatusBar(fileCount: displayedFiles.count)
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

    @ViewBuilder
    private var detailContent: some View {
        if selectedDrive == "__s3__" {
            S3BrowserView()
                .environmentObject(appState)
        } else {
            HSplitView {
                Group {
                    switch viewMode {
                    case .activity:
                        ActivityView()
                            .environmentObject(appState)
                    case .tree:
                        FileTreeView(
                            root: buildTree(from: displayedFiles, emptyDirs: filteredEmptyDirs),
                            selectedFile: $selectedFile
                        )
                        .environmentObject(appState)
                    case .list:
                        FileColumnView(
                            root: buildTree(from: displayedFiles, emptyDirs: filteredEmptyDirs),
                            selectedFile: $selectedFile
                        )
                        .environmentObject(appState)
                    }
                }
                .frame(minWidth: 300)

                if showInspector, let file = selectedFile {
                    InspectorView(file: file)
                        .environmentObject(appState)
                        .frame(width: 240)
                }
            }
        }
    }

    private var filteredEmptyDirs: [String] {
        if let drive = selectedDrive, drive != "__s3__" {
            return appState.emptyDirs.filter { $0.namespace == drive }.map { $0.path }
        }
        return appState.emptyDirs.map { $0.path }
    }

    private var displayedFiles: [FileNode] {
        var result = appState.files
        if let drive = selectedDrive, drive != "__s3__" {
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
