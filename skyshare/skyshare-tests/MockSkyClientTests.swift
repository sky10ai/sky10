import XCTest
@testable import skyshare

/// Tests for the mock itself — ensures the mock behaves correctly for other tests.
final class MockSkyClientTests: XCTestCase {

    func testListFilesEmpty() async throws {
        let mock = MockSkyClient()
        let files = try await mock.listFiles(prefix: "")
        XCTAssertTrue(files.isEmpty)
    }

    func testListFilesWithPrefix() async throws {
        let mock = MockSkyClient()
        mock.files = [
            FileNode(id: "docs/a.md", path: "docs/a.md", name: "a.md", size: 10,
                     modified: "", checksum: "", namespace: "docs", chunks: 1),
            FileNode(id: "other/b.md", path: "other/b.md", name: "b.md", size: 20,
                     modified: "", checksum: "", namespace: "other", chunks: 1),
        ]

        let docsOnly = try await mock.listFiles(prefix: "docs/")
        XCTAssertEqual(docsOnly.count, 1)
        XCTAssertEqual(docsOnly[0].path, "docs/a.md")
    }

    func testPutAddsFile() async throws {
        let mock = MockSkyClient()
        try await mock.putFile(path: "new.md", localPath: "/tmp/new.md")
        XCTAssertEqual(mock.files.count, 1)
        XCTAssertEqual(mock.files[0].path, "new.md")
        XCTAssertEqual(mock.info.fileCount, 1)
    }

    func testRemoveDeletesFile() async throws {
        let mock = MockSkyClient()
        try await mock.putFile(path: "rm.md", localPath: "/tmp/rm.md")
        XCTAssertEqual(mock.files.count, 1)

        try await mock.removeFile(path: "rm.md")
        XCTAssertTrue(mock.files.isEmpty)
        XCTAssertEqual(mock.info.fileCount, 0)
    }

    func testErrorMode() async {
        let mock = MockSkyClient()
        mock.shouldError = true
        mock.errorMessage = "test error"

        do {
            _ = try await mock.listFiles(prefix: "")
            XCTFail("should throw")
        } catch {
            XCTAssertTrue(error.localizedDescription.contains("test error"))
        }
    }

    func testGetRecordsCalls() async throws {
        let mock = MockSkyClient()
        try await mock.getFile(path: "file.md", outPath: "/tmp/out.md")
        XCTAssertEqual(mock.getCalls.count, 1)
        XCTAssertEqual(mock.getCalls[0].path, "file.md")
        XCTAssertEqual(mock.getCalls[0].outPath, "/tmp/out.md")
    }
}
