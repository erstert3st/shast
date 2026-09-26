#!/usr/bin/env bash
# seed.sh builds the pristine sandbox workspace for shast.
#
# Usage: seed.sh SEED_DIR MANIFEST
#
# Everything here is deterministic: no $RANDOM, no clock reads. Pseudo-random
# variation comes from a fixed linear congruential generator, all timestamps
# are hard-coded, and git commits use fixed author/committer dates. The last
# step writes MANIFEST, a sorted list of every path with its mode, size,
# mtime and sha256, so two builds can be compared byte for byte.
#
# Only mtimes are fixed. ctime, atime and birth time are set by the kernel
# when the image is built or the seed is copied, so commands that show them
# (ls -lc/-lu, plain stat, find -cmin/-amin/-newerct) are non-deterministic.
set -euo pipefail

seed_dir=${1:?usage: seed.sh SEED_DIR MANIFEST}
manifest=${2:?usage: seed.sh SEED_DIR MANIFEST}

export TZ=UTC LC_ALL=C.UTF-8
umask 022
cd "$seed_dir"

# Default mtime for every path that is not stamped explicitly below.
readonly default_mtime='2025-03-08 09:00:00'

# ---------------------------------------------------------------------------
# Deterministic pseudo-random numbers (classic ANSI C LCG).
lcg_state=42
# rnd N sets $r to a value in [0, N).
rnd() {
	lcg_state=$(((lcg_state * 1103515245 + 12345) % 2147483648))
	r=$(((lcg_state / 65536) % $1))
}

# ---------------------------------------------------------------------------
# Git history helpers.
# at DATE CMD... runs a git command with fixed author/committer dates, so
# commits, tags and reflog entries do not depend on the build time.
at() {
	local date="$1 +0000"
	shift
	GIT_AUTHOR_DATE=$date GIT_COMMITTER_DATE=$date "$@"
}
commit() { # commit DATE MESSAGE
	at "$1" git commit --quiet --no-verify -m "$2"
}

at '2025-03-08T09:00:00' git init --quiet --initial-branch=main .

# --- commit 1: initial ------------------------------------------------------
cat >README.md <<'EOF'
# shopapi

Small REST API for the shop backend: users, orders and a health check.

## Development

    make install
    make test
    make run

## Deployment

Deployments run `app/scripts/deploy.sh` on the web hosts (see configs/hosts).
Configuration lives in `configs/`; secrets are read from `configs/.env`.
EOF
cat >.gitignore <<'EOF'
# runtime artefacts
logs/
cache/
tmp/
backups/
*.gz

# secrets
.env
*.key

# python
__pycache__/
*.pyc
EOF
git add README.md .gitignore
commit '2025-03-08T09:12:00' 'Initial commit'

# --- commit 2: Makefile and app config -------------------------------------
cat >Makefile <<'EOF'
PYTHON ?= python3
APP    := app/src/main.py

.PHONY: install test run lint deploy

install:
	$(PYTHON) -m pip install -r requirements.txt

test:
	$(PYTHON) -m pytest app/tests

run:
	$(PYTHON) $(APP) --config configs/app.yaml

lint:
	$(PYTHON) -m ruff check app

deploy:
	./app/scripts/deploy.sh production
EOF
mkdir -p configs
cat >configs/app.yaml <<'EOF'
app:
  name: shopapi
  version: 1.0.0
  environment: production

server:
  host: 0.0.0.0
  port: 8080
  workers: 4
  timeout: 30

database:
  host: db01.internal
  port: 5432
  name: shop
  user: shop_app
  pool_size: 10

redis:
  host: cache01.internal
  port: 6379

log_level: info

features:
  new_checkout: true
  rate_limit: true
  beta_search: false
  dark_mode: false
EOF
git add Makefile configs/app.yaml
commit '2025-03-08T11:40:00' 'Add Makefile and app config'

# --- commit 3: user API ------------------------------------------------------
mkdir -p app/src/api
cat >app/src/main.py <<'EOF'
import argparse
import logging

from config import load_config
from db import connect
from api import create_app

log = logging.getLogger("shopapi")


def parse_args():
    parser = argparse.ArgumentParser(description="shopapi server")
    parser.add_argument("--config", default="configs/app.yaml")
    return parser.parse_args()


