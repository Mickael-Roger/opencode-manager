package workspace

import (
	"fmt"
	"net"
)

const (
	// OpenCodePortMin and OpenCodePortMax bound the loopback TCP port range from
	// which each workspace is assigned a unique OpenCode server port. The range
	// starts at 4096 (the historical fixed port) and is wide enough for many
	// concurrent workspaces.
	OpenCodePortMin = 4096
	OpenCodePortMax = 4999

	// OpenCodePortEnv is the environment variable carrying the assigned port into
	// the container, read by the entrypoint and attach scripts.
	OpenCodePortEnv = "OCM_OPENCODE_PORT"
)

// AllocateOpenCodePort returns a port in [OpenCodePortMin, OpenCodePortMax] that
// is not already claimed by another workspace and is currently free on the host
// loopback. It scans existing workspace manifests to avoid reassigning a port,
// which keeps assignments stable even before the owning containers are running.
func (r Registry) AllocateOpenCodePort() (int, error) {
	summaries, err := r.List()
	if err != nil {
		return 0, err
	}

	used := make(map[int]bool, len(summaries))
	for _, s := range summaries {
		if s.Manifest.OpenCodePort != 0 {
			used[s.Manifest.OpenCodePort] = true
		}
	}

	return allocateOpenCodePort(used)
}

// allocateOpenCodePort picks the first port in range that is neither in used nor
// currently bound on the host loopback. The loopback probe matters only when
// containers share the host network namespace (config.HostNetwork); with isolated
// namespaces it is a harmless always-passes check, keeping a single code path.
func allocateOpenCodePort(used map[int]bool) (int, error) {
	for port := OpenCodePortMin; port <= OpenCodePortMax; port++ {
		if used[port] {
			continue
		}
		if !loopbackPortFree(port) {
			continue
		}
		return port, nil
	}

	return 0, fmt.Errorf("no free OpenCode port available in range %d-%d", OpenCodePortMin, OpenCodePortMax)
}

// loopbackPortFree reports whether port can currently be bound on 127.0.0.1.
func loopbackPortFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
