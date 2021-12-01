# Build the manager binary
#FROM golang:1.16 as builder
FROM golang:1.17-bullseye as builder

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git linux-headers-amd64 musl-dev gcc bash

# Copy the predefined netrc file into the location that git depends on
COPY ./.netrc /root/.netrc
RUN chmod 600 /root/.netrc

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
copy network/ network/
COPY api/ api/
COPY k8s/ k8s/
#COPY constants/ constants/
COPY utils/ utils/
COPY examples/ examples/

# Build
RUN GOOS=linux GOARCH=amd64 go build -o simplenet ./examples/k8s/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/base-debian11
COPY --from=builder /workspace/simplenet / 
COPY --from=builder /workspace/examples/ /examples/

ENTRYPOINT ["/simplenet"]
