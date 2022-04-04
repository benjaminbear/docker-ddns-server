FROM golang:latest as builder

ENV GO111MODULE=on
ENV GOPATH=/root/go
RUN mkdir -p /root/go/src
COPY dyndns /root/go/src/dyndns
RUN cd /root/go/src/dyndns && go mod download && GOOS=linux go build -o /root/go/bin/dyndns && go test -v

FROM debian:bullseye-slim

RUN DEBIAN_FRONTEND=noninteractive apt-get update && \
	apt-get install -q -y bind9 dnsutils curl && \
	apt-get clean

RUN chmod 770 /var/cache/bind
COPY deployment/setup.sh /root/setup.sh
RUN chmod +x /root/setup.sh
COPY deployment/named.conf.options /etc/bind/named.conf.options

WORKDIR /root
COPY --from=builder /root/go/bin/dyndns /root/dyndns
COPY dyndns/views /root/views
COPY dyndns/static /root/static

EXPOSE 53 8080
CMD ["sh", "-c", "/root/setup.sh ; service bind9 start ; /root/dyndns"]
