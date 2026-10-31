# Builds any binary under ./cmd: docker build --build-arg CMD=sample-be .
FROM golang:1.26-alpine AS build
ARG CMD
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/app ./cmd/${CMD}

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
