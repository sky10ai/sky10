import Foundation
import Testing
@testable import cirrus

@Suite("ActivityLog")
@MainActor
struct ActivityLogTests {

    @Test("Log upload adds entry")
    func logUpload() {
        let log = ActivityLog()
        log.logUpload(path: "docs/readme.md", size: 1024)

        #expect(log.entries.count == 1)
        #expect(log.entries[0].path == "docs/readme.md")
        #expect(log.entries[0].type == .uploaded)
    }

    @Test("Log download adds entry")
    func logDownload() {
        let log = ActivityLog()
        log.logDownload(path: "photo.jpg", size: 2048)

        #expect(log.entries.count == 1)
        #expect(log.entries[0].type == .downloaded)
    }

    @Test("Log delete adds entry")
    func logDelete() {
        let log = ActivityLog()
        log.logDelete(path: "old.md")

        #expect(log.entries[0].type == .deleted)
    }

    @Test("Log conflict adds entry")
    func logConflict() {
        let log = ActivityLog()
        log.logConflict(path: "shared.md")

        #expect(log.entries[0].type == .conflict)
    }

    @Test("Log error adds entry")
    func logError() {
        let log = ActivityLog()
        log.logError(path: "fail.md", message: "network timeout")

        #expect(log.entries[0].type == .error)
        #expect(log.entries[0].detail == "network timeout")
    }

    @Test("Entries are newest first")
    func newestFirst() {
        let log = ActivityLog()
        log.logUpload(path: "first.md", size: 100)
        log.logUpload(path: "second.md", size: 200)

        #expect(log.entries[0].path == "second.md")
        #expect(log.entries[1].path == "first.md")
    }

    @Test("Caps at max entries")
    func maxEntries() {
        let log = ActivityLog()
        for i in 0..<150 {
            log.logUpload(path: "file\(i).md", size: 100)
        }

        #expect(log.entries.count == 100)
    }

    @Test("Entry has icon and color")
    func iconAndColor() {
        let entry = ActivityEntry(
            timestamp: Date(), type: .uploaded,
            path: "test.md", detail: "test"
        )
        #expect(!entry.icon.isEmpty)
        #expect(!entry.color.isEmpty)
        #expect(!entry.formattedTime.isEmpty)
    }
}
