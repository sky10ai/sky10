import Foundation
import Testing
@testable import skyshare

@Suite("ExternalLinks")
struct ExternalLinksTests {

    @Test("All external URLs return HTTP 200", arguments: ExternalLinks.all)
    func urlReturns200(url: URL) async throws {
        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        request.timeoutInterval = 15

        let (_, response) = try await URLSession.shared.data(for: request)

        guard let http = response as? HTTPURLResponse else {
            Issue.record("No HTTP response for \(url)")
            return
        }

        #expect(http.statusCode == 200, "Expected 200 for \(url), got \(http.statusCode)")
    }

    @Test("All links array is not empty")
    func linksNotEmpty() {
        #expect(!ExternalLinks.all.isEmpty)
    }

    @Test("Storage providers URL points to storage-providers.md")
    func storageProvidersPath() {
        #expect(ExternalLinks.storageProviders.absoluteString.contains("storage-providers.md"))
    }
}