def main():
    args = parse_args()
    cfg = load_config(args.config)
    logging.basicConfig(level=cfg["log_level"].upper())
    db = connect(cfg["database"])
    app = create_app(cfg, db)
    # TODO: graceful shutdown on SIGTERM
    app.run(host=cfg["server"]["host"], port=cfg["server"]["port"])


if __name__ == "__main__":
    main()
EOF
cat >app/src/config.py <<'EOF'
import os

import yaml

DEFAULTS = {
    "log_level": "info",
    "server": {"host": "127.0.0.1", "port": 8080},
}


def load_config(path):
    with open(path) as fh:
        cfg = yaml.safe_load(fh)
    merged = {**DEFAULTS, **cfg}
    merged["database"]["password"] = os.environ.get("DB_PASSWORD", "")
    return merged
EOF
cat >app/src/db.py <<'EOF'
import logging

import psycopg

log = logging.getLogger("shopapi.db")


def connect(cfg):
    dsn = "host={host} port={port} dbname={name} user={user}".format(**cfg)
    log.info("connecting to %s", cfg["host"])
    # FIXME: pool_size is ignored, use psycopg_pool
    return psycopg.connect(dsn, password=cfg.get("password"))


def fetch_all(conn, query, *params):
    with conn.cursor() as cur:
        cur.execute(query, params)
        return cur.fetchall()
EOF
cat >app/src/api/__init__.py <<'EOF'
from flask import Flask

from .users import users_bp


def create_app(cfg, db):
    app = Flask("shopapi")
    app.config["DB"] = db
    app.register_blueprint(users_bp, url_prefix="/api/users")
    return app
EOF
cat >app/src/api/users.py <<'EOF'
from flask import Blueprint, current_app, jsonify, request

from db import fetch_all

users_bp = Blueprint("users", __name__)


def get_db():
    return current_app.config["DB"]


@users_bp.get("/")
def list_users():
    rows = fetch_all(get_db(), "SELECT id, name, email FROM users")
    return jsonify([dict(zip(("id", "name", "email"), r)) for r in rows])


@users_bp.get("/<int:user_id>")
def get_user(user_id):
    rows = fetch_all(get_db(), "SELECT id, name, email FROM users WHERE id = %s", user_id)
    if not rows:
        return jsonify(error="not found"), 404
    return jsonify(dict(zip(("id", "name", "email"), rows[0])))


@users_bp.post("/")
def create_user():
    payload = request.get_json()
    # TODO: validate email format
    fetch_all(get_db(), "INSERT INTO users (name, email) VALUES (%s, %s)",
              payload["name"], payload["email"])
    return jsonify(status="created"), 201
EOF
git add app
commit '2025-03-09T10:05:00' 'Add user API'

# --- commit 4: order API -----------------------------------------------------
cat >app/src/utils.py <<'EOF'
import re
from decimal import Decimal

EMAIL_RE = re.compile(r"^[^@\s]+@[^@\s]+\.[a-z]{2,}$")


def is_valid_email(value):
    return bool(EMAIL_RE.match(value))


def money(value):
    return float(value)


def paginate(items, page, per_page=20):
    start = (page - 1) * per_page
    return items[start:start + per_page]
EOF
cat >app/src/api/orders.py <<'EOF'
from flask import Blueprint, current_app, jsonify

from db import fetch_all
from utils import money

orders_bp = Blueprint("orders", __name__)


def order_total(items):
    return money(sum(i["qty"] * i["price"] for i in items))


@orders_bp.get("/")
def list_orders():
    rows = fetch_all(current_app.config["DB"], "SELECT id, user_id, status FROM orders")
    return jsonify([dict(zip(("id", "user_id", "status"), r)) for r in rows])


@orders_bp.get("/<int:order_id>")
def get_order(order_id):
    # FIXME: N+1 query for order items
    rows = fetch_all(current_app.config["DB"], "SELECT * FROM orders WHERE id = %s", order_id)
    if not rows:
        return jsonify(error="not found"), 404
    return jsonify(rows[0])
EOF
cat >app/src/api/__init__.py <<'EOF'
from flask import Flask

from .orders import orders_bp
from .users import users_bp


def create_app(cfg, db):
    app = Flask("shopapi")
    app.config["DB"] = db
    app.register_blueprint(users_bp, url_prefix="/api/users")
    app.register_blueprint(orders_bp, url_prefix="/api/orders")
    return app
EOF
git add app
commit '2025-03-10T14:22:00' 'Add order API'

