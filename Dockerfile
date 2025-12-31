FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN if [ "${VERSION}" = "dev" ] || [ "${COMMIT}" = "none" ] || [ "${BUILD_DATE}" = "unknown" ]; then \
      echo "production build required: VERSION/COMMIT/BUILD_DATE must be injected via build args" 1>&2; \
      echo "got VERSION=${VERSION} COMMIT=${COMMIT} BUILD_DATE=${BUILD_DATE}" 1>&2; \
      exit 1; \
    fi

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X 'main.Version=${VERSION}' -X 'main.Commit=${COMMIT}' -X 'main.BuildDate=${BUILD_DATE}'" -o ./CLIProxyAPI ./cmd/server/

FROM alpine:3.22.0

RUN apk add --no-cache tzdata

RUN mkdir /CLIProxyAPI

COPY --from=builder ./app/CLIProxyAPI /CLIProxyAPI/CLIProxyAPI

COPY config.example.yaml /CLIProxyAPI/config.example.yaml

WORKDIR /CLIProxyAPI

EXPOSE 8317

ENV TZ=Asia/Shanghai
ENV GIN_MODE=release

RUN cp /usr/share/zoneinfo/${TZ} /etc/localtime && echo "${TZ}" > /etc/timezone

CMD ["./CLIProxyAPI"]
