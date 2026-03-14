import XCTest
@testable import skyshare

final class RPCErrorTests: XCTestCase {

    func testErrorDescriptions() {
        XCTAssertEqual(RPCError.connectionFailed.errorDescription, "Cannot connect to skyfs daemon")
        XCTAssertEqual(RPCError.readFailed.errorDescription, "Failed to read from daemon")
        XCTAssertEqual(RPCError.invalidResponse.errorDescription, "Invalid response from daemon")
        XCTAssertEqual(RPCError.serverError("file not found").errorDescription, "file not found")
    }

    func testServerErrorPreservesMessage() {
        let err = RPCError.serverError("namespace key expired")
        XCTAssertEqual(err.errorDescription, "namespace key expired")
    }
}
