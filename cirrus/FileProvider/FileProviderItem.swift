import FileProvider
import UniformTypeIdentifiers

/// Maps a skyfs file entry to an NSFileProviderItem for Finder display.
class FileProviderItem: NSObject, NSFileProviderItem {
    let path: String
    let fileSize: Int64
    let fileModified: Date
    let fileChecksum: String
    let namespace: String
    let parentPath: String?

    init(path: String, size: Int64, modified: Date, checksum: String, namespace: String) {
        self.path = path
        self.fileSize = size
        self.fileModified = modified
        self.fileChecksum = checksum
        self.namespace = namespace

        // Derive parent path
        let components = path.split(separator: "/")
        if components.count > 1 {
            self.parentPath = components.dropLast().joined(separator: "/")
        } else {
            self.parentPath = nil
        }

        super.init()
    }

    // MARK: - NSFileProviderItem

    var itemIdentifier: NSFileProviderItemIdentifier {
        NSFileProviderItemIdentifier(path)
    }

    var parentItemIdentifier: NSFileProviderItemIdentifier {
        if let parent = parentPath {
            return NSFileProviderItemIdentifier(parent)
        }
        return .rootContainer
    }

    var filename: String {
        (path as NSString).lastPathComponent
    }

    var contentType: UTType {
        let ext = (filename as NSString).pathExtension.lowercased()
        return UTType(filenameExtension: ext) ?? .data
    }

    var documentSize: NSNumber? {
        NSNumber(value: fileSize)
    }

    var contentModificationDate: Date? {
        fileModified
    }

    var capabilities: NSFileProviderItemCapabilities {
        [.allowsReading, .allowsWriting, .allowsDeleting, .allowsRenaming]
    }

    var itemVersion: NSFileProviderItemVersion {
        let data = fileChecksum.data(using: .utf8) ?? Data()
        return NSFileProviderItemVersion(contentVersion: data, metadataVersion: data)
    }
}

/// Represents a namespace folder in the file provider.
class FileProviderFolder: NSObject, NSFileProviderItem {
    let folderName: String

    init(name: String) {
        self.folderName = name
        super.init()
    }

    var itemIdentifier: NSFileProviderItemIdentifier {
        NSFileProviderItemIdentifier(folderName)
    }

    var parentItemIdentifier: NSFileProviderItemIdentifier {
        .rootContainer
    }

    var filename: String {
        folderName
    }

    var contentType: UTType {
        .folder
    }

    var capabilities: NSFileProviderItemCapabilities {
        [.allowsReading, .allowsContentEnumerating]
    }

    var itemVersion: NSFileProviderItemVersion {
        NSFileProviderItemVersion(contentVersion: Data(), metadataVersion: Data())
    }
}
