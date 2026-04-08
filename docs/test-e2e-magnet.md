# End-to-End Test: Magnet → qBittorrent

This document walks through a reproducible end-to-end smoke test that
proves the full Marauder pipeline works: a magnet URI added through the
API appears in qBittorrent within one scheduler tick.

The test relies only on Docker and assumes nothing is installed on the
host besides Docker and git.

## 1. Bring up the dev stack

The dev overlay (`deploy/docker-compose.dev.yml`) publishes the database
and backend ports to the host and starts a real qBittorrent container on
`http://localhost:34611` for integration testing. (qBittorrent still
listens on its conventional 6611 inside the container; only the host-
side mapping uses the safe 34xxx range per local-port-ranges.)

```bash
cd deploy
cp .env.example .env

# Generate a master key and metrics token
MASTER=$(openssl rand -base64 32)
METRICS=$(openssl rand -hex 32)
sed -i "s|MARAUDER_MASTER_KEY=.*|MARAUDER_MASTER_KEY=$MASTER|" .env
sed -i "s|MARAUDER_METRICS_TOKEN=.*|MARAUDER_METRICS_TOKEN=$METRICS|" .env

docker compose --env-file .env \
  -f docker-compose.yml \
  -f docker-compose.dev.yml \
  up -d
```

## 2. Grab the qBittorrent temporary password

On first start, qBittorrent writes a one-off password to stdout.

```bash
docker logs deploy-qbittorrent-1 2>&1 | grep "temporary password"
```

Copy the password. You'll use it in step 4.

## 3. Log in to Marauder

```bash
LOGIN=$(curl -sS -X POST http://localhost:34080/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"pleasechangeme"}')

TOK=$(echo "$LOGIN" | awk -F'"access_token":"' '{print $2}' | awk -F'"' '{print $1}')
echo "token length: ${#TOK}"
```

Expected output: `token length: 547` (give or take a few characters).

## 4. Create a qBittorrent client

Replace `QBIT_PASSWORD` with the temporary password from step 2.

```bash
curl -sS -X POST http://localhost:34080/api/v1/clients \
  -H "Authorization: Bearer $TOK" \
  -H "Content-Type: application/json" \
  -d '{
    "client_name": "qbittorrent",
    "display_name": "Dev qBit",
    "is_default": true,
    "config": {
      "url": "http://qbittorrent:6611",
      "username": "admin",
      "password": "QBIT_PASSWORD"
    }
  }'
```

The backend validates the config by calling `qbittorrent.Test()` before
persisting, so a bad password or wrong URL is rejected with HTTP 422. On
success you get back a `clientView` JSON with a UUID.

Save the `id` field as `CLIENT_ID`.

## 5. Add a magnet topic

```bash
MAG='magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=marauder-test-file'

curl -sS -X POST http://localhost:34080/api/v1/topics \
  -H "Authorization: Bearer $TOK" \
  -H "Content-Type: application/json" \
  -d "{\"url\":\"$MAG\",\"client_id\":\"$CLIENT_ID\"}"
```

You should see a `Topic` JSON with `"TrackerName":"genericmagnet"`.

## 6. Wait for one scheduler tick

The default tick is 1 minute. Wait 75 seconds to be safe:

```bash
sleep 75
docker logs deploy-backend-1 2>&1 | grep -E "topic|scheduler" | tail -5
```

Expected: a `"message":"topic updated"` log line referencing the topic
UUID and the tracker name.

## 7. Verify in qBittorrent

```bash
COOKIE=$(curl -sS -i -X POST "http://localhost:34611/api/v2/auth/login" \
  -H "Referer: http://localhost:34611" \
  --data-urlencode "username=admin" \
  --data-urlencode "password=QBIT_PASSWORD" | \
  grep -i "^set-cookie:" | sed 's/Set-Cookie: //i' | cut -d';' -f1)

curl -sS -H "Cookie: $COOKIE" http://localhost:34611/api/v2/torrents/info
```

Expected: a JSON array containing at least one entry with:

- `"name": "marauder-test-file"`
- `"hash": "0123456789abcdef0123456789abcdef01234567"`
- `"state": "metaDL"` (fetching metadata from DHT / trackers)

This is the full chain working:

```
UI/API → Postgres → scheduler tick → genericmagnet plugin →
AES-GCM decrypt client config → qBittorrent WebUI API v2 →
torrent accepted.
```

## 8. Tear down

```bash
docker compose --env-file .env \
  -f docker-compose.yml \
  -f docker-compose.dev.yml \
  down -v
```

The `-v` flag also removes the named volumes (postgres data, qBittorrent
config, transmission config) so the next run is clean.
