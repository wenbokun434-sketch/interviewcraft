package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/coding"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

const cosignVersion = "v3.1.3"

var cosignHashes = map[string]struct{ filename, digest string }{
	"darwin/amd64":  {"cosign-darwin-amd64", "2347488e5d5b25336644024dfeca5601b190e91197a71a917bda44744aff106c"},
	"darwin/arm64":  {"cosign-darwin-arm64", "5cf948c2f4dfe59687bdd0b8523709067383e03982cc543475c8a7dc70e92a76"},
	"linux/amd64":   {"cosign-linux-amd64", "4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71"},
	"linux/arm64":   {"cosign-linux-arm64", "c5d324e091826b0d7a78eb16fef316450b4eb9aaec045611c08ba06f5e73220a"},
	"windows/amd64": {"cosign-windows-amd64.exe", "9fe59be0eca1271873ce019061335eb1ac419b7059202e797828467ddabe33be"},
	"windows/arm64": {"cosign-windows-amd64.exe", "9fe59be0eca1271873ce019061335eb1ac419b7059202e797828467ddabe33be"},
}

type ProvisionStage string

const (
	ProvisionResolve ProvisionStage = "resolve"
	ProvisionPull    ProvisionStage = "pull"
	ProvisionVerify  ProvisionStage = "signature"
	ProvisionInspect ProvisionStage = "inspect"
	ProvisionSmoke   ProvisionStage = "smoke"
	ProvisionEnable  ProvisionStage = "enable"
)

type ProvisionProgress struct {
	Stage   ProvisionStage
	Current int
	Total   int
}

type ProvisionObserver func(async.State[ProvisionProgress])

type ProvisionRequest struct {
	Version string
	DataDir string
}

type Provisioned struct {
	Image        string
	Digest       string
	Version      string
	Protocol     string
	Architecture string
}

type SupplyChainVerifier interface {
	VerifyBlob(context.Context, string, string, string, string) error
	VerifyImage(context.Context, string, string, string) error
}

type ProvisionOptions struct {
	Client     *http.Client
	Docker     CommandExecutor
	Verifier   SupplyChainVerifier
	GOOS       string
	GOARCH     string
	ReleaseURL string
	TempParent string
	Smoke      func(context.Context, Config, CommandExecutor) error
}

