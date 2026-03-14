import Foundation

/// Represents a file in the encrypted store.
struct FileNode: Identifiable, Hashable {
    let id: String
    let path: String
    let name: String
    let size: Int64
    let modified: String
    let checksum: String
    let namespace: String
    let chunks: Int

    var isDirectory: Bool {
        false // skyfs stores files, not directories
    }

    var formattedSize: String {
        ByteCountFormatter.string(fromByteCount: size, countStyle: .file)
    }

    var formattedDate: String {
        // Parse ISO date and format for display
        let formatter = ISO8601DateFormatter()
        if let date = formatter.date(from: modified) {
            let display = DateFormatter()
            display.dateStyle = .medium
            display.timeStyle = .short
            return display.string(from: date)
        }
        return modified
    }

    var fileExtension: String {
        (name as NSString).pathExtension.lowercased()
    }

    var icon: String {
        switch fileExtension {
        case "md", "txt", "text":     return "doc.text"
        case "pdf":                    return "doc.richtext"
        case "jpg", "jpeg", "png",
             "gif", "webp", "heic":   return "photo"
        case "mp4", "mov", "avi":     return "film"
        case "mp3", "wav", "m4a":     return "music.note"
        case "zip", "tar", "gz":      return "archivebox"
        case "json", "yaml", "xml":   return "curlybraces"
        case "swift", "go", "py",
             "js", "ts", "rs":        return "chevron.left.forwardslash.chevron.right"
        default:                       return "doc"
        }
    }
}

/// Info about the store from the backend.
struct StoreInfo: Codable {
    let id: String
    let fileCount: Int
    let totalSize: Int64
    let namespaces: [String]?

    enum CodingKeys: String, CodingKey {
        case id
        case fileCount = "file_count"
        case totalSize = "total_size"
        case namespaces
    }
}
