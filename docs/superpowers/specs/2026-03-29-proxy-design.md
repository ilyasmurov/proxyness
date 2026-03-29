# SmurovProxy: TLS-туннель с кастомным протоколом

## Контекст

Нужен прокси для обхода блокировок в РФ. VPS в Нидерландах (Ubuntu/Debian), клиенты на macOS и Windows. Трафик должен выглядеть как обычный HTTPS для обхода DPI.

## Архитектура

```
Клиент (Россия)                     Сервер (Нидерланды)
┌──────────┐    TLS (порт 443)     ┌──────────┐
│ Браузер  │ ──► SOCKS5 ═══════════► │ Прокси   │ ──► Интернет
│          │ ◄── (1080)  ◄═══════════ │ сервер   │ ◄──
└──────────┘                        └──────────┘
```

### Сервер (VPS)
- Слушает на порту 443 через TLS
- Проверяет авторизацию по HMAC ключу
- Подключается к целевым ресурсам и проксирует данные
- Самоподписанный TLS-сертификат (генерируется при первом запуске)

### Клиент — два компонента

**Go демон** (фоновый процесс):
- Локальный SOCKS5 сервер на `127.0.0.1:1080`
- HTTP API для управления на `127.0.0.1:9090`
- Подключается к VPS через TLS
- Авторизуется и передаёт запросы на подключение

**Electron GUI** (React + TypeScript):
- Окно с кнопкой Подключить/Отключить, статусом, полями настроек
- Иконка в системном трее (меню: подключить/отключить, статус, выход)
- При сворачивании уходит в трей
- Общается с Go демоном через HTTP API
- Запускает Go демон как дочерний процесс

## Кастомный протокол (внутри TLS)

### Фаза 1: Авторизация

```
Клиент → Сервер:
  [1 байт]  версия протокола (0x01)
  [8 байт]  timestamp (unix, big-endian)
  [32 байта] HMAC-SHA256(key, timestamp)

Сервер → Клиент:
  [1 байт]  результат (0x01=OK, 0x00=fail)
```

Сервер проверяет:
- HMAC соответствует ключу
- Timestamp не старше 30 секунд (защита от replay)

### Фаза 2: Запрос подключения

```
Клиент → Сервер:
  [1 байт]  тип адреса:
              0x01 = IPv4 (4 байта адреса)
              0x03 = Домен (1 байт длина + N байт домен)
              0x04 = IPv6 (16 байт адреса)
  [N байт]  адрес
  [2 байта] порт (big-endian)

Сервер → Клиент:
  [1 байт]  результат (0x01=connected, 0x00=fail)
```

### Фаза 3: Передача данных

Двусторонний relay чистых байтов. Шифрование обеспечивается TLS.

## Структура проекта

```
proxy/
├── server/                        # Go — серверная часть
│   ├── cmd/main.go                # Точка входа сервера
│   ├── internal/
│   │   ├── proto/proto.go         # Кастомный протокол
│   │   ├── socks5/socks5.go       # SOCKS5 обработка
│   │   └── auth/auth.go           # HMAC-SHA256 авторизация
│   └── go.mod
│
├── daemon/                        # Go — клиентский демон
│   ├── cmd/main.go                # Точка входа демона
│   ├── internal/
│   │   ├── proto/proto.go         # Кастомный протокол (shared)
│   │   ├── socks5/socks5.go       # SOCKS5 сервер
│   │   ├── auth/auth.go           # HMAC-SHA256 авторизация
│   │   ├── tunnel/tunnel.go       # Управление туннелем
│   │   └── api/api.go             # HTTP API для Electron
│   └── go.mod
│
├── client/                        # Electron — GUI
│   ├── src/
│   │   ├── main/                  # Electron main process
│   │   │   ├── index.ts           # Запуск, трей, управление демоном
│   │   │   └── daemon.ts          # Запуск/остановка Go демона
│   │   └── renderer/              # React UI
│   │       ├── App.tsx            # Главный компонент
│   │       ├── components/
│   │       │   ├── ConnectionButton.tsx
│   │       │   ├── StatusBar.tsx
│   │       │   └── Settings.tsx
│   │       └── hooks/
│   │           └── useDaemon.ts   # Хук для HTTP API демона
│   ├── package.json
│   └── electron-builder.json
│
└── Makefile                       # Сборка всего
```

## Компоненты

### `internal/auth/auth.go`
- `GenerateKey() string` - генерация 256-бит ключа
- `CreateAuthMessage(key string) []byte` - создание auth сообщения (version + timestamp + HMAC)
- `ValidateAuthMessage(key string, msg []byte) bool` - проверка auth сообщения

