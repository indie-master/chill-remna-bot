# Сборка идёт на архитектуре раннера, бинарник кросс-компилируется под целевую.
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine AS build
ARG COMMIT=dev
ARG BUILD_DATE=
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" -o /out/bot ./cmd/bot
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/store-check ./cmd/store-check

FROM alpine:3.24
# Только docker-клиент: compose выполняется отдельным контейнером docker:cli,
# база часовых поясов вшита в бинарник (time/tzdata).
RUN apk add --no-cache ca-certificates docker-cli
WORKDIR /app
COPY --from=build /out/bot /app/bot
COPY --from=build /out/store-check /app/store-check
# Уведомления о лицензиях едут вместе с бинарником (требование MIT/BSD).
COPY LICENSE THIRD-PARTY-NOTICES.md /app/
VOLUME ["/data"]
ENV DATA_DIR=/data
ENTRYPOINT ["/app/bot"]
