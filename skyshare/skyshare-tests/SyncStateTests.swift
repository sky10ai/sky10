import Testing
@testable import skyshare

@Suite("SyncState")
struct SyncStateTests {

    @Test("Icons for all states")
    func icons() {
        #expect(SyncState.synced.icon == "cloud.fill")
        #expect(SyncState.syncing.icon == "arrow.triangle.2.circlepath.cloud")
        #expect(SyncState.error.icon == "exclamationmark.icloud")
        #expect(SyncState.offline.icon == "icloud.slash")
    }

    @Test("Labels for all states")
    func labels() {
        #expect(SyncState.synced.label == "Synced")
        #expect(SyncState.syncing.label == "Syncing...")
        #expect(SyncState.error.label == "Sync Error")
        #expect(SyncState.offline.label == "Offline")
    }

    @Test("Colors for all states")
    func colors() {
        #expect(SyncState.synced.color == "green")
        #expect(SyncState.syncing.color == "blue")
        #expect(SyncState.error.color == "red")
        #expect(SyncState.offline.color == "gray")
    }

    @Test("All states have non-empty values")
    func allNonEmpty() {
        let cases: [SyncState] = [.synced, .syncing, .error, .offline]
        for state in cases {
            #expect(!state.icon.isEmpty)
            #expect(!state.label.isEmpty)
            #expect(!state.color.isEmpty)
        }
    }
}
