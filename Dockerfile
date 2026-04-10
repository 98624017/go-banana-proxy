FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w" -o /out/banana-proxy ./...

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
ENV PORT=8787
EXPOSE 8787
COPY --from=build /out/banana-proxy /usr/local/bin/banana-proxy
ENTRYPOINT ["/usr/local/bin/banana-proxy"]
