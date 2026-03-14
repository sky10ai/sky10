import SwiftUI

// MARK: - Sample Data for Previews

extension FileNode {
    static let sampleFiles: [FileNode] = [
        FileNode(id: "journal/2026-03-14.md", path: "journal/2026-03-14.md",
                 name: "2026-03-14.md", size: 4523,
                 modified: "2026-03-14T09:15:00Z", checksum: "abcdef1234567890",
                 namespace: "journal", chunks: 1),
        FileNode(id: "journal/2026-03-13.md", path: "journal/2026-03-13.md",
                 name: "2026-03-13.md", size: 8901,
                 modified: "2026-03-13T22:00:00Z", checksum: "567890abcdef1234",
                 namespace: "journal", chunks: 1),
        FileNode(id: "financial/q4-report.pdf", path: "financial/q4-report.pdf",
                 name: "q4-report.pdf", size: 3_400_000,
                 modified: "2026-01-15T14:00:00Z", checksum: "fedcba9876543210",
                 namespace: "financial", chunks: 4),
        FileNode(id: "photos/sunset.jpg", path: "photos/sunset.jpg",
                 name: "sunset.jpg", size: 2_100_000,
                 modified: "2026-03-10T18:30:00Z", checksum: "1234567890abcdef",
                 namespace: "photos", chunks: 2),
        FileNode(id: "notes.md", path: "notes.md",
                 name: "notes.md", size: 256,
                 modified: "2026-03-14T12:00:00Z", checksum: "aabbccdd11223344",
                 namespace: "default", chunks: 1),
    ]
}

extension AppState {
    static var preview: AppState {
        let mock = MockSkyClient()
        mock.files = FileNode.sampleFiles
        mock.info = StoreInfo(
            id: "sky://k1_preview1234567890",
            fileCount: 5,
            totalSize: 5_914_680,
            namespaces: ["default", "financial", "journal", "photos"]
        )
        let state = AppState(client: mock)
        state.files = FileNode.sampleFiles
        state.storeInfo = mock.info
        state.syncState = .synced
        return state
    }

    static var previewSyncing: AppState {
        let state = preview
        state.syncState = .syncing
        return state
    }

    static var previewError: AppState {
        let state = AppState(client: MockSkyClient())
        state.syncState = .error
        state.error = "Connection to S3 failed"
        return state
    }

    static var previewEmpty: AppState {
        let state = AppState(client: MockSkyClient())
        state.syncState = .synced
        state.storeInfo = StoreInfo(id: "sky://k1_empty", fileCount: 0, totalSize: 0, namespaces: nil)
        return state
    }
}

// MARK: - View Previews

#Preview("Menu Bar - Synced") {
    MenuBarView()
        .environmentObject(AppState.preview)
        .frame(width: 220)
}

#Preview("Menu Bar - Error") {
    MenuBarView()
        .environmentObject(AppState.previewError)
        .frame(width: 220)
}

#Preview("Browser - Populated") {
    BrowserView()
        .environmentObject(AppState.preview)
        .frame(width: 1000, height: 600)
}

#Preview("Browser - Empty") {
    BrowserView()
        .environmentObject(AppState.previewEmpty)
        .frame(width: 1000, height: 600)
}

#Preview("File Row") {
    VStack(alignment: .leading) {
        ForEach(FileNode.sampleFiles) { file in
            FileRowView(file: file)
        }
    }
    .padding()
}

#Preview("Detail View") {
    DetailView(file: FileNode.sampleFiles[2])
        .environmentObject(AppState.preview)
        .frame(width: 300, height: 400)
}

#Preview("Sidebar") {
    SidebarView(selectedNamespace: .constant(nil))
        .environmentObject(AppState.preview)
        .frame(width: 200, height: 400)
}
