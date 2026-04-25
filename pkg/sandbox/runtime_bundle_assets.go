package sandbox

import runtimebundles "github.com/sky10/sky10/external/runtimebundles"

const (
	runtimeBundleOpenClawPluginDir        = runtimebundles.OpenClawSky10PluginDir
	runtimeBundleOpenClawPluginPackage    = runtimeBundleOpenClawPluginDir + "/package.json"
	runtimeBundleOpenClawPluginManifest   = runtimeBundleOpenClawPluginDir + "/openclaw.plugin.json"
	runtimeBundleOpenClawPluginIndex      = runtimeBundleOpenClawPluginDir + "/src/index.js"
	runtimeBundleOpenClawPluginMedia      = runtimeBundleOpenClawPluginDir + "/src/media.js"
	runtimeBundleOpenClawPluginClient     = runtimeBundleOpenClawPluginDir + "/src/sky10.js"
	runtimeBundleOpenClawDockerRuntimeDir = runtimebundles.OpenClawDockerDir
	runtimeBundleOpenClawDockerfile       = runtimeBundleOpenClawDockerRuntimeDir + "/Dockerfile"
	runtimeBundleOpenClawDockerEntrypoint = runtimeBundleOpenClawDockerRuntimeDir + "/entrypoint.sh"
)
