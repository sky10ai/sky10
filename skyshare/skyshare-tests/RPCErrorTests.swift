import Testing
@testable import skyshare

@Suite("RPCError")
struct RPCErrorTests {

    @Test("Error descriptions")
    func descriptions() {
        #expect(RPCError.connectionFailed.errorDescription == "Cannot connect to skyfs daemon")
        #expect(RPCError.readFailed.errorDescription == "Failed to read from daemon")
        #expect(RPCError.invalidResponse.errorDescription == "Invalid response from daemon")
        #expect(RPCError.serverError("file not found").errorDescription == "file not found")
    }

    @Test("Server error preserves message")
    func serverError() {
        let err = RPCError.serverError("namespace key expired")
        #expect(err.errorDescription == "namespace key expired")
    }
}
