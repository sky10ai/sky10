import AppKit
import SwiftUI

/// First-run onboarding — shown when no config exists.
struct OnboardingView: View {
    @EnvironmentObject var appState: AppState
    @State private var mode: OnboardingMode = .choose

    enum OnboardingMode {
        case choose
        case newSetup
        case joinInvite
    }

    var body: some View {
        VStack(spacing: 0) {
            switch mode {
            case .choose:
                ChooseView(mode: $mode)
            case .newSetup:
                NewSetupView(onComplete: { appState.onboardingComplete = true })
                    .environmentObject(appState)
            case .joinInvite:
                JoinInviteView(onComplete: { appState.onboardingComplete = true })
                    .environmentObject(appState)
            }
        }
        .frame(width: 480, height: 400)
    }
}

/// First screen — choose between new setup or join.
struct ChooseView: View {
    @Binding var mode: OnboardingView.OnboardingMode

    var body: some View {
        VStack(spacing: 24) {
            Spacer()

            Image(systemName: "cloud.fill")
                .font(.system(size: 56))
                .foregroundStyle(.blue)

            Text("Welcome to Cirrus")
                .font(.title)
                .fontWeight(.bold)

            Text("Encrypted file sync. Your data, your keys.")
                .foregroundStyle(.secondary)

            Spacer()

            VStack(spacing: 12) {
                Button {
                    mode = .newSetup
                } label: {
                    HStack {
                        Image(systemName: "plus.circle.fill")
                        Text("Set Up New Storage")
                    }
                    .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)

                Button {
                    mode = .joinInvite
                } label: {
                    HStack {
                        Image(systemName: "link.circle.fill")
                        Text("Join With Invite Code")
                    }
                    .frame(maxWidth: .infinity)
                }
                .buttonStyle(.bordered)
                .controlSize(.large)
            }
            .frame(width: 280)

            Spacer()
        }
        .padding()
    }
}

/// New setup — endpoint, bucket, credentials.
struct NewSetupView: View {
    let onComplete: () -> Void
    @EnvironmentObject var appState: AppState
    @State private var endpoint = ""
    @State private var bucket = ""
    @State private var accessKey = ""
    @State private var secretKey = ""
    @State private var pathStyle = false
    @State private var testing = false
    @State private var error: String?

    var body: some View {
        VStack(spacing: 16) {
            Text("Connect Storage")
                .font(.title2)
                .fontWeight(.semibold)

            Text("Enter your S3-compatible storage details.")
                .font(.caption)
                .foregroundStyle(.secondary)

            Form {
                TextField("Endpoint", text: $endpoint)
                    .textFieldStyle(.roundedBorder)
                TextField("Bucket", text: $bucket)
                    .textFieldStyle(.roundedBorder)
                SecureField("Access Key", text: $accessKey)
                    .textFieldStyle(.roundedBorder)
                SecureField("Secret Key", text: $secretKey)
                    .textFieldStyle(.roundedBorder)
                Toggle("Path-style addressing (MinIO)", isOn: $pathStyle)
                    .font(.caption)
            }
            .padding(.horizontal)

            if let error = error {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
            }

            Spacer()

            HStack {
                Spacer()
                if testing {
                    ProgressView()
                        .scaleEffect(0.7)
                    Text("Connecting...")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Button("Connect") {
                    saveAndTest()
                }
                .buttonStyle(.borderedProminent)
                .disabled(endpoint.isEmpty || bucket.isEmpty || accessKey.isEmpty || secretKey.isEmpty || testing)
            }
        }
        .padding()
    }

    private func saveAndTest() {
        testing = true
        error = nil

        // Save to UserDefaults
        let defaults = UserDefaults.standard
        defaults.set(endpoint, forKey: "s3Endpoint")
        defaults.set(bucket, forKey: "s3Bucket")
        defaults.set(accessKey, forKey: "s3AccessKeyID")
        defaults.set(secretKey, forKey: "s3SecretAccessKey")
        defaults.set(pathStyle, forKey: "s3ForcePathStyle")

        // Restart daemon with new config
        appState.daemonManager.restart()

        Task {
            try? await Task.sleep(for: .seconds(8))
            do {
                _ = try await appState.client.getInfo()
                await appState.refresh()
                onComplete()
            } catch {
                self.error = "Connection failed: \(error.localizedDescription)"
            }
            testing = false
        }
    }
}

/// Join with invite code.
struct JoinInviteView: View {
    let onComplete: () -> Void
    @EnvironmentObject var appState: AppState
    @State private var inviteCode = ""
    @State private var joining = false
    @State private var status = ""
    @State private var error: String?