// Provision downloads signed release metadata, pulls one immutable image and
// enables it only after signature, policy inspection and an isolated smoke run.
func Provision(ctx context.Context, request ProvisionRequest, options ProvisionOptions, observer ProvisionObserver) (provisioned Provisioned, returnErr error) {
	notifyProvision(observer, async.NewPending[ProvisionProgress]())
	defer func() {
		if returnErr == nil {
			return
		}
		var typed *domainerr.Error
		if !errors.As(returnErr, &typed) {
			typed = provisionFailure("provision Full Practice Runner", returnErr)
		}
		notifyProvision(observer, async.NewFailed[ProvisionProgress](typed))
	}()
	if !releaseVersionPattern.MatchString(request.Version) {
		return Provisioned{}, provisionFailure("resolve Runner version", errors.New("Full Practice requires a released semantic version"))
	}
	options = fillProvisionOptions(options)
	if strings.HasSuffix(options.ReleaseURL, "/v") {
		options.ReleaseURL += request.Version
	}
	if options.GOARCH != "amd64" && options.GOARCH != "arm64" {
		return Provisioned{}, provisionFailure("resolve Runner architecture", errors.New("unsupported architecture"))
	}
	temporary, err := os.MkdirTemp(options.TempParent, "interviewcraft-runner-")
	if err != nil {
		return Provisioned{}, provisionFailure("create Runner verification directory", err)
	}
	defer os.RemoveAll(temporary)

	verifier := options.Verifier
	downloadedVerifier := ""
	if verifier == nil {
		binary, downloadErr := downloadCosign(ctx, options.Client, options.GOOS, options.GOARCH, temporary)
		if downloadErr != nil {
			return Provisioned{}, provisionFailure("prepare pinned Cosign verifier", downloadErr)
		}
		downloadedVerifier = binary
		verifier = commandSupplyChainVerifier{binary: binary}
	}

	manifestPath := filepath.Join(temporary, "runner-manifest.txt")
	bundlePath := filepath.Join(temporary, "runner-manifest.sigstore.json")
	base := strings.TrimRight(options.ReleaseURL, "/")
	if err := streamDownload(ctx, options.Client, base+"/runner-manifest.txt", manifestPath, 64<<10); err != nil {
		return Provisioned{}, provisionFailure("download Runner manifest", err)
	}
	if err := streamDownload(ctx, options.Client, base+"/runner-manifest.sigstore.json", bundlePath, 4<<20); err != nil {
		return Provisioned{}, provisionFailure("download Runner signature bundle", err)
	}
	identity := CertificateIdentityURL + request.Version
	if err := verifier.VerifyBlob(ctx, manifestPath, bundlePath, identity, OIDCIssuer); err != nil {
		return Provisioned{}, provisionFailure("verify Runner manifest signature", err)
	}
	file, err := os.Open(manifestPath)
	if err != nil {
		return Provisioned{}, provisionFailure("open Runner manifest", err)
	}
	manifest, parseErr := ParseRunnerManifest(file)
	_ = file.Close()
	if parseErr != nil || manifest.Version != request.Version {
		return Provisioned{}, provisionFailure("parse Runner manifest", parseErr)
	}
	image, err := manifest.ImageFor(options.GOARCH)
	if err != nil {
		return Provisioned{}, provisionFailure("resolve Runner image", err)
	}
	progressProvision(observer, ProvisionResolve, 1)

	reference := image.Repository + "@" + image.Digest
	existed := imageExists(ctx, options.Docker, reference)
	pullAttempted := false
	success := false
	defer func() {
		if pullAttempted && !existed && !success {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, _ = options.Docker.Run(cleanupCtx, nil, "image", "rm", "--force", reference)
		}
	}()
	pullAttempted = true
	result, pullErr := options.Docker.Run(ctx, nil, "pull", "--platform", "linux/"+options.GOARCH, reference)
	if pullErr != nil || result.ExitCode != 0 {
		return Provisioned{}, provisionFailure("pull immutable Runner image", pullErr)
	}
	progressProvision(observer, ProvisionPull, 2)
	if err := verifier.VerifyImage(ctx, reference, identity, OIDCIssuer); err != nil {
		return Provisioned{}, provisionFailure("verify Runner image signature", err)
	}
	progressProvision(observer, ProvisionVerify, 3)

	config := ReleaseConfig(image, manifest.Version, identity)
	adapter, err := New(config, Options{Command: options.Docker, SignatureVerifier: acceptedSignature{}})
	if err != nil {
		return Provisioned{}, provisionFailure("configure verified Runner", err)
	}
	if _, err := adapter.Diagnose(ctx); err != nil {
		return Provisioned{}, provisionFailure("inspect verified Runner", err)
	}
	progressProvision(observer, ProvisionInspect, 4)
	if err := options.Smoke(ctx, config, options.Docker); err != nil {
		return Provisioned{}, provisionFailure("run isolated Runner smoke test", err)
	}
	progressProvision(observer, ProvisionSmoke, 5)
	if downloadedVerifier != "" {
		if strings.TrimSpace(request.DataDir) == "" {
			return Provisioned{}, provisionFailure("persist pinned Cosign verifier", errors.New("Runner data directory is required"))
		}
		if err := persistCosign(downloadedVerifier, PersistentCosignPath(request.DataDir, options.GOOS, options.GOARCH)); err != nil {
			return Provisioned{}, provisionFailure("persist pinned Cosign verifier", err)
		}
	}
	progressProvision(observer, ProvisionEnable, 6)
	success = true
	completed := ProvisionProgress{Stage: ProvisionEnable, Current: 6, Total: 6}
	notifyProvision(observer, async.NewSucceeded(completed))
	return Provisioned{Image: image.Repository, Digest: image.Digest, Version: manifest.Version, Protocol: image.Protocol, Architecture: image.Architecture}, nil
}

func ReleaseConfig(image ManifestImage, version, identity string) Config {
	config := DefaultConfig()
	config.Image = image.Repository + "@" + image.Digest
	config.ExpectedDigest = image.Digest
	config.ExpectedVersion = version
	config.ExpectedProtocol = image.Protocol
	config.ExpectedArchitecture = image.Architecture
	config.CertificateIdentity = identity
	config.OIDCIssuer = OIDCIssuer
	return config
}

func fillProvisionOptions(options ProvisionOptions) ProvisionOptions {
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 2 * time.Minute}
	}
	if options.Docker == nil {
		options.Docker = osCommand{binary: "docker", maxOutputBytes: 256 << 10}
	}
	if options.GOOS == "" {
		options.GOOS = runtime.GOOS
	}
	if options.GOARCH == "" {
		options.GOARCH = runtime.GOARCH
	}
	if options.ReleaseURL == "" {
		options.ReleaseURL = "https://github.com/wenbokun434-sketch/interviewcraft/releases/download/v"
	}
	if options.Smoke == nil {
		options.Smoke = smokeRunner
	}
	return options
}

func smokeRunner(ctx context.Context, config Config, docker CommandExecutor) error {
	adapter, err := New(config, Options{Command: docker, SignatureVerifier: acceptedSignature{}})
	if err != nil {
		return err
	}
	result, err := adapter.Run(ctx, coding.ExecutionRequest{
		QuestionID: "pair_sum", Language: coding.LanguagePython,
		Source: "def pair_sum(nums, target):\n    seen = {}\n    for i, value in enumerate(nums):\n        if target - value in seen:\n            return [seen[target - value], i]\n        seen[value] = i\n    return []\n",
	})
	if err != nil || result.Result.Status != coding.RunPassed {
		return errors.New("Runner smoke result was not successful")
	}
	return nil
}

