FROM node:22-alpine AS web-build
WORKDIR /src/web
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
ARG VITE_ALLOW_DEV_AUTH_UI=false
ENV VITE_ALLOW_DEV_AUTH_UI=$VITE_ALLOW_DEV_AUTH_UI
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
COPY marketing/ /src/marketing/
COPY shared/brand/ /src/shared/brand/
COPY shared/openapi/fixtures/curation-step4.v1.json /src/shared/openapi/fixtures/curation-step4.v1.json
COPY docs/design/ /src/docs/design/
RUN npm run build

FROM golang:1.26.6-alpine AS server-build
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
ARG VCS_REF=unknown
RUN CGO_ENABLED=0 go build -ldflags "-X main.sourceRevision=${VCS_REF}" -o /out/vitlane ./cmd/vitlane

FROM alpine:3.21
ARG VERSION=dev
ARG VCS_REF=unknown
ARG SOURCE_URL=https://github.com/HershyOrg/Vitlane-AI-Browser-Preview
LABEL org.opencontainers.image.title="Vitlane" \
      org.opencontainers.image.description="Vitlane AI Browser public preview" \
      org.opencontainers.image.source="${SOURCE_URL}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.version="${VERSION}"
RUN addgroup -S -g 10001 vitlane \
    && adduser -S -D -H -u 10001 -G vitlane vitlane
WORKDIR /app
COPY --from=server-build /out/vitlane /app/vitlane
COPY server/migrations /app/migrations
COPY --from=web-build /src/web/dist /app/web
COPY marketing /app/marketing
COPY --from=web-build /src/marketing/assets/generated /app/marketing/assets/generated
USER vitlane
ENV HTTP_ADDR=:8080
ENV MIGRATIONS_DIR=/app/migrations
ENV WEB_DIR=/app/web
EXPOSE 8080
ENTRYPOINT ["/app/vitlane"]
