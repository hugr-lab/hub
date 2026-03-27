#!/bin/bash
# Mount cloud storage volumes from MOUNT_* environment variables.
# Called as entrypoint wrapper — mounts FUSE filesystems, then exec's CMD.

for var in $(env | grep '^MOUNT_' | cut -d= -f1); do
    config=$(printenv "$var")

    type=$(echo "$config" | python3 -c "import sys,json; print(json.load(sys.stdin)['type'])" 2>/dev/null)
    mount=$(echo "$config" | python3 -c "import sys,json; print(json.load(sys.stdin)['mount'])" 2>/dev/null)

    if [ -z "$type" ] || [ -z "$mount" ]; then
        echo "[mount-storage] WARN: Failed to parse $var"
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
                echo "[mount-storage] WARN: S3 mount $var missing bucket or credentials"
                continue
            fi

            echo "${access_key}:${secret_key}" > /tmp/.s3fs-${var}
            chmod 600 /tmp/.s3fs-${var}

            opts="-o passwd_file=/tmp/.s3fs-${var}"
            opts="$opts -o allow_other"
            opts="$opts -o uid=$(id -u jovyan),gid=$(id -g jovyan)"
            opts="$opts -o use_cache=/tmp/s3cache-${var}"

            if [ -n "$endpoint" ]; then
                opts="$opts -o url=${endpoint}"
                opts="$opts -o use_path_request_style"
            fi

            mkdir -p "/tmp/s3cache-${var}"

            echo "[mount-storage] Mounting S3 bucket '$bucket' at $mount"
            s3fs "$bucket" "$mount" $opts $read_only 2>&1 || echo "[mount-storage] WARN: Failed to mount S3 $bucket"
            ;;

        azure)
            echo "[mount-storage] WARN: Azure blobfuse2 not yet installed"
            ;;

        gcs)
            echo "[mount-storage] WARN: gcsfuse not yet installed"
            ;;

        *)
            echo "[mount-storage] WARN: Unknown storage type '$type' for $var"
            ;;
    esac
done

exec "$@"