type acceptedSignature struct{}

func (acceptedSignature) VerifyImage(context.Context, string, string, string) error { return nil }

type commandSupplyChainVerifier struct{ binary string }

func (verifier commandSupplyChainVerifier) VerifyBlob(ctx context.Context, manifest, bundle, identity, issuer string) error {
	command := osCommand{binary: verifier.binary, maxOutputBytes: 256 << 10}
	result, err := command.Run(ctx, nil, "verify-blob", "--bundle", bundle, "--certificate-identity", identity, "--certificate-oidc-issuer", issuer, manifest)
	if err != nil || result.ExitCode != 0 {
		return errors.New("Runner manifest signature verification failed")
	}
	return nil
}
func (verifier commandSupplyChainVerifier) VerifyImage(ctx context.Context, image, identity, issuer string) error {
	return cosignVerifier{binary: verifier.binary, maxOutputBytes: 256 << 10}.VerifyImage(ctx, image, identity, issuer)
}

func streamDownload(ctx context.Context, client *http.Client, uri, target string, limit int64) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("release endpoint returned HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, limit+1))
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written == 0 || written > limit {
		return errors.New("download is empty or exceeds the size limit")
	}
	return nil
}

func downloadCosign(ctx context.Context, client *http.Client, goos, goarch, directory string) (string, error) {
	record, ok := cosignHashes[goos+"/"+goarch]
	if !ok {
		return "", errors.New("Cosign is not available for this platform")
	}
	target := filepath.Join(directory, record.filename)
	uri := "https://github.com/sigstore/cosign/releases/download/" + cosignVersion + "/" + record.filename
	if err := streamDownload(ctx, client, uri, target, 256<<20); err != nil {
		return "", err
	}
	hash, err := fileSHA256(target)
	if err != nil {
		return "", err
	}
	if hash != record.digest {
		return "", errors.New("pinned Cosign checksum mismatch")
	}
	if err := os.Chmod(target, 0o700); err != nil {
		return "", err
	}
	return target, nil
}

// PersistentCosignPath is the versioned, non-secret verifier used by setup,
// doctor and runtime startup. It never points outside the selected data dir.
func PersistentCosignPath(dataDir, goos, goarch string) string {
	record, ok := cosignHashes[goos+"/"+goarch]
	if !ok {
		return ""
	}
	return filepath.Join(dataDir, "runner-tools", cosignVersion, record.filename)
}

func persistCosign(source, target string) error {
	if strings.TrimSpace(target) == "" {
		return errors.New("Cosign target path is invalid")
	}
	directory := filepath.Dir(target)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".cosign-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	input, err := os.Open(source)
	if err != nil {
		_ = temporary.Close()
		return err
	}
	_, copyErr := io.Copy(temporary, input)
	closeInputErr := input.Close()
	if copyErr == nil {
		copyErr = temporary.Sync()
	}
	closeOutputErr := temporary.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeInputErr != nil {
		return closeInputErr
	}
	if closeOutputErr != nil {
		return closeOutputErr
	}
	if err := os.Chmod(temporaryPath, 0o700); err != nil {
		return err
	}
	if existingHash, hashErr := fileSHA256(target); hashErr == nil {
		newHash, newHashErr := fileSHA256(temporaryPath)
		if newHashErr != nil {
			return newHashErr
		}
		if existingHash == newHash {
			committed = true
			return os.Remove(temporaryPath)
		}
	}
	if err := replaceVerifierFile(temporaryPath, target); err != nil {
		return err
	}
	committed = true
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func imageExists(ctx context.Context, docker CommandExecutor, reference string) bool {
	result, err := docker.Run(ctx, nil, "image", "inspect", reference)
	return err == nil && result.ExitCode == 0
}

func progressProvision(observer ProvisionObserver, stage ProvisionStage, current int) {
	value := ProvisionProgress{Stage: stage, Current: current, Total: 6}
	notifyProvision(observer, async.NewStreaming(&value))
}

func notifyProvision(observer ProvisionObserver, state async.State[ProvisionProgress]) {
	if observer != nil {
		observer(state)
	}
}

func provisionFailure(operation string, cause error) *domainerr.Error {
	if cause == nil {
		cause = errors.New("Runner provisioning failed")
	}
	return domainerr.Wrap(domainerr.CodeDependencyUnavailable, operation, "Docker Runner",
		"Full Practice Runner 未通过可信部署检查；Lite 文字训练仍可使用。",
		"确认 Docker daemon 与网络可用后，重新运行 `interviewcraft setup --profile full --restart`。",
		true, cause)
}
