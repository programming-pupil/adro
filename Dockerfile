FROM golang:1.27@sha256:3680233e3204827fbdc66088528ae6d4b3d034f51d03a99d454f6de034888244 AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/adro-api ./cmd/adro-api

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/adro-api /adro-api
VOLUME ["/var/lib/adro/artifacts"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/adro-api", "-addr", ":8080", "-artifact-root", "/var/lib/adro/artifacts"]
