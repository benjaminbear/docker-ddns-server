#!/bin/bash

#[ -z "$DDNS_ADMIN_LOGIN" ] && echo "DDNS_ADMIN_LOGIN not set" && exit 1;
[ -z "$DDNS_DOMAINS" ] && echo "DDNS_DOMAINS not set" && exit 1;
[ -z "$DDNS_PARENT_NS" ] && echo "DDNS_PARENT_NS not set" && exit 1;
[ -z "$DDNS_DEFAULT_TTL" ] && echo "DDNS_DEFAULT_TTL not set" && exit 1;
[ -z "$DDNS_TRANSFER" ] && echo "DDNS_TRANSFER not set" && exit 1;

DDNS_IP=$(curl icanhazip.com)

for d in ${DDNS_DOMAINS//,/ }
do
        if ! grep 'zone "'$d'"' /etc/bind/named.conf > /dev/null
        then
                echo "creating zone...";
                cat >> /etc/bind/named.conf <<EOF
zone "$d" {
        type master;
        file "$d.zone";
        allow-query { any; };
        allow-transfer { ${DDNS_TRANSFER}; };
        allow-update { localhost; };
};
EOF
        fi

        if [ ! -f /var/cache/bind/$d.zone ]
        then
                echo "creating zone file..."
                cat > /var/cache/bind/$d.zone <<EOF
\$ORIGIN .
\$TTL 86400     ; 1 day
$d              IN SOA  ${DDNS_PARENT_NS}. root.${d}. (
                                74         ; serial
                                3600       ; refresh (1 hour)
                                900        ; retry (15 minutes)
                                604800     ; expire (1 week)
                                86400      ; minimum (1 day)
                                )
                        NS      ${DDNS_PARENT_NS}.
                        A       ${DDNS_IP}
\$ORIGIN ${d}.
\$TTL ${DDNS_DEFAULT_TTL}
EOF
        fi
done

# If /var/cache/bind is a volume, permissions are probably not ok
chown root:bind /var/cache/bind
chown bind:bind /var/cache/bind/*
chmod 770 /var/cache/bind
chmod 644 /var/cache/bind/*
