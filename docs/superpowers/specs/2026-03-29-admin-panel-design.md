# Admin Panel + Multi-User Support

## Контекст

Сейчас сервер принимает один `-key` флаг — все клиенты используют один ключ. Нужно: поддержка нескольких юзеров с несколькими девайсами у каждого, веб-админка для управления на `proxy.smurov.com/admin`.

## Архитектура

Сервер получает:
- SQLite БД для хранения юзеров/девайсов/ключей
- REST API для админки (`/admin/api/*`)
- React SPA с shadcn/ui (`/admin/*`), встроенная в Go бинарник через `go:embed`
- Мультиплексор на порту 443: первый байт `0x01` → прокси-протокол, иначе → HTTP (админка)

```
proxy.smurov.com:443 (TLS)
│
├─ первый байт 0x01 → кастомный прокси-протокол
│  └─ auth по ключу из БД (перебор активных ключей)
│
└─ первый байт HTTP (GET/POST...) → HTTP роутер
   ├─ /admin/api/* → REST API (JSON)
   ├─ /admin/* → React SPA (static files из embed)
   └─ / → 404 (или заглушка)
```

## Модель данных (SQLite)

```sql
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE devices (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    key TEXT UNIQUE NOT NULL,
    active INTEGER DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

- `key` — hex-строка, 64 символа (256 бит), генерируется через `auth.GenerateKey()`
- `active` — 0/1, можно деактивировать девайс без удаления
- ON DELETE CASCADE — удаление юзера удаляет все его девайсы

## REST API (`/admin/api/*`)

Все эндпоинты защищены Basic Auth (`ADMIN_USER` / `ADMIN_PASSWORD` env vars).

### Users

```
GET    /admin/api/users              → [{id, name, created_at, device_count}]
POST   /admin/api/users              → {name} → {id, name, created_at}
DELETE /admin/api/users/:id          → 204
```

### Devices

```
GET    /admin/api/users/:id/devices  → [{id, name, key, active, created_at}]
POST   /admin/api/users/:id/devices  → {name} → {id, name, key, active, created_at}
PATCH  /admin/api/devices/:id        → {active: true/false} → 200
DELETE /admin/api/devices/:id        → 204
```

При создании девайса ключ генерируется автоматически и возвращается в ответе. Это единственный момент когда ключ виден полностью.

## Admin UI (React + shadcn/ui)

SPA собирается Vite → статика встраивается в Go binary через `//go:embed`.

Структура: `server/admin-ui/`

### Страницы

**Список юзеров** (`/admin/`)
- Таблица: имя, кол-во девайсов, дата создания
- Кнопка "Add User" → диалог с полем name
- Клик на юзера → страница девайсов

**Девайсы юзера** (`/admin/users/:id`)
- Имя юзера сверху, кнопка удалить юзера
- Таблица девайсов: имя, статус (active/inactive), дата
- Кнопка "Add Device" → диалог с полем name → показывает сгенерированный ключ (скопировать)
- Toggle active/inactive для каждого девайса
- Кнопка удалить девайс

### Стек

- React 19 + TypeScript
- Vite для сборки
- shadcn/ui (Tailwind CSS)
- Встраивание: `//go:embed admin-ui/dist` в Go

## Изменения в существующем коде

### `pkg/auth`

`ValidateAuthMessage` сейчас принимает один ключ. Добавить:

```go
func ValidateAuthMessageMulti(keys []string, msg []byte) (matchedKey string, err error)
```

Перебирает ключи, возвращает первый совпавший. При 5-10 ключах — мгновенно.

### `server/cmd/main.go`

- Убрать флаг `-key` (ключи теперь в БД)
- Добавить флаги: `-db` (путь к SQLite, default `data.db`), `-admin-user`, `-admin-password`
- Мультиплексор: читает первый байт соединения, решает HTTP или прокси
- Для прокси: получает активные ключи из БД, передаёт в `proto.ReadAuth`

### Новые пакеты

- `server/internal/db` — SQLite: миграции, CRUD для users/devices
- `server/internal/admin` — HTTP хендлеры для API + serving SPA

### Dockerfile

- Multi-stage: сначала собрать admin-ui (node), потом Go binary
- Volume для SQLite файла

## Конфигурация сервера

```bash
./server \
  -addr ":443" \
  -db "/data/data.db" \
  -admin-user "admin" \
  -admin-password "secret" \
  -cert "/data/cert.pem" \
  -keyfile "/data/key.pem"
```

Или через env: `ADMIN_USER`, `ADMIN_PASSWORD`.

Docker:
```bash
docker run -d \
  -p 443:443 \
  -v smurov-proxy-data:/data \
  -e ADMIN_USER=admin \
  -e ADMIN_PASSWORD=secret \
  ghcr.io/ilyasmurov/smurov-proxy:latest
```

## Безопасность

- Admin API только через Basic Auth
- Ключи девайсов показываются только при создании
- SQLite файл в Docker volume (персистентный)
- PROXY_KEY env var больше не нужен (ключи в БД)

## Верификация

1. Создать юзера через админку
2. Добавить девайс, скопировать ключ
3. Запустить демон с этим ключом — подключение работает
4. Деактивировать девайс в админке — подключение отклоняется
5. Удалить юзера — все его девайсы перестают работать
