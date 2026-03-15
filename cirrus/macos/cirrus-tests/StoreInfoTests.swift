import Foundation
import Testing
@testable import cirrus

@Suite("StoreInfo")
struct StoreInfoTests {

    @Test("Decodes full JSON")
    func decodeFull() throws {
        let json = """
        {"id":"sky10qabc123","file_count":42,"total_size":1048576,"namespaces":["journal","financial"]}
        """
        let info = try JSONDecoder().decode(StoreInfo.self, from: json.data(using: .utf8)!)
        #expect(info.id == "sky10qabc123")
        #expect(info.fileCount == 42)
        #expect(info.totalSize == 1048576)
        #expect(info.namespaces == ["journal", "financial"])
    }

    @Test("Decodes null namespaces")
    func decodeNullNamespaces() throws {
        let json = """
        {"id":"sky10qabc","file_count":0,"total_size":0,"namespaces":null}
        """
        let info = try JSONDecoder().decode(StoreInfo.self, from: json.data(using: .utf8)!)
        #expect(info.namespaces == nil)
    }

    @Test("Decodes missing namespaces")
    func decodeMissingNamespaces() throws {
        let json = """
        {"id":"sky10qabc","file_count":0,"total_size":0}
        """
        let info = try JSONDecoder().decode(StoreInfo.self, from: json.data(using: .utf8)!)
        #expect(info.namespaces == nil)
    }
}
