#!/usr/bin/env bash
# Fills a TEST Vaultwarden account with data for vaultcrab.
# Idempotent: creates/updates only objects whose names start with "vaultcrab-test".
# Other items of the vault are not touched.
#
#   BW_SESSION=... scripts/seed-vault.sh          # create/update
#   BW_SESSION=... scripts/seed-vault.sh --wipe   # delete the test objects only
#
# Variables (optional):
#   MINT_HOST (192.0.2.133), WIN_HOST (192.0.2.134), ORG_NAME (organization)
#   VAULTCRAB_TEST_USER (testusr), WIN_ADMIN_USER (admin)
#   ROS_PW - password of the test MikroTik CHR routers; without it the RouterOS
#            items are not created, and existing ones keep their password
#
# Secrets (generated passwords, private keys) go to bw through stdin/env,
# never through argv, and are not printed.
set -euo pipefail

cd "$(dirname "$0")/.."
PREFIX=vaultcrab-test
MINT_HOST=${MINT_HOST:-192.0.2.133}
WIN_HOST=${WIN_HOST:-192.0.2.134}
ORG_NAME=${ORG_NAME:-organization}
TUSER=${VAULTCRAB_TEST_USER:-testusr}
WIN_ADMIN=${WIN_ADMIN_USER:-admin}
KEYS=testdata/keys

die() { echo "seed-vault: $*" >&2; exit 1; }
log() { echo "seed-vault: $*" >&2; }
export NODE_NO_WARNINGS=1
bwq() { bw "$@"; }

command -v bw >/dev/null || die "bw not found"
command -v jq >/dev/null || die "jq not found"
status=$(bwq status | jq -r .status)
[ "$status" = unlocked ] || die "the vault is not unlocked (status=$status): export BW_SESSION=\$(bw unlock --raw)"
bwq sync >/dev/null

folders_json=$(bwq list folders)
folder_id() { jq -r --arg n "$1" '.[]|select(.name==$n)|.id' <<<"$folders_json"; }

ensure_folder() {
	local name=$1 id
	id=$(folder_id "$name")
	if [ -z "$id" ]; then
		id=$(bwq get template folder | jq --arg n "$name" '.name=$n' | bwq encode | bwq create folder | jq -r .id)
		[ -n "$id" ] && [ "$id" != null ] || die "could not create the folder $name"
		folders_json=$(bwq list folders)
		log "folder created: $name"
	fi
	echo "$id"
}

org_id=$(bwq list organizations | jq -r --arg n "$ORG_NAME" '.[]|select(.name==$n)|.id' | head -1)

ensure_collection() {
	local name=$1 id
	[ -n "$org_id" ] || return 0
	id=$(bwq list org-collections --organizationid "$org_id" | jq -r --arg n "$name" '.[]|select(.name==$n)|.id')
	if [ -z "$id" ]; then
		id=$(bwq get template org-collection | jq --arg n "$name" --arg o "$org_id" \
			'.name=$n | .organizationId=$o | .groups=[] | .users=[]' | bwq encode |
			bwq create org-collection --organizationid "$org_id" | jq -r '.id // empty')
		[ -n "$id" ] || die "could not create the collection $name"
		log "collection created: $name"
	fi
	echo "$id"
}

items_json=$(bwq list items --search "$PREFIX")
item_id() { jq -r --arg n "$1" '.[]|select(.name==$n)|.id' <<<"$items_json" | head -1; }

if [ "${1:-}" = "--wipe" ]; then
	for id in $(jq -r --arg p "$PREFIX" '.[]|select(.name|startswith($p))|.id' <<<"$items_json"); do
		bwq delete item "$id" --permanent && log "deleted item $id"
	done
	if [ -n "$org_id" ]; then
		for id in $(bwq list org-collections --organizationid "$org_id" | jq -r --arg p "$PREFIX" '.[]|select(.name|startswith($p))|.id'); do
			bwq delete org-collection "$id" --organizationid "$org_id" && log "deleted collection $id"
		done
	fi
	# Nested folders first.
	for id in $(jq -r --arg p "$PREFIX" '[.[]|select(.name|startswith($p))]|sort_by(.name|length)|reverse|.[].id' <<<"$folders_json"); do
		bwq delete folder "$id" && log "deleted folder $id"
	done
	exit 0
fi

# --- test keys (gitignored) ---
mkdir -p "$KEYS"
[ -f "$KEYS/ed25519" ] || ssh-keygen -q -t ed25519 -N '' -C "$PREFIX-ed25519" -f "$KEYS/ed25519"
[ -f "$KEYS/rsa_pem" ] || ssh-keygen -q -t rsa -b 3072 -m PEM -N '' -C "$PREFIX-rsa" -f "$KEYS/rsa_pem"
[ -f "$KEYS/ecdsa" ] || ssh-keygen -q -t ecdsa -b 256 -N '' -C "$PREFIX-ecdsa" -f "$KEYS/ecdsa"

