import FileProvider
import Foundation

/// File Provider extension that makes skyfs appear in Finder's sidebar.
/// Connects to the Go skyfs daemon via the same Unix socket RPC.
class FileProviderExtension: NSObject, NSFileProviderReplicatedExtension {
    let domain: NSFileProviderDomain
    let rpc: RPCClient
    let tempDir: URL

    required init(domain: NSFileProviderDomain) {
        self.domain = domain
        self.rpc = RPCClient()
        self.tempDir = FileManager.default.temporaryDirectory
            .appendingPathComponent("skyshare-fileprovider", isDirectory: true)
        super.init()

        try? FileManager.default.createDirectory(at: tempDir, withIntermediateDirectories: true)
    }

    func invalidate() {
        Task { await rpc.disconnect() }
    }

    // MARK: - Item Lookup

    func item(for identifier: NSFileProviderItemIdentifier,
              request: NSFileProviderRequest,
              completionHandler: @escaping (NSFileProviderItem?, Error?) -> Void) -> Progress {
        Task {
            do {
                if identifier == .rootContainer {
                    completionHandler(RootContainerItem(), nil)
                    return
                }

                let result: ListResult = try await rpc.call("skyfs.list", params: ["prefix": ""])
                let file = result.files.first { $0.path == identifier.rawValue }

                if let file = file {
                    let formatter = ISO8601DateFormatter()
                    let modified = formatter.date(from: file.modified) ?? Date()
                    let item = FileProviderItem(
                        path: file.path,
                        size: file.size,
                        modified: modified,
                        checksum: file.checksum,
                        namespace: file.namespace
                    )
                    completionHandler(item, nil)
                } else {
                    // Could be a namespace folder
                    let namespaces = Set(result.files.map { $0.namespace })
                    if namespaces.contains(identifier.rawValue) {
                        completionHandler(FileProviderFolder(name: identifier.rawValue), nil)
                    } else {
                        completionHandler(nil, NSFileProviderError(.noSuchItem))
                    }
                }
            } catch {
                completionHandler(nil, error)
            }
        }
        return Progress()
    }

    // MARK: - Enumeration

    func enumerator(for containerItemIdentifier: NSFileProviderItemIdentifier,
                    request: NSFileProviderRequest) throws -> NSFileProviderEnumerator {
        return FileProviderEnumerator(containerIdentifier: containerItemIdentifier, rpc: rpc)
    }

    // MARK: - Download (Finder opens a file)

    func fetchContents(for itemIdentifier: NSFileProviderItemIdentifier,
                       version requestedVersion: NSFileProviderItemVersion?,
                       request: NSFileProviderRequest,
                       completionHandler: @escaping (URL?, NSFileProviderItem?, Error?) -> Void) -> Progress {
        let progress = Progress(totalUnitCount: 100)

        Task {
            do {
                let path = itemIdentifier.rawValue
                let localURL = tempDir.appendingPathComponent((path as NSString).lastPathComponent)

                let _: GetResult = try await rpc.call("skyfs.get",
                    params: ["path": path, "out_path": localURL.path])

                let result: ListResult = try await rpc.call("skyfs.list", params: ["prefix": ""])
                let file = result.files.first { $0.path == path }

                let formatter = ISO8601DateFormatter()
                let modified = formatter.date(from: file?.modified ?? "") ?? Date()
                let item = FileProviderItem(
                    path: path,
                    size: file?.size ?? 0,
                    modified: modified,
                    checksum: file?.checksum ?? "",
                    namespace: file?.namespace ?? "default"
                )

                progress.completedUnitCount = 100
                completionHandler(localURL, item, nil)
            } catch {
                completionHandler(nil, nil, error)
            }
        }

        return progress
    }

    // MARK: - Upload (Finder saves a file)

