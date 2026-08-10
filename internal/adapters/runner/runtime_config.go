package runner

import (
	"runtime"

	"github.com/interviewcraft/interviewcraft/internal/config"
)

// ConfigForRuntime maps persisted, verified metadata to the strict adapter
// policy used by doctor and the TUI runtime.
func ConfigForRuntime(configured config.Runtime) Config {
	value := DefaultConfig()
	value.CosignBinary = PersistentCosignPath(configured.DataDir, runtime.GOOS, runtime.GOARCH)
	value.Image = configured.Runner.Reference()
	value.ExpectedDigest = configured.Runner.Digest
	value.ExpectedVersion = configured.Runner.Version
	value.ExpectedProtocol = configured.Runner.Protocol
	value.ExpectedArchitecture = configured.Runner.Architecture
	value.CertificateIdentity = CertificateIdentityURL + configured.Runner.Version
	value.OIDCIssuer = OIDCIssuer
	return value
}
