FROM golang:1.25-bookworm AS build

WORKDIR /src

COPY services/auth-service/go.mod services/auth-service/go.mod
COPY services/auth-service/go.sum services/auth-service/go.sum

COPY pkg ./pkg

COPY services/auth-service ./services/auth-service

WORKDIR /src/services/auth-service

RUN CGO_ENABLED=0 go build -o /out/auth-service ./cmd

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/auth-service /auth-service

EXPOSE 9001

ENTRYPOINT ["/auth-service"]