# --- commit 5: tests ---------------------------------------------------------
mkdir -p app/tests
cat >app/tests/test_users.py <<'EOF'
from utils import is_valid_email


def test_valid_email():
    assert is_valid_email("anna@shop.example")


def test_invalid_email():
    assert not is_valid_email("not-an-email")
    assert not is_valid_email("a b@shop.example")
EOF
cat >app/tests/test_orders.py <<'EOF'
from api.orders import order_total


def test_order_total():
    items = [{"qty": 2, "price": 9.99}, {"qty": 1, "price": 0.02}]
    assert order_total(items) == 20.00


def test_empty_order():
    assert order_total([]) == 0
EOF
git add app/tests
commit '2025-03-11T09:30:00' 'Add tests for users and orders'

# --- commit 6: ops: scripts, nginx, hosts, crontab ---------------------------
mkdir -p app/scripts configs/ssl
cat >app/scripts/deploy.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

ENVIRONMENT=${1:-staging}
HOSTS=$(awk '/web[0-9]+/ {print $2}' configs/hosts)

echo "deploying shopapi to ${ENVIRONMENT}"
for host in $HOSTS; do
    echo "-> ${host}"
    rsync -az --delete app/ "deploy@${host}:/srv/shopapi/"
    ssh "deploy@${host}" 'sudo systemctl restart shopapi'
done
EOF
cat >app/scripts/backup.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

STAMP=$(date +%F)
TARGET="backups/db-${STAMP}.sql.gz"

pg_dump --host db01.internal --username shop_app shop | gzip >"${TARGET}"
find backups -name 'db-*.sql.gz' -mtime +7 -delete
echo "backup written to ${TARGET}"
EOF
cat >app/scripts/healthcheck.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

