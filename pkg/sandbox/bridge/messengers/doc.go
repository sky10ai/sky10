// Package messengers is the per-intent bridge endpoint for exposing approved
// messaging connections to sandboxed agents. It mounts at /bridge/messengers/ws
// and forwards guest requests over the host-owned sandbox bridge when running
// inside a guest daemon.
package messengers
