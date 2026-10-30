#!/usr/bin/env bash
# Наполняет ТЕСТОВЫЙ Vaultwarden данными для vaultcrab.
# Идемпотентен: создаёт/обновляет только объекты с префиксом "vaultcrab-test".
# Существующие записи хранилища не трогает.
#
#   BW_SESSION=... scripts/seed-vault.sh          # создать/обновить
#   BW_SESSION=... scripts/seed-vault.sh --wipe   # удалить только тестовые объекты
#
# Переменные (необязательно):
#   MINT_HOST (192.0.2.133), WIN_HOST (192.0.2.134), ORG_NAME (organization)
#   VAULTCRAB_TEST_USER (testusr), WIN_ADMIN_USER (admin)
#   ROS_PW — пароль тестовых MikroTik CHR; без него записи RouterOS
#            не создаются, а существующие сохраняют свой пароль
#
# Секреты (сгенерированные пароли, приватные ключи) передаются в bw через stdin/env,
# не через argv, и никуда не выводятся.
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

command -v bw >/dev/null || die "bw не найден"
command -v jq >/dev/null || die "jq не найден"
status=$(bwq status | jq -r .status)
[ "$status" = unlocked ] || die "хранилище не разблокировано (status=$status): export BW_SESSION=\$(bw unlock --raw)"
bwq sync >/dev/null

folders_json=$(bwq list folders)
folder_id() { jq -r --arg n "$1" '.[]|select(.name==$n)|.id' <<<"$folders_json"; }

ensure_folder() {
	local name=$1 id
	id=$(folder_id "$name")
	if [ -z "$id" ]; then
		id=$(bwq get template folder | jq --arg n "$name" '.name=$n' | bwq encode | bwq create folder | jq -r .id)
		[ -n "$id" ] && [ "$id" != null ] || die "не удалось создать папку $name"
		folders_json=$(bwq list folders)
		log "папка создана: $name"
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
		[ -n "$id" ] || die "не удалось создать коллекцию $name"
		log "коллекция создана: $name"
	fi
	echo "$id"
}

items_json=$(bwq list items --search "$PREFIX")
item_id() { jq -r --arg n "$1" '.[]|select(.name==$n)|.id' <<<"$items_json" | head -1; }

if [ "${1:-}" = "--wipe" ]; then
	for id in $(jq -r --arg p "$PREFIX" '.[]|select(.name|startswith($p))|.id' <<<"$items_json"); do
		bwq delete item "$id" --permanent && log "удалена запись $id"
	done
	if [ -n "$org_id" ]; then
		for id in $(bwq list org-collections --organizationid "$org_id" | jq -r --arg p "$PREFIX" '.[]|select(.name|startswith($p))|.id'); do
			bwq delete org-collection "$id" --organizationid "$org_id" && log "удалена коллекция $id"
		done
	fi
	# Сначала вложенные папки.
	for id in $(jq -r --arg p "$PREFIX" '[.[]|select(.name|startswith($p))]|sort_by(.name|length)|reverse|.[].id' <<<"$folders_json"); do
		bwq delete folder "$id" && log "удалена папка $id"
	done
	exit 0
fi

# --- тестовые ключи (gitignored) ---
mkdir -p "$KEYS"
[ -f "$KEYS/ed25519" ] || ssh-keygen -q -t ed25519 -N '' -C "$PREFIX-ed25519" -f "$KEYS/ed25519"
[ -f "$KEYS/rsa_pem" ] || ssh-keygen -q -t rsa -b 3072 -m PEM -N '' -C "$PREFIX-rsa" -f "$KEYS/rsa_pem"
[ -f "$KEYS/ecdsa" ] || ssh-keygen -q -t ecdsa -b 256 -N '' -C "$PREFIX-ecdsa" -f "$KEYS/ecdsa"

# «Сплющенные» варианты: переводы строк -> пробелы (как при вставке в одно поле), и CRLF.
flat() { tr '\n' ' ' <"$1"; }
crlf() { sed 's/$/\r/' <"$1"; }

