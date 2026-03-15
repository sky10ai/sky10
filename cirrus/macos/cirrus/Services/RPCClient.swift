import Foundation

/// JSON-RPC 2.0 client over Unix domain socket.
actor RPCClient {
    private let socketPath: String
    private var connection: FileHandle?
    private var inputStream: InputStream?
    private var outputStream: OutputStream?
    private var requestID = 0

    init(socketPath: String? = nil) {
        if let path = socketPath {
            self.socketPath = path
        } else {
            let home = FileManager.default.homeDirectoryForCurrentUser.path
            self.socketPath = "/tmp/sky10.sock"
        }
    }

    /// Call an RPC method and decode the result.
    func call<P: Encodable, R: Decodable>(
        _ method: String,
        params: P
    ) async throws -> R {
        let data = try await rawCall(method, params: params)
        return try JSONDecoder().decode(R.self, from: data)
    }

    /// Call an RPC method with no params.
    func call<R: Decodable>(_ method: String) async throws -> R {
        let data = try await rawCall(method, params: Optional<String>.none)
        return try JSONDecoder().decode(R.self, from: data)
    }

    private func rawCall<P: Encodable>(_ method: String, params: P?) async throws -> Data {
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

        // Connect if needed
        let (input, output) = try connectIfNeeded()

        // Write request + newline
        var toSend = requestData
        toSend.append(contentsOf: [0x0A]) // newline
        let bytes = [UInt8](toSend)
        output.write(bytes, maxLength: bytes.count)

        // Read response
        let responseData = try readLine(from: input)

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

    private func connectIfNeeded() throws -> (InputStream, OutputStream) {
        if let input = inputStream, let output = outputStream,
           input.streamStatus == .open, output.streamStatus == .open {
            return (input, output)
        }

        // Create Unix domain socket streams
        var readStream: Unmanaged<CFReadStream>?
        var writeStream: Unmanaged<CFWriteStream>?

        CFStreamCreatePairWithSocketToHost(nil, socketPath as CFString, 0, &readStream, &writeStream)

        // For Unix sockets, we need to use a different approach
        let sock = socket(AF_UNIX, SOCK_STREAM, 0)
        guard sock >= 0 else { throw RPCError.connectionFailed }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        socketPath.withCString { ptr in
            withUnsafeMutablePointer(to: &addr.sun_path.0) { dest in
                _ = strcpy(dest, ptr)
            }
        }

        let connectResult = withUnsafePointer(to: &addr) { ptr in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockPtr in
                connect(sock, sockPtr, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }

        guard connectResult == 0 else {
            close(sock)
            throw RPCError.connectionFailed
        }

        let input = InputStream(fileAtPath: "/dev/null")! // placeholder
        let output = OutputStream(toMemory: ())            // placeholder

        // Use FileHandle for actual I/O
        let fileHandle = FileHandle(fileDescriptor: sock, closeOnDealloc: true)
        self.connection = fileHandle

        // Create proper streams from file descriptor
        let cfSock = CFSocketCreateWithNative(nil, Int32(sock), 0, nil, nil)

        var inRef: Unmanaged<CFReadStream>?
        var outRef: Unmanaged<CFWriteStream>?
        CFStreamCreatePairWithSocket(nil, CFSocketGetNative(cfSock), &inRef, &outRef)

        let inStream = inRef!.takeRetainedValue() as InputStream
        let outStream = outRef!.takeRetainedValue() as OutputStream

        CFReadStreamSetProperty(inStream as CFReadStream, CFStreamPropertyKey(kCFStreamPropertyShouldCloseNativeSocket), kCFBooleanTrue)
        CFWriteStreamSetProperty(outStream as CFWriteStream, CFStreamPropertyKey(kCFStreamPropertyShouldCloseNativeSocket), kCFBooleanFalse)

        inStream.open()
        outStream.open()

        self.inputStream = inStream
        self.outputStream = outStream

        _ = input
        _ = output

        return (inStream, outStream)
    }

    private func readLine(from stream: InputStream) throws -> Data {
        var buffer = Data()
        let chunk = UnsafeMutablePointer<UInt8>.allocate(capacity: 4096)
        defer { chunk.deallocate() }

        while true {
            let bytesRead = stream.read(chunk, maxLength: 4096)
            if bytesRead < 0 { throw RPCError.readFailed }
            if bytesRead == 0 { break }

            buffer.append(chunk, count: bytesRead)

            // Check for newline
            if buffer.contains(0x0A) {
                if let nlIndex = buffer.firstIndex(of: 0x0A) {
                    return Data(buffer[buffer.startIndex..<nlIndex])
                }
            }
        }

        return buffer
    }

    func disconnect() {
        inputStream?.close()
        outputStream?.close()
        inputStream = nil
        outputStream = nil
        connection = nil
    }
}

enum RPCError: Error, LocalizedError {
    case connectionFailed
    case readFailed
    case invalidResponse
    case serverError(String)

    var errorDescription: String? {
        switch self {
        case .connectionFailed: return "Cannot connect to skyfs daemon"
        case .readFailed:       return "Failed to read from daemon"
        case .invalidResponse:  return "Invalid response from daemon"
        case .serverError(let msg): return msg
        }
    }
}
