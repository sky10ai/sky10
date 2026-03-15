import SwiftUI

/// Sidebar showing namespaces.
struct SidebarView: View {
    @EnvironmentObject var appState: AppState
    @Binding var selectedNamespace: String?

    var body: some View {
        List(selection: $selectedNamespace) {
            Section("Namespaces") {
                Label("All Files", systemImage: "tray.full")
                    .tag(nil as String?)

                ForEach(appState.namespaces, id: \.self) { ns in
                    Label(ns.capitalized, systemImage: iconForNamespace(ns))
                        .tag(ns as String?)
                }
            }

            if let info = appState.storeInfo {
                Section("Storage") {
                    HStack {
                        Text("Files")
                        Spacer()
                        Text("\(info.fileCount)")
                            .foregroundStyle(.secondary)
                    }
                    HStack {
                        Text("Total")
                        Spacer()
                        Text(ByteCountFormatter.string(fromByteCount: info.totalSize, countStyle: .file))
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
        .listStyle(.sidebar)
        .frame(minWidth: 180)
    }

    private func iconForNamespace(_ ns: String) -> String {
        switch ns {
        case "default":    return "folder"
        case "financial":  return "dollarsign.circle"
        case "contacts":   return "person.2"
        case "docs":       return "doc.text"
        default:           return "folder.fill"
        }
    }
}
