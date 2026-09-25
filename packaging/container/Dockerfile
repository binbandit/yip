# yip runner in a restricted Linux container — the "container" execution profile.
#
# The image contains the yip binary, git, and certificates only. Provider CLIs
# are NOT installed here: yip never silently downloads an unpinned
# executable. Extend this image with the provider you use, pinned to a tested
# version (see docs/compatibility.md), for example:
#
#   FROM yip-runner
#   USER root
#   RUN npm install -g @anthropic-ai/claude-code@2.1.282   # needs nodejs in the image
#   USER yip
#
# Then sign in through the provider's own flow inside the container, stored on
# the /var/lib/yip volume (never baked into the image).

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/yip ./cmd/yip

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates tini \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --uid 10001 --create-home --home-dir /home/yip --shell /usr/sbin/nologin yip \
 && mkdir -p /var/lib/yip && chown yip:yip /var/lib/yip
COPY --from=build /out/yip /usr/local/bin/yip
USER yip
ENV YIP_EXECUTION_PROFILE=container \
    YIP_RUNNER_STATE=/var/lib/yip/runner \
    HOME=/home/yip
VOLUME ["/var/lib/yip"]
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/yip"]
CMD ["runner", "--profile", "container"]
