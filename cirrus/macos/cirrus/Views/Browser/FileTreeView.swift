import AppKit
import SwiftUI

/// A node in the file tree — either a folder or a file.
struct TreeNode: Identifiable, Hashable {
    let id: String
    let name: String
    let isFolder: Bool
    let file: FileNode? // nil for folders
    var children: [TreeNode]

    func hash(into hasher: inout Hasher) {
        hasher.combine(id)
    }

    static func == (lhs: TreeNode, rhs: TreeNode) -> Bool {
        lhs.id == rhs.id
    }
}

/// Build a tree from flat file paths.
func buildTree(from files: [FileNode]) -> [TreeNode] {
    var folderMap: [String: [TreeNode]] = [:]

    for file in files {
        let components = file.path.split(separator: "/").map(String.init)
        if components.count == 1 {
            // Root-level file
            let node = TreeNode(id: file.id, name: file.name, isFolder: false, file: file, children: [])
            folderMap["", default: []].append(node)
        } else {
            // Build intermediate folders
            for i in 0..<(components.count - 1) {
                let folderPath = components[0...i].joined(separator: "/")
                let parentPath = i == 0 ? "" : components[0..<i].joined(separator: "/")
                let folderName = components[i]

                // Create folder node if it doesn't exist
                if folderMap[parentPath]?.contains(where: { $0.id == "folder:" + folderPath }) != true {
                    let folderNode = TreeNode(
                        id: "folder:" + folderPath,
                        name: folderName,
                        isFolder: true,
                        file: nil,
                        children: []
                    )
                    folderMap[parentPath, default: []].append(folderNode)
                }
            }

            // Add file to its parent folder
            let parentPath = components.dropLast().joined(separator: "/")
            let node = TreeNode(id: file.id, name: file.name, isFolder: false, file: file, children: [])
            folderMap[parentPath, default: []].append(node)
        }
    }

    // Recursively attach children
    func resolveChildren(for path: String) -> [TreeNode] {
        guard let nodes = folderMap[path] else { return [] }
        return nodes.map { node in
            if node.isFolder {
                let folderPath = node.id.replacingOccurrences(of: "folder:", with: "")
                var resolved = node
                resolved.children = resolveChildren(for: folderPath)
                return resolved
            }
            return node
        }.sorted { a, b in
            // Folders first, then alphabetical
            if a.isFolder != b.isFolder { return a.isFolder }
            return a.name.localizedCaseInsensitiveCompare(b.name) == .orderedAscending
        }
    }

    return resolveChildren(for: "")
}

/// Tree-based file browser with disclosure groups for folders.
struct FileTreeView: View {
    let root: [TreeNode]
    @Binding var selectedFile: FileNode?
    @EnvironmentObject var appState: AppState

    var body: some View {
        List(selection: Binding(
            get: { selectedFile?.id },
            set: { id in selectedFile = findFile(id: id, in: root) }
        )) {
            ForEach(root) { node in
                treeRow(node)
            }
        }
        .listStyle(.inset(alternatesRowBackgrounds: true))
        .contextMenu(forSelectionType: String.self) { ids in
            if let id = ids.first, let file = findFile(id: id, in: root) {
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
            if let id = ids.first, let file = findFile(id: id, in: root) {
                downloadFile(file)
            }
        }
        .overlay {
            if root.isEmpty && appState.storeInfo != nil && !appState.isLoading {
                ContentUnavailableView(
                    "No Files",
                    systemImage: "doc",
                    description: Text("Upload files or sync a directory to get started.")
                )
            }
        }
    }

    private func treeRow(_ node: TreeNode) -> AnyView {
        if node.isFolder {
            return AnyView(
                DisclosureGroup {
                    ForEach(node.children) { child in
                        treeRow(child)
                    }
                } label: {
                    Label(node.name, systemImage: "folder.fill")
                        .foregroundStyle(.primary)
                }
            )
        } else if let file = node.file {
            return AnyView(
                HStack(spacing: 8) {
                    Image(systemName: file.icon)
                        .foregroundStyle(.blue)
                        .frame(width: 18)
                    Text(file.name)
                        .lineLimit(1)
                    Spacer()
                    Text(file.formattedSize)
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                    Text(file.formattedDate)
                        .font(.caption)
                        .foregroundStyle(.tertiary)
                }
                .tag(file.id)
            )
        } else {
            return AnyView(EmptyView())
        }
    }

    private func findFile(id: String?, in nodes: [TreeNode]) -> FileNode? {
        guard let id = id else { return nil }
        for node in nodes {
            if node.file?.id == id { return node.file }
            if let found = findFile(id: id, in: node.children) { return found }
        }
        return nil
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
