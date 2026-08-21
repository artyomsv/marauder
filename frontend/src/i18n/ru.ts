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
  "topics.search.tab": "Поиск по трекерам",
  "topics.search.byUrl": "По ссылке",
  "topics.search.placeholder": "Поиск релизов по вашим трекерам…",
  "topics.search.button": "Найти",
  "topics.search.failed": "Поиск не удался. Попробуйте ещё раз.",
  "topics.search.noResults": "Ничего не найдено",
  "topics.search.needsAccount": "нужен аккаунт трекера — добавьте его в разделе «Аккаунты»",
  "topics.search.loginFailed": "не удалось войти на трекер — проверьте аккаунт в разделе «Аккаунты»",
  "topics.search.trackerFailed": "поиск на этом трекере не удался",
  "topics.search.coverage": "Поиск по:",
  "topics.add.urlPlaceholder":
    "magnet:?xt=urn:btih:... или https://tracker.example.com/.../file.torrent",
  "topics.add.displayName": "Отображаемое имя (необязательно)",
  "topics.add.displayNamePlaceholder": "Оставьте пустым для автоопределения",
  "topics.add.cancel": "Отмена",
  "topics.add.submit": "Добавить тему",
  "topics.col.checked": "проверено",
  "topics.col.updated": "обновлено",
  // Понятные сообщения об ошибках проверки по коду last_error_code.
  "topics.error.timeout":
    "Трекер не ответил вовремя — возможно, он временно недоступен. Повтор автоматически.",
  "topics.error.unreachable":
    "Не удалось подключиться к трекеру — возможно, он недоступен или блокирует запросы. Повтор автоматически.",
  "topics.error.auth":
    "Ошибка входа или сессия истекла — проверьте учётные данные этого трекера.",
  "topics.error.cloudflare":
    "Cloudflare заблокировал запрос до трекера — учётные данные в порядке. Обходчик получил разрешение, но трекер его отклонил: обходчик должен выходить в интернет с того же публичного IP, что и Marauder.",
  "topics.error.solverMissing":
    "Обходчик Cloudflare не настроен, а без него этот трекер недоступен. Запустите контейнер FlareSolverr и укажите его адрес в MARAUDER_FLARESOLVERR_URL.",
  "topics.error.solver":
    "Обходчик Cloudflare недоступен или не ответил вовремя — сам трекер, скорее всего, доступен. Проверки будут повторяться.",
  "topics.error.parse":
    "Не удалось прочитать страницу трекера — возможно, изменилась её разметка.",
  "topics.error.pluginMissing":
    "Этот трекер не поддерживается текущей версией Marauder.",
  "topics.error.client":
    "Не удалось подключиться к торрент-клиенту — проверьте, что он запущен и доступен из Marauder. С трекером всё в порядке.",
  "topics.error.internal":
    "Marauder не смог сохранить или загрузить данные этой темы — с трекером всё в порядке. Повтор автоматически.",

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
  "credentials.unverified":
    "Сохранено, но вход не подтверждён — плагин этого трекера не умеет проверять сессию.",
  "credentials.unverifiedShort": "Вход не подтверждён",
  "credentials.testPending": "Проверка входа",
  "credentials.testVerified": "Вход подтверждён",
  "credentials.testFailed": "Ошибка входа",

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
    "Marauder автоматически берёт на контроль обновляемые темы форум-трекеров, которые скачал Sonarr (RuTracker, Kinozal, …). Добавьте по одному экземпляру на каждый Sonarr — например, отдельно для сериалов и для аниме. Только для администратора.",
  "settings.sonarr.name": "Название",
  "settings.sonarr.namePlaceholder": "напр. Сериалы, Аниме",
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
  "settings.sonarr.categoryHintSuggestions":
    "Выберите существующую категорию клиента или «Своя…», чтобы ввести свою.",
  "settings.sonarr.categoryHintFree": "Вкладывается в базовую папку загрузки клиента.",
  "settings.sonarr.defaultDownloadDir": "Папка загрузки по умолчанию",
  "settings.sonarr.updateExisting": "Приводить существующие темы к этим значениям",
  "settings.sonarr.lastSeen": "Последний опрос",
  "settings.sonarr.test": "Проверить подключение",
  "settings.sonarr.testing": "Проверка…",
  "settings.sonarr.testOk": "Подключено к Sonarr",
  "settings.sonarr.testFailed": "Не удалось подключиться",
  "settings.sonarr.toggleFailed": "Не удалось изменить состояние экземпляра",
  "settings.sonarr.deleteFailed": "Не удалось удалить экземпляр",
  "settings.sonarr.save": "Сохранить",
  "settings.sonarr.saving": "Сохранение…",
  "settings.sonarr.saved": "Настройки сохранены.",
  "settings.sonarr.cancel": "Отмена",
  "settings.sonarr.instances.add": "Добавить экземпляр",
  "settings.sonarr.instances.addTitle": "Новый экземпляр Sonarr",
  "settings.sonarr.instances.empty": "Пока нет экземпляров Sonarr. Добавьте один, чтобы начать авто-мониторинг загрузок.",
  "settings.sonarr.status.active": "Активен",
  "settings.sonarr.status.paused": "Пауза",
  "settings.sonarr.status.draft": "Черновик",
  "settings.sonarr.actions.edit": "Изменить экземпляр",
  "settings.sonarr.actions.delete": "Удалить экземпляр",
  "settings.sonarr.actions.enable": "Включить",
  "settings.sonarr.actions.disable": "Выключить",
  "settings.domains.title": "Домены трекеров",
  "settings.domains.blurb":
    "Переопределите домен, который использует каждый трекер, и добавьте дополнительные зеркала на случай, если основной домен заблокирован.",
  "settings.domains.instanceWideNote": "Действует для всего экземпляра. Только для администратора.",
  "settings.domains.defaultSuffix": "(по умолчанию)",
  "settings.domains.addLabel": "Добавить зеркало",
  "settings.domains.addPlaceholder": "mirror.example.com",
  "settings.domains.addButton": "Добавить",
  "settings.domains.invalidHostname": "Некорректное имя хоста",
  "settings.domains.duplicateHostname": "Этот домен уже в списке",
  "settings.domains.test": "Проверить",
  "settings.domains.testOk": "Доступен",
  "settings.domains.testFail": "Недоступен",
  "settings.domains.saveFailed": "Не удалось сохранить",
  "settings.domains.remove": "Удалить {domain}",
  "settings.domains.summary": "{overridden} из {total} переопределены",
  "settings.domains.activeLabel": "Активный домен",
  "settings.domains.mirrorsLabel": "Зеркала",
  "settings.domains.usingDefault": "Домен по умолчанию",
  "settings.domains.overridden": "Переопределён",
  "settings.domains.singleDomainHint": "Известен только один домен — добавьте зеркало для переключения.",

  // Topic live check status (fed by SSE check.* events)
  "topics.check.checking": "Проверка…",
  "topics.check.error": "Ошибка проверки",
  "topics.check.next": "Следующая проверка {time}",

  // Topic delivery status
  "topics.delivery.delivered": "Доставлено",
  "topics.delivery.downloading": "Загружается",
  "topics.delivery.finished": "Завершено",
  "topics.delivery.count": "Доставлено: {n}",
  "topics.delivery.season": "Сезон {n}",
  "topics.delivery.reissued": "{n} версии · трекер перевыпустил торрент · последняя {date}",
  "topics.delivery.deliveredOn": "доставлено {date}",
  "topics.delivery.copy": "Скопировать метку",
  "topics.delivery.copied": "Скопировано",

  // Панель массовых действий (появляется при выборе одной или нескольких тем)
  "topics.bulk.selected": "Выбрано: {count}",
  "topics.bulk.pause": "Приостановить",
  "topics.bulk.resume": "Возобновить",
  "topics.bulk.reset": "Сбросить",
  "topics.bulk.recheck": "Проверить сейчас",
  "topics.bulk.delete": "Удалить",
  "topics.bulk.confirmDelete": "Удалить тем: {count}?",
  "topics.bulk.yes": "Да",
  "topics.bulk.no": "Нет",
  "topics.bulk.clear": "Снять выбор",

  // Действие строки: немедленная проверка вне расписания темы
  "topics.recheck": "Проверить сейчас",
  "topics.actions.menu": "Действия с темой",
  "topics.actions.edit": "Изменить",
  "topics.actions.reset": "Сбросить",
  "topics.actions.delete": "Удалить",
  "topics.actions.confirmDelete": "Подтвердить удаление",

  // Сброс темы (сброс доставок/прогресса/ошибок, повторная проверка с нуля)
  "topics.reset.title": "Сбросить «{name}»",
  "topics.reset.titleBulk": "Сбросить тем: {count}",
  "topics.reset.body":
    "Удаляет записи о загрузках, прогресс по сериям и состояние ошибки, затем сразу проверяет тему заново, чтобы всё скачалось с нуля. Настройки и история событий сохраняются. Приостановленная тема останется приостановленной.",
  "topics.reset.deleteData": "Также удалить скачанные файлы из клиента",
  "topics.reset.confirm": "Сбросить",
  "topics.reset.cancel": "Отмена",
  "topics.reset.pending": "Сброс…",
  "topics.reset.done": "Удалено торрентов: {count}. Поставлено в очередь на проверку.",
  "topics.reset.warnings": "Некоторые торренты удалить не удалось:",
  "topics.reset.close": "Закрыть",

  // Notifier event subscriptions (which events a notifier fires on)
  "notifiers.events.prefix": "Уведомлять о",
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
  "events.topic_reset": "тема сброшена",

  // Topic history timeline
  "topics.history.empty": "История пока пуста",
  "topics.history.show": "История",
  "topics.history.hide": "Скрыть историю",
  "topics.history.repeated": "повторов: {count}",

  // Generic
  "common.loading": "Загрузка...",
  "common.justNow": "только что",
  "common.never": "никогда",
};

export default ru;
