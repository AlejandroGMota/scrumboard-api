#!/usr/bin/env bash
#
# Nightly backup of the SQLite file, rotated weekly, with a copy off the VM.
#
# Install on the VM:
#   cp backup.sh ~/scrumboard-api/backup.sh && chmod +x ~/scrumboard-api/backup.sh
#   sudo mkdir -p /var/backups/scrumboard
#   # The container (65534) writes the snapshot through the group; this
#   # script (ubuntu) owns the directory, so it can delete the plaintext.
#   sudo chown ubuntu:65534 /var/backups/scrumboard && sudo chmod 2770 /var/backups/scrumboard
#   echo age1... > ~/scrumboard-api/backup-recipient.txt   # the PUBLIC key only
#   crontab -e
#     PATH=/usr/local/bin:/usr/bin:/bin
#     17 3 * * *  CONTAINER=scrumboard-api BACKUP_DIR=/var/backups/scrumboard \
#       /home/ubuntu/scrumboard-api/backup.sh >> /var/log/scrumboard-backup.log 2>&1
#
# The PATH line is not decoration: cron's default PATH does not include
# /usr/local/bin, which is where rclone installs. Without it the off-VM copy
# silently takes the "no rclone configured" branch every night.
#
# The offline write queue is what raises the stakes here (00-scope.md §6.4): a
# lost volume is no longer "I will rebuild the board", it is changes that
# devices believe were synced.

set -euo pipefail

CONTAINER=${CONTAINER:-scrumboard-api}
BACKUP_DIR=${BACKUP_DIR:-/var/backups/scrumboard}
# %u is 1..7, so seven files rotate on their own with no cleanup logic to get
# wrong. A week of history is the right amount for a board one person edits.
DAY=$(date +%u)
NAME="scrumboard-${DAY}.db"
SNAPSHOT="scrumboard-snapshot.db"
# Backups leave this machine encrypted to a key that never touches it: the
# VM holds only the public half, so a copy of every backup is not a copy of
# the board. Without the recipient there is no backup at all, rather than a
# plaintext one that looks like the usual.
RECIPIENTS=${RECIPIENTS:-$HOME/scrumboard-api/backup-recipient.txt}
test -s "$RECIPIENTS" || { echo "[$(date -Is)] ERROR: no age recipient in $RECIPIENTS"; exit 1; }

mkdir -p "$BACKUP_DIR"

# 1. A consistent snapshot, written straight to the host directory.
#
#    Never `cp`. With WAL enabled, copying the file while writes are in flight
#    produces a snapshot that looks fine and fails on the day it is needed.
#    The binary does this with VACUUM INTO — SQLite's own hot-backup path —
#    because the image is FROM scratch and has no sqlite3 in it.
#
#    It writes to /backups, which the compose file bind-mounts from
#    $BACKUP_DIR, rather than into the data volume. Writing it inside the
#    volume and copying it out would need a `docker cp` and then an `rm` to
#    clean up — and there is no `rm` in a scratch image, so the temporary
#    copies would accumulate until the volume held eight databases instead of
#    one. That failure is silent and slow, which is the worst kind.
echo "[$(date -Is)] snapshot"
docker exec "$CONTAINER" /api -backup "/backups/${SNAPSHOT}"

# No `latest.db` symlink. The directory has to be owned by the container's
# user so the container can write into it, which means this script — running
# as the cron user — cannot create a link there. Consumers pick the newest
# file by mtime instead; `make restore-check` does exactly that.

# 2. Check it before trusting it. A backup nobody has ever opened is a
#    hypothesis; this is what makes it a fact.
#
#    Mounted read-only, which is also what proves `-verify` never writes to
#    the file it is checking.
echo "[$(date -Is)] verify"
docker run --rm \
  -v "${BACKUP_DIR}:/backups:ro" \
  --entrypoint /api \
  "$(docker inspect --format '{{.Config.Image}}' "$CONTAINER")" \
  -verify "/backups/${SNAPSHOT}"

# 3. Encrypt, then drop the plaintext. Only the .age file is kept or copied.
echo "[$(date -Is)] encrypt"
age -R "$RECIPIENTS" -o "${BACKUP_DIR}/${NAME}.age" "${BACKUP_DIR}/${SNAPSHOT}"
rm -f "${BACKUP_DIR}/${SNAPSHOT}"

# 4. Off the VM, so losing the whole machine is survivable rather than fatal.
#
#    Destination still open (00-scope.md §11, question 1). The proposed default
#    is Cloudflare R2: S3-compatible, and a few megabytes of SQLite fit inside
#    the free tier with room to spare. Configure rclone once
#    (`rclone config`, remote named "r2") and this starts working:
if command -v rclone >/dev/null 2>&1 && rclone listremotes | grep -q '^r2:'; then
  echo "[$(date -Is)] copying off the VM"
  rclone copyto "${BACKUP_DIR}/${NAME}.age" "r2:scrumboard-backups/${NAME}.age"
else
  echo "[$(date -Is)] WARNING: no off-VM copy — rclone remote 'r2' is not configured."
  echo "               The backup survives a lost volume, but not a lost machine."
fi

echo "[$(date -Is)] done: ${BACKUP_DIR}/${NAME}.age"
