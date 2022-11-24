FROM golang:1.16-alpine

WORKDIR /catchpole

COPY go.mod ./
COPY go.sum ./

RUN go mod download

COPY *.go ./

RUN go build -o /catchpole

CMD [ "/catchpole" ]
