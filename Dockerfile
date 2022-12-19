FROM golang:alpine as builder

WORKDIR /catchpole_build

COPY go.mod go.sum ./
COPY *.go ./

RUN go mod download
RUN go mod verify

ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -v -ldflags="-w -s" -o catchpole .

FROM alpine

COPY --from=builder /catchpole_build/catchpole /catchpole

ENTRYPOINT ["/catchpole"]
