# Remnanode (Go)

[![Release](https://github.com/aaaSaZaN/remnanode-go/actions/workflows/release.yml/badge.svg)](https://github.com/aaaSaZaN/remnanode-go/actions/workflows/release.yml)
[![GitHub release](https://img.shields.io/github/v/release/aaaSaZaN/remnanode-go?include_prereleases&style=flat-square)](https://github.com/aaaSaZaN/remnanode-go/releases)
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL_3.0-blue.svg?style=flat-square)](LICENSE)

Высокопроизводительный, легковесный Xray-core для панелей **Remnawave** и **Gowave**, написанный на **100% чистом Go**.

Remnanode (Go) — это полноценная замена официальной Node.js-ноды. Больше не нужны тяжелые Node.js и зависимости: один компактный бинарник потребляет всего **~10–15 МБ RAM** и готов работать даже на слабых VPS, микро-инстансах и роутерах.

---

## Ключевые преимущества

-  **Минимальное потребление памяти:** всего ~10–15 МБ RAM (против 50–150 МБ у оригинальной ноды на Node.js). * примерные данные, всё зависит от вашей конфигурации Xray.
-  **Один статичный бинарник:** готов к работе сразу после скачивания, не требует Node.js и внешних зависимостей.
-  **mTLS:** взаимная TLS-аутентификация с панелью через встроенный `SECRET_KEY`.
-  **Защита и плагины:**
  - Встроенный сбор статистики и онлайна Xray-core.
  - Поддержка Torrent Blocker (сбор репортов о BitTorrent активности).
  - Управление правилами и блокировками через `nftables`.
  - Принудительный сброс соединений и IP (`drop-ips`, `drop-users-connections`).
- **Управление и обновления:** автоматическое обновление ядра Xray (`rw-core`) и самой ноды прямо из веб-панели управления.
-  **Широкая поддержка архитектур:**
  - `x86_64` (`amd64`)
  - `arm64` (ARMv8 / AArch64)
  - `armv7` (ARMv7 / 32-bit одноплатники)
  - `mips` / `mipsle` / `mips64` / `mips64le` (роутеры и сетевые шлюзы)

---

## Архитектура и совместимость

- **Поддерживаемые панели:** [Gowave](https://github.com/aaaSaZaN/remnawave-go) и оригинальный [Remnawave](https://github.com/remnawave).
- **Ядро Xray:** размещается рядом с бинарником ноды (`rw-core`, например в `/opt/remnanode/rw-core`).
- **Связь с панелью:** двусторонний шифрованный mTLS-канал по порту `3000` (или кастомному).

---

## Миграция с Docker (Node.js/TS) на Native Go (~0s Downtime)

Если на сервере уже работает нода в Docker (`remnawave/node`), переключить её на Go-ноду можно одной командой:

```bash
# Выполните на сервере ноды от root:
curl -sSL https://raw.githubusercontent.com/aaaSaZaN/remnanode-go/main/migrate.sh | bash
```

Скрипт автоматически обнаружит контейнер с нодой, перенесет `SECRET_KEY`, сертификаты, порты и конфигурацию в `/opt/remnanode`, создаст systemd-службу `remnanode.service`, запустит бинарник и остановит Docker-контейнер. Всё происходит с минимальным временем простоя (~0 секунд).

---

## Быстрый запуск (Готовые бинарники)

Для работы ноды нужен только бинарник и строка `SECRET_KEY`, которую вы копируете из панели при добавлении ноды.

### 1. Подготовка каталога
```bash
mkdir -p /opt/remnanode && cd /opt/remnanode
```

### 2. Скачивание бинарника под вашу архитектуру

#### Для x86_64 (AMD / Intel):
```bash
curl -L -o remnanode https://github.com/aaaSaZaN/remnanode-go/releases/latest/download/remnanode-linux-amd64
chmod +x remnanode
```

#### Для arm64 (Apple Silicon VPS, Oracle ARM, Raspberry Pi 4/5):
```bash
curl -L -o remnanode https://github.com/aaaSaZaN/remnanode-go/releases/latest/download/remnanode-linux-arm64
chmod +x remnanode
```

#### Для armv7 (32-bit ARM, Orange Pi, старые Raspberry Pi):
```bash
curl -L -o remnanode https://github.com/aaaSaZaN/remnanode-go/releases/latest/download/remnanode-linux-armv7
chmod +x remnanode
```

#### Для MIPS / MIPSLE (роутеры OpenWrt, Keenetic и др.):
```bash
# 32-bit mipsle (Little Endian):
curl -L -o remnanode https://github.com/aaaSaZaN/remnanode-go/releases/latest/download/remnanode-linux-mipsle
# 32-bit mips (Big Endian):
curl -L -o remnanode https://github.com/aaaSaZaN/remnanode-go/releases/latest/download/remnanode-linux-mips

# 64-bit mips64le (Little Endian):
curl -L -o remnanode https://github.com/aaaSaZaN/remnanode-go/releases/latest/download/remnanode-linux-mips64le
# 64-bit mips64 (Big Endian):
curl -L -o remnanode https://github.com/aaaSaZaN/remnanode-go/releases/latest/download/remnanode-linux-mips64

chmod +x remnanode
```

---

### 3. Настройка окружения (`.env`)

Создайте файл `/opt/remnanode/.env`:

```bash
nano /opt/remnanode/.env
```

Вставьте в него секретный ключ, скопированный из панели:

```env
SECRET_KEY=вставьте_сюда_SECRET_KEY_из_панели
NODE_PORT=3000
```

> [!TIP]
> Убедитесь, что порт `3000` (или указанный в `NODE_PORT`) открыт в фаерволе вашего сервера:
> `ufw allow 3000/tcp` или `iptables -I INPUT -p tcp --dport 3000 -j ACCEPT`.
> `Лучше вообще не открывать порт в интернет.`

---

### 4. Автозапуск через Systemd

Создайте файл службы `/etc/systemd/system/remnanode.service`:

```ini
[Unit]
Description=Remnanode (Go) Service
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/remnanode
ExecStart=/opt/remnanode/remnanode
Restart=always
RestartSec=5
LimitNOFILE=1000000

[Install]
WantedBy=multi-user.target
```

Активируйте и запустите службу:

```bash
systemctl daemon-reload
systemctl enable --now remnanode
systemctl status remnanode
```

---

## Управление через консоль (CLI и системный `$PATH`)

При первом запуске с правами root бинарник автоматически регистрирует символические ссылки в системных путях (`/usr/local/bin` на Linux серверах и `/opt/bin` на роутерах Entware):
- `remnanode`
- `remnanode-go`
- `xlogs`

Благодаря этому можно управлять нодой и смотреть логи из **любого каталога** системы:

### 1. Проверка статуса
```bash
remnanode status
# или
remnanode-go status

# Непосредственно запросить установленную версию ядра:
/opt/remnanode/rw-core version
```
Первая команда показывает фактическую версию запущенного бинарника Xray Core. Вторая напрямую запрашивает версию установленного файла ядра.

### 2. Мониторинг логов Xray-core
```bash
xlogs -f
# или
remnanode xlogs -n 100 -f
```
Цветной вывод логов ядра Xray в реальном времени с подсветкой ошибок (красный), предупреждений (желтый) и подключений пользователей (зеленый/голубой).
- `-n <число>` — количество строк из истории (по умолчанию 100)
- `-f` — режим отслеживания в реальном времени (follow)

> [!TIP]
> Команда `xlogs` доступна как самостоятельная утилита в `$PATH` — для быстрого дебага трафика достаточно набрать `xlogs -f` в терминале.

### 3. Мониторинг логов самой ноды
```bash
remnanode logs -f
# или
remnanode-go logs -n 50
```
Выводит системные логи сервиса Remnanode (запросы панели по mTLS, синхронизация пользователей, Torrent Blocker и правила nftables).

### 4. Перезапуск службы
```bash
remnanode restart
```
Быстрый перезапуск службы через systemd или init.d.

### 5. Управление ядром  (`rw-core`)
```bash
# Скачать и установить Xray core:
remnanode core install

# Установить конкретную версию:
remnanode core install --version v25.1.30 -y

# Обновить ядро до последней версии:
remnanode core update

# Сбросить ядро к дефолтной конфигурации:
remnanode core reset
```

### 6. Самообновление Remnanode
```bash
# Интерактивный выбор и обновление бинарника из GitHub Releases:
remnanode update

# Автоматическое обновление до стабильной версии:
remnanode update -y

# Обновление до pre-release:
remnanode update --channel prerelease -y

# Установка конкретной версии из Releases:
remnanode update --version 1.0.5 -y
```

### 7. Версия и справка
```bash
remnanode version   # Вывод текущей версии
remnanode help      # Справка по командам
```

---

## Переменные окружения (Environment Variables)

Все настройки задаются через файл `.env` в директории ноды либо через системные переменные:

| Переменная | По умолчанию | Описание |
| :--- | :--- | :--- |
| `SECRET_KEY` | *(обязательно)* | Ключ привязки к панели (содержит SSL-сертификаты и параметры ноды) |
| `NODE_PORT` | `3000` | Порт mTLS сервера ноды для входящих запросов от панели |
| `NFTABLES_LOGGING` | `true` | Логирование блокировок nftables в системный журнал |
| `NFTABLES_ACCEPT_REPLY_TRAFFIC` | `false` | Разрешать ответный трафик для заблокированных адресов |
| `SNI_VERIFICATION` | `false` | Дополнительная верификация SNI |
| `INTERNAL_REST_TOKEN` | `remnanode-internal-secret` | Токен для внутренних локальных вызовов |
| `XTLS_API_SOCKET_PATH` | `rw-api.sock` | Путь к Unix-сокету статистики Xray-core |

---

## Сборка из исходников

Для самостоятельной компиляции требуется **Go 1.22+**:

```bash
git clone https://github.com/aaaSaZaN/remnanode-go.git
cd remnanode-go

# Сборка под текущую ОС и архитектуру (с указанием версии):
go build -ldflags="-s -w -X 'main.AppVersion=X.X.X'" -o remnanode cmd/remnanode/main.go

# Кросс-компиляция под Linux amd64:
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X 'main.AppVersion=1.0.6'" -o remnanode-linux-amd64 cmd/remnanode/main.go

# Кросс-компиляция под Linux arm64:
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w -X 'main.AppVersion=1.0.6'" -o remnanode-linux-arm64 cmd/remnanode/main.go


# Linux armv7 (32-bit ARM):
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -ldflags="-s -w -X 'main.AppVersion=1.0.6'" -o remnanode-linux-armv7 cmd/remnanode/main.go

# Linux mipsle (softfloat, Keenetic/OpenWrt):
CGO_ENABLED=0 GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -ldflags="-s -w -X 'main.AppVersion=1.0.6'" -o remnanode-linux-mipsle cmd/remnanode/main.go

# Linux mips (softfloat):
CGO_ENABLED=0 GOOS=linux GOARCH=mips GOMIPS=softfloat go build -ldflags="-s -w -X 'main.AppVersion=1.0.6'" -o remnanode-linux-mips cmd/remnanode/main.go

# Linux mips64le:
CGO_ENABLED=0 GOOS=linux GOARCH=mips64le go build -ldflags="-s -w -X 'main.AppVersion=1.0.6'" -o remnanode-linux-mips64le cmd/remnanode/main.go

# Linux mips64:
CGO_ENABLED=0 GOOS=linux GOARCH=mips64 go build -ldflags="-s -w -X 'main.AppVersion=1.0.6'" -o remnanode-linux-mips64 cmd/remnanode/main.go
```

---

## Совместимость и документация

- **Панель Gowave (Go):** [https://github.com/aaaSaZaN/remnawave-go](https://github.com/aaaSaZaN/remnawave-go)
- **Официальная документация экосистемы:** [https://docs.rw](https://docs.rw)

---

## Автор и благодарности

- Разработка Go-версии (**remnanode-go**): [@aaaSaZaN](https://github.com/aaaSaZaN)
- Оригинальный проект и архитектура: [Remnawave](https://github.com/remnawave)
