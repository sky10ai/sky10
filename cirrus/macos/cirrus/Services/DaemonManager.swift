import Foundation

/// Manages the lifecycle of the Go sky10 daemon process.
class DaemonManager: ObservableObject {
    @Published var isRunning = false
    @Published var error: String?

    private var process: Process?
    private var stderrPath: String?

    /// Find the sky10 binary. Search order:
    /// 1. App bundle Resources/
    /// 2. Build output bin/sky10 (development)
    /// 3. Homebrew /opt/homebrew/bin/sky10
    /// 4. /usr/local/bin/sky10
    var binaryPath: String? {
        // 1. App bundle
        if let p = Bundle.main.path(forResource: "sky10", ofType: nil) {
            return p
        }

        // 2. Development: walk up from the app to find the repo's bin/sky10
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let candidates = [
            findRepoRoot().map { $0 + "/bin/sky10" },
            Optional(home + "/Documents/projects/sky10/bin/sky10"),
            Optional(home + "/go/bin/sky10"),
            Optional(home + "/.local/bin/sky10"),
        ].compactMap { $0 }

        for path in candidates {
            if FileManager.default.isExecutableFile(atPath: path) {
                return path
            }
        }

        // 3. Homebrew
        if FileManager.default.isExecutableFile(atPath: "/opt/homebrew/bin/sky10") {
            return "/opt/homebrew/bin/sky10"
        }

        // 4. Standard paths
        for path in ["/usr/local/bin/sky10", "/usr/bin/sky10"] {
            if FileManager.default.isExecutableFile(atPath: path) {
                return path
            }
        }

        return nil
    }

    /// Restart the daemon (e.g., after credentials change).
    func restart() {
        stop()
        start()
    }

    /// Start the sky10 serve process.
    func start() {
        if isRunning { return }

        guard let path = binaryPath else {
            self.error = "sky10 binary not found. Run 'make build' first."
            try? "sky10 not found".write(toFile: "/tmp/sky10/cirrus-daemon.log", atomically: true, encoding: .utf8)
            return
        }
        try? "Found sky10 at: \(path)".write(toFile: "/tmp/sky10/cirrus-daemon.log", atomically: true, encoding: .utf8)

        // Write config from UI settings before starting daemon
        writeConfig()

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: path)
        proc.arguments = ["serve"]

        // Pass S3 credentials via environment
        var env = ProcessInfo.processInfo.environment
        let defaults = UserDefaults.standard
        if let key = defaults.string(forKey: "s3AccessKeyID"), !key.isEmpty {
            env["S3_ACCESS_KEY_ID"] = key
        }
        if let secret = defaults.string(forKey: "s3SecretAccessKey"), !secret.isEmpty {
            env["S3_SECRET_ACCESS_KEY"] = secret
        }
        proc.environment = env

        // Redirect stderr to a file instead of a pipe. Pipes have a 64KB
        // buffer; if Cirrus doesn't drain it, the daemon's write() syscalls
        // block and freeze any goroutine that logs to stderr (AWS SDK
        // checksum middleware, log.Printf, panics, etc.). A file never blocks.
        try? FileManager.default.createDirectory(atPath: "/tmp/sky10", withIntermediateDirectories: true)
        let stderrLog = "/tmp/sky10/daemon.stderr.log"
        FileManager.default.createFile(atPath: stderrLog, contents: nil)
        let stderrHandle = FileHandle(forWritingAtPath: stderrLog)
        proc.standardError = stderrHandle ?? FileHandle.nullDevice
        proc.standardOutput = FileHandle.nullDevice
        stderrPath = stderrLog

        proc.terminationHandler = { [weak self] p in
            stderrHandle?.closeFile()
            DispatchQueue.main.async {
                guard let self = self else { return }
                self.isRunning = false
                if p.terminationStatus != 0 {
                    // Read tail of stderr log for error details.
                    let errMsg = (try? String(contentsOfFile: stderrLog, encoding: .utf8))?
                        .trimmingCharacters(in: .whitespacesAndNewlines)
                    self.error = errMsg.flatMap { $0.isEmpty ? nil : $0 }
                        ?? "sky10 exited with status \(p.terminationStatus)"
                }
            }
        }

        do {
            try proc.run()
            process = proc
            isRunning = true
            error = nil
            try? "Process started PID=\(proc.processIdentifier)".write(toFile: "/tmp/sky10/cirrus-daemon.log", atomically: true, encoding: .utf8)
        } catch {
            self.error = "Failed to start sky10: \(error.localizedDescription)"
            try? "Failed: \(error.localizedDescription)".write(toFile: "/tmp/sky10/cirrus-daemon.log", atomically: true, encoding: .utf8)
        }
    }

    /// Stop the daemon gracefully.
    func stop() {
        guard let proc = process, proc.isRunning else { return }
        proc.terminate()
        proc.waitUntilExit()
        process = nil
        isRunning = false
    }

    deinit {
        stop()
    }

    /// Write ~/.sky10/fs/config.json from UserDefaults settings.
    private func writeConfig() {
        let defaults = UserDefaults.standard
        let bucket = defaults.string(forKey: "s3Bucket") ?? ""
        let endpoint = defaults.string(forKey: "s3Endpoint") ?? ""
        let pathStyle = defaults.bool(forKey: "s3ForcePathStyle")

        guard !bucket.isEmpty else { return }

        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let configDir = "\(home)/.sky10/fs"
        let keysDir = "\(home)/.sky10/keys"
        try? FileManager.default.createDirectory(atPath: configDir, withIntermediateDirectories: true)
        try? FileManager.default.createDirectory(atPath: keysDir, withIntermediateDirectories: true)

        // Extract region from endpoint if possible, default to us-east-1
        let region = "us-east-1"

        let config: [String: Any] = [
            "bucket": bucket,
            "region": region,
            "endpoint": endpoint,
            "force_path_style": pathStyle,
            "identity_file": "\(keysDir)/key.json"
        ]

        if let data = try? JSONSerialization.data(withJSONObject: config, options: .prettyPrinted) {
            try? data.write(to: URL(fileURLWithPath: "\(configDir)/config.json"))
        }

        // Generate key if it doesn't exist
        let keyPath = "\(keysDir)/key.json"
        if !FileManager.default.fileExists(atPath: keyPath) {
            // Run sky10 key generate
            if let binary = binaryPath {
                let proc = Process()
                proc.executableURL = URL(fileURLWithPath: binary)
                proc.arguments = ["key", "generate"]
                try? proc.run()
                proc.waitUntilExit()
            }
        }
    }

    /// Try to find the sky10 repo root by walking up from known paths.
    private func findRepoRoot() -> String? {
        // Try from the bundle location (works when app is in the repo)
        let startPaths = [
            Bundle.main.bundlePath,
            (#filePath as NSString).deletingLastPathComponent, // compile-time source path
        ]
        for start in startPaths {
            var dir = start
            for _ in 0..<15 {
                let gomod = (dir as NSString).appendingPathComponent("go.mod")
                if FileManager.default.fileExists(atPath: gomod) {
                    return dir
                }
                dir = (dir as NSString).deletingLastPathComponent
            }
        }
        return nil
    }
}
