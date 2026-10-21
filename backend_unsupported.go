//go:build !js && (!(darwin || linux || windows) || !(amd64 || arm64))

package ntvideo

func platformCapabilities() CapabilitySet { return CapabilitySet{Reason: ErrUnsupported.Error()} }
func newBackend(config) (backend, error)  { return nil, ErrUnsupported }
