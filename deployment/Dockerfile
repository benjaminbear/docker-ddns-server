FROM golang:1.18 as builder

ENV GO111MODULE=on
RUN mkdir -p /root/go/src
COPY dyndns /root/go/src/dyndns
WORKDIR /root/go/src/dyndns
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /root/go/bin/dyndns && go test -v

FROM scratch

COPY --from=builder /root/go/bin/dyndns /dyndns
COPY dyndns/views /views
COPY dyndns/static /static

EXPOSE 53 8080
CMD ["/dyndns"]