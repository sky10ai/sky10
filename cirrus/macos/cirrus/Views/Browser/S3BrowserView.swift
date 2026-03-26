import SwiftUI

/// Browse the raw S3 bucket structure — directories and files.
struct S3BrowserView: View {
    @EnvironmentObject var appState: AppState
    @State private var prefix: String = ""
    @State private var dirs: [String] = []
    @State private var files: [SkyClient.S3Entry] = []
    @State private var total: Int = 0
    @State private var isLoading = false
    @State private var breadcrumb: [String] = [""]

    var body: some View {
        VStack(spacing: 0) {
            // Breadcrumb bar
            HStack(spacing: 4) {
                Button("Bucket") {
                    navigateTo("")
                }
                .buttonStyle(.plain)
                .foregroundStyle(.blue)

                ForEach(breadcrumbParts, id: \.path) { part in
                    Text("/").foregroundStyle(.tertiary)
                    Button(part.name) {
                        navigateTo(part.path)
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(.blue)
                }

                Spacer()

                if total > 0 {
                    Text("\(total) objects")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                if isLoading {
                    ProgressView()
                        .controlSize(.small)
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(.bar)

            Divider()

            // Content
            List {
                if !dirs.isEmpty {
                    Section("Prefixes") {
                        ForEach(dirs, id: \.self) { dir in
                            Button {
                                navigateTo(dir)
                            } label: {
                                HStack(spacing: 8) {
                                    Image(systemName: "folder.fill")
                                        .foregroundStyle(.blue)
                                    Text(displayName(dir))
                                    Spacer()
                                }
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }

                if !files.isEmpty {
                    Section("Objects (\(files.count))") {
                        ForEach(files) { file in
                            HStack(spacing: 8) {
                                Image(systemName: iconForKey(file.key))
                                    .foregroundStyle(.secondary)
                                Text(displayName(file.key))
                                    .lineLimit(1)
                                    .truncationMode(.middle)
                                Spacer()
                                Text(ByteCountFormatter.string(fromByteCount: file.size, countStyle: .file))
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                    .monospacedDigit()
                            }
                        }
                    }
                }

                if dirs.isEmpty && files.isEmpty && !isLoading {
                    ContentUnavailableView("Empty", systemImage: "tray",
                        description: Text(prefix.isEmpty ? "No objects in bucket" : "No objects under \(prefix)"))
                }
            }
        }
        .task {
            await load()
        }
    }

    private func navigateTo(_ newPrefix: String) {
        prefix = newPrefix
        // Build breadcrumb from prefix
        if newPrefix.isEmpty {
            breadcrumb = [""]
        } else {
            let parts = newPrefix.split(separator: "/", omittingEmptySubsequences: true)
            breadcrumb = [""]
            var running = ""
            for part in parts {
                running += part + "/"
                breadcrumb.append(running)
            }
        }
        Task { await load() }
    }

    private func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let result = try await appState.client.s3List(prefix: prefix)
            dirs = result.dirs ?? []
            files = result.files ?? []
            total = result.total
        } catch {
            dirs = []
            files = []
            total = 0
        }
    }

    private func displayName(_ key: String) -> String {
        let trimmed = key.hasPrefix(prefix) ? String(key.dropFirst(prefix.count)) : key
        return trimmed.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
    }

    private func iconForKey(_ key: String) -> String {
        if key.hasSuffix(".enc") { return "lock.fill" }
        if key.hasSuffix(".json") { return "doc.text" }
        if key.hasSuffix(".pack") { return "archivebox" }
        return "doc"
    }

    private struct BreadcrumbPart: Hashable {
        let name: String
        let path: String
    }

    private var breadcrumbParts: [BreadcrumbPart] {
        guard breadcrumb.count > 1 else { return [] }
        return breadcrumb.dropFirst().map { path in
            let name = path.split(separator: "/", omittingEmptySubsequences: true).last.map(String.init) ?? path
            return BreadcrumbPart(name: name, path: path)
        }
    }
}
