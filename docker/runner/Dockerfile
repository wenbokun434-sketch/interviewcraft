FROM golang:1.26 AS build

WORKDIR /src
COPY agent/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/runner-agent .

FROM alpine:3.22

ARG ALPINE_MIRROR=https://dl-cdn.alpinelinux.org/alpine
ARG APPLICATION_VERSION=dev
ARG GIT_COMMIT=unknown
ARG RUNNER_PROTOCOL=interviewcraft-runner-response-v1

RUN case "$ALPINE_MIRROR" in https://*) ;; *) exit 1 ;; esac \
    && printf '%s\n' \
        "${ALPINE_MIRROR}/v3.22/main" \
        "${ALPINE_MIRROR}/v3.22/community" \
        > /etc/apk/repositories \
    && apk add --no-cache --timeout 20 openjdk21-jdk nodejs python3 \
    && addgroup -S -g 65532 runner \
    && adduser -S -D -H -u 65532 -G runner runner

COPY --from=build --chown=65532:65532 /out/runner-agent /usr/local/bin/runner-agent

LABEL io.interviewcraft.runner="true"
LABEL io.interviewcraft.version="${APPLICATION_VERSION}"
LABEL io.interviewcraft.protocol="${RUNNER_PROTOCOL}"
LABEL org.opencontainers.image.title="InterviewCraft isolated code runner"
LABEL org.opencontainers.image.source="https://github.com/wenbokun434-sketch/interviewcraft"
LABEL org.opencontainers.image.version="${APPLICATION_VERSION}"
LABEL org.opencontainers.image.revision="${GIT_COMMIT}"

USER 65532:65532
WORKDIR /tmp
ENTRYPOINT ["/usr/local/bin/runner-agent"]
