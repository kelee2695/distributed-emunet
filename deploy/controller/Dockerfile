FROM 100.75.179.29:5000/golang:1.25-alpine AS builder

ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -a -o ./bin/controller ./master/controller/cmd/main.go

FROM 100.75.179.29:5000/alpine:latest

WORKDIR /

RUN apk add --no-cache ca-certificates

COPY --from=builder /workspace/bin/controller /controller   

RUN chmod +x /controller   
