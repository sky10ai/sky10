import SwiftUI

/// Conflict resolution options.
enum ConflictResolution {
    case keepLatest
    case keepBoth
}

/// Alert view for sync conflicts.
struct ConflictAlertModifier: ViewModifier {
    @Binding var conflictPath: String?
    let onResolve: (ConflictResolution) -> Void

    func body(content: Content) -> some View {
        content.alert(
            "Sync Conflict",
            isPresented: Binding(
                get: { conflictPath != nil },
                set: { if !$0 { conflictPath = nil } }
            )
        ) {
            Button("Keep Latest") {
                onResolve(.keepLatest)
                conflictPath = nil
            }
            Button("Keep Both") {
                onResolve(.keepBoth)
                conflictPath = nil
            }
            Button("Cancel", role: .cancel) {
                conflictPath = nil
            }
        } message: {
            if let path = conflictPath {
                Text("\(path) was modified on multiple devices. Choose how to resolve.")
            }
        }
    }
}

extension View {
    func conflictAlert(path: Binding<String?>, onResolve: @escaping (ConflictResolution) -> Void) -> some View {
        modifier(ConflictAlertModifier(conflictPath: path, onResolve: onResolve))
    }
}
