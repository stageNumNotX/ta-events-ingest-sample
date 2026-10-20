# syntax=docker/dockerfile:1

FROM golang:1.26 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Static binary, no CGO; -s -w strips debug info.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /bin/server /server
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
