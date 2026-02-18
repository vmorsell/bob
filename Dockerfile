FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bob .

FROM alpine:latest
RUN apk add --no-cache ca-certificates git
COPY --from=build /bob /bob
ENTRYPOINT ["/bob"]
