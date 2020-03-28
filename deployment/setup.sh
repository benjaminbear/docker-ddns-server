#!/bin/bash

[ -z "$DDNS_ADMIN_LOGIN" ] && echo "DDNS_ADMIN_LOGIN not set" && exit 1;
[ -z "$DDNS_DOMAIN" ] && echo "DDNS_DOMAIN not set" && exit 1;
[ -z "$DDNS_PARENT_NS" ] && echo "DDNS_PARENT_NS not set" && exit 1;
[ -z "$DDNS_DEFAULT_TTL" ] && echo "DDNS_DEFAULT_TTL not set" && exit 1;

DDNS_IP=$(curl icanhazip.com)

if ! grep 'zone "'$DDNS_DOMAIN'"' /etc/bind/named.conf > /dev/null
then
	echo "creating zone...";
	cat >> /etc/bind/named.conf <<EOF
zone "$DDNS_DOMAIN" {
	type master;
	file "$DDNS_DOMAIN.zone";
	allow-query { any; };
	allow-transfer { none; };
	allow-update { localhost; };
};
EOF
fi

if [ ! -f /var/cache/bind/$DDNS_DOMAIN.zone ]
then
	echo "creating zone file..."
	cat > /var/cache/bind/$DDNS_DOMAIN.zone <<EOF
\$ORIGIN .
\$TTL 86400	; 1 day
$DDNS_DOMAIN		IN SOA	${DDNS_PARENT_NS}. root.${DDNS_DOMAIN}. (
				74         ; serial
				3600       ; refresh (1 hour)
				900        ; retry (15 minutes)
				604800     ; expire (1 week)
				86400      ; minimum (1 day)
				)
			NS	${DDNS_PARENT_NS}.
			A	${DDNS_IP}
\$ORIGIN ${DDNS_DOMAIN}.
\$TTL ${DDNS_DEFAULT_TTL}
EOF
fi

# If /var/cache/bind is a volume, permissions are probably not ok
chown root:bind /var/cache/bind
chown bind:bind /var/cache/bind/*
chmod 770 /var/cache/bind
chmod 644 /var/cache/bind/*