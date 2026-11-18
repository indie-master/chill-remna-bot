#!/usr/bin/env bash
set -Eeuo pipefail
source_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
fixture=$(mktemp -d)
project=customer-store-update-ci
container=customer-store-update-ci-bot
volume=customer-store-update-ci-data
revision=$(printf '%040d' 1)
revision2=$(printf '%040d' 2)
image="customer-store-local:$revision"
cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  if [[ -n "${http_pid:-}" ]]; then kill "$http_pid" 2>/dev/null || true; fi
}
trap cleanup EXIT
bash -n "$source_dir/deploy/update-existing.sh"
mkdir "$fixture/seed"
mkdir "$fixture/www"
printf 'ok\n' > "$fixture/www/healthz"
# The fixture never starts the real Telegram receiver. A loopback HTTP stub
# verifies readiness while the test container shares this isolated CI host.
python3 -m http.server 9080 --bind 127.0.0.1 --directory "$fixture/www" > "$fixture/http.log" 2>&1 &
http_pid=$!
for ((i=0;i<20;i++)); do
  if curl -fsS http://127.0.0.1:9080/healthz >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -fsS http://127.0.0.1:9080/healthz >/dev/null
python3 - "$source_dir" "$fixture/seed" <<'PY'
import pathlib, sqlite3, sys
root, out = map(pathlib.Path, sys.argv[1:])
db = sqlite3.connect(out / 'bot.db')
db.execute('CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)')
for path in sorted((root / 'internal/storage/migrations/sqlite').glob('*.sql')):
    version = int(path.name.split('_', 1)[0])
    if version <= 77:
        db.executescript(path.read_text(encoding='utf-8'))
        db.execute('INSERT INTO schema_migrations VALUES (?)', (version,))
db.execute("INSERT INTO users (telegram_id, p2p_approved, created_at, balance) VALUES (10001, 0, '2026-01-01T00:00:00Z', 12345)")
db.commit()
db.close()
(out / 'secret.key').write_text('synthetic-test-key-only')
(out / 'bootstrap.json').write_text('{}')
PY
docker build --build-arg "COMMIT=$revision" -t "$image" -f "$source_dir/Dockerfile" "$source_dir"
docker volume create "$volume" >/dev/null
docker run --rm --network none --entrypoint sh \
  --mount "type=volume,src=$volume,dst=/data" --mount "type=bind,src=$fixture/seed,dst=/seed,readonly" \
  "$image" -ec 'cp /seed/* /data/'
cat > "$fixture/compose.yaml" <<EOF
name: $project
services:
  bot:
    image: $image
    pull_policy: never
    container_name: $container
    network_mode: host
    entrypoint: [/bin/sh, -ec]
    command: ['echo after > /data/after-switch; exec sleep 3600']
    environment:
      BOT_TOKEN: synthetic-only
      ADMIN_TELEGRAM_ID: "12345"
      COMPOSE_HOST_DIR: $fixture
    volumes:
      - data:/data
      - $fixture:/compose
volumes:
  data:
    external: true
    name: $volume
EOF
docker compose -p "$project" --project-directory "$fixture" -f "$fixture/compose.yaml" up -d --no-deps bot
old_id=$(docker inspect -f '{{.Id}}' "$container")
before=$(docker exec "$container" sha256sum /data/bot.db)
sudo STORE_HEALTH_PORT=9080 bash "$source_dir/deploy/update-existing.sh" prepare "$fixture" "$container" "$revision"
[[ $(docker inspect -f '{{.Id}}' "$container") == "$old_id" ]]
[[ $(docker inspect -f '{{.State.Running}}' "$container") == true ]]
[[ $(docker exec "$container" sha256sum /data/bot.db) == "$before" ]]
stage="$fixture/.private/store-update/$revision"
sudo python3 - "$stage/check/bot.db" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
assert db.execute('SELECT MAX(version) FROM schema_migrations').fetchone()[0] == 84
assert db.execute('SELECT balance FROM users WHERE telegram_id=10001').fetchone()[0] == 12345
PY
cp "$fixture/compose.yaml" "$fixture/compose.original"
printf '\n# changed after prepare\n' >> "$fixture/compose.yaml"
if sudo bash "$source_dir/deploy/update-existing.sh" apply "$fixture" "$container" "$revision"; then exit 1; fi
[[ $(docker inspect -f '{{.Id}}' "$container") == "$old_id" ]]
cp "$fixture/compose.original" "$fixture/compose.yaml"
# An unreadable file makes the restricted backup helper fail. The original
# container must resume, retaining its identity, before any image handoff.
docker exec "$container" sh -ec 'touch /data/backup-denied; chmod 000 /data/backup-denied'
if sudo bash "$source_dir/deploy/update-existing.sh" apply "$fixture" "$container" "$revision"; then exit 1; fi
[[ $(docker inspect -f '{{.Id}}' "$container") == "$old_id" ]]
[[ $(docker inspect -f '{{.State.Running}}' "$container") == true ]]
sudo test ! -e "$stage/handoff"
docker exec "$container" sh -ec 'chmod 600 /data/backup-denied; rm /data/backup-denied'
sudo bash "$source_dir/deploy/update-existing.sh" apply "$fixture" "$container" "$revision"
sudo test -f "$stage/applied"
sudo test "$(sudo stat -c %a "$stage/previous.yaml")" == 600
sudo sha256sum --check "$stage/data.tar.sha256"
sudo tar -tf "$stage/data.tar" | grep -q './secret.key'
[[ $(docker exec "$container" sha256sum /data/bot.db) == "$before" ]]
new_id=$(docker inspect -f '{{.Id}}' "$container")
[[ "$new_id" != "$old_id" ]]
if sudo bash "$source_dir/deploy/update-existing.sh" apply "$fixture" "$container" "$revision"; then exit 1; fi
[[ $(docker inspect -f '{{.Id}}' "$container") == "$new_id" ]]

# A failure after handoff must keep newly accepted data, not restore an old DB.
sudo STORE_HEALTH_PORT=9081 bash "$source_dir/deploy/update-existing.sh" prepare "$fixture" "$container" "$revision2"
docker exec "$container" rm /data/after-switch
if sudo STORE_HEALTH_WAIT_SECONDS=3 bash "$source_dir/deploy/update-existing.sh" apply "$fixture" "$container" "$revision2"; then exit 1; fi
docker exec "$container" test -f /data/after-switch
[[ $(docker exec "$container" sha256sum /data/bot.db) == "$before" ]]
sudo test -f "$fixture/.private/store-update/$revision2/data.tar"
echo 'Existing-install update tests passed. No real credentials or external API calls were used.'
