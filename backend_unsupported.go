//go:build !js && (!libmpv || !cgo)

package ntvideo

func platformCapabilities() CapabilitySet { return CapabilitySet{Reason: ErrUnsupported.Error()} }
func newBackend(config) (backend, error)  { return nil, ErrUnsupported }
