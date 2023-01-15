FROM golang:alpine as builder

WORKDIR /go/github.com/fireacademy/catchpole

COPY go.mod go.sum ./

RUN go mod download
RUN go mod verify

COPY *.go ./

ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -v -ldflags="-w -s" -o /catchpole .

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /catchpole /catchpole

ENTRYPOINT ["/catchpole"]
