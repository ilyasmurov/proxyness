## feature
Config-driven notifications replace GitHub update polling
Client now polls /api/client-config from config service for notifications, version updates, and service discovery. NotificationBanner replaces UpdateBanner with server-pushed notifications (update, migration, maintenance, info types).
