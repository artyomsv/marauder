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
  "nav.notifiers": "Уведомления",
  "nav.system": "Система",
  "nav.audit": "Журнал аудита",
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

  // Generic
  "common.loading": "Загрузка...",
  "common.justNow": "только что",
  "common.never": "никогда",
};

export default ru;
