export type Notifier = {
  slug: string;
  name: string;
  description: string;
};

export const notifiers: Notifier[] = [
  {
    slug: "telegram",
    name: "Telegram",
    description: "Bot token + chat ID. Markdown formatting.",
  },
  {
    slug: "email",
    name: "Email (SMTP)",
    description: "STARTTLS + PLAIN auth via net/smtp.",
  },
  {
    slug: "webhook",
    name: "Webhook",
    description: "POST { source, title, body, link } JSON to any URL.",
  },
  {
    slug: "pushover",
    name: "Pushover",
    description: "Push notifications to iOS, Android, and desktop via api.pushover.net.",
  },
];
