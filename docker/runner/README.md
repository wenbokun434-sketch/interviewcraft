# InterviewCraft Docker Runner

The Runner is optional and remains disabled unless the application is started
with `RUNNER_MODE=docker`. Build its local, pinned image explicitly:

```text
docker build -t interviewcraft-runner:local docker/runner
```

If the default Alpine CDN is unavailable, select another HTTPS endpoint from
the official Alpine mirror list without changing the image definition:

```text
docker build --build-arg ALPINE_MIRROR=https://mirrors.aliyun.com/alpine -t interviewcraft-runner:local docker/runner
```

Behind an HTTP proxy, pass Docker's predefined proxy build arguments. Do not
persist proxy values with a Dockerfile `ENV` instruction:

```text
docker build --build-arg HTTP_PROXY=http://host.docker.internal:PORT --build-arg HTTPS_PROXY=http://host.docker.internal:PORT -t interviewcraft-runner:local docker/runner
```

Every execution uses a fresh container with no network, no host mounts, a
read-only root filesystem, UID/GID 65532, dropped capabilities,
`no-new-privileges`, and CPU, memory, PID, file-descriptor, temporary-filesystem,
and wall-clock limits. The host removes the container (including anonymous
volumes) on success, test failure, timeout, OOM, cancellation, and protocol
failure.

The JSON response intentionally contains only public test names/statuses,
hidden pass/fail counts, an enumerated error kind, duration, and peak memory.
Compiler output, stderr, source, test inputs, expected values, and filesystem
paths never cross the adapter boundary.

Run the full isolation gate from the repository root:

```text
powershell -ExecutionPolicy Bypass -File scripts/test-runner-isolation.ps1
```
