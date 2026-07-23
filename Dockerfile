FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /pharos .

FROM alpine:3.20
COPY --from=builder /pharos /usr/local/bin/pharos
ENTRYPOINT ["pharos"]
