import SwiftUI

/// A single row in the file list.
struct FileRowView: View {
    let file: FileNode

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: file.icon)
                .foregroundStyle(.blue)
                .frame(width: 20)
            Text(file.name)
                .lineLimit(1)
        }
    }
}
