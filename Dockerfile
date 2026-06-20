FROM golang:1.25-alpine

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY main.go .

RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w" -o /usr/local/bin/hh-ai-responder . && \
    rm main.go

CMD ["hh-ai-responder"]
