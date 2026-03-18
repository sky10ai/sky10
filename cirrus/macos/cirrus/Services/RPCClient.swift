import Foundation

/// JSON-RPC 2.0 client over Unix domain socket.
actor RPCClient {
    private let socketPath: String
    private var requestID = 0

    init(socketPath: String? = nil) {
        self.socketPath = socketPath ?? "/tmp/sky10.sock"
    }

    func call<P: Encodable, R: Decodable>(_ method: String, params: P) async throws -> R {
        let data = try rawCall(method, params: params)
        return try JSONDecoder().decode(R.self, from: data)
    }

    func call<R: Decodable>(_ method: String) async throws -> R {
        let data = try rawCall(method, params: Optional<String>.none)
        return try JSONDecoder().decode(R.self, from: data)
    }

    private func rawCall<P: Encodable>(_ method: String, params: P?) throws -> Data {
        requestID += 1

        var request: [String: Any] = [
            "jsonrpc": "2.0",
            "method": method,
            "id": requestID
        ]

        if let params = params {
            let paramsData = try JSONEncoder().encode(params)
            request["params"] = try JSONSerialization.jsonObject(with: paramsData)
        }

        let requestData = try JSONSerialization.data(withJSONObject: request)

        // Fresh connection every call — no stale socket issues after daemon restart
        let fh = try newConnection()

        var payload = requestData
        payload.append(0x0A)
        fh.write(payload)

        let responseData = try readLine(from: fh)
        fh.closeFile()

        guard let json = try JSONSerialization.jsonObject(with: responseData) as? [String: Any] else {
            throw RPCError.invalidResponse
        }

        if let error = json["error"] as? [String: Any],
           let message = error["message"] as? String {
            throw RPCError.serverError(message)
        }

        guard let result = json["result"] else {
            throw RPCError.invalidResponse
        }

        return try JSONSerialization.data(withJSONObject: result)
    }

    private func newConnection() throws -> FileHandle {
        let sock = socket(AF_UNIX, SOCK_STREAM, 0)
        guard sock >= 0 else { throw RPCError.connectionFailed }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        socketPath.withCString { ptr in
            withUnsafeMutablePointer(to: &addr.sun_path.0) { dest in
                strcpy(dest, ptr)
            }
        }

        let result = withUnsafePointer(to: &addr) { ptr in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockPtr in
                Foundation.connect(sock, sockPtr, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }

        guard result == 0 else {
            close(sock)
            throw RPCError.connectionFailed
        }

        return FileHandle(fileDescriptor: sock, closeOnDealloc: true)
    }

    private func readLine(from fh: FileHandle) throws -> Data {
        var buffer = Data()

        while true {
            let chunk = fh.availableData
            if chunk.isEmpty {
                throw RPCError.readFailed
            }
            buffer.append(chunk)

            if let nlIndex = buffer.firstIndex(of: 0x0A) {
                return Data(buffer[buffer.startIndex..<nlIndex])
            }
        }
    }

    func disconnect() {
        // No-op — connections are per-call now
    }

    /// Subscribe to push events from the daemon. Calls onEvent for each
    /// event received. Blocks until the connection drops.
    func subscribe(onEvent: @escaping (String) -> Void) {
        guard let fh = try? newConnection() else { return }

        // Send subscribe request
        var request: [String: Any] = [
            "jsonrpc": "2.0",
            "method": "skyfs.subscribe",
            "id": 0
        ]
        if let data = try? JSONSerialization.data(withJSONObject: request) {
            var payload = data
            payload.append(0x0A)
            fh.write(payload)
        }

        // Read the initial "subscribed" response
        _ = try? readLine(from: fh)

        // Read push events until connection drops
        var buffer = Data()
        while true {
            let chunk = fh.availableData
            if chunk.isEmpty { break } // connection closed
            buffer.append(chunk)

            // Process complete lines
            while let nlIndex = buffer.firstIndex(of: 0x0A) {
                let line = Data(buffer[buffer.startIndex..<nlIndex])
                buffer = Data(buffer[buffer.index(after: nlIndex)...])

                if let json = try? JSONSerialization.jsonObject(with: line) as? [String: Any],
                   let params = json["params"] as? [String: Any],
                   let event = params["event"] as? String {
                    onEvent(event)
                }
            }
        }

        fh.closeFile()
    }
}

enum RPCError: Error, LocalizedError {
    case connectionFailed
    case readFailed
    case invalidResponse
    case serverError(String)

    var errorDescription: String? {
        switch self {
        case .connectionFailed: return "Cannot connect to sky10 daemon"
        case .readFailed:       return "Failed to read from daemon"
        case .invalidResponse:  return "Invalid response from daemon"
        case .serverError(let msg): return msg
        }
    }
}
