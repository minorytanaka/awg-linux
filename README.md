# awg-linux

Клиент AmneziaWG на Linux через `awg-quick` и маленькая панель на Go с кнопкой вкл/выкл и статистикой.

Зачем: официальный GUI-клиент AmneziaVPN на Linux показывает "Подключено", но трафик не идет. Это известный баг ([amnezia-client#2048](https://github.com/amnezia-vpn/amnezia-client/issues/2048)): клиент не может настроить DNS через systemd-resolved. Поэтому туннель здесь поднимается консольными утилитами AmneziaWG напрямую, без GUI-клиента.

Проверено на Pop!_OS 24.04 (COSMIC, ядро 7.2). Должно работать на Ubuntu 24.04 и производных.

## Что внутри

```
scripts/install-awg.sh    установка модуля AmneziaWG и конфига
scripts/install-panel.sh  сборка и установка панели
scripts/uninstall.sh      удаление
panel/main.go             код панели (Go + Fyne)
packaging/                правило polkit, ярлык, иконка
```

## Быстрый старт

```bash
git clone https://github.com/<твой-ник>/awg-linux.git
cd awg-linux

# 1. AmneziaWG + конфиг (конфиг сначала получить, см. раздел 1)
sudo ./scripts/install-awg.sh ~/Загрузки/awg0.conf

# 2. Проверка
sudo awg-quick up awg0
curl -m 10 -I https://www.youtube.com
sudo awg-quick down awg0

# 3. Панель (по желанию)
./scripts/install-panel.sh
```

Дальше туннель включается кнопкой в AWG Panel.

---

## 1. Получить конфиг

Конфиг выпускается в приложении AmneziaVPN на устройстве, где есть полный доступ к серверу (например, на Windows).

1. Поделиться -> вкладка "Соединение".
2. Имя пользователя: свое для каждого устройства. Один конфиг на двух устройствах сразу работать не будет, они выбивают друг друга.
3. Сервер: свой. Протокол: AmneziaWG.
4. Формат подключения: "Оригинальный формат AmneziaWG" (не "Для приложения AmneziaVPN", тот дает ключ vpn://).
5. Поделиться -> сохранить `.conf`.

Перенести файл на Linux: флешка, общий диск (NTFS-раздел Windows виден из Linux), scp. В файле приватный ключ, поэтому не через публичные места.

## 2. Установка вручную

То же самое, что делает `install-awg.sh`, по шагам.

### 2.1 Пакеты для сборки модуля

```bash
sudo apt update
sudo apt install -y software-properties-common python3-launchpadlib gnupg2 linux-headers-$(uname -r)
```

### 2.2 Модуль ядра и утилиты AmneziaWG

```bash
sudo add-apt-repository ppa:amnezia/ppa
sudo apt install -y amneziawg
modinfo amneziawg
```

`modinfo` должен показать filename и version. Модуль собирается через DKMS, поэтому при обновлении ядра пересобирается сам.

Для Debian репозиторий подключается иначе, см. [официальную инструкцию](https://docs.amnezia.org/ru/documentation/instructions/install-amneziawg-kernel-module-linux).

### 2.3 Конфиг

```bash
sudo install -d -m 700 /etc/amnezia/amneziawg
sudo install -m 600 ~/Загрузки/awg0.conf /etc/amnezia/amneziawg/awg0.conf

# убрать CRLF, если файл с Windows
sudo sed -i 's/\r$//' /etc/amnezia/amneziawg/awg0.conf

# удалить пустые параметры вроде "I2 =", иначе awg падает с "Line unrecognized"
sudo sed -i -E '/^[[:space:]]*[A-Za-z0-9]+[[:space:]]*=[[:space:]]*$/d' /etc/amnezia/amneziawg/awg0.conf
```

Имя файла = имя интерфейса. `awg0.conf` -> интерфейс `awg0`.

### 2.4 DNS

awg-quick прописывает DNS из конфига через команду `resolvconf`. В Ubuntu ее нет, но есть systemd-resolved, который понимает тот же синтаксис:

```bash
sudo ln -s /usr/bin/resolvectl /usr/local/bin/resolvconf
```

Если в системе нет systemd-resolved, поставить `openresolv`.

### 2.5 Включение и проверка

```bash
sudo awg-quick up awg0      # включить
sudo awg show awg0          # статус: должна быть строка "latest handshake"
curl -m 10 -I https://www.youtube.com   # HTTP/2 200 или 301 = работает
sudo awg-quick down awg0    # выключить
```

Внешний IP должен стать IP сервера (2ip.ru, ifconfig.me).

### 2.6 Автозапуск при загрузке

```bash
sudo systemctl enable --now awg-quick@awg0
```

Если туннель запущен через systemd, выключать тоже через systemd: `sudo systemctl stop awg-quick@awg0`. Убрать из автозапуска: `sudo systemctl disable awg-quick@awg0`. Если опустить его через `awg-quick down`, служба продолжит считать себя запущенной и следующий `start` ничего не сделает.

### Что делает awg-quick

- создает интерфейс `awg0` и загружает в него ключи и параметры обфускации;
- назначает адрес из конфига и MTU;
- прописывает DNS через resolvconf;
- заводит таблицу маршрутизации 51820, где весь трафик идет в туннель;
- помечает свои зашифрованные пакеты fwmark 51820, чтобы они шли к серверу обычным путем, а не в туннель сами в себя;
- добавляет правила nftables против подмены адресов.

## 3. Панель AWG Panel

Окно с кнопкой "Включить/Выключить" и статистикой: статус, время последнего рукопожатия, адрес сервера, принято/отправлено, текущая скорость, проверка внешнего IP.

### Как устроена

- Вкл/выкл: `systemctl start|stop awg-quick@awg0`.
- Трафик и скорость: `/sys/class/net/awg0/statistics`, читаются без прав.
- Рукопожатие и сервер: `sudo -n awg show awg0 ...`.
- Статус "Подключено", если рукопожатие было меньше 3 минут назад; "Нет связи", если интерфейс есть, а рукопожатия нет; "Выключено", если интерфейса нет.

Поднимать туннель и читать `awg show` может только root, поэтому `install-panel.sh` добавляет два узких разрешения:

- `/etc/polkit-1/rules.d/50-awg.rules` - твоему пользователю можно start/stop/restart только службы `awg-quick@awg0.service`;
- `/etc/sudoers.d/awg-status` - без пароля можно только `awg show awg0 ...`.

Без polkit-правила кнопка тоже работает, но спрашивает пароль. Без sudoers туннель включается, но вместо рукопожатия будет "нет прав".

Панель управляет только службой. Если туннель поднят руками через `awg-quick up`, сначала `sudo awg-quick down awg0`.

### Сборка вручную

```bash
# Go с go.dev (в apt старая версия)
VER=$(curl -s 'https://go.dev/VERSION?m=text' | head -1)
curl -LO https://go.dev/dl/$VER.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf $VER.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc
source ~/.bashrc

# зависимости для графики
sudo apt install -y gcc pkg-config libgl1-mesa-dev xorg-dev libxkbcommon-dev libwayland-dev wayland-protocols

# сборка
cd panel
go mod init awgpanel            # если нет go.mod
go get fyne.io/fyne/v2@latest   # если нет go.mod
go mod tidy
go build -ldflags "-s -w" -o awg-panel .
sudo install -m 755 awg-panel /usr/local/bin/
```

Первая сборка идет несколько минут: компилируется сишная часть графики.

## 4. Проблемы и решения

`Line unrecognized: 'I2='` - в конфиге пустые параметры, см. 2.3.

`resolvconf: command not found` - нет ссылки, см. 2.4.

`Module amneziawg not found` - модуль не собрался под текущее ядро. Проверить `uname -r`, поставить `linux-headers-$(uname -r)`, затем `sudo apt install --reinstall amneziawg-dkms` и `sudo dkms status`.

Служба `awg-quick@awg0` не стартует, `awg0 already exists` - туннель уже поднят вручную: `sudo awg-quick down awg0`.

Туннель поднят, но нет "latest handshake" - сервер недоступен или пользователь отозван. Проверить, работает ли тот же сервер на другом устройстве, выпустить новый конфиг.

`fatal error: xkbcommon/xkbcommon.h` при сборке панели - `sudo apt install libxkbcommon-dev libwayland-dev wayland-protocols`.

Ошибка про `fyne.Do` при сборке - подтянулся Fyne старее 2.6: `go get fyne.io/fyne/v2@latest && go mod tidy`.

## 5. Удаление

```bash
./scripts/uninstall.sh          # только панель
./scripts/uninstall.sh --awg    # панель + AmneziaWG + конфиг
```

## Безопасность

- `.conf` в `.gitignore`. Конфиги с ключами в репозиторий не класть, в gist тоже.
- Если конфиг утек: в AmneziaVPN -> Поделиться -> "Пользователи" отозвать этого пользователя и выпустить новый конфиг.
