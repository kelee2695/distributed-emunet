FROM 100.75.179.29:5000/golang:1.25-alpine AS builder

ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN apk add --no-cache make gcc musl-dev linux-headers

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -ldflags="-w -s" -o ./bin/node-agent ./node/emunet-node-agent/cmd/main.go
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -ldflags="-w -s" -o ./bin/emu-cni ./emu-cni/cmd/main.go
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -ldflags="-w -s" -o ./bin/debug-cni ./debug-cni/main.go

FROM 100.75.179.29:5000/alpine:latest

WORKDIR /

RUN apk add --no-cache ca-certificates

COPY --from=builder /workspace/bin/node-agent /node-agent
COPY --from=builder /workspace/bin/emu-cni /emu-cni
COPY --from=builder /workspace/bin/debug-cni /debug-cni
COPY deploy/emunet-node-agent/install-cni.sh /install-cni.sh

RUN chmod +x /node-agent /emu-cni /debug-cni /install-cni.sh    
