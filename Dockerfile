FROM golang:1.23-alpine AS build
WORKDIR /app
COPY . .
RUN go mod tidy && go build -o /api-gateway .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /api-gateway /api-gateway
EXPOSE 8080
ENTRYPOINT ["/api-gateway"]
