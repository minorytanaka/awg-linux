#!/usr/bin/env bash
# Установка AmneziaWG (модуль ядра + awg-quick) и конфига клиента.
#
# Использование:
#   sudo ./scripts/install-awg.sh путь/к/конфигу.conf [имя_интерфейса] [--enable]
#
#   имя_интерфейса  по умолчанию awg0
#   --enable        сразу включить и добавить в автозапуск (awg-quick@<iface>)
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "Запусти через sudo: sudo $0 $*" >&2
  exit 1
fi

CONF=""
IFACE="awg0"
ENABLE=0
for arg in "$@"; do
  case "$arg" in
    --enable) ENABLE=1 ;;
    *.conf)   CONF="$arg" ;;
    *)        IFACE="$arg" ;;
  esac
done

if [[ -z "$CONF" || ! -f "$CONF" ]]; then
  echo "Укажи путь к существующему .conf файлу" >&2
  exit 1
fi

# shellcheck disable=SC1091
. /etc/os-release
if [[ "${ID:-}" != "ubuntu" && "${ID_LIKE:-}" != *ubuntu* ]]; then
  echo "Скрипт рассчитан на Ubuntu и производные (Pop!_OS, Mint и т.п.)." >&2
  echo "Для Debian поставь модуль по README, раздел 2.2, и запусти скрипт снова." >&2
  command -v awg >/dev/null || exit 1
fi

if ! modinfo amneziawg >/dev/null 2>&1; then
  echo "==> Пакеты для сборки модуля"
  apt-get update
  apt-get install -y software-properties-common python3-launchpadlib gnupg2 "linux-headers-$(uname -r)"

  echo "==> Репозиторий Amnezia и пакет amneziawg"
  add-apt-repository -y ppa:amnezia/ppa
  apt-get install -y amneziawg

  if ! modinfo amneziawg >/dev/null 2>&1; then
    echo "Модуль amneziawg не собрался для ядра $(uname -r). Смотри README, раздел 'Проблемы'." >&2
    exit 1
  fi
else
  echo "==> Модуль amneziawg уже установлен, пропускаю"
fi

DEST="/etc/amnezia/amneziawg/$IFACE.conf"
echo "==> Конфиг $DEST"
install -d -m 700 /etc/amnezia/amneziawg
install -m 600 "$CONF" "$DEST"
# Файл мог прийти с Windows: убираем CRLF
sed -i 's/\r$//' "$DEST"
# Amnezia экспортирует пустые параметры (I2 =, I3 = ...), awg на них падает
sed -i -E '/^[[:space:]]*[A-Za-z0-9]+[[:space:]]*=[[:space:]]*$/d' "$DEST"

# awg-quick прописывает DNS через команду resolvconf
if grep -qE '^[[:space:]]*DNS[[:space:]]*=' "$DEST" && ! command -v resolvconf >/dev/null; then
  if systemctl is-active --quiet systemd-resolved; then
    echo "==> Ссылка resolvconf -> resolvectl"
    ln -sf "$(command -v resolvectl)" /usr/local/bin/resolvconf
  else
    echo "В конфиге есть DNS, но нет ни resolvconf, ни systemd-resolved." >&2
    echo "Поставь openresolv или убери строку DNS из $DEST." >&2
    exit 1
  fi
fi

if [[ $ENABLE -eq 1 ]]; then
  # если туннель поднят руками, служба не стартует - опускаем
  if ip link show "$IFACE" >/dev/null 2>&1; then
    awg-quick down "$IFACE" || true
  fi
  echo "==> Включаю и добавляю в автозапуск awg-quick@$IFACE"
  systemctl enable --now "awg-quick@$IFACE"
  echo "Готово. Статус: sudo awg show $IFACE"
else
  echo
  echo "Готово."
  echo "  Включить:  sudo awg-quick up $IFACE"
  echo "  Выключить: sudo awg-quick down $IFACE"
  echo "  Статус:    sudo awg show $IFACE"
fi
