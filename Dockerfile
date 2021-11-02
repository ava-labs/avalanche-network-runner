# Build the manager binary
#FROM golang:1.16 as builder
FROM golang:1.17-bullseye as builder

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git linux-headers-amd64 musl-dev gcc bash

# Create the user and group files that will be used in the running
# container to run the process as an unprivileged user.
#RUN mkdir /user && \
#    echo 'nobody:x:65534:65534:nobody:/:' > /user/passwd && \
#    echo 'nobody:x:65534:' > /user/group

# RUN echo "[url \"git@github.com:\"]\n\tinsteadOf = https://github.com/" >> /root/.gitconfig
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
COPY api/ api/
COPY k8s/ k8s/
COPY client/ client/
COPY example/ example/

# Build
#RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ./simpletest ./simplek8s/main
RUN GOOS=linux GOARCH=amd64 go build -o simplenet ./example/k8s/main.go

# Final stage: the running container.
#FROM scratch AS final
# Import the user and group files from the first stage.
#COPY --from=builder /user/group /user/passwd /etc/
# Import the Certificate-Authority certificates for enabling HTTPS.
#COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/base-debian11
COPY --from=builder /workspace/simplenet / 
#COPY /workspace/simpletest .
#USER nobody:nobody
#USER 65532:65532

ENTRYPOINT ["/simplenet"]