    func createItem(basedOn itemTemplate: NSFileProviderItem,
                    fields: NSFileProviderItemFields,
                    contents url: URL?,
                    options: NSFileProviderCreateItemOptions = [],
                    request: NSFileProviderRequest,
                    completionHandler: @escaping (NSFileProviderItem?, NSFileProviderItemFields, Bool, Error?) -> Void) -> Progress {
        let progress = Progress(totalUnitCount: 100)

        Task {
            do {
                guard let localURL = url else {
                    completionHandler(nil, [], false, NSFileProviderError(.noSuchItem))
                    return
                }

                let parentID = itemTemplate.parentItemIdentifier
                let remotePath: String
                if parentID == .rootContainer {
                    remotePath = itemTemplate.filename
                } else {
                    remotePath = parentID.rawValue + "/" + itemTemplate.filename
                }

                let _: PutResult = try await rpc.call("skyfs.put",
                    params: ["path": remotePath, "local_path": localURL.path])

                let attrs = try FileManager.default.attributesOfItem(atPath: localURL.path)
                let size = (attrs[.size] as? Int64) ?? 0

                let item = FileProviderItem(
                    path: remotePath,
                    size: size,
                    modified: Date(),
                    checksum: "",
                    namespace: parentID == .rootContainer ? "default" : parentID.rawValue
                )

                progress.completedUnitCount = 100
                completionHandler(item, [], false, nil)
            } catch {
                completionHandler(nil, [], false, error)
            }
        }

        return progress
    }

    // MARK: - Modify (Finder overwrites a file)

    func modifyItem(_ item: NSFileProviderItem,
                    baseVersion version: NSFileProviderItemVersion,
                    changedFields: NSFileProviderItemFields,
                    contents newContents: URL?,
                    options: NSFileProviderModifyItemOptions = [],
                    request: NSFileProviderRequest,
                    completionHandler: @escaping (NSFileProviderItem?, NSFileProviderItemFields, Bool, Error?) -> Void) -> Progress {
        let progress = Progress(totalUnitCount: 100)

        Task {
            do {
                if let localURL = newContents {
                    let _: PutResult = try await rpc.call("skyfs.put",
                        params: ["path": item.itemIdentifier.rawValue, "local_path": localURL.path])
                }

                let updatedItem = FileProviderItem(
                    path: item.itemIdentifier.rawValue,
                    size: item.documentSize?.int64Value ?? 0,
                    modified: Date(),
                    checksum: "",
                    namespace: "default"
                )

                progress.completedUnitCount = 100
                completionHandler(updatedItem, [], false, nil)
            } catch {
                completionHandler(nil, [], false, error)
            }
        }

        return progress
    }

    // MARK: - Delete

    func deleteItem(identifier: NSFileProviderItemIdentifier,
                    baseVersion version: NSFileProviderItemVersion,
                    options: NSFileProviderDeleteItemOptions = [],
                    request: NSFileProviderRequest,
                    completionHandler: @escaping (Error?) -> Void) -> Progress {
        Task {
            do {
                let _: StatusResult = try await rpc.call("skyfs.remove",
                    params: ["path": identifier.rawValue])
                completionHandler(nil)
            } catch {
                completionHandler(error)
            }
        }
        return Progress()
    }
}

// MARK: - Root container item

private class RootContainerItem: NSObject, NSFileProviderItem {
    var itemIdentifier: NSFileProviderItemIdentifier { .rootContainer }
    var parentItemIdentifier: NSFileProviderItemIdentifier { .rootContainer }
    var filename: String { "Sky" }
    var contentType: UTType { .folder }
    var capabilities: NSFileProviderItemCapabilities {
        [.allowsReading, .allowsContentEnumerating, .allowsAddingSubItems]
    }
    var itemVersion: NSFileProviderItemVersion {
        NSFileProviderItemVersion(contentVersion: Data(), metadataVersion: Data())
    }
}

// RPC result types
private struct ListResult: Codable { let files: [FileResult] }
private struct FileResult: Codable {
    let path: String; let size: Int64; let modified: String
    let checksum: String; let namespace: String; let chunks: Int
}
private struct PutResult: Codable { let size: Int64? }
private struct GetResult: Codable { let size: Int64? }
private struct StatusResult: Codable { let status: String? }
