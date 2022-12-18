FROM golang:alpine as builder

WORKDIR /catchpole_build

COPY go.mod go.sum ./
COPY *.go ./

RUN go mod download
RUN go mod verify

RUN go build -v -o catchpole .

FROM alpine

COPY --from=builder /catchpole_build/catchpole /usr/local/bin/catchpole

CMD beta