# "Flattened" variants: line breaks -> spaces (as when pasted into one field), and CRLF.
flat() { tr '\n' ' ' <"$1"; }
crlf() { sed 's/$/\r/' <"$1"; }

# --- structure ---
f_root=$(ensure_folder "$PREFIX")
f_prod=$(ensure_folder "$PREFIX/Prod")
f_db=$(ensure_folder "$PREFIX/Prod/DB")
f_lab=$(ensure_folder "$PREFIX/Lab")
f_lin=$(ensure_folder "$PREFIX/Lab/Linux")
f_mint=$(ensure_folder "$PREFIX/Lab/Linux/Mint")
f_win=$(ensure_folder "$PREFIX/Lab/Windows")
f_ros=$(ensure_folder "$PREFIX/Lab/MikroTik")
: "$f_root" "$f_prod" "$f_lab" "$f_lin"

c_ops=$(ensure_collection "$PREFIX-ops")
c_dev=$(ensure_collection "$PREFIX-dev")
c_back=$(ensure_collection "$PREFIX-dev/backend")
[ -n "$org_id" ] || log "organization '$ORG_NAME' not found: organization items skipped"

# The password of an existing item is kept (to stay in sync with the VMs); a new one is generated.
existing_password() {
	local id
	id=$(item_id "$1")
	[ -n "$id" ] && bwq get password "$id" || true
}
gen_password() { bwq generate -ulns --length 20; }

