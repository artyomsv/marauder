export type Notifier = {
  slug: string;
  name: string;
  description: string;
  status: "validated" | "alpha";
};

export const notifiers: Notifier[] = [
  {
    slug: "telegram",
    name: "Telegram",
    description: "Bot token + chat ID. Markdown formatting. Tested against the live Bot API.",
    status: "validated",
  },
  {
    slug: "email",
    name: "Email (SMTP)",
    description: "STARTTLS + PLAIN auth via net/smtp.",
    status: "alpha",
  },
  {
    slug: "webhook",
    name: "Webhook",
    description: "POST { source, title, body, link } JSON to any URL.",
    status: "alpha",
  },
  {
    slug: "pushover",
    name: "Pushover",
    description: "Push notifications to iOS, Android, and desktop via api.pushover.net.",
    status: "alpha",
  },
];