    var body: some View {
        VStack(spacing: 16) {
            Text("Join With Invite")
                .font(.title2)
                .fontWeight(.semibold)

            Text("Paste the invite code from another device.")
                .font(.caption)
                .foregroundStyle(.secondary)

            TextEditor(text: $inviteCode)
                .font(.system(.body, design: .monospaced))
                .frame(height: 100)
                .border(Color.gray.opacity(0.3))
                .padding(.horizontal)

            if !status.isEmpty {
                HStack {
                    if joining {
                        ProgressView()
                            .scaleEffect(0.7)
                    }
                    Text(status)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }

            if let error = error {
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.red)
            }

            Spacer()

            HStack {
                Spacer()
                Button("Join") {
                    joinWithCode()
                }
                .buttonStyle(.borderedProminent)
                .disabled(inviteCode.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || joining)
            }
        }
        .padding()
    }

    private func joinWithCode() {
        joining = true
        error = nil
        status = "Decoding invite..."

        let code = inviteCode.trimmingCharacters(in: .whitespacesAndNewlines)

        guard code.hasPrefix("sky10invite_") else {
            error = "Invalid invite code — must start with sky10invite_"
            joining = false
            return
        }

        let b64 = String(code.dropFirst(12))
        guard let jsonData = Data(base64Encoded: b64, options: .ignoreUnknownCharacters),
              let invite = try? JSONDecoder().decode(InvitePayload.self, from: jsonData) else {
            // Try URL-safe base64
            guard let jsonData2 = Data(base64Encoded: b64
                .replacingOccurrences(of: "-", with: "+")
                .replacingOccurrences(of: "_", with: "/")
                .padding(toLength: ((b64.count + 3) / 4) * 4, withPad: "=", startingAt: 0),
                options: .ignoreUnknownCharacters),
                  let invite2 = try? JSONDecoder().decode(InvitePayload.self, from: jsonData2) else {
                error = "Invalid invite code — could not decode"
                joining = false
                return
            }
            applyInvite(invite2)
            return
        }

        applyInvite(invite)
    }

    private func applyInvite(_ invite: InvitePayload) {
        let defaults = UserDefaults.standard
        defaults.set(invite.endpoint, forKey: "s3Endpoint")
        defaults.set(invite.bucket, forKey: "s3Bucket")
        defaults.set(invite.accessKey, forKey: "s3AccessKeyID")
        defaults.set(invite.secretKey, forKey: "s3SecretAccessKey")
        defaults.set(invite.forcePathStyle ?? false, forKey: "s3ForcePathStyle")

        status = "Starting backend..."
        appState.daemonManager.restart()

        Task {
            try? await Task.sleep(for: .seconds(8))
            await appState.start()
            status = "Connected! Complete the join in terminal:\n  sky10 fs join <invite-code>"
            onComplete()
            joining = false
        }
    }
}

private struct InvitePayload: Codable {
    let endpoint: String
    let bucket: String
    let region: String?
    let accessKey: String
    let secretKey: String
    let forcePathStyle: Bool?
    let devicePubkey: String?
    let inviteId: String?

    enum CodingKeys: String, CodingKey {
        case endpoint, bucket, region
        case accessKey = "access_key"
        case secretKey = "secret_key"
        case forcePathStyle = "force_path_style"
        case devicePubkey = "device_pubkey"
        case inviteId = "invite_id"
    }
}