# upsert NAME FOLDER_ID ORG_ID COLLECTIONS_JSON USERNAME URI FIELDS_JSON NOTES
# The password comes from the environment variable SEED_PW (not argv).
upsert() {
	local name=$1 folder=$2 org=$3 cols=$4 user=$5 uri=$6 fields=$7 notes=$8 id json
	json=$(bwq get template item | jq \
		--arg name "$name" --arg folder "$folder" --arg org "$org" --argjson cols "$cols" \
		--arg user "$user" --arg uri "$uri" --argjson fields "$fields" --arg notes "$notes" '
		.type=1 | .name=$name | .notes=(if $notes=="" then null else $notes end)
		| .folderId=(if $folder=="" then null else $folder end)
		| .organizationId=(if $org=="" then null else $org end)
		| .collectionIds=$cols
		| .fields=$fields
		| .login={username:$user, password:env.SEED_PW,
		          uris:(if $uri=="" then [] else [{match:null, uri:$uri}] end), totp:null}')
	id=$(item_id "$name")
	if [ -n "$id" ]; then
		bwq encode <<<"$json" | bwq edit item "$id" >/dev/null || die "could not update $name"
		if [ -n "$org" ]; then bwq edit item-collections "$id" --organizationid "$org" "$(bwq encode <<<"$cols")" >/dev/null || log "warning: collections of $name not updated"; fi
		log "updated: $name"
	else
		bwq encode <<<"$json" | bwq create item >/dev/null || die "could not create $name"
		log "created: $name"
	fi
}

F_SSH='{"name":"ssh","value":"true","type":2}'
fields() { # fields '"k"="v"' ... -> JSON array: ssh=true + text fields
	local out="[$F_SSH" kv
	for kv in "$@"; do out+=",{\"name\":${kv%%=*},\"value\":${kv#*=},\"type\":0}"; done
	echo "$out]"
}

# 1. Linux test VM: password, user@host form (as in existing items)
SEED_PW=$(existing_password "$PREFIX-mint-password"); [ -n "$SEED_PW" ] || SEED_PW=$(gen_password)
export SEED_PW
upsert "$PREFIX-mint-password" "$f_mint" "" '[]' "$TUSER@$MINT_HOST" "" \
	"$(fields '"tags"="linux,lab"' '"env"="lab"')" "Linux Mint 20, password login"
MINT_PW=$SEED_PW

# 2. Linux test VM: ed25519 key in the password field, flattened into one line
SEED_PW=$(flat "$KEYS/ed25519")
upsert "$PREFIX-mint-key-ed25519" "$f_mint" "" '[]' "$TUSER@$MINT_HOST" "" \
	"$(fields '"tags"="linux,lab,key"' '"env"="lab"')" "ed25519 key (one line)"

# 3. Linux test VM: RSA PEM key with CRLF, host in an ssh:// URI, port in a separate field
SEED_PW=$(crlf "$KEYS/rsa_pem")
upsert "$PREFIX-mint-key-rsa-uri" "$f_mint" "" '[]' "$TUSER" "ssh://$MINT_HOST" \
	"$(fields '"tags"="linux,key"' '"port"="22"')" "RSA PEM with CRLF, address in the URI"

# 4. Linux test VM: ECDSA, Cyrillic in the name (deliberately)
SEED_PW=$(cat "$KEYS/ecdsa")
upsert "$PREFIX-минт ecdsa [кириллица]" "$f_mint" "" '[]' "$TUSER@$MINT_HOST:22" "" \
	"$(fields '"tags"="linux,key"')" ""

# 5. Windows test VM: password, a separate test user
SEED_PW=$(existing_password "$PREFIX-win-password"); [ -n "$SEED_PW" ] || SEED_PW=$(gen_password)
upsert "$PREFIX-win-password" "$f_win" "" '[]' "$TUSER@$WIN_HOST" "" \
	"$(fields '"tags"="windows,lab"' '"env"="lab"')" "Windows 11, password login"
WIN_PW=$SEED_PW

# 6. Windows test VM: ed25519 key of the administrator (added to administrators_authorized_keys)
SEED_PW=$(flat "$KEYS/ed25519")
upsert "$PREFIX-win-key" "$f_win" "" '[]' "$WIN_ADMIN@$WIN_HOST" "" \
	"$(fields '"tags"="windows,key"')" ""

# 7. An unreachable host in Prod/DB (connection errors)
SEED_PW=not-a-real-password
upsert "$PREFIX-db-unreachable" "$f_db" "" '[]' "postgres@192.0.2.250" "" \
	"$(fields '"tags"="db,prod"' '"env"="prod"')" "the address is unreachable on purpose"

# 8. VNC and SMB (address in the URI only)
SEED_PW=$MINT_PW
upsert "$PREFIX-mint-vnc" "$f_mint" "" '[]' "$TUSER" "vnc://$MINT_HOST:5900" '[{"name":"tags","value":"vnc","type":0}]' ""
# VNC through an SSH tunnel: 127.0.0.1 is reachable only from the Linux test VM itself (the jump field names an SSH item)
upsert "$PREFIX-mint-vnc-jump" "$f_mint" "" '[]' "$TUSER" "vnc://127.0.0.1:5900" \
	'[{"name":"tags","value":"vnc","type":0},{"name":"jump","value":"'"$PREFIX"'-mint-password","type":0}]' \
	"VNC through the jump host vaultcrab-test-mint-password"
SEED_PW=$WIN_PW
upsert "$PREFIX-win-smb" "$f_win" "" '[]' "$TUSER" "smb://$WIN_HOST/vaultcrab-test" '[{"name":"tags","value":"smb","type":0}]' ""

# 8a. SFTP/FTP/RDP on the Linux test VM: flag field and URIs
SEED_PW=$MINT_PW
upsert "$PREFIX-mint-sftp" "$f_mint" "" '[]' "$TUSER@$MINT_HOST" "" '[{"name":"sftp","value":"true","type":2},{"name":"tags","value":"linux,files","type":0}]' ""
upsert "$PREFIX-mint-ftp" "$f_mint" "" '[]' "$TUSER" "ftp://$MINT_HOST/" '[{"name":"tags","value":"linux,files","type":0}]' ""
upsert "$PREFIX-mint-rdp" "$f_mint" "" '[]' "$TUSER" "rdp://$MINT_HOST:3389" '[{"name":"tags","value":"linux,rdp","type":0}]' "xrdp on the Linux test VM"

# 9. Not a host (no ssh field, no URI): must be filtered out
SEED_PW=not-a-host
upsert "$PREFIX-not-a-host" "$f_root" "" '[]' "someone@example.invalid" "" '[]' "an ordinary item, not a host"

# 10. MikroTik RouterOS 6 and 7 (CHR in QEMU on the Linux test VM)
for n in 6 7; do
	user=$TUSER; [ $n = 6 ] && user=admin   # CHR 6.49 does not keep added users
	SEED_PW=${ROS_PW:-$(existing_password "$PREFIX-ros$n")}
	[ -n "$SEED_PW" ] || { log "ROS_PW not set: $PREFIX-ros$n skipped"; continue; }
	upsert "$PREFIX-ros$n" "$f_ros" "" '[]' "$user@$MINT_HOST:220$n" "" \
		"$(fields '"tags"="mikrotik,ros'$n'"' '"env"="lab"')" "MikroTik CHR RouterOS $n (QEMU on the Linux test VM)"
done

# 11. Organization items in collections
if [ -n "$org_id" ]; then
	SEED_PW=$MINT_PW
	upsert "$PREFIX-org-mint" "$f_prod" "$org_id" "[\"$c_ops\",\"$c_dev\"]" "$TUSER@$MINT_HOST" "" \
		"$(fields '"tags"="linux,org"' '"env"="prod"')" "organization item, 2 collections"
	SEED_PW=$WIN_PW
	upsert "$PREFIX-org-win" "" "$org_id" "[\"$c_ops\",\"$c_back\"]" "$TUSER@$WIN_HOST" "" \
		"$(fields '"tags"="windows,org"')" ""
fi
unset SEED_PW MINT_PW WIN_PW

bwq sync >/dev/null
log "done"
