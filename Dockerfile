ARG RUNTIME_IMAGE=alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
ARG IMAGE_SOURCE="https://github.com/zxzharmlesszxz/prometheus-config-sync"

FROM golang:1.27.0@sha256:65b6f280bf050ec5af12716857e8ea8439d694dbba8f31ceeb7630670071f2bb AS build

WORKDIR /src

ARG LDFLAGS
ARG TARGETOS
ARG TARGETARCH

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN if [ -n "${LDFLAGS}" ]; then \
      make build GOOS=${TARGETOS} GOARCH=${TARGETARCH} LDFLAGS="${LDFLAGS}"; \
    else \
      unset LDFLAGS; \
      make build GOOS=${TARGETOS} GOARCH=${TARGETARCH}; \
    fi

FROM prom/prometheus:v3.13.2@sha256:508729e0e2d18e11fd742a5a5ca70e557b940a93948c3c95fd0123a6fd538b69 AS prometheus

FROM ${RUNTIME_IMAGE}

ARG RUNTIME_FAMILY=alpine
ARG IMAGE_SOURCE="https://github.com/zxzharmlesszxz/prometheus-config-sync"

LABEL org.opencontainers.image.title="prometheus-config-sync" \
      org.opencontainers.image.description="Synchronize generated Prometheus scrape configuration and rules" \
      org.opencontainers.image.source="${IMAGE_SOURCE}"

RUN if [ "${RUNTIME_FAMILY}" = "alpine" ]; then \
      apk add --no-cache ca-certificates wget; \
      addgroup -S -g 10001 config-sync; \
      adduser -S -D -H -u 10001 -G config-sync config-sync; \
    elif [ "${RUNTIME_FAMILY}" = "debian" ]; then \
      apt-get update; \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ca-certificates wget; \
      rm -rf /var/lib/apt/lists/*; \
      groupadd --gid 10001 config-sync; \
      useradd --uid 10001 --gid 10001 --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin config-sync; \
    else \
      echo "unsupported runtime family: ${RUNTIME_FAMILY}" >&2; \
      exit 1; \
    fi \
    && mkdir -p /etc/prometheus/generated/rules \
    && chown -R config-sync:config-sync /etc/prometheus/generated

COPY --from=build /src/dist/prometheus-config-sync /usr/local/bin/prometheus-config-sync
COPY --from=prometheus /bin/promtool /usr/local/bin/promtool

EXPOSE 9534

USER 10001:10001

HEALTHCHECK --interval=5s --timeout=3s --start-period=20s --retries=6 \
  CMD wget -q -O /dev/null http://127.0.0.1:9534/livez || exit 1
ENTRYPOINT ["/usr/local/bin/prometheus-config-sync"]
CMD ["--output.dir=/etc/prometheus/generated", "--promtool.path=/usr/local/bin/promtool", "--web.listen-address=:9534"]
