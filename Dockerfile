FROM node:20-alpine AS ui
WORKDIR /ui
COPY ui/package.json ui/package-lock.json* ./
RUN npm install
COPY ui/ .
RUN npm run build

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /ui/dist ui/dist
RUN CGO_ENABLED=0 go build -o /bob .

FROM alpine:latest
RUN apk add --no-cache ca-certificates git go nodejs npm \
    && npm install -g @anthropic-ai/claude-code \
    && adduser -D -u 1000 worker
COPY --from=build /bob /bob
RUN mkdir -p /workspace && chown worker:worker /workspace
USER worker
ENTRYPOINT ["/bob"]
