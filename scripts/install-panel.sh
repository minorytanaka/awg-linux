#!/usr/bin/env bash
# Сборка и установка AWG Panel.
# Запускать от своего пользователя, без sudo (пароль спросят, где нужно):
#   ./scripts/install-panel.sh
set -euo pipefail

IFACE="awg0" # панель управляет службой awg-quick@awg0
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
USER_NAME="$(id -un)"

if [[ $EUID -eq 0 ]]; then
  echo "Запусти без sudo, от своего пользователя" >&2
  exit 1
fi

if ! command -v awg >/dev/null; then
  echo "awg не найден. Сначала установи AmneziaWG: sudo ./scripts/install-awg.sh <конфиг>" >&2
  exit 1
fi
AWG_BIN="$(command -v awg)"

export PATH="$PATH:/usr/local/go/bin:$HOME/go/bin"
if ! command -v go >/dev/null; then
  echo "==> Go не найден, ставлю официальный архив с go.dev"
  case "$(uname -m)" in
    x86_64)  GOARCH=amd64 ;;
    aarch64) GOARCH=arm64 ;;
    *) echo "Неизвестная архитектура $(uname -m), поставь Go вручную" >&2; exit 1 ;;
  esac
  VER="$(curl -fsS 'https://go.dev/VERSION?m=text' | head -1)"
  TMP="$(mktemp -d)"
  curl -fL -o "$TMP/go.tgz" "https://go.dev/dl/$VER.linux-$GOARCH.tar.gz"
  sudo rm -rf /usr/local/go
  sudo tar -C /usr/local -xzf "$TMP/go.tgz"
  rm -rf "$TMP"
  if ! grep -q '/usr/local/go/bin' "$HOME/.bashrc"; then
    echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> "$HOME/.bashrc"
  fi
fi
go version

echo "==> Зависимости для сборки графики (Fyne / GLFW)"
sudo apt-get install -y gcc pkg-config libgl1-mesa-dev xorg-dev \
  libxkbcommon-dev libwayland-dev wayland-protocols

echo "==> Сборка (первый раз может занять несколько минут)"
cd "$ROOT/panel"
if [[ ! -f go.mod ]]; then
  go mod init awgpanel
  go get fyne.io/fyne/v2@latest
fi
go mod tidy
go build -ldflags "-s -w" -o awg-panel .
sudo install -m 755 awg-panel /usr/local/bin/awg-panel

echo "==> polkit: вкл/выкл службы awg-quick@$IFACE без пароля для $USER_NAME"
sed -e "s/@USER@/$USER_NAME/" -e "s/@IFACE@/$IFACE/" \
  "$ROOT/packaging/50-awg.rules.in" | sudo tee /etc/polkit-1/rules.d/50-awg.rules >/dev/null

echo "==> sudoers: только '$AWG_BIN show $IFACE ...' без пароля"
TMP="$(mktemp)"
echo "$USER_NAME ALL=(root) NOPASSWD: $AWG_BIN show $IFACE *" > "$TMP"
sudo visudo -cf "$TMP" >/dev/null
sudo install -m 440 -o root -g root "$TMP" /etc/sudoers.d/awg-status
rm -f "$TMP"

echo "==> Ярлык и иконка"
install -Dm 644 "$ROOT/packaging/awg-panel.svg" \
  "$HOME/.local/share/icons/hicolor/scalable/apps/awg-panel.svg"
install -Dm 644 "$ROOT/packaging/awg-panel.desktop" \
  "$HOME/.local/share/applications/awg-panel.desktop"

if ip link show "$IFACE" >/dev/null 2>&1 && ! systemctl is-active --quiet "awg-quick@$IFACE"; then
  echo
  echo "Туннель $IFACE поднят вручную. Чтобы им управляла панель, опусти его:"
  echo "  sudo awg-quick down $IFACE"
fi

echo
echo "Готово. AWG Panel есть в меню приложений, или запусти: awg-panel"
