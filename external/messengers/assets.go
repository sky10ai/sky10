// Package messengers embeds first-party external messenger adapter bundles.
package messengers

import "embed"

//go:embed slack
var assets embed.FS

// FS returns the embedded first-party messenger adapter bundle tree.
func FS() embed.FS {
	return assets
}
