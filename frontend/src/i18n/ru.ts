const ru: Record<string, string> = {
  // App-wide
  "app.name": "marauder",
  "app.tagline": "self-hosted автоматизация торрентов",

  // Auth
  "login.welcome": "С возвращением",
  "login.subtitle":
    "Войдите в свой экземпляр, чтобы управлять отслеживаемыми темами.",
  "login.username": "Имя пользователя",
  "login.password": "Пароль",
  "login.signIn": "Войти",
  "login.or": "или",
  "login.signInOIDC": "Войти через Keycloak",
  "login.legal":
    "Войдя в систему, вы соглашаетесь с тем, что несёте полную ответственность за контент, который отслеживаете. Marauder не размещает никакого контента.",
  "login.signOut": "Выйти",

  // Nav
  "nav.dashboard": "Главная",
  "nav.topics": "Темы",
  "nav.clients": "Клиенты",
  "nav.accounts": "Аккаунты",
  "nav.notifiers": "Уведомления",
  "nav.system": "Система",
  "nav.audit": "Журнал аудита",
  "nav.integrations": "Интеграции",
  "nav.settings": "Настройки",

  // Dashboard
  "dashboard.section": "обзор",
  "dashboard.title": "Снова рады вас видеть.",
  "dashboard.subtitle":
    "Вот что Marauder отслеживал, пока вас не было.",
  "dashboard.tile.active": "Активные темы",
  "dashboard.tile.updates24h": "Обновления за 24 ч",
  "dashboard.tile.errored": "С ошибками",
  "dashboard.tile.totalTracked": "Всего отслеживается",
  "dashboard.recentActivity": "Недавняя активность",
  "dashboard.recentActivity.subtitle": "Последние 10 обновлённых тем",
  "dashboard.empty.title": "Тем пока нет",
  "dashboard.empty.body":
    "Перейдите на вкладку «Темы» и вставьте ссылку на тему или magnet-ссылку.",

  // Topics
  "topics.section": "список наблюдения",
  "topics.title": "Темы",
  "topics.subtitle": "Все URL, которые Marauder активно отслеживает.",
  "topics.add": "Добавить тему",
  "topics.empty.title": "Тем пока нет",
  "topics.empty.body":
    "Вставьте ссылку на тему трекера, magnet-ссылку или URL .torrent-файла.",
  "topics.empty.cta": "Добавить первую тему",
  "topics.add.title": "Добавить новую тему",
  "topics.add.url": "URL или magnet-ссылка",
  "topics.add.urlPlaceholder":
    "magnet:?xt=urn:btih:... или https://tracker.example.com/.../file.torrent",
  "topics.add.displayName": "Отображаемое имя (необязательно)",
  "topics.add.displayNamePlaceholder": "Оставьте пустым для автоопределения",
  "topics.add.cancel": "Отмена",
  "topics.add.submit": "Добавить тему",
  "topics.col.checked": "проверено",
  "topics.col.updated": "обновлено",

  // Clients
  "clients.section": "доставка",
  "clients.title": "Клиенты",
  "clients.subtitle":
    "Куда Marauder отправляет торренты при обновлении темы.",
  "clients.add": "Добавить клиента",
  "clients.empty.title": "Клиентов пока нет",
  "clients.empty.body":
    "Добавьте торрент-клиент (qBittorrent, Transmission, Deluge или папку загрузки), чтобы Marauder было куда отправлять обновления.",
  "clients.empty.cta": "Добавить первого клиента",
  "clients.testConnection": "Проверить соединение",
  "clients.add.title": "Добавить торрент-клиент",
  "clients.add.plugin": "Плагин",
  "clients.add.displayName": "Отображаемое имя",
  "clients.add.displayNamePlaceholder": "напр. Гостиная qBit",
  "clients.add.useDefault": "Использовать по умолчанию для новых тем",
  "clients.add.cancel": "Отмена",
  "clients.add.submit": "Проверить и сохранить",
  "clients.badge.default": "по умолчанию",

  // Credentials — интерактивный вход с капчей
  "credentials.captchaTitle": "Введите код с картинки",
  "credentials.captchaPlaceholder": "Код с картинки",
  "credentials.captchaImageAlt": "Изображение капчи",
  "credentials.captchaRefresh": "Обновить код",
  "credentials.captchaIncorrect": "Неверный код, попробуйте ещё раз",
  "credentials.captchaSubmit": "Проверить и сохранить",
  "credentials.captchaCancel": "Отмена",
  "credentials.sessionExpired": "Сессия истекла",
  "credentials.reauthenticate": "Повторная авторизация",

  // Settings
  "integrations.kicker": "подключения",
  "integrations.title": "Интеграции",
  "integrations.blurb": "Подключение Marauder к внешним сервисам, таким как Sonarr.",
  "settings.kicker": "настройки",
  "settings.title": "Настройки",
  "settings.blurb": "Персонализируйте внешний вид Marauder, управляйте аккаунтом и просматривайте сведения о сборке.",
  "settings.appearance.title": "Внешний вид",
  "settings.appearance.blurb": "Тема, язык и плотность таблиц сохраняются локально в этом браузере.",
  "settings.appearance.theme": "Тема",
  "settings.appearance.themeLight": "Светлая",
  "settings.appearance.themeDark": "Тёмная",
  "settings.appearance.language": "Язык",
  "settings.appearance.density": "Плотность таблиц",
  "settings.appearance.densityComfortable": "Комфортная",
  "settings.appearance.densityCompact": "Плотная",
  "settings.account.title": "Аккаунт",
  "settings.account.blurb": "Локальные учётные данные Marauder. OIDC-пользователи входят через свой провайдер.",
  "settings.account.username": "Имя пользователя",
  "settings.account.email": "Почта",
  "settings.account.changePassword": "Сменить пароль",
  "settings.account.currentPassword": "Текущий пароль",
  "settings.account.newPassword": "Новый пароль",
  "settings.account.confirmPassword": "Повторите новый пароль",
  "settings.account.savePassword": "Обновить пароль",
  "settings.account.saving": "Сохранение...",
  "settings.account.passwordChanged": "Пароль обновлён.",
  "settings.account.passwordMismatch": "Новый пароль и подтверждение не совпадают.",
  "settings.account.passwordTooShort": "Новый пароль должен содержать не менее 8 символов.",
  "settings.account.signOut": "Выйти",
  "settings.about.title": "О программе",
  "settings.about.blurb": "Метаданные сборки и ссылки на проект.",
  "settings.about.version": "Версия",
  "settings.about.license": "Лицензия",
  "settings.about.links": "Ссылки",
  "settings.sonarr.title": "Интеграция с Sonarr",
  "settings.sonarr.blurb":
    "Marauder автоматически берёт на контроль обновляемые темы форум-трекеров, которые скачал Sonarr (RuTracker, Kinozal, …). Только для администратора.",
  "settings.sonarr.enabled": "Включить интеграцию",
  "settings.sonarr.url": "URL Sonarr",
  "settings.sonarr.apiKey": "API-ключ",
  "settings.sonarr.apiKeySet": "Ключ сохранён — оставьте пустым, чтобы не менять.",
  "settings.sonarr.pollInterval": "Интервал опроса (секунды)",
  "settings.sonarr.allowedTrackers": "Разрешённые трекеры",
  "settings.sonarr.allowedTrackersHint":
    "Не отмечайте ничего, чтобы разрешить все поддерживаемые трекеры.",
  "settings.sonarr.defaultClient": "Клиент загрузки по умолчанию",
  "settings.sonarr.defaultClientNone": "Нет (использовать клиент по умолчанию)",
  "settings.sonarr.defaultCategory": "Категория по умолчанию",
  "settings.sonarr.defaultDownloadDir": "Папка загрузки по умолчанию",
  "settings.sonarr.updateExisting": "Приводить существующие темы к этим значениям",
  "settings.sonarr.test": "Проверить подключение",
  "settings.sonarr.testing": "Проверка…",
  "settings.sonarr.testOk": "Подключено к Sonarr",
  "settings.sonarr.save": "Сохранить",
  "settings.sonarr.saving": "Сохранение…",
  "settings.sonarr.saved": "Настройки сохранены.",

  // Topic delivery status
  "topics.delivery.delivered": "Доставлено",
  "topics.delivery.downloading": "Загружается",
  "topics.delivery.finished": "Завершено",
  "topics.delivery.count": "Доставлено: {n}",
  "topics.delivery.season": "Сезон {n}",

  // Notifier event subscriptions (which events a notifier fires on)
  "notifiers.events.prefix": "Уведомлять о",
  "notifiers.event.updated": "новых релизах",
  "notifiers.event.error": "ошибках",
  "notifiers.notify_on": "Уведомлять о",

  // Canonical event labels (notifier picker + topic timeline)
  "events.topic_added": "тема добавлена",
  "events.check_started": "проверка начата",
  "events.check_completed": "проверка завершена",
  "events.release_found": "новый релиз",
  "events.download_submitted": "отправлено клиенту",
  "events.download_progress": "прогресс загрузки",
  "events.download_completed": "загрузка завершена",
  "events.check_failed": "ошибка",
  "events.session_expired": "сессия истекла",

  // Generic
  "common.loading": "Загрузка...",
  "common.justNow": "только что",
  "common.never": "никогда",
};

export default ru;
