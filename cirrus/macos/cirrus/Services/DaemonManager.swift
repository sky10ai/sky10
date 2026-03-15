import Foundation

/// Manages the lifecycle of the Go sky10 daemon process.
class DaemonManager: ObservableObject {
    @Published var isRunning = false
    @Published var error: String?

    private var process: Process?

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

    /// Start the sky10 fs serve process.
    func start() {
        guard !isRunning else { return }

        guard let path = binaryPath else {
            self.error = "sky10 binary not found. Run 'make build' first."
            return
        }

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: path)
        proc.arguments = ["fs", "serve"]

        // Pass S3 credentials — check UserDefaults, then inherit from parent env
        var env = ProcessInfo.processInfo.environment
        let defaults = UserDefaults.standard
        if let key = defaults.string(forKey: "s3AccessKeyID"), !key.isEmpty {
            env["S3_ACCESS_KEY_ID"] = key
        }
        if let secret = defaults.string(forKey: "s3SecretAccessKey"), !secret.isEmpty {
            env["S3_SECRET_ACCESS_KEY"] = secret
        }
        proc.environment = env

        let errPipe = Pipe()
        proc.standardError = errPipe
        proc.standardOutput = Pipe() // suppress stdout

        proc.terminationHandler = { [weak self] p in
            DispatchQueue.main.async {
                self?.isRunning = false
                if p.terminationStatus != 0 {
                    // Try to read stderr for error details
                    let errData = errPipe.fileHandleForReading.availableData
                    let errMsg = String(data: errData, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines)
                    self?.error = errMsg ?? "sky10 exited with status \(p.terminationStatus)"
                }
            }
        }

        do {
            try proc.run()
            process = proc
            isRunning = true
            error = nil
        } catch {
            self.error = "Failed to start sky10: \(error.localizedDescription)"
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
