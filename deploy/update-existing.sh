#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

# The compatibility image deliberately keeps the installed process identity,
# data volume and mounts. Fresh isolated installs use compose.pilot.yaml.
mode=${1:-}
install_arg=${2:-}
container=${3:-}
revision=${4:-}
[[ "$mode" == prepare || "$mode" == apply ]] || { echo 'Usage: update-existing.sh prepare|apply INSTALL_DIR CONTAINER REVISION' >&2; exit 1; }
[[ -d "$install_arg" && "$container" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]*$ && "$revision" =~ ^[a-f0-9]{40}$ ]] || { echo 'Invalid installation directory, container or revision.' >&2; exit 1; }
install_dir=$(cd -- "$install_arg" && pwd -P)
source_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
stage="$install_dir/.private/store-update/$revision"
d=(docker --host unix:///var/run/docker.sock)
image="customer-store-local:$revision"
[[ $(id -u) == 0 ]] || { echo 'Run this compatibility update as the existing root operator.' >&2; exit 1; }
[[ -S /var/run/docker.sock ]] || { echo 'The local Docker socket is unavailable.' >&2; exit 1; }

inspect() { "${d[@]}" inspect --format "$1" "$container"; }
verify_installation() {
  [[ $(inspect '{{.State.Running}}') == true ]] || { echo 'The existing bot must be running.' >&2; return 1; }
  [[ $(inspect '{{.Config.User}}') == '' || $(inspect '{{.Config.User}}') == root || $(inspect '{{.Config.User}}') == 0 ]] || { echo 'This helper requires the existing root-based installation.' >&2; return 1; }
  [[ $(inspect '{{index .Config.Labels "com.docker.compose.project.working_dir"}}') == "$install_dir" ]] || { echo 'Container belongs to a different installation.' >&2; return 1; }
  [[ $(inspect '{{index .Config.Labels "com.docker.compose.service"}}') == bot ]] || { echo 'Only the bot service can be updated.' >&2; return 1; }
  [[ $(inspect '{{range .Mounts}}{{if eq .Destination "/compose"}}{{.Source}}{{end}}{{end}}') == "$install_dir" ]] || { echo 'The existing configuration mount does not match.' >&2; return 1; }
  [[ $(inspect '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Type}}{{end}}{{end}}') == volume ]] || { echo 'Only an existing named SQLite data volume is supported.' >&2; return 1; }
}

if [[ "$mode" == prepare ]]; then
  verify_installation
  health_port=${STORE_HEALTH_PORT:-}
  [[ "$health_port" =~ ^[0-9]{1,5}$ && "$health_port" -gt 0 && "$health_port" -le 65535 ]] || { echo 'Set STORE_HEALTH_PORT to the existing internal HTTP port.' >&2; exit 1; }
  if [[ -e "$stage" ]]; then
    [[ ! -e "$stage/handoff" && ! -e "$stage/applied" ]] || { echo 'This revision has already been handed off. Inspect its recorded state before retrying.' >&2; exit 1; }
    mv -- "$stage" "$stage.previous.$(date -u +%Y%m%dT%H%M%SZ).$$"
  fi
  mkdir -p -- "$stage/check"
  chmod 700 "$install_dir/.private" "$install_dir/.private/store-update" "$stage" "$stage/check"
  project=$(inspect '{{index .Config.Labels "com.docker.compose.project"}}')
  old_id=$(inspect '{{.Id}}')
  old_image=$(inspect '{{.Image}}')
  data_volume=$(inspect '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}')
  files_csv=$(inspect '{{index .Config.Labels "com.docker.compose.project.config_files"}}')
  [[ -n "$project" && -n "$data_volume" && -n "$files_csv" ]] || { echo 'Missing Compose metadata.' >&2; exit 1; }
  IFS=, read -r -a files <<< "$files_csv"
  compose=("${d[@]}" compose -p "$project" --project-directory "$install_dir")
  for file in "${files[@]}"; do
    [[ -f "$file" ]] || { echo 'An existing Compose file is unavailable.' >&2; exit 1; }
    compose+=(-f "$file")
  done
  "${compose[@]}" config --quiet
  # Render privately: the installed Compose may contain inline credentials.
  sha256sum -- "${files[@]}" > "$stage/config.sha256"
  if [[ -f "$install_dir/.env" ]]; then sha256sum -- "$install_dir/.env" >> "$stage/config.sha256"; fi
  "${d[@]}" image tag "$old_image" "customer-store-previous:${revision:0:12}"
  cat > "$stage/previous-image.yaml" <<EOF
services:
  bot:
    image: customer-store-previous:${revision:0:12}
    pull_policy: never
EOF
  "${compose[@]}" -f "$stage/previous-image.yaml" config > "$stage/previous.yaml"
  "${d[@]}" build --build-arg "COMMIT=$revision" -t "$image" -f "$source_dir/Dockerfile" "$source_dir"
  "${d[@]}" run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges:true \
    --memory 512m --cpus 1 --pids-limit 64 --tmpfs /tmp:size=32m,mode=1777 \
    --mount "type=volume,src=$data_volume,dst=/source,readonly" \
    --mount "type=bind,src=$stage/check,dst=/check" \
    --entrypoint /app/store-check "$image" --bootstrap-dir /source --source /source/bot.db --copy /check/bot.db
  cat > "$stage/overlay.yaml" <<EOF
services:
  bot:
    image: $image
    pull_policy: never
    environment:
      MULTI_SUBSCRIPTIONS_ENABLED: "true"
      COMPOSE_FILE_PATH: /compose/.private/store-update/$revision/active.yaml
EOF
  "${compose[@]}" -f "$stage/overlay.yaml" config > "$stage/active.yaml"
  "${d[@]}" compose -p "$project" --project-directory "$install_dir" -f "$stage/active.yaml" config --quiet
  {
    declare -p project old_id old_image data_volume health_port image
  } > "$stage/plan.env"
  touch "$stage/prepared"
  echo 'Prepared. The running bot and its SQLite source were not changed.'
  echo 'The private copy passed migration, row-count, schema and integrity checks without network access.'
  echo 'Next: run this same helper with apply instead of prepare.'
  exit 0
fi

[[ -f "$stage/prepared" && -f "$stage/plan.env" && ! -e "$stage/applied" && ! -e "$stage/handoff" ]] || { echo 'A fresh successful prepare is required; do not replay a previous handoff.' >&2; exit 1; }
# The plan is written only by prepare inside the owner-only installation directory.
# shellcheck disable=SC1091
source "$stage/plan.env"
verify_installation
[[ $(inspect '{{.Id}}') == "$old_id" && $(inspect '{{.Image}}') == "$old_image" ]] || { echo 'The running container changed after prepare. Prepare a new revision.' >&2; exit 1; }
sha256sum --check --status "$stage/config.sha256" || { echo 'Configuration changed after prepare. Prepare a new revision.' >&2; exit 1; }
"${d[@]}" image inspect "$image" > /dev/null
"${d[@]}" compose -p "$project" --project-directory "$install_dir" -f "$stage/active.yaml" config --quiet
wait_seconds=${STORE_HEALTH_WAIT_SECONDS:-90}
[[ "$wait_seconds" =~ ^[0-9]{1,3}$ && "$wait_seconds" -gt 0 && "$wait_seconds" -le 180 ]] || { echo 'Invalid readiness timeout.' >&2; exit 1; }

stopped=false
handoff=false
finish() {
  local code=$?
  if [[ "$code" != 0 && "$stopped" == true && "$handoff" == false ]]; then
    "${d[@]}" start "$old_id" > /dev/null || echo 'Could not resume the original container; inspect this bot only.' >&2
  elif [[ "$code" != 0 && "$handoff" == true ]]; then
    echo 'Update needs review. The database was NOT restored to an older snapshot.' >&2
    echo "Private backup and the pinned previous image configuration: $stage" >&2
  fi
}
trap finish EXIT
stopped=true
"${d[@]}" stop --timeout 45 "$old_id" > /dev/null
if [[ -e "$stage/data.tar" ]]; then mv -- "$stage/data.tar" "$stage/data.tar.previous.$(date -u +%Y%m%dT%H%M%SZ).$$"; fi
"${d[@]}" run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  --mount "type=volume,src=$data_volume,dst=/source,readonly" \
  --mount "type=bind,src=$stage,dst=/backup" \
  --entrypoint /bin/sh "$old_image" -ec 'umask 077; tar -cpf /backup/data.tar -C /source .'
sha256sum "$stage/data.tar" > "$stage/data.tar.sha256"
touch "$stage/handoff"
handoff=true
# Only this service is recreated. No down, volume removal or database restore.
"${d[@]}" compose -p "$project" --project-directory "$install_dir" -f "$stage/active.yaml" \
  up -d --no-build --no-deps --force-recreate bot
for ((i=0;i<wait_seconds;i++)); do
  if "${d[@]}" exec "$container" wget -q -O /dev/null "http://127.0.0.1:$health_port/healthz" 2>/dev/null; then
    touch "$stage/applied"
    echo 'Updated. Readiness check passed. The existing data volume and ownership were retained.'
    echo "Private backup and pinned previous configuration: $stage"
    echo 'Use the prepared active.yaml for Compose operations; the original upstream update command does not install this fork.'
    exit 0
  fi
  sleep 1
done
echo 'Readiness did not pass in time. Check this bot before retrying; do not restore an older database over new payments.' >&2
exit 1
