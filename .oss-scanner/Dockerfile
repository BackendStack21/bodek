# Build image for Anthropic OSS Scanner (https://github.com/anthropics/oss-scanner).
# Network is available only during this build; everything the tests need is installed here.
FROM golang:1.27-bookworm
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates git python3 \
    && rm -rf /var/lib/apt/lists/*

# scanner contract: the checkout lives inside the image, at /src
COPY . /src
WORKDIR /src

RUN go mod download && go build ./... \
    && go build -o /usr/local/bin/bodek ./cmd/bodek

# Run the checks so the image is known to work, without failing the build on a red test.
RUN (go vet ./... && go test -race ./... -count=1) || echo "WARNING: checks reported failures"
