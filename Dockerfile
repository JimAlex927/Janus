FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npx tsc -b && npx vite build --outDir /ui

FROM golang:1.26.8-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN rm -rf internal/admin/ui/assets && mkdir -p internal/admin/ui/assets
COPY --from=frontend /ui/ internal/admin/ui/
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/janus ./cmd/janus

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build --chown=nonroot:nonroot /out/janus /janus
USER nonroot:nonroot
EXPOSE 8081/tcp 9090/tcp
ENTRYPOINT ["/janus"]
