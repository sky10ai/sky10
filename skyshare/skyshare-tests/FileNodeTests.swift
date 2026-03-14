import XCTest
@testable import skyshare

final class FileNodeTests: XCTestCase {

    func testFormattedSize() {
        let small = makeFile(size: 512)
        XCTAssertEqual(small.formattedSize, "512 bytes")

        let kb = makeFile(size: 2048)
        XCTAssertTrue(kb.formattedSize.contains("KB"))

        let mb = makeFile(size: 5 * 1024 * 1024)
        XCTAssertTrue(mb.formattedSize.contains("MB"))
    }

    func testFileExtension() {
        XCTAssertEqual(makeFile(name: "doc.md").fileExtension, "md")
        XCTAssertEqual(makeFile(name: "photo.JPG").fileExtension, "jpg")
        XCTAssertEqual(makeFile(name: "archive.tar.gz").fileExtension, "gz")
        XCTAssertEqual(makeFile(name: "noext").fileExtension, "")
    }

    func testIcon() {
        XCTAssertEqual(makeFile(name: "readme.md").icon, "doc.text")
        XCTAssertEqual(makeFile(name: "report.pdf").icon, "doc.richtext")
        XCTAssertEqual(makeFile(name: "photo.jpg").icon, "photo")
        XCTAssertEqual(makeFile(name: "video.mp4").icon, "film")
        XCTAssertEqual(makeFile(name: "song.mp3").icon, "music.note")
        XCTAssertEqual(makeFile(name: "backup.zip").icon, "archivebox")
        XCTAssertEqual(makeFile(name: "config.json").icon, "curlybraces")
        XCTAssertEqual(makeFile(name: "main.go").icon, "chevron.left.forwardslash.chevron.right")
        XCTAssertEqual(makeFile(name: "unknown.xyz").icon, "doc")
    }

    func testFormattedDate() {
        let file = makeFile(modified: "2026-03-14T10:30:00Z")
        // Should parse and format (exact format depends on locale)
        XCTAssertFalse(file.formattedDate.isEmpty)
        XCTAssertNotEqual(file.formattedDate, "2026-03-14T10:30:00Z") // should be reformatted
    }

    func testFormattedDateInvalid() {
        let file = makeFile(modified: "not-a-date")
        XCTAssertEqual(file.formattedDate, "not-a-date") // falls back to raw string
    }

    func testIdentity() {
        let file = makeFile(path: "docs/readme.md")
        XCTAssertEqual(file.id, "docs/readme.md")
    }

    func testHashable() {
        let a = makeFile(path: "a.md")
        let b = makeFile(path: "b.md")
        let set: Set<FileNode> = [a, b, a]
        XCTAssertEqual(set.count, 2)
    }

    // MARK: - Helpers

    private func makeFile(
        path: String = "test.md",
        name: String? = nil,
        size: Int64 = 100,
        modified: String = "2026-03-14T10:00:00Z"
    ) -> FileNode {
        let fileName = name ?? (path as NSString).lastPathComponent
        return FileNode(
            id: path, path: path, name: fileName,
            size: size, modified: modified, checksum: "abc123",
            namespace: "default", chunks: 1
        )
    }
}