### `internal/proto/proto.go`
- `WriteAuth(conn, key)` / `ReadAuth(conn, key)` - авторизация
- `WriteConnect(conn, addr, port)` / `ReadConnect(conn)` - запрос подключения
- `Relay(conn1, conn2)` - двусторонняя передача данных

### `internal/socks5/socks5.go`
- Минимальная реализация SOCKS5 (RFC 1928)
- Поддержка CONNECT команды
- Без аутентификации (только localhost)

### `cmd/server/main.go`
- TLS listener на указанном порту
- Генерация самоподписанного сертификата при первом запуске
- Для каждого подключения: auth → connect → relay

### `daemon/internal/tunnel/tunnel.go`
- `Tunnel` struct — управление жизненным циклом туннеля
- `Start(serverAddr, key string) error` — запуск SOCKS5 + подключение к серверу
- `Stop()` — остановка туннеля
- `Status() string` — текущий статус (disconnected/connecting/connected)

### `daemon/internal/api/api.go` — HTTP API
- `POST /connect` — подключить туннель `{server: "ip:port", key: "secret"}`
- `POST /disconnect` — отключить туннель
- `GET /status` — текущий статус `{status: "connected|disconnected|connecting", uptime: 123}`
- `GET /health` — проверка что демон жив
- Слушает только на `127.0.0.1:9090`

### `client/` — Electron GUI (React + TypeScript)
- Главное окно: поля ввода (сервер, ключ), кнопка Подключить/Отключить, статус
- Системный трей: иконка (красная/зелёная), меню с connect/disconnect/quit
- Сворачивание в трей при закрытии окна
- Main process запускает Go демон как child process
- Настройки сохраняются в electron-store (`~/.smurov-proxy/config.json`)

## Конфигурация

### Сервер (CLI)
```bash
./server -key "your-secret-key" -addr ":443"
```
При первом запуске автоматически создаются `cert.pem` и `key.pem`.

### Демон (CLI, запускается Electron'ом)
```bash
./daemon -key "secret" -server "vps-ip:443" -listen "127.0.0.1:1080" -api "127.0.0.1:9090"
```

### Electron клиент
Настройки вводятся в GUI и сохраняются в `~/.smurov-proxy/config.json`:
```json
{
  "server": "vps-ip:443",
  "key": "your-secret-key",
  "listenAddr": "127.0.0.1:1080"
}
```

## Сборка

```makefile
# Сервер (только Linux для VPS)
build-server:
    cd server && GOOS=linux GOARCH=amd64 go build -o ../dist/server-linux ./cmd

# Go демон (для каждой платформы)
build-daemon:
    cd daemon && GOOS=darwin GOARCH=arm64 go build -o ../client/resources/daemon-darwin-arm64 ./cmd
    cd daemon && GOOS=darwin GOARCH=amd64 go build -o ../client/resources/daemon-darwin-amd64 ./cmd
    cd daemon && GOOS=windows GOARCH=amd64 go build -o ../client/resources/daemon-windows.exe ./cmd

# Electron GUI (включает Go демон)
build-client:
    cd client && npm run build && npx electron-builder
```

## Безопасность

- TLS 1.3 для транспорта (стандартная Go реализация)
- HMAC-SHA256 для авторизации
- Timestamp-based replay protection (30 сек окно)
- Сервер слушает на 443 — неотличим от HTTPS
- SOCKS5 только на localhost — внешний доступ невозможен
- Клиент пропускает проверку TLS-сертификата (`InsecureSkipVerify`) — авторизация обеспечивается HMAC ключом, TLS нужен для шифрования и маскировки под HTTPS

## Верификация

### Бэкенд (Go)
1. Собрать: `make build-server && make build-daemon`
2. Запустить сервер: `./dist/server-linux -key testkey -addr :8443`
3. Запустить демон: `./daemon -key testkey -server 127.0.0.1:8443 -listen 127.0.0.1:1080 -api 127.0.0.1:9090`
4. Проверить API: `curl http://127.0.0.1:9090/status`
5. Проверить прокси: `curl -x socks5://127.0.0.1:1080 https://ifconfig.me`
6. Убедиться что IP в ответе — IP сервера
7. Проверить неправильный ключ: демон с другим ключом получает отказ

### Фронтенд (Electron)
1. `cd client && npm install && npm run dev`
2. Проверить что окно открывается, поля настроек работают
3. Кнопка Connect вызывает `POST /connect` к демону
4. Статус обновляется при подключении/отключении
5. Трей иконка появляется и меню работает
6. Сворачивание в трей при закрытии окна