# --- структура ---
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
[ -n "$org_id" ] || log "организация '$ORG_NAME' не найдена — записи организации пропущены"

# Пароль существующей записи сохраняется (чтобы не расходиться с VM), новый — генерируется.
existing_password() {
	local id
	id=$(item_id "$1")
	[ -n "$id" ] && bwq get password "$id" || true
}
gen_password() { bwq generate -ulns --length 20; }

# upsert NAME FOLDER_ID ORG_ID COLLECTIONS_JSON USERNAME URI FIELDS_JSON NOTES
# Пароль берётся из переменной окружения SEED_PW (не argv).
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
		bwq encode <<<"$json" | bwq edit item "$id" >/dev/null || die "ошибка обновления $name"
		if [ -n "$org" ]; then bwq edit item-collections "$id" --organizationid "$org" "$(bwq encode <<<"$cols")" >/dev/null || log "предупреждение: не обновлены коллекции $name"; fi
		log "обновлена: $name"
	else
		bwq encode <<<"$json" | bwq create item >/dev/null || die "ошибка создания $name"
		log "создана: $name"
	fi
}

F_SSH='{"name":"ssh","value":"true","type":2}'
fields() { # fields '"k":"v"' ... -> JSON array: ssh=true + text-поля
	local out="[$F_SSH" kv
	for kv in "$@"; do out+=",{\"name\":${kv%%=*},\"value\":${kv#*=},\"type\":0}"; done
	echo "$out]"
}

# 1. Linux test VM: пароль, формат user@host (как в существующих записях)
SEED_PW=$(existing_password "$PREFIX-mint-password"); [ -n "$SEED_PW" ] || SEED_PW=$(gen_password)
export SEED_PW
upsert "$PREFIX-mint-password" "$f_mint" "" '[]' "$TUSER@$MINT_HOST" "" \
	"$(fields '"tags"="linux,lab"' '"env"="lab"')" "Linux Mint 20, вход по паролю"
MINT_PW=$SEED_PW

# 2. Linux test VM: ed25519-ключ в поле пароля, сплющенный в одну строку
SEED_PW=$(flat "$KEYS/ed25519")
upsert "$PREFIX-mint-key-ed25519" "$f_mint" "" '[]' "$TUSER@$MINT_HOST" "" \
	"$(fields '"tags"="linux,lab,key"' '"env"="lab"')" "ключ ed25519 (одна строка)"

# 3. Linux test VM: RSA PEM-ключ с CRLF, хост через URI ssh://, порт отдельным полем
SEED_PW=$(crlf "$KEYS/rsa_pem")
upsert "$PREFIX-mint-key-rsa-uri" "$f_mint" "" '[]' "$TUSER" "ssh://$MINT_HOST" \
	"$(fields '"tags"="linux,key"' '"port"="22"')" "RSA PEM с CRLF, адрес в URI"

# 4. Linux test VM: ECDSA, кириллица в имени
SEED_PW=$(cat "$KEYS/ecdsa")
upsert "$PREFIX-минт ecdsa [кириллица]" "$f_mint" "" '[]' "$TUSER@$MINT_HOST:22" "" \
	"$(fields '"tags"="linux,key"')" ""

# 5. Windows test VM: пароль, отдельный тестовый пользователь
SEED_PW=$(existing_password "$PREFIX-win-password"); [ -n "$SEED_PW" ] || SEED_PW=$(gen_password)
upsert "$PREFIX-win-password" "$f_win" "" '[]' "$TUSER@$WIN_HOST" "" \
	"$(fields '"tags"="windows,lab"' '"env"="lab"')" "Windows 11, вход по паролю"
WIN_PW=$SEED_PW

# 6. Windows test VM: ed25519-ключ администратора (ключ добавляется в administrators_authorized_keys)
SEED_PW=$(flat "$KEYS/ed25519")
upsert "$PREFIX-win-key" "$f_win" "" '[]' "$WIN_ADMIN@$WIN_HOST" "" \
	"$(fields '"tags"="windows,key"')" ""

