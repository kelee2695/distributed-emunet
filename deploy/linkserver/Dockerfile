FROM 100.75.179.29:5000/golang:1.25-alpine AS builder

ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -a -o ./bin/linkserver ./master/linkserver/cmd/main.go

FROM 100.75.179.29:5000/alpine:latest

WORKDIR /

RUN apk add --no-cache ca-certificates

COPY --from=builder /workspace/bin/linkserver /linkserver
COPY web-console /web-console

RUN chmod +x /linkserver
