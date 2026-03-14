import XCTest
@testable import skyshare

final class StoreInfoTests: XCTestCase {

    func testDecodable() throws {
        let json = """
        {
            "id": "sky://k1_abc123",
            "file_count": 42,
            "total_size": 1048576,
            "namespaces": ["journal", "financial"]
        }
        """
        let data = json.data(using: .utf8)!
        let info = try JSONDecoder().decode(StoreInfo.self, from: data)

        XCTAssertEqual(info.id, "sky://k1_abc123")
        XCTAssertEqual(info.fileCount, 42)
        XCTAssertEqual(info.totalSize, 1048576)
        XCTAssertEqual(info.namespaces, ["journal", "financial"])
    }

    func testDecodableNullNamespaces() throws {
        let json = """
        {
            "id": "sky://k1_abc",
            "file_count": 0,
            "total_size": 0,
            "namespaces": null
        }
        """
        let data = json.data(using: .utf8)!
        let info = try JSONDecoder().decode(StoreInfo.self, from: data)

        XCTAssertNil(info.namespaces)
    }

    func testDecodableMissingNamespaces() throws {
        let json = """
        {
            "id": "sky://k1_abc",
            "file_count": 0,
            "total_size": 0
        }
        """
        let data = json.data(using: .utf8)!
        let info = try JSONDecoder().decode(StoreInfo.self, from: data)

        XCTAssertNil(info.namespaces)
    }
}
