# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26
FROM golang:${GO_VERSION} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/specter ./cmd/specter

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /data
COPY --from=build /out/specter /usr/local/bin/specter
ENV SPECTER_OUTPUT_DIR=/data/reports
VOLUME ["/data"]
ENTRYPOINT ["specter"]
CMD ["help"]
