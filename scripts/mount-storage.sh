#!/bin/bash
# Mount cloud storage volumes from MOUNT_* environment variables.
# Called as entrypoint wrapper — mounts FUSE filesystems, then exec's CMD.
#
# Design: mount failures MUST NOT block container startup.
# Each mount runs with a timeout; on failure we log ERROR and continue.

MOUNT_TIMEOUT=${MOUNT_TIMEOUT:-30}  # seconds per mount attempt

# Run a mount command with timeout. On failure log ERROR and continue.
try_mount() {
    local label="$1"
    shift
    timeout "$MOUNT_TIMEOUT" "$@" 2>&1
    local rc=$?
    if [ $rc -ne 0 ]; then
        if [ $rc -eq 124 ]; then
            echo "[mount-storage] ERROR: $label timed out after ${MOUNT_TIMEOUT}s"
        else
            echo "[mount-storage] ERROR: $label failed (exit $rc)"
        fi
        return 1
    fi
    echo "[mount-storage] OK: $label"
    return 0
}

for var in $(env | grep '^MOUNT_' | cut -d= -f1); do
    config=$(printenv "$var")

    type=$(echo "$config" | python3 -c "import sys,json; print(json.load(sys.stdin)['type'])" 2>/dev/null)
    mount=$(echo "$config" | python3 -c "import sys,json; print(json.load(sys.stdin)['mount'])" 2>/dev/null)

    if [ -z "$type" ] || [ -z "$mount" ]; then
        echo "[mount-storage] ERROR: Failed to parse $var — skipping"
        continue
    fi

    mkdir -p "$mount"

    case "$type" in
        s3)
            bucket=$(echo "$config" | python3 -c "import sys,json; print(json.load(sys.stdin).get('bucket',''))" 2>/dev/null)
            read_only=$(echo "$config" | python3 -c "import sys,json; print('-o ro' if json.load(sys.stdin).get('read_only') else '')" 2>/dev/null)
            endpoint=$(echo "$config" | python3 -c "import sys,json; c=json.load(sys.stdin).get('credentials',{}); print(c.get('endpoint_url',''))" 2>/dev/null)
            access_key=$(echo "$config" | python3 -c "import sys,json; c=json.load(sys.stdin).get('credentials',{}); print(c.get('access_key_id',''))" 2>/dev/null)
            secret_key=$(echo "$config" | python3 -c "import sys,json; c=json.load(sys.stdin).get('credentials',{}); print(c.get('secret_access_key',''))" 2>/dev/null)

            if [ -z "$bucket" ] || [ -z "$access_key" ]; then
                echo "[mount-storage] ERROR: S3 mount $var missing bucket or credentials — skipping"
                continue
            fi

            echo "${access_key}:${secret_key}" > /tmp/.s3fs-${var}
            chmod 600 /tmp/.s3fs-${var}

            opts="-o passwd_file=/tmp/.s3fs-${var}"
            opts="$opts -o allow_other"
            opts="$opts -o uid=$(id -u jovyan),gid=$(id -g jovyan)"
            opts="$opts -o use_cache=/tmp/s3cache-${var}"
            opts="$opts -o connect_timeout=10"
            opts="$opts -o retries=1"

            if [ -n "$endpoint" ]; then
                opts="$opts -o url=${endpoint}"
                opts="$opts -o use_path_request_style"
            fi

            mkdir -p "/tmp/s3cache-${var}"

            try_mount "S3 bucket '$bucket' at $mount" \
                s3fs "$bucket" "$mount" $opts $read_only
            ;;

        azure)
            account=$(echo "$config" | python3 -c "import sys,json; print(json.load(sys.stdin).get('account',''))" 2>/dev/null)
            container=$(echo "$config" | python3 -c "import sys,json; print(json.load(sys.stdin).get('container',''))" 2>/dev/null)
            read_only=$(echo "$config" | python3 -c "import sys,json; print('-o ro' if json.load(sys.stdin).get('read_only') else '')" 2>/dev/null)
            account_key=$(echo "$config" | python3 -c "import sys,json; c=json.load(sys.stdin).get('credentials',{}); print(c.get('account_key',''))" 2>/dev/null)
            access_token=$(echo "$config" | python3 -c "import sys,json; print(json.load(sys.stdin).get('access_token',''))" 2>/dev/null)

            if [ -z "$account" ] || [ -z "$container" ]; then
                echo "[mount-storage] ERROR: Azure mount $var missing account or container — skipping"
                continue
            fi

            # Write blobfuse2 config
            cat > /tmp/blobfuse2-${var}.yaml <<BFEOF
logging:
  type: syslog
  level: log_warning
components:
  - libfuse
  - file_cache
  - attr_cache
  - azstorage
libfuse:
  attribute-expiration-sec: 120
  entry-expiration-sec: 120
file_cache:
  path: /tmp/blobfuse2-cache-${var}
  timeout-sec: 120
  max-size-mb: 4096
azstorage:
  type: block
  account-name: ${account}
  container: ${container}
  endpoint: https://${account}.blob.core.windows.net
BFEOF

            # Auth: account key or OAuth token
            if [ -n "$account_key" ]; then
                echo "  account-key: ${account_key}" >> /tmp/blobfuse2-${var}.yaml
            elif [ -n "$access_token" ]; then
                echo "  oauth-token: ${access_token}" >> /tmp/blobfuse2-${var}.yaml
            fi

            mkdir -p "/tmp/blobfuse2-cache-${var}"

            try_mount "Azure blob '${account}/${container}' at $mount" \
                blobfuse2 mount "$mount" --config-file=/tmp/blobfuse2-${var}.yaml \
                    -o allow_other \
                    -o uid=$(id -u jovyan) -o gid=$(id -g jovyan) \
                    $read_only
            ;;

        gcs)
            echo "[mount-storage] ERROR: gcsfuse not yet installed — skipping $var"
            ;;

        *)
            echo "[mount-storage] ERROR: Unknown storage type '$type' for $var — skipping"
            ;;
    esac
done

echo "[mount-storage] Storage init complete — starting application"
exec "$@"
