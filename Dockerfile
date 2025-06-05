FROM golang:1.24 AS build

WORKDIR /go/src/docker-mtsd
COPY . .

RUN mkdir -p ratelimiter cache

RUN go test ./...

ENV CGO_ENABLED=0
RUN go install ./cmd/...

FROM alpine:latest
WORKDIR /opt/docker-mtsd

COPY entry.sh /opt/docker-mtsd/
RUN chmod +x /opt/docker-mtsd/entry.sh

COPY --from=build /go/bin/* /opt/docker-mtsd

RUN ls -la /opt/docker-mtsd

RUN apk --no-cache add ca-certificates

ENTRYPOINT ["/opt/docker-mtsd/entry.sh"]
CMD ["server"]