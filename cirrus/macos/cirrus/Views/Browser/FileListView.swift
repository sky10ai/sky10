import AppKit
import SwiftUI

/// Finder-style column browser. Each column shows the contents of the selected
/// folder from the previous column. Clicking a folder opens a new column to the right.
struct FileColumnView: View {
    let root: [TreeNode]
    @Binding var selectedFile: FileNode?
    @EnvironmentObject var appState: AppState
    @State private var path: [String] = [] // stack of selected folder IDs

    var body: some View {
        HStack(spacing: 0) {
            // Root column
            columnList(nodes: root, depth: 0)

            // Child columns based on selection path
            ForEach(Array(path.enumerated()), id: \.offset) { depth, folderID in
                if let nodes = childrenFor(folderID: folderID, at: depth) {
                    columnList(nodes: nodes, depth: depth + 1)
                }
            }

            Spacer(minLength: 0)
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

    private func columnList(nodes: [TreeNode], depth: Int) -> some View {
        List(selection: Binding(
            get: { selectionAt(depth: depth) },
            set: { id in selectAt(depth: depth, id: id) }
        )) {
            ForEach(nodes) { node in
                columnRow(node)
                    .tag(node.id)
            }
        }
        .listStyle(.plain)
        .frame(width: 250)
        .background(Color(nsColor: .controlBackgroundColor))
        .border(Color(nsColor: .separatorColor), width: 0.5)
    }

    @ViewBuilder
    private func columnRow(_ node: TreeNode) -> some View {
        HStack(spacing: 8) {
            Image(systemName: node.isFolder ? "folder.fill" : (node.file?.icon ?? "doc"))
                .foregroundColor(node.isFolder ? .primary : .blue)
                .frame(width: 18)
            Text(node.name)
                .lineLimit(1)
            Spacer()
            if node.isFolder {
                Image(systemName: "chevron.right")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            } else if let file = node.file {
                Text(file.formattedSize)
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }
        }
    }

    private func selectionAt(depth: Int) -> String? {
        if depth < path.count {
            return path[depth]
        }
        return selectedFile.map { $0.id }
    }

    private func selectAt(depth: Int, id: String?) {
        guard let id = id else { return }

        // Truncate path to this depth
        if depth < path.count {
            path = Array(path.prefix(depth))
        }

        // Find the node
        let nodes = depth == 0 ? root : (childrenFor(folderID: path[depth - 1], at: depth - 1) ?? [])
        guard let node = nodes.first(where: { $0.id == id }) else { return }

        if node.isFolder {
            path.append(id)
            selectedFile = nil
        } else {
            selectedFile = node.file
        }
    }

    private func childrenFor(folderID: String, at depth: Int) -> [TreeNode]? {
        let nodes = depth == 0 ? root : (childrenFor(folderID: path[depth - 1], at: depth - 1) ?? [])
        return nodes.first(where: { $0.id == folderID })?.children
    }
}
