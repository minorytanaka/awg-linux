#!/usr/bin/env bash
# Удаление AWG Panel. С флагом --awg удаляет и сам AmneziaWG вместе с конфигом.
#   ./scripts/uninstall.sh          только панель
#   ./scripts/uninstall.sh --awg    панель + AmneziaWG
set -euo pipefail

IFACE="awg0"

echo "==> Удаляю панель"
sudo rm -f /usr/local/bin/awg-panel /etc/polkit-1/rules.d/50-awg.rules /etc/sudoers.d/awg-status
rm -f "$HOME/.local/share/applications/awg-panel.desktop" \
      "$HOME/.local/share/icons/hicolor/scalable/apps/awg-panel.svg"

if [[ "${1:-}" == "--awg" ]]; then
  echo "==> Удаляю AmneziaWG"
  sudo systemctl disable --now "awg-quick@$IFACE" 2>/dev/null || true
  sudo awg-quick down "$IFACE" 2>/dev/null || true
  sudo apt-get purge -y amneziawg amneziawg-dkms amneziawg-tools
  sudo add-apt-repository -y --remove ppa:amnezia/ppa
  sudo rm -rf /etc/amnezia/amneziawg
  if [[ -L /usr/local/bin/resolvconf ]]; then
    sudo rm /usr/local/bin/resolvconf
  fi
fi

echo "Готово."
