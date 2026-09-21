#!/bin/sh
# Creates a PRIVATE local CA and a server certificate; does not install trust.
# Requires OpenSSL 3. Keep this directory private; share only rootCA.crt.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cert_dir="$root/.local/vault-tls"
openssl_bin=${JANUS_OPENSSL:-openssl}
case "$("$openssl_bin" version)" in
  "OpenSSL 3."*) ;;
  *) echo 'OpenSSL 3 is required (set JANUS_OPENSSL to its executable).' >&2; exit 1 ;;
esac
if test -e "$cert_dir"; then
  echo "Refusing to overwrite $cert_dir; preserve existing keys and device trust." >&2
  exit 1
fi
umask 077
mkdir -p "$cert_dir"
"$openssl_bin" req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -noenc \
  -keyout "$cert_dir/rootCA.key" -out "$cert_dir/rootCA.crt" -days 3650 \
  -subj '/CN=Janus local-vault private CA' \
  -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' \
  -addext 'keyUsage=critical,keyCertSign,cRLSign'
"$openssl_bin" req -new -newkey ec -pkeyopt ec_paramgen_curve:P-256 -noenc \
  -keyout "$cert_dir/server.key" -out "$cert_dir/server.csr" \
  -subj '/CN=39.104.66.49' \
  -addext 'basicConstraints=critical,CA:FALSE' \
  -addext 'keyUsage=critical,digitalSignature' \
  -addext 'extendedKeyUsage=serverAuth' \
  -addext 'subjectAltName=IP:39.104.66.49,IP:127.0.0.1,DNS:localhost'
"$openssl_bin" x509 -req -in "$cert_dir/server.csr" \
  -CA "$cert_dir/rootCA.crt" -CAkey "$cert_dir/rootCA.key" \
  -set_serial "0x$("$openssl_bin" rand -hex 16)" -days 365 -copy_extensions copy \
  -out "$cert_dir/server.crt"
"$openssl_bin" verify -CAfile "$cert_dir/rootCA.crt" -purpose sslserver \
  -verify_ip 39.104.66.49 "$cert_dir/server.crt"
"$openssl_bin" x509 -in "$cert_dir/rootCA.crt" -noout -fingerprint -sha256
echo "Created $cert_dir. Install rootCA.crt on clients; NEVER share either .key file."
