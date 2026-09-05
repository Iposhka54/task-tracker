FROM golang:1.25-bookworm AS build

WORKDIR /src

COPY services/gateway/go.mod services/gateway/go.mod
COPY services/gateway/go.sum services/gateway/go.sum

COPY pkg ./pkg

COPY services/gateway ./services/gateway

WORKDIR /src/services/gateway

RUN CGO_ENABLED=0 go build -o /out/gateway ./cmd

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/gateway /gateway

EXPOSE 80

ENTRYPOINT ["/gateway"]