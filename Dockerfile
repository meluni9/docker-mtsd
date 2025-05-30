FROM golang:1.24 AS build

WORKDIR /go/src/docker-mtsd
COPY . .

RUN go test ./...
ENV CGO_ENABLED=0
RUN go install ./cmd/...

# ==== Final image ====
FROM alpine:latest
WORKDIR /opt/docker-mtsd
COPY entry.sh /opt/docker-mtsd/
COPY --from=build /go/bin/* /opt/docker-mtsd
RUN ls /opt/docker-mtsd
ENTRYPOINT ["/opt/docker-mtsd/entry.sh"]
CMD ["server"]