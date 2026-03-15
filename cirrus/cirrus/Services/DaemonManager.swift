import Foundation

/// Manages the lifecycle of the Go sky10 daemon process.
class DaemonManager: ObservableObject {
    @Published var isRunning = false
    @Published var error: String?

    private var process: Process?

    /// Path to the sky10 binary embedded in the app bundle.
    var binaryPath: String {
        if let bundlePath = Bundle.main.path(forResource: "sky10", ofType: nil) {
            return bundlePath
        }
        // Fallback: look in PATH (for development)
        return "/usr/local/bin/sky10"
    }

    /// Start the sky10 fs serve process.
    func start() {
        guard !isRunning else { return }

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: binaryPath)
        proc.arguments = ["fs", "serve"]

        let pipe = Pipe()
        proc.standardError = pipe

        proc.terminationHandler = { [weak self] p in
            DispatchQueue.main.async {
                self?.isRunning = false
                if p.terminationStatus != 0 {
                    self?.error = "sky10 exited with status \(p.terminationStatus)"
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
}
