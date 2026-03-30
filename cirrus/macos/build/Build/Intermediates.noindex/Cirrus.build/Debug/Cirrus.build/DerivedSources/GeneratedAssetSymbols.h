#import <Foundation/Foundation.h>

#if __has_attribute(swift_private)
#define AC_SWIFT_PRIVATE __attribute__((swift_private))
#else
#define AC_SWIFT_PRIVATE
#endif

/// The "cloud_sync_1" asset catalog image resource.
static NSString * const ACImageNameCloudSync1 AC_SWIFT_PRIVATE = @"cloud_sync_1";

/// The "cloud_sync_2" asset catalog image resource.
static NSString * const ACImageNameCloudSync2 AC_SWIFT_PRIVATE = @"cloud_sync_2";

/// The "cloud_sync_3" asset catalog image resource.
static NSString * const ACImageNameCloudSync3 AC_SWIFT_PRIVATE = @"cloud_sync_3";

#undef AC_SWIFT_PRIVATE
