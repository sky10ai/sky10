import Testing
@testable import skyshare

@Suite("FileNode")
struct FileNodeTests {

    @Test("Formatted size")
    func formattedSize() {
        #expect(makeFile(size: 512).formattedSize == "512 bytes")
        #expect(makeFile(size: 2048).formattedSize.contains("KB"))
        #expect(makeFile(size: 5 * 1024 * 1024).formattedSize.contains("MB"))
    }

    @Test("File extension parsing")
    func fileExtension() {
        #expect(makeFile(name: "doc.md").fileExtension == "md")
        #expect(makeFile(name: "photo.JPG").fileExtension == "jpg")
        #expect(makeFile(name: "archive.tar.gz").fileExtension == "gz")
        #expect(makeFile(name: "noext").fileExtension == "")
    }

    @Test("Icon based on extension", arguments: [
        ("readme.md", "doc.text"),
        ("report.pdf", "doc.richtext"),
        ("photo.jpg", "photo"),
        ("video.mp4", "film"),
        ("song.mp3", "music.note"),
        ("backup.zip", "archivebox"),
        ("config.json", "curlybraces"),
        ("main.go", "chevron.left.forwardslash.chevron.right"),
        ("unknown.xyz", "doc"),
    ])
    func icon(name: String, expectedIcon: String) {
        #expect(makeFile(name: name).icon == expectedIcon)
    }

    @Test("Formatted date parses ISO8601")
    func formattedDate() {
        let file = makeFile(modified: "2026-03-14T10:30:00Z")
        #expect(!file.formattedDate.isEmpty)
        #expect(file.formattedDate != "2026-03-14T10:30:00Z") // reformatted
    }

    @Test("Invalid date falls back to raw string")
    func invalidDate() {
        #expect(makeFile(modified: "not-a-date").formattedDate == "not-a-date")
    }

    @Test("Identity is path")
    func identity() {
        #expect(makeFile(path: "docs/readme.md").id == "docs/readme.md")
    }

    @Test("Hashable and unique in sets")
    func hashable() {
        let a = makeFile(path: "a.md")
        let b = makeFile(path: "b.md")
        let set: Set<FileNode> = [a, b, a]
        #expect(set.count == 2)
    }

    private func makeFile(
        path: String = "test.md",
        name: String? = nil,
        size: Int64 = 100,
        modified: String = "2026-03-14T10:00:00Z"
    ) -> FileNode {
        FileNode(
            id: path, path: path, name: name ?? (path as NSString).lastPathComponent,
            size: size, modified: modified, checksum: "abc123",
            namespace: "default", chunks: 1
        )
    }
}