# 7. Недоступный хост в Prod/DB (проверка ошибок подключения)
SEED_PW=not-a-real-password
upsert "$PREFIX-db-unreachable" "$f_db" "" '[]' "postgres@192.0.2.250" "" \
	"$(fields '"tags"="db,prod"' '"env"="prod"')" "адрес заведомо недоступен"

# 8. VNC и SMB (адрес только в URI)
SEED_PW=$MINT_PW
upsert "$PREFIX-mint-vnc" "$f_mint" "" '[]' "$TUSER" "vnc://$MINT_HOST:5900" '[{"name":"tags","value":"vnc","type":0}]' ""
# VNC через SSH-туннель: 127.0.0.1 доступен только с самой Linux test VM (поле jump — SSH-запись)
upsert "$PREFIX-mint-vnc-jump" "$f_mint" "" '[]' "$TUSER" "vnc://127.0.0.1:5900" \
	'[{"name":"tags","value":"vnc","type":0},{"name":"jump","value":"'"$PREFIX"'-mint-password","type":0}]' \
	"VNC через jump-хост vaultcrab-test-mint-password"
SEED_PW=$WIN_PW
upsert "$PREFIX-win-smb" "$f_win" "" '[]' "$TUSER" "smb://$WIN_HOST/vaultcrab-test" '[{"name":"tags","value":"smb","type":0}]' ""

# 8a. SFTP/FTP/RDP на Linux test VM: флаг-поле и URI
SEED_PW=$MINT_PW
upsert "$PREFIX-mint-sftp" "$f_mint" "" '[]' "$TUSER@$MINT_HOST" "" '[{"name":"sftp","value":"true","type":2},{"name":"tags","value":"linux,files","type":0}]' ""
upsert "$PREFIX-mint-ftp" "$f_mint" "" '[]' "$TUSER" "ftp://$MINT_HOST/" '[{"name":"tags","value":"linux,files","type":0}]' ""
upsert "$PREFIX-mint-rdp" "$f_mint" "" '[]' "$TUSER" "rdp://$MINT_HOST:3389" '[{"name":"tags","value":"linux,rdp","type":0}]' "xrdp на Linux test VM"

# 9. Не хост (нет поля ssh и URI) — должен отфильтровываться
SEED_PW=not-a-host
upsert "$PREFIX-not-a-host" "$f_root" "" '[]' "someone@example.invalid" "" '[]' "обычная запись, не хост"

# 10. MikroTik RouterOS 6 и 7 (CHR в QEMU на Linux test VM)
for n in 6 7; do
	user=$TUSER; [ $n = 6 ] && user=admin   # в CHR 6.49 пользователи не сохраняются
	SEED_PW=${ROS_PW:-$(existing_password "$PREFIX-ros$n")}
	[ -n "$SEED_PW" ] || { log "ROS_PW не задан — $PREFIX-ros$n пропущена"; continue; }
	upsert "$PREFIX-ros$n" "$f_ros" "" '[]' "$user@$MINT_HOST:220$n" "" \
		"$(fields '"tags"="mikrotik,ros'$n'"' '"env"="lab"')" "MikroTik CHR RouterOS $n (QEMU на Linux test VM)"
done

# 11. Записи организации в коллекциях
if [ -n "$org_id" ]; then
	SEED_PW=$MINT_PW
	upsert "$PREFIX-org-mint" "$f_prod" "$org_id" "[\"$c_ops\",\"$c_dev\"]" "$TUSER@$MINT_HOST" "" \
		"$(fields '"tags"="linux,org"' '"env"="prod"')" "запись организации, 2 коллекции"
	SEED_PW=$WIN_PW
	upsert "$PREFIX-org-win" "" "$org_id" "[\"$c_ops\",\"$c_back\"]" "$TUSER@$WIN_HOST" "" \
		"$(fields '"tags"="windows,org"')" ""
fi
unset SEED_PW MINT_PW WIN_PW

bwq sync >/dev/null
log "готово"
