import Foundation
#if canImport(DeveloperToolsSupport)
import DeveloperToolsSupport
#endif

#if SWIFT_PACKAGE
private let resourceBundle = Foundation.Bundle.module
#else
private class ResourceBundleClass {}
private let resourceBundle = Foundation.Bundle(for: ResourceBundleClass.self)
#endif

// MARK: - Color Symbols -

@available(iOS 17.0, macOS 14.0, tvOS 17.0, watchOS 10.0, *)
extension DeveloperToolsSupport.ColorResource {

}

// MARK: - Image Symbols -

@available(iOS 17.0, macOS 14.0, tvOS 17.0, watchOS 10.0, *)
extension DeveloperToolsSupport.ImageResource {

    /// The "cloud_sync_1" asset catalog image resource.
    static let cloudSync1 = DeveloperToolsSupport.ImageResource(name: "cloud_sync_1", bundle: resourceBundle)

    /// The "cloud_sync_2" asset catalog image resource.
    static let cloudSync2 = DeveloperToolsSupport.ImageResource(name: "cloud_sync_2", bundle: resourceBundle)

    /// The "cloud_sync_3" asset catalog image resource.
    static let cloudSync3 = DeveloperToolsSupport.ImageResource(name: "cloud_sync_3", bundle: resourceBundle)

}

