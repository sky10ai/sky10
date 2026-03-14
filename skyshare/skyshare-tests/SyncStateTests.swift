import XCTest
@testable import skyshare

final class SyncStateTests: XCTestCase {

    func testIcon() {
        XCTAssertEqual(SyncState.synced.icon, "cloud.fill")
        XCTAssertEqual(SyncState.syncing.icon, "arrow.triangle.2.circlepath.cloud")
        XCTAssertEqual(SyncState.error.icon, "exclamationmark.icloud")
        XCTAssertEqual(SyncState.offline.icon, "icloud.slash")
    }

    func testLabel() {
        XCTAssertEqual(SyncState.synced.label, "Synced")
        XCTAssertEqual(SyncState.syncing.label, "Syncing...")
        XCTAssertEqual(SyncState.error.label, "Sync Error")
        XCTAssertEqual(SyncState.offline.label, "Offline")
    }

    func testColor() {
        XCTAssertEqual(SyncState.synced.color, "green")
        XCTAssertEqual(SyncState.syncing.color, "blue")
        XCTAssertEqual(SyncState.error.color, "red")
        XCTAssertEqual(SyncState.offline.color, "gray")
    }

    func testAllCasesCovered() {
        // Verify all cases have non-empty values
        let cases: [SyncState] = [.synced, .syncing, .error, .offline]
        for state in cases {
            XCTAssertFalse(state.icon.isEmpty, "\(state) has empty icon")
            XCTAssertFalse(state.label.isEmpty, "\(state) has empty label")
            XCTAssertFalse(state.color.isEmpty, "\(state) has empty color")
        }
    }
}