URL=${1:-http://localhost:8080/health}

if curl -fsS --max-time 5 "$URL" >/dev/null; then
    echo "OK"
else
    echo "FAIL: $URL"
    exit 1
fi
EOF
chmod 755 app/scripts/*.sh
cat >configs/nginx.conf <<'EOF'
user www-data;
worker_processes auto;
pid /run/nginx.pid;

events {
    worker_connections 1024;
}

http {
    sendfile on;
    keepalive_timeout 65;
    access_log /var/log/nginx/access.log;
    error_log /var/log/nginx/error.log warn;

    upstream shopapi {
        server 10.0.2.11:8080 max_fails=3;
        server 10.0.2.12:8080 max_fails=3;
        server 10.0.2.13:8080 backup;
    }

    server {
        listen 80;
        server_name shop.example api.shop.example;
        return 301 https://$host$request_uri;
    }

    server {
        listen 443 ssl;
        server_name api.shop.example;

        ssl_certificate     /etc/nginx/ssl/server.crt;
        ssl_certificate_key /etc/nginx/ssl/server.key;

        location / {
            proxy_pass http://shopapi;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
        }

        location /api/ {
            proxy_pass http://shopapi;
            proxy_read_timeout 30s;
        }

        location /health {
            access_log off;
            return 200 "ok\n";
        }

        location /static/ {
            root /srv/shopapi;
            expires 7d;
        }
    }
}
EOF
cat >configs/hosts <<'EOF'
127.0.0.1    localhost
10.0.1.10    lb01.internal lb01
10.0.2.11    web01.internal web01
10.0.2.12    web02.internal web02
10.0.2.13    web03.internal web03
10.0.3.20    db01.internal db01
10.0.3.21    db02.internal db02
10.0.4.30    cache01.internal cache01
10.0.5.40    worker01.internal worker01
EOF
cat >configs/crontab <<'EOF'
# m h dom mon dow command
*/5 * * * *  /srv/shopapi/app/scripts/healthcheck.sh >/dev/null 2>&1
15 2 * * *   /srv/shopapi/app/scripts/backup.sh
0 3 * * 0    find /var/log/shopapi -name '*.gz' -mtime +30 -delete
30 4 * * 1-5 /srv/shopapi/app/scripts/deploy.sh staging
EOF
cat >configs/ssl/server.crt <<'EOF'
-----BEGIN CERTIFICATE-----
MIIBszCCAVmgAwIBAgIUQ2xvdWRTaG9wRXhhbXBsZUNlcnQwCgYIKoZIzj0EAwIw
FzEVMBMGA1UEAwwMc2hvcC5leGFtcGxlMB4XDTI1MDMwMTAwMDAwMFoXDTI2MDMw
MTAwMDAwMFowFzEVMBMGA1UEAwwMc2hvcC5leGFtcGxlMFkwEwYHKoZIzj0CAQYI
KoZIzj0DAQcDQgAEZmFrZWtleWZvcnNoYXN0c2FuZGJveG9ubHlub3RyZWFsZmFr
ZWtleWZvcnNoYXN0c2FuZGJveG9ubHlub3RyZWFsMAoGCCqGSM49BAMCA0gAMEUC
IQDmYWtlc2lnbmF0dXJlZm9yc2hhc3RzYW5kYm94AiBmYWtlc2lnbmF0dXJlMg==
-----END CERTIFICATE-----
EOF
git add app/scripts configs/nginx.conf configs/hosts configs/crontab configs/ssl/server.crt
commit '2025-03-12T16:48:00' 'Add deploy scripts and nginx config'

# --- commit 7: bugfix ----------------------------------------------------------
sed -i 's/^    return float(value)$/    return float(Decimal(str(value)).quantize(Decimal("0.01")))/' app/src/utils.py
git add app/src/utils.py
commit '2025-03-13T11:02:00' 'Fix order total rounding'

# --- side branch -----------------------------------------------------------------
at '2025-03-13T15:00:00' git checkout --quiet -b feature/rate-limit
cat >>configs/nginx.conf <<'EOF'
# limit_req_zone $binary_remote_addr zone=api:10m rate=10r/s;
EOF
git add configs/nginx.conf
commit '2025-03-13T15:20:00' 'WIP: rate limiting for the API'
at '2025-03-14T09:00:00' git checkout --quiet main

# --- commit 8: sample data ---------------------------------------------------------
mkdir -p data

first_names=(Anna Ben Clara David Emma Felix Greta Hannah Ivan Julia Karl Lena Max Nina Oskar Paula Quentin Rosa Stefan Tina)
last_names=(Schmidt Mueller Weber Fischer Meyer Wagner Becker Hoffmann Koch Richter Klein Wolf)
roles=(dev dev ops viewer dev admin viewer ops)
countries=(DE DE US FR NL DE AT US CH GB)
{
	echo 'id,name,email,role,country,created_at,active'
	for i in $(seq 1 50); do
		rnd ${#first_names[@]}; fn=${first_names[r]}
		rnd ${#last_names[@]}; ln=${last_names[r]}
		rnd ${#roles[@]}; role=${roles[r]}
		rnd ${#countries[@]}; country=${countries[r]}
		rnd 28; day=$((r + 1))
		rnd 12; month=$((r + 1))
		rnd 5; active=$([[ $r -eq 0 ]] && echo false || echo true)
		lower_fn=${fn,,}; lower_ln=${ln,,}
		printf '%d,%s %s,%s.%s%d@shop.example,%s,%s,2024-%02d-%02d,%s\n' \
			"$i" "$fn" "$ln" "$lower_fn" "$lower_ln" "$i" "$role" "$country" "$month" "$day" "$active"
	done
} >data/users.csv

statuses=(paid paid shipped shipped shipped pending cancelled paid)
skus=(SKU-1001 SKU-1002 SKU-1003 SKU-2001 SKU-2002 SKU-3001 SKU-3002 SKU-4001)
prices=(1999 499 8950 2500 1299 34900 750 5999)
{
	echo '['
	for i in $(seq 1 40); do
		rnd 50; user_id=$((r + 1))
		rnd ${#statuses[@]}; status=${statuses[r]}
		rnd 3; n_items=$((r + 1))
		items='' total=0
		for ((k = 0; k < n_items; k++)); do
			rnd ${#skus[@]}; idx=$r
			rnd 4; qty=$((r + 1))
			price=${prices[idx]}
			total=$((total + qty * price))
			[[ -n $items ]] && items+=', '
			items+=$(printf '{"sku": "%s", "qty": %d, "price": %d.%02d}' "${skus[idx]}" "$qty" $((price / 100)) $((price % 100)))
		done
		rnd 24; hour=$r
		rnd 60; minute=$r
		day=$((1 + (i - 1) * 14 / 40))
		sep=','; [[ $i -eq 40 ]] && sep=''
		printf '  {"id": %d, "user_id": %d, "status": "%s", "total": %d.%02d, "currency": "EUR", "items": [%s], "created_at": "2025-03-%02dT%02d:%02d:00Z"}%s\n' \
			$((1000 + i)) "$user_id" "$status" $((total / 100)) $((total % 100)) "$items" "$day" "$hour" "$minute" "$sep"
	done
	echo ']'
} >data/orders.json

cat >data/servers.json <<'EOF'
[
  {"name": "lb01", "ip": "10.0.1.10", "role": "lb", "region": "eu-central", "cpu": 2, "mem_gb": 4, "status": "up", "tags": ["edge", "prod"]},
  {"name": "web01", "ip": "10.0.2.11", "role": "web", "region": "eu-central", "cpu": 4, "mem_gb": 8, "status": "up", "tags": ["app", "prod"]},
  {"name": "web02", "ip": "10.0.2.12", "role": "web", "region": "eu-central", "cpu": 4, "mem_gb": 8, "status": "degraded", "tags": ["app", "prod"]},
  {"name": "web03", "ip": "10.0.2.13", "role": "web", "region": "eu-west", "cpu": 4, "mem_gb": 8, "status": "up", "tags": ["app", "prod", "backup"]},
  {"name": "db01", "ip": "10.0.3.20", "role": "db", "region": "eu-central", "cpu": 8, "mem_gb": 32, "status": "up", "tags": ["postgres", "primary", "prod"]},
  {"name": "db02", "ip": "10.0.3.21", "role": "db", "region": "eu-west", "cpu": 8, "mem_gb": 32, "status": "up", "tags": ["postgres", "replica", "prod"]},
  {"name": "cache01", "ip": "10.0.4.30", "role": "cache", "region": "eu-central", "cpu": 2, "mem_gb": 16, "status": "up", "tags": ["redis", "prod"]},
  {"name": "worker01", "ip": "10.0.5.40", "role": "worker", "region": "eu-central", "cpu": 4, "mem_gb": 8, "status": "down", "tags": ["queue", "prod"]},
  {"name": "worker02", "ip": "10.0.5.41", "role": "worker", "region": "eu-west", "cpu": 4, "mem_gb": 8, "status": "up", "tags": ["queue", "prod"]},
  {"name": "staging01", "ip": "10.0.9.90", "role": "web", "region": "eu-central", "cpu": 2, "mem_gb": 4, "status": "up", "tags": ["app", "staging"]}
]
EOF
git add data
commit '2025-03-14T10:15:00' 'Add sample data'
at '2025-03-14T10:20:00' git tag --annotate v1.0.0 --message 'Release 1.0.0'

# Uncommitted work in progress, so `git status` / `git diff` have something to show.
sed -i 's/^    "log_level": "info",$/    "log_level": "debug",/' app/src/config.py
cat >notes.txt <<'EOF'
- rotate API_KEY before the next release
- check why web02 is degraded
- move backups to object storage
EOF

# The index caches stat data (inode, ctime) of this build; rebuild it from
# HEAD without stat data so the repository is byte-identical across builds.
rm .git/index
git read-tree HEAD
rm -f .git/COMMIT_EDITMSG .git/ORIG_HEAD

# ---------------------------------------------------------------------------
# Untracked runtime files: secrets, logs, cache, backups, tmp.
cat >configs/.env <<'EOF'
APP_ENV=production
DB_HOST=db01.internal
DB_PORT=5432
DB_NAME=shop
DB_USER=shop_app
DB_PASSWORD=s3cr3t-db-pass
REDIS_URL=redis://cache01.internal:6379/0
API_KEY=sk_live_4f9a2c7e1b8d
JWT_SECRET=change-me-please
SMTP_HOST=mail.shop.example
EOF
chmod 600 configs/.env
cat >configs/ssl/server.key <<'EOF'
-----BEGIN PRIVATE KEY-----
ZmFrZS1rZXktZm9yLXRoZS1zaGFzdC1zYW5kYm94LW5vdC1hLXJlYWwta2V5LWZh
a2Uta2V5LWZvci10aGUtc2hhc3Qtc2FuZGJveC1ub3QtYS1yZWFsLWtleQ==
-----END PRIVATE KEY-----
EOF
chmod 600 configs/ssl/server.key

mkdir -p logs
client_ips=(10.0.1.10 192.168.1.23 192.168.1.23 203.0.113.7 203.0.113.7 203.0.113.7 198.51.100.14
	198.51.100.14 10.0.5.40 172.16.4.2 192.168.1.77 203.0.113.99 45.33.12.8 45.33.12.8 45.33.12.8
	91.198.174.2 66.249.66.1 10.0.2.11 192.0.2.44 192.0.2.45)
methods=(GET GET GET GET GET GET POST POST PUT DELETE)
paths=(/api/users /api/users /api/users/42 /api/orders /api/orders /api/orders/1017 /api/products
	/api/cart /api/checkout /api/login /health /health /static/app.js /static/style.css /api/search /admin)
statuses_http=(200 200 200 200 200 200 200 200 201 304 301 400 401 403 404 404 500 502)
agents=('curl/8.5.0' 'Mozilla/5.0 (X11; Linux x86_64) Firefox/123.0' 'Mozilla/5.0 (Macintosh) Safari/17.3'
	'python-requests/2.31.0' 'Googlebot/2.1' 'kube-probe/1.29')

# access_log START_EPOCH LINES writes nginx combined log lines to stdout.
access_log() {
	local t=$1 n=$2 i ip method path status bytes agent
	for ((i = 0; i < n; i++)); do
		rnd 90; t=$((t + r + 5))
		rnd ${#client_ips[@]}; ip=${client_ips[r]}
		rnd ${#methods[@]}; method=${methods[r]}
		rnd ${#paths[@]}; path=${paths[r]}
		[[ $path == /static/* || $path == /health ]] && method=GET
		rnd ${#statuses_http[@]}; status=${statuses_http[r]}
		rnd 9000; bytes=$((r + 120))
		[[ $status == 304 || $status == 301 ]] && bytes=0
		rnd ${#agents[@]}; agent=${agents[r]}
		printf '%s - - [%(%d/%b/%Y:%H:%M:%S +0000)T] "%s %s HTTP/1.1" %s %d "-" "%s"\n' \
			"$ip" "$t" "$method" "$path" "$status" "$bytes" "$agent"
	done
}

levels=(INFO INFO INFO INFO INFO DEBUG DEBUG WARN WARN ERROR)
components=(api api db auth worker)
declare -A messages=(
	[INFO]='request completed|user logged in|order created|cache refreshed|job finished'
	[DEBUG]='query took 12ms|cache hit for user list|payload validated|retrying connection'
	[WARN]='slow query detected|rate limit approaching|disk usage above 80%|deprecated endpoint called'
	[ERROR]='database connection refused|payment gateway timeout|failed to send email|unhandled exception in handler'
)

# app_log START_EPOCH LINES writes application log lines to stdout.
app_log() {
	local t=$1 n=$2 i level component msgs
	for ((i = 0; i < n; i++)); do
		rnd 240; t=$((t + r + 10))
		rnd ${#levels[@]}; level=${levels[r]}
		rnd ${#components[@]}; component=${components[r]}
		IFS='|' read -ra msgs <<<"${messages[$level]}"
		rnd ${#msgs[@]}
		printf '%(%Y-%m-%dT%H:%M:%SZ)T %s [%s] %s\n' "$t" "$level" "$component" "${msgs[r]}"
	done
}

# Epoch seconds of 2025-03-13/14/15 00:00:00 UTC.
readonly day13=1741824000 day14=1741910400 day15=1741996800

access_log "$day15" 500 >logs/access.log
access_log "$day14" 200 >logs/access.log.1
access_log "$day13" 150 >logs/access.log.2
app_log "$day15" 300 >logs/app.log
app_log "$day14" 120 >logs/app.log.1

errors=('connect() failed (111: Connection refused) while connecting to upstream'
	'upstream timed out (110: Connection timed out) while reading response header from upstream'
	'open() "/srv/shopapi/static/favicon.ico" failed (2: No such file or directory)'
	'client intended to send too large body: 10485761 bytes')
upstreams=(10.0.2.11 10.0.2.12 10.0.2.13)
{
	t=$day15
	for i in $(seq 1 40); do
		rnd 1800; t=$((t + r + 60))
		rnd ${#errors[@]}; err=${errors[r]}
		rnd ${#client_ips[@]}; ip=${client_ips[r]}
		rnd ${#upstreams[@]}; up=${upstreams[r]}
		level=error; [[ $err == open* ]] && level=warn
		printf '%(%Y/%m/%d %H:%M:%S)T [%s] 812#812: *%d %s, client: %s, server: api.shop.example, upstream: "http://%s:8080"\n' \
			"$t" "$level" $((i * 7)) "$err" "$ip" "$up"
	done
} >logs/error.log

gzip -n -9 logs/access.log.1 logs/access.log.2 logs/app.log.1

mkdir -p cache
for spec in sessions:4 thumbnails:16 search-index:64 templates:256 assets:512 products:1024 geoip:2048; do
	name=${spec%%:*} kib=${spec#*:}
	head -c $((kib * 1024)) /dev/zero | tr '\0' 'x' >"cache/${name}.bin"
done

mkdir -p backups
for d in 10 11 12 13 14; do
	{
		echo "-- PostgreSQL database dump of shop, 2025-03-${d}"
		for ((i = 1; i <= (d - 9) * 400; i++)); do
			rnd 100000
			printf "INSERT INTO orders VALUES (%d, %d, 'paid', %d.%02d);\n" "$i" $((r % 50 + 1)) $((r / 100)) $((r % 100))
		done
	} | gzip -n -9 >"backups/db-2025-03-${d}.sql.gz"
done

mkdir -p tmp
printf 'user_id=17\ncart=SKU-1001,SKU-2002\n' >tmp/session-a1b2.tmp
printf 'user_id=42\n' >tmp/session-c3d4.tmp
: >tmp/session-e5f6.tmp
: >tmp/upload-0001.tmp

# Sanity checks: generated data must be valid.
jq empty data/orders.json data/servers.json
gzip -t logs/*.gz backups/*.gz

# ---------------------------------------------------------------------------
# Timestamps. First everything gets the default, then files get their
# realistic times; directories are stamped last because creating entries
# changes their mtime.
find . -exec touch -h -d "$default_mtime" {} +

stamp() { # stamp DATE PATH...
	local d=$1
	shift
	touch -h -d "$d" "$@"
}
stamp '2025-03-08 09:12:00' README.md .gitignore
stamp '2025-03-08 11:40:00' Makefile configs/app.yaml
stamp '2025-03-09 10:05:00' app/src/main.py app/src/db.py app/src/api/users.py
stamp '2025-03-10 14:22:00' app/src/api/orders.py app/src/api/__init__.py
stamp '2025-03-11 09:30:00' app/tests/test_users.py app/tests/test_orders.py
stamp '2025-03-12 16:48:00' app/scripts/*.sh configs/nginx.conf configs/hosts configs/crontab configs/ssl/server.crt
stamp '2025-03-13 11:02:00' app/src/utils.py
stamp '2025-03-14 10:15:00' data/users.csv data/orders.json data/servers.json
stamp '2025-03-15 08:30:00' app/src/config.py notes.txt
stamp '2025-03-09 18:00:00' configs/.env configs/ssl/server.key
stamp '2025-03-13 23:59:58' logs/access.log.2.gz
stamp '2025-03-14 23:59:59' logs/access.log.1.gz logs/app.log.1.gz
stamp '2025-03-15 17:42:10' logs/access.log logs/app.log logs/error.log
for d in 10 11 12 13 14; do
	stamp "2025-03-$((d + 1)) 02:15:00" "backups/db-2025-03-${d}.sql.gz"
done
stamp '2025-03-11 06:00:00' cache/sessions.bin cache/thumbnails.bin
stamp '2025-03-13 06:00:00' cache/search-index.bin cache/templates.bin cache/assets.bin
stamp '2025-03-15 06:00:00' cache/products.bin cache/geoip.bin
stamp '2025-03-12 20:00:00' tmp/session-a1b2.tmp tmp/upload-0001.tmp
stamp '2025-03-15 12:00:00' tmp/session-c3d4.tmp tmp/session-e5f6.tmp

find .git -exec touch -h -d '2025-03-14 10:20:00' {} +
find . -mindepth 1 -type d -not -path './.git*' -exec touch -h -d '2025-03-15 18:00:00' {} +
touch -d '2025-03-15 18:00:00' .

# ---------------------------------------------------------------------------
# Manifest: path, mode, size, mtime, sha256 (directories and links: "-").
{
	find . -printf '%p\t%M\t%s\t%TY-%Tm-%Td %TH:%TM:%TS\n' | while IFS=$'\t' read -r path mode size mtime; do
		sum=-
		[[ -f $path && ! -L $path ]] && sum=$(sha256sum <"$path" | cut -d' ' -f1)
		[[ -d $path ]] && size=-
		printf '%s %s %s %s %s\n' "$path" "$mode" "$size" "$mtime" "$sum"
	done
} | LC_ALL=C sort >"$manifest"
