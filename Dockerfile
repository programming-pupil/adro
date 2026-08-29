FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/adro-api ./cmd/adro-api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/adro-api /adro-api
VOLUME ["/var/lib/adro/artifacts"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/adro-api", "-addr", ":8080", "-artifact-root", "/var/lib/adro/artifacts"]
