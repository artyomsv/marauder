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

  // Settings
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

  // Generic
  "common.loading": "Загрузка...",
  "common.justNow": "только что",
  "common.never": "никогда",
};

export default ru;
