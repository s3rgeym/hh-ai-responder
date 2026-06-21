FROM golang:1.25-alpine

RUN apk add --no-cache ca-certificates tzdata

ARG UID=1000
ARG GID=1000

RUN addgroup -g ${GID} -S appgroup \
 && adduser  -u ${UID} -S appuser -G appgroup

WORKDIR /app

COPY main.go go.mod .

RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w" -o /usr/local/bin/hh-ai-responder .

RUN chmod +x /usr/local/bin/hh-ai-responder

USER appuser

ENTRYPOINT ["hh-ai-responder"